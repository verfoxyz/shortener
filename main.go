package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// 测试
const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomCode(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rand.Intn(len(alphabet))]
	}
	return string(b)
}

type Handler struct {
	store   *Store
	tmpl    *template.Template
	baseURL string
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}

	var code string
	for i := range 5 {
		code = randomCode(6)
		if err := h.store.Save(r.Context(), code, req.URL); err == nil {
			break
		} else if i == 4 {
			log.Printf("save link:%v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":      code,
		"short_url": h.baseURL + "/" + code,
	})
}

func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	link, err := h.store.Get(r.Context(), code)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("get link: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 点击记录
	if err := h.store.RecordClick(r.Context(), code, Click{
		UserAgent: r.UserAgent(),
		Referer:   r.Referer(),
		IP:        clientIP(r),
	}); err != nil {
		log.Printf("record click:%v", err)
	}

	http.Redirect(w, r, link, http.StatusFound)
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) Admin(w http.ResponseWriter, r *http.Request) {
	links, err := h.store.LinkStat(r.Context())
	if err != nil {
		log.Printf("list stats:%v", err)
		http.Error(w, "internal errror", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.Execute(w, map[string]any{"Links": links}); err != nil {
		log.Printf("render admin:%v", err)
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
	cfg := LoadConfig()
	tmpl := template.Must(template.ParseFiles("web/admin.html"))
	store, err := NewStore(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}

	h := &Handler{store: store, tmpl: tmpl, baseURL: cfg.BaseURL}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /admin", h.Admin)
	mux.HandleFunc("POST /shorten", h.Create)
	mux.HandleFunc("GET /{code}", h.Redirect)

	var handler http.Handler = mux
	handler = logMiddeware(handler)
	handler = recoverMiddeware(handler)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
	if err := store.Close(); err != nil {
		slog.Error("close store", "err", err)
	}
	slog.Info("bye")
}

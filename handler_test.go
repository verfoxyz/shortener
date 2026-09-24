package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdmin(t *testing.T) {
	store := newTestStore(t)
	tmpl := template.Must(template.ParseFiles("web/admin.html"))
	h := &Handler{store: store, tmpl: tmpl, baseURL: "http://test.local"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin", h.Admin)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d,want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "短链接管理") {
		t.Errorf("body missing title,got:%s", rec.Body.String())
	}
}

func newTestHandler(t *testing.T) (*Handler, *http.ServeMux) {
	t.Helper()
	store := newTestStore(t)

	h := &Handler{
		store:   store,
		baseURL: "http://test.local",
		// tmpl 在 Admin 测试里单独设置
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /shorten", h.Create)
	mux.HandleFunc("GET /admin", h.Admin)
	mux.HandleFunc("GET /{code}", h.Redirect)
	return h, mux
}

func TestCreateAndRedirect(t *testing.T) {
	_, mux := newTestHandler(t)

	body := `{"url":"https://go.dev"}`
	req := httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code     string `json:"code"`
		ShortURL string `json:"short_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code == "" {
		t.Fatal("empty code")
	}
	if resp.ShortURL != "http://test.local/"+resp.Code {
		t.Errorf("short_url = %q", resp.ShortURL)
	}

	// 访问短链
	req2 := httptest.NewRequest(http.MethodGet, "/"+resp.Code, nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusFound {
		t.Fatalf("redirect status = %d, want 302", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); loc != "https://go.dev" {
		t.Errorf("Location = %q, want https://go.dev", loc)
	}
}

func TestCreateInvalidJSON(t *testing.T) {
	_, mux := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCreateInvalidURL(t *testing.T) {
	_, mux := newTestHandler(t)

	for _, bad := range []string{"", "ftp://x.com", "not-a-url"} {
		body := `{"url":"` + bad + `"}`
		req := httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("url %q: status = %d, want 400", bad, rec.Code)
		}
	}
}

func TestRedirectNotFound(t *testing.T) {
	_, mux := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

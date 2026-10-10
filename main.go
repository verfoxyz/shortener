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

// alphabet 是 base62 字符集：26 小写 + 26 大写 + 10 数字 = 62 个。
// 6 位短码的组合空间是 62^6 ≈ 568 亿，远大于实际链接数，冲突概率极低；
// 但"极低"不等于"零"，所以 Create 里仍保留了冲突重试（见下）。
const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// randomCode 生成 n 位随机短码。
// 注意 rand 用的是 math/rand（非加密安全）：它追求快、种子可预测，
// 因此只适合"被猜到也无所谓"的场景。若短链指向私有资源，应换成 crypto/rand。
func randomCode(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rand.Intn(len(alphabet))]
	}
	return string(b)
}

// Handler 把三个 handler 需要的依赖显式挂在结构体上（依赖注入）。
// 好处：不依赖任何包级全局变量，测试时可以塞进临时库和临时 baseURL，用例之间互不干扰。
type Handler struct {
	store   *Store             // 数据访问层
	tmpl    *template.Template // 管理页模板，启动时只解析一次
	baseURL string             // 拼短链前缀，来自 Config.BaseURL
}

// Create 处理 POST /shorten：校验入参 → 生成不冲突的短码 → 落库 → 返回 201。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	// 用匿名 struct 定义请求体，而不是专门声明一个命名类型：
	// 它只在"解码"这一处用到，命名反而增加阅读时的跳转成本。
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
	// 只允许 http/https：既挡掉 "javascript:" 这类会让短链变成 XSS 载体的 scheme，
	// 也挡掉相对路径等无意义输入。url.Parse 对大量脏输入会返回 err，故一并检查。
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}

	// 冲突重试：links.code 上有 UNIQUE 约束，插入撞车时 Save 会返回错误。
	// 最多重试 5 次（而非无限循环），避免极端情况下把请求卡死。
	// 第 5 次（i == 4）仍失败说明不只是撞码，而是真出问题了：记日志并返回 500。
	var code string
	for i := range 5 { // Go 1.22+ 的 range over int，等价于 for i := 0; i < 5; i++
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
	// 显式忽略 Encode 的返回值：状态码已经写出去了，此时再出错也无法改响应。
	// 赋给 _ 是为了向读者说明"这里是有意忽略"，而不是忘了处理。
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":      code,
		"short_url": h.baseURL + "/" + code,
	})
}

// Redirect 处理 GET /{code}：查短码 → 记录点击 → 302 跳转。
func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	// {code} 通配符由 ServeMux 解析，用 PathValue 取回（Go 1.22+ 的新路由能力）。
	code := r.PathValue("code")
	link, err := h.store.Get(r.Context(), code)

	// 关键：用 errors.Is 判断哨兵错误，而不是比较错误字符串。
	// 这样即使 Get 内部把错误 wrap 过（fmt.Errorf("%w")）也依然能识别出来。
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("get link: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 点击记录：失败只记日志，绝不因此中断跳转。
	// 统计是"尽力而为"的副作用，核心功能（把用户送到目标页）不能被它拖垮。
	if err := h.store.RecordClick(r.Context(), code, Click{
		UserAgent: r.UserAgent(),
		Referer:   r.Referer(),
		IP:        clientIP(r),
	}); err != nil {
		log.Printf("record click:%v", err)
	}

	// 用 302 而不是 301：301 会被浏览器与中间层长期缓存，
	// 后续点击不再回到本服务，点击统计就彻底失效了。
	http.Redirect(w, r, link, http.StatusFound)
}

// clientIP 取客户端 IP，优先信任代理写入的 X-Forwarded-For。
// 注意该头可被客户端伪造，只有在"前面一定挂了自己可信的反代"时才可信。
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return ip
	}
	// r.RemoteAddr 形如 "1.2.3.4:5678"，需要剥离端口；
	// 剥离失败就原样返回，保证"至少有值可记"，不因解析失败丢掉整条点击记录。
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Admin 处理 GET /admin：聚合每个短链的点击数并渲染 HTML 看板。
func (h *Handler) Admin(w http.ResponseWriter, r *http.Request) {
	links, err := h.store.LinkStat(r.Context())
	if err != nil {
		log.Printf("list stats:%v", err)
		http.Error(w, "internal errror", http.StatusInternalServerError)
		return
	}
	// 先设 Content-Type 再 Execute：一旦模板开始往 w 写字节，响应头就定型了。
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.Execute(w, map[string]any{"Links": links}); err != nil {
		log.Printf("render admin:%v", err)
	}
}

func main() {
	// 1) 日志：用 slog 输出 JSON，便于日志系统结构化采集。
	//    先建好 logger 再 SetDefault，之后全项目对 log/slog 的默认调用都会走它。
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// 2) 依赖装配：按"配置 → 模板 → 存储 → Handler"自底向上构建。
	cfg := LoadConfig()
	// template.Must：模板是启动期就确定的资源，解析失败直接 panic 好过带病运行。
	tmpl := template.Must(template.ParseFiles("web/admin.html"))
	store, err := NewStore(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}

	h := &Handler{store: store, tmpl: tmpl, baseURL: cfg.BaseURL}

	// 3) 路由：Go 1.22+ 的 ServeMux 支持"方法 + 路径"模式，无需第三方框架。
	//    注册顺序不重要，ServeMux 按具体程度匹配：
	//    /healthz、/admin 这类字面量路径优先于 /{code} 通配符。
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /admin", h.Admin)
	mux.HandleFunc("POST /shorten", h.Create)
	mux.HandleFunc("GET /{code}", h.Redirect)

	// 4) 中间件：从内到外逐层包裹，最终顺序为 recover( log( mux ) )：
	//    - logMiddleware 记录访问日志；
	//    - recoverMiddleware 在最外层兜住 panic，防止单个请求崩掉整个进程。
	//    ⚠️ 副作用：handler panic 时 panic 会直接展开栈，
	//       logMiddleware 结尾那句 slog.Info 不会执行，因此 panic 的请求不出现在访问日志里。
	var handler http.Handler = mux
	handler = logMiddleware(handler)
	handler = recoverMiddleware(handler)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
	}

	// 5) 优雅退出：NotifyContext 把 SIGINT/SIGTERM 变成一个可 <- 的 ctx。
	//    defer stop() 用于注销信号处理器，避免资源泄漏。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ListenAndServe 是阻塞的，放进 goroutine，主 goroutine 才能去等退出信号。
	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		// ErrServerClosed 是 Shutdown 主动关闭时的正常返回，必须排除，否则会误判为启动失败而退出。
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done() // 阻塞在此，直到收到退出信号

	// 6) 收尾：给在途请求最多 10 秒完成时间，超时则强制关闭。
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
	// 等 Shutdown 返回后再关库，确保没有请求还在使用连接。
	// 注意：这里只等 HTTP 请求，自定义的后台 goroutine 需要另外收尾。
	if err := store.Close(); err != nil {
		slog.Error("close store", "err", err)
	}
	slog.Info("bye")
}

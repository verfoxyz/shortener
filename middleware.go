package main

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder 包一层 ResponseWriter，目的是"偷看"handler 最终写出的状态码。
// http.ResponseWriter 没有读回状态码的方法，所以只能在 WriteHeader 被调用的瞬间自己记一笔。
type statusRecorder struct {
	http.ResponseWriter // 内嵌：自动继承 Header/Write 等方法，只需覆写 WriteHeader
	status              int
}

// WriteHeader 覆写：先记下状态码，再交给被包裹的原 writer 真正写出去。
// 构造时默认值设为 200，所以 handler 不显式调用 WriteHeader（隐式 200）时也能记对。
func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logMiddleware 是"环绕式"中间件：在 next 前后各插一段逻辑。
// 签名故意写成 http.Handler → http.Handler，这样才能像洋葱一样层层包裹、自由组合。
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// 用带记录功能的 wrapper 代替原始 w 传给下游，才能拿到最终状态码。
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", clientIP(r),
		)
	})
}

// recoverMiddleware 兜住下游任何 panic，转成 500，避免一个请求把整个进程带崩。
// defer 必须写在 next.ServeHTTP 之前：defer 注册即生效，否则 panic 发生时还没注册就兜不住。
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic", "err", err, "path", r.URL.Path)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

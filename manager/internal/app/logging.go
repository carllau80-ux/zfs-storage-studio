package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Version/Commit/BuildTime 由 -ldflags 注入。
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

var (
	reqCount    atomic.Int64
	reqInflight atomic.Int64
	reqErrors   atomic.Int64
	startedAt   = time.Now()
	readyFlag   atomic.Bool
)

// InitLogger 输出 JSON 结构化日志(slog)。
func InitLogger() {
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
}

type ctxReqID struct{}

func reqIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxReqID{}).(string)
	return v
}

func randReqID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// middleware:request_id → 响应头回传,并注入日志上下文。
func (a *App) mwRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = randReqID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxReqID{}, id)))
	})
}

// middleware:panic 边界(500 而非进程退出)。
func (a *App) mwRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if e := recover(); e != nil {
				reqErrors.Add(1)
				slog.Error("panic recovered", "request_id", reqIDFrom(r.Context()),
					"path", r.URL.Path, "err", e)
				writeErr(w, http.StatusInternalServerError, "INTERNAL", "服务内部错误(已记录)")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusCap struct {
	http.ResponseWriter
	code int
}

func (s *statusCap) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusCap) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// middleware:访问日志 + 指标计数。
func (a *App) mwAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqCount.Add(1)
		reqInflight.Add(1)
		sc := &statusCap{ResponseWriter: w}
		next.ServeHTTP(sc, r)
		reqInflight.Add(-1)
		code := sc.code
		if code == 0 {
			code = http.StatusOK
		}
		if code >= 500 {
			reqErrors.Add(1)
		}
		// 静态资源不刷日志
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			return
		}
		slog.Info("http",
			"request_id", reqIDFrom(r.Context()),
			"method", r.Method, "path", r.URL.Path, "code", code,
			"dur_ms", time.Since(start).Milliseconds(),
			"remote", clientIP(r))
	})
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	// RemoteAddr 形如 host:port,去掉端口作为稳定限速键
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// ---------- 健康 / 版本 / 指标 ----------

func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "alive"})
}

// handleReadyz 探依赖:DB ping + 迁移完成。
func (a *App) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !readyFlag.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "status": "starting"})
		return
	}
	var one int
	if err := a.db.QueryRow(r.Context(), `SELECT 1`).Scan(&one); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "status": "db_unavailable", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "ready"})
}

func (a *App) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": Version, "commit": Commit, "build_time": BuildTime,
		"uptime_s": int64(time.Since(startedAt).Seconds()), "go": goVersion(),
	})
}

// handleMetrics 输出 Prometheus 文本格式(最小集,无第三方依赖)。
func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writef := func(format string, args ...any) {
		_, _ = w.Write([]byte(strings.TrimRight(sprintf(format, args...), "\n") + "\n"))
	}
	writef("# HELP runstor_http_requests_total Total HTTP requests")
	writef("# TYPE runstor_http_requests_total counter")
	writef("runstor_http_requests_total %d", reqCount.Load())
	writef("# HELP runstor_http_requests_errors_total HTTP 5xx/panic responses")
	writef("# TYPE runstor_http_requests_errors_total counter")
	writef("runstor_http_requests_errors_total %d", reqErrors.Load())
	writef("# HELP runstor_http_inflight_requests In-flight requests")
	writef("# TYPE runstor_http_inflight_requests gauge")
	writef("runstor_http_inflight_requests %d", reqInflight.Load())
	writef("# HELP runstor_uptime_seconds Process uptime")
	writef("# TYPE runstor_uptime_seconds gauge")
	writef("runstor_uptime_seconds %s", strconv.FormatInt(int64(time.Since(startedAt).Seconds()), 10))
	writef("# HELP runstor_build_info Build info")
	writef("# TYPE runstor_build_info gauge")
	writef(`runstor_build_info{version=%q,commit=%q} 1`, Version, Commit)
	// 节点与资源计数
	var nodes, online, pools, vols int64
	_ = a.db.QueryRow(r.Context(), `SELECT count(*), count(*) FILTER (WHERE status='online') FROM nodes`).Scan(&nodes, &online)
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM pools`).Scan(&pools)
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM datasets WHERE type='volume'`).Scan(&vols)
	writef("# HELP runstor_nodes Nodes by state")
	writef("# TYPE runstor_nodes gauge")
	writef(`runstor_nodes{state="online"} %d`, online)
	writef(`runstor_nodes{state="offline"} %d`, nodes-online)
	writef("# HELP runstor_pools Pools")
	writef("# TYPE runstor_pools gauge")
	writef("runstor_pools %d", pools)
	writef("# HELP runstor_volumes zvol count")
	writef("# TYPE runstor_volumes gauge")
	writef("runstor_volumes %d", vols)
}

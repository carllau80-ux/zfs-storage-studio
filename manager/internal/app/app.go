package app

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed static/dist
var staticFS embed.FS

type App struct {
	cfg *Config
	db  *pgxpool.Pool
	srv *http.Server

	mu          sync.Mutex
	sessionSnap map[int64][]SessionLive // node_id -> 会话快照(轮询更新)
	pollFailed  map[int64]bool
	metrics     map[int64][]MetricPoint // node_id -> 性能指标环形缓冲
}

func New(cfg *Config, pool *pgxpool.Pool) *App {
	return &App{cfg: cfg, db: pool, sessionSnap: map[int64][]SessionLive{},
		pollFailed: map[int64]bool{}, metrics: map[int64][]MetricPoint{}}
}

func (a *App) logf(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}

// routes 组装全部 HTTP 路由。
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	auth := a.requireAuth

	// --- 健康 / 版本 / 指标(无需认证;通常由入口代理放行内网) ---
	mux.HandleFunc("GET /healthz", a.handleHealthz)
	mux.HandleFunc("GET /readyz", a.handleReadyz)
	mux.HandleFunc("GET /version", a.handleVersion)
	mux.HandleFunc("GET /metrics", a.handleMetrics)

	// --- 认证 ---
	mux.HandleFunc("POST /api/v1/auth/login", a.handleLogin)
	mux.HandleFunc("GET /api/v1/auth/me", auth(a.handleMe))
	mux.HandleFunc("POST /api/v1/auth/logout", auth(a.handleLogout))

	// --- Agent 通道(X-Agent-Token 鉴权)---
	agent := a.requireAgent
	mux.HandleFunc("POST /api/v1/agent/register", agent(a.handleAgentRegister))
	mux.HandleFunc("POST /api/v1/agent/nodes/{id}/heartbeat", agent(a.handleAgentHeartbeat))

	// --- 节点 ---
	mux.HandleFunc("GET /api/v1/nodes", auth(a.handleNodesList))
	mux.HandleFunc("GET /api/v1/nodes/{id}", auth(a.handleNodeGet))
	mux.HandleFunc("DELETE /api/v1/nodes/{id}", auth(a.requireRole("admin")(a.auditWrap("node", a.handleNodeDelete))))
	mux.HandleFunc("GET /api/v1/nodes/{id}/sessions", auth(a.handleNodeSessions))
	mux.HandleFunc("GET /api/v1/nodes/{id}/capabilities", auth(a.handleNodeCapabilities))

	// --- 池 ---
	mux.HandleFunc("GET /api/v1/pools", auth(a.handlePoolsList))
	mux.HandleFunc("POST /api/v1/pools", auth(a.requireRole("operator")(a.auditWrap("pool", a.handlePoolCreate))))
	mux.HandleFunc("GET /api/v1/pools/{id}", auth(a.handlePoolGet))
	mux.HandleFunc("GET /api/v1/pools/{id}/status", auth(a.handlePoolStatus))
	mux.HandleFunc("POST /api/v1/pools/{id}/scrub", auth(a.requireRole("operator")(a.auditWrap("pool", a.handlePoolScrub))))
	mux.HandleFunc("DELETE /api/v1/pools/{id}", auth(a.requireRole("operator")(a.auditWrap("pool", a.handlePoolDelete))))
	mux.HandleFunc("GET /api/v1/nodes/{id}/disks", auth(a.handleNodeDisks))

	// --- 卷(dataset) ---
	mux.HandleFunc("GET /api/v1/datasets", auth(a.handleDatasetsList))
	mux.HandleFunc("POST /api/v1/datasets", auth(a.requireRole("operator")(a.auditWrap("dataset", a.handleDatasetCreate))))
	mux.HandleFunc("GET /api/v1/datasets/{id}", auth(a.handleDatasetGet))
	mux.HandleFunc("PATCH /api/v1/datasets/{id}/resize", auth(a.requireRole("operator")(a.auditWrap("dataset", a.handleDatasetResize))))
	mux.HandleFunc("DELETE /api/v1/datasets/{id}", auth(a.requireRole("operator")(a.auditWrap("dataset", a.handleDatasetDelete))))
	mux.HandleFunc("POST /api/v1/datasets/{id}/format", auth(a.requireRole("operator")(a.auditWrap("dataset", a.handleDatasetFormat))))
	mux.HandleFunc("PATCH /api/v1/datasets/{id}/properties", auth(a.requireRole("operator")(a.auditWrap("dataset", a.handleDatasetProps))))
	mux.HandleFunc("POST /api/v1/datasets/clone", auth(a.requireRole("operator")(a.auditWrap("dataset", a.handleDatasetClone))))

	// --- 快照 ---
	mux.HandleFunc("GET /api/v1/datasets/{id}/snapshots", auth(a.handleSnapList))
	mux.HandleFunc("POST /api/v1/datasets/{id}/snapshots", auth(a.requireRole("operator")(a.auditWrap("snapshot", a.handleSnapCreate))))
	mux.HandleFunc("POST /api/v1/datasets/{id}/rollback", auth(a.requireRole("operator")(a.auditWrap("dataset", a.handleSnapRollback))))
	mux.HandleFunc("DELETE /api/v1/snapshots/{id}", auth(a.requireRole("operator")(a.auditWrap("snapshot", a.handleSnapDelete))))

	// --- iSCSI Target ---
	mux.HandleFunc("GET /api/v1/targets", auth(a.handleTargetsList))
	mux.HandleFunc("POST /api/v1/targets", auth(a.requireRole("operator")(a.auditWrap("target", a.handleTargetCreate))))
	mux.HandleFunc("GET /api/v1/targets/{id}", auth(a.handleTargetGet))
	mux.HandleFunc("DELETE /api/v1/targets/{id}", auth(a.requireRole("operator")(a.auditWrap("target", a.handleTargetDelete))))
	mux.HandleFunc("POST /api/v1/targets/{id}/luns", auth(a.requireRole("operator")(a.auditWrap("target", a.handleLunAdd))))
	mux.HandleFunc("DELETE /api/v1/targets/{id}/luns/{lun}", auth(a.requireRole("operator")(a.auditWrap("target", a.handleLunRemove))))
	mux.HandleFunc("POST /api/v1/targets/{id}/acls", auth(a.requireRole("operator")(a.auditWrap("target", a.handleAclAdd))))
	mux.HandleFunc("DELETE /api/v1/targets/{id}/acls/{iqn}", auth(a.requireRole("operator")(a.auditWrap("target", a.handleAclRemove))))
	mux.HandleFunc("DELETE /api/v1/targets/{id}/sessions/{sid}", auth(a.requireRole("operator")(a.auditWrap("session", a.handleSessionLogout))))

	// --- 主机/映射 ---
	mux.HandleFunc("GET /api/v1/hosts", auth(a.handleHostsList))
	mux.HandleFunc("POST /api/v1/hosts", auth(a.requireRole("operator")(a.auditWrap("host", a.handleHostCreate))))
	mux.HandleFunc("PATCH /api/v1/hosts/{id}", auth(a.requireRole("operator")(a.auditWrap("host", a.handleHostUpdate))))
	mux.HandleFunc("DELETE /api/v1/hosts/{id}", auth(a.requireRole("operator")(a.auditWrap("host", a.handleHostDelete))))
	mux.HandleFunc("GET /api/v1/hosts/{id}", auth(a.handleHostGet))
	mux.HandleFunc("POST /api/v1/hosts/{id}/initiators", auth(a.requireRole("operator")(a.auditWrap("initiator", a.handleInitiatorAdd))))
	mux.HandleFunc("DELETE /api/v1/hosts/{id}/initiators/{iid}", auth(a.requireRole("operator")(a.auditWrap("initiator", a.handleInitiatorDelete))))
	// --- 文件共享 NFS / SMB ---
	mux.HandleFunc("GET /api/v1/shares", auth(a.handleSharesList))
	mux.HandleFunc("POST /api/v1/shares", auth(a.requireRole("operator")(a.auditWrap("share", a.handleShareCreate))))
	mux.HandleFunc("DELETE /api/v1/shares/{id}", auth(a.requireRole("operator")(a.auditWrap("share", a.handleShareDelete))))
	mux.HandleFunc("POST /api/v1/shares/{id}/toggle", auth(a.requireRole("operator")(a.auditWrap("share", a.handleShareToggle))))

	mux.HandleFunc("GET /api/v1/mappings", auth(a.handleMappingsList))
	mux.HandleFunc("POST /api/v1/mappings", auth(a.requireRole("operator")(a.auditWrap("mapping", a.handleMappingCreate))))
	mux.HandleFunc("DELETE /api/v1/mappings/{id}", auth(a.requireRole("operator")(a.auditWrap("mapping", a.handleMappingDelete))))

	// --- 审计/用户/总览 ---
	mux.HandleFunc("GET /api/v1/audit-logs", auth(a.handleAuditList))
	mux.HandleFunc("GET /api/v1/users", auth(a.requireRole("admin")(a.handleUsersList)))
	mux.HandleFunc("POST /api/v1/users", auth(a.requireRole("admin")(a.auditWrap("user", a.handleUserCreate))))
	mux.HandleFunc("PATCH /api/v1/users/{id}", auth(a.requireRole("admin")(a.auditWrap("user", a.handleUserUpdate))))
	mux.HandleFunc("GET /api/v1/dashboard", auth(a.handleDashboard))

	chain := a.mwRecover(a.mwAccess(a.cors(a.mwRequestID(mux))))
	return chain
}

func (a *App) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Agent-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// staticHandler 提供内嵌前端;非 API 路径回退 index.html(SPA)。
func (a *App) staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static/dist")
	if err != nil {
		slog.Error("embed 失败(前端资源缺失)", "err", err)
		os.Exit(1)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "接口不存在")
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := sub.Open(p); err == nil {
			f.Close()
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, sub, p)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, sub, "index.html")
	})
}

func (a *App) Run(cfgPath string) error {
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	a.cfg = cfg
	ctx := context.Background()
	pool, err := OpenDB(ctx, cfg.DBURL())
	if err != nil {
		return err
	}
	a.db = pool
	if err := Migrate(ctx, pool); err != nil {
		return err
	}
	readyFlag.Store(false)
	if err := SeedAdmin(ctx, pool, "/var/lib/runstor", cfg.InitAdminPassword()); err != nil {
		return err
	}
	if err := MigrateTokens(ctx, pool); err != nil {
		return fmt.Errorf("令牌哈希迁移失败: %w", err)
	}
	os.MkdirAll("/var/lib/runstor", 0o750)

	runCtx, stopSig := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSig()
	go a.poller(runCtx)

	mux := a.routes()
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 运维端点与 API 走 mux;其余交给内嵌前端(SPA 回退)
		switch r.URL.Path {
		case "/healthz", "/readyz", "/version", "/metrics":
			mux.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		a.staticHandler().ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: cfg.Listen, Handler: final, ReadHeaderTimeout: 10 * time.Second}
	readyFlag.Store(true)
	slog.Info("manager started", "listen", cfg.Listen, "poll_s", cfg.PollSeconds,
		"version", Version, "commit", Commit)

	// 优雅退出:停止接收新请求 → 在途 drain(20s)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	case <-runCtx.Done():
		readyFlag.Store(false)
		slog.Info("shutdown signal received, draining...")
		shCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(shCtx); err != nil {
			slog.Error("graceful shutdown failed", "err", err)
		}
		slog.Info("shutdown complete")
	}
	return nil
}

// requireAgent 校验 Agent 通道请求头 X-Agent-Token。
func (a *App) requireAgent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg == nil || r.Header.Get("X-Agent-Token") != a.cfg.AgentToken() {
			writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "Agent 令牌无效")
			return
		}
		next(w, r)
	}
}

// auditWrap 为审计标注资源类型,resourceName 取自路径 id 或 body。
func (a *App) auditWrap(rtype string, next http.HandlerFunc) http.HandlerFunc {
	return a.withAudit(func(r *http.Request) string { return rtype },
		func(r *http.Request) string {
			if id := r.PathValue("id"); id != "" {
				return id
			}
			if iqn := r.PathValue("iqn"); iqn != "" {
				return iqn
			}
			return r.URL.Path
		}, next)
}

// poller 周期对账:刷新节点状态与资源元数据。
func (a *App) poller(ctx context.Context) {
	tick := time.NewTicker(time.Duration(a.cfg.PollSeconds) * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.pollOnce(ctx)
		}
	}
}

func (a *App) pollOnce(ctx context.Context) {
	nodes, err := a.listNodes(ctx)
	if err != nil {
		return
	}
	for i := range nodes {
		n := &nodes[i]
		online := n.online()
		want := "offline"
		if online {
			want = "online"
		}
		if want != n.Status {
			_, _ = a.db.Exec(ctx, `UPDATE nodes SET status=$1 WHERE id=$2`, want, n.ID)
			n.Status = want
			if !online {
				a.logf("节点 %s 变更为 offline", n.Name)
			}
		}
		if !online {
			continue
		}
		a.collectMetrics(ctx, n)
		if err := a.syncNodeState(ctx, n); err != nil {
			if !a.pollFailed[n.ID] {
				a.logf("节点 %s 状态同步失败: %v", n.Name, err)
				a.pollFailed[n.ID] = true
			}
			continue
		}
		a.pollFailed[n.ID] = false
	}
}

var _ = fmt.Sprintf

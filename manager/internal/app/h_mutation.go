package app

import (
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// 只读节点临时解锁:必须再次校验当前管理员口令,解锁仅存于 Agent 内存,
// TTL 到期自动回锁,Agent 重启也会恢复只读。

func (a *App) handleNodeMutationState(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	st, err := a.agentQuery(r.Context(), n, "mutation_state", nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "读取写保护状态失败: %v", err)
		return
	}
	st["node_id"] = n.ID
	st["node_name"] = n.Name
	writeOK(w, st)
}

type mutationUnlockReq struct {
	Password   string `json:"password"`
	TTLSeconds int    `json:"ttl_s"`
}

func (a *App) handleNodeUnlock(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	u := userFrom(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "未登录")
		return
	}
	var body mutationUnlockReq
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if body.Password == "" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "请输入当前账号口令")
		return
	}
	limKey := "unlock|" + strings.ToLower(u.Username) + "|" + clientIP(r)
	if d := limiter.locked(limKey); d > 0 {
		writeErrK(w, http.StatusTooManyRequests, "RATE_LIMITED", KErrRateLimited,
			map[string]any{"minutes": int(d.Minutes()) + 1},
			"口令尝试过多,请在 %d 分钟后重试", int(d.Minutes())+1)
		return
	}
	var hash string
	var disabled bool
	if err := a.db.QueryRow(r.Context(),
		`SELECT password_hash,disabled FROM users WHERE id=$1`, u.ID).
		Scan(&hash, &disabled); err != nil || disabled {
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "账号不可用")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		limiter.failAdd(limKey)
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "当前账号口令错误")
		return
	}
	limiter.reset(limKey)

	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	ttl := body.TTLSeconds
	if ttl <= 0 {
		ttl = 900
	}
	ttl = max(60, min(ttl, 3600))
	st, err := a.agentOp(r.Context(), n, "mutation_unlock", map[string]any{"ttl_s": ttl})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "解锁失败: %v", err)
		return
	}
	writeOK(w, st)
}

func (a *App) handleNodeLock(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	st, err := a.agentOp(r.Context(), n, "mutation_lock", nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "回锁失败: %v", err)
		return
	}
	writeOK(w, st)
}

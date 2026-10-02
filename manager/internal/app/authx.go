package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// loginLimiter 内存失败计数:防暴力破解(常数时间哈希比较由 bcrypt 提供)。
type loginLimiter struct {
	mu   sync.Mutex
	fail map[string]*failState
}

type failState struct {
	count int
	last  time.Time
	until time.Time
}

var limiter = &loginLimiter{fail: map[string]*failState{}}

const (
	loginMaxFail = 5
	loginWindow  = 5 * time.Minute
)

func (l *loginLimiter) locked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.fail[key]
	if st == nil || st.until.IsZero() || !time.Now().Before(st.until) {
		return 0
	}
	return time.Until(st.until)
}

func (l *loginLimiter) failAdd(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	st := l.fail[key]
	if st == nil {
		st = &failState{}
		l.fail[key] = st
	}
	// 窗口过期则重新计数
	if !st.last.IsZero() && now.Sub(st.last) > loginWindow {
		st.count = 0
	}
	st.count++
	st.last = now
	if st.count >= loginMaxFail {
		st.until = now.Add(loginWindow)
		st.count = 0
	}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.fail, key)
	l.mu.Unlock()
}

type ctxKey int

const userKey ctxKey = 1

var roleRank = map[string]int{"viewer": 1, "operator": 2, "admin": 3}

func userFrom(r *http.Request) *User {
	u, _ := r.Context().Value(userKey).(*User)
	return u
}

// requireAuth: Bearer token → users 表鉴权。
func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if h == "" {
			writeErrK(w, http.StatusUnauthorized, "UNAUTHORIZED", KErrUnauthorized, nil, "缺少访问令牌")
			return
		}
		u, err := a.userByToken(r.Context(), h)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "令牌无效或已过期")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

// hashToken 令牌只存哈希(sha256: 前缀便于识别与幂等迁移)。
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (a *App) userByToken(ctx context.Context, token string) (*User, error) {
	var u User
	err := a.db.QueryRow(ctx, `
		SELECT u.id, u.username, u.role FROM tokens t JOIN users u ON u.id=t.user_id
		WHERE t.token=$1 AND t.expires_at > now() AND NOT u.disabled`, hashToken(token)).
		Scan(&u.ID, &u.Username, &u.Role)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (a *App) newToken(ctx context.Context, uid int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	_, err := a.db.Exec(ctx,
		`INSERT INTO tokens(token,user_id,expires_at) VALUES($1,$2,now()+interval '7 days')`,
		hashToken(tok), uid)
	return tok, err
}

// requireRole 等级检查(> = 允许)。
func (a *App) requireRole(min string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			u := userFrom(r)
			if u == nil || roleRank[u.Role] < roleRank[min] {
				writeErrK(w, http.StatusForbidden, "FORBIDDEN", KErrForbidden, map[string]any{"role": min}, "权限不足:需要 %s 及以上角色", min)
				return
			}
			next(w, r)
		}
	}
}

// handleLogin POST /api/v1/auth/login
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	limKey := strings.ToLower(body.Username) + "|" + clientIP(r)
	if d := limiter.locked(limKey); d > 0 {
		writeErrK(w, http.StatusTooManyRequests, "RATE_LIMITED", KErrRateLimited, map[string]any{"minutes": int(d.Minutes()) + 1},
			"登录尝试过多,请在 %d 分钟后重试", int(d.Minutes())+1)
		return
	}
	var u User
	var hash string
	var disabled bool
	err := a.db.QueryRow(r.Context(),
		`SELECT id,username,role,password_hash,disabled FROM users WHERE username=$1`,
		body.Username).Scan(&u.ID, &u.Username, &u.Role, &hash, &disabled)
	if err == pgx.ErrNoRows || disabled {
		limiter.failAdd(limKey)
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "用户名或密码错误")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		limiter.failAdd(limKey)
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "用户名或密码错误")
		return
	}
	limiter.reset(limKey)
	tok, err := a.newToken(r.Context(), u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	writeOK(w, map[string]any{"token": tok, "user": u})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	writeOK(w, userFrom(r))
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	h := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	_, _ = a.db.Exec(r.Context(), `DELETE FROM tokens WHERE token=$1`, hashToken(h))
	writeOK(w, map[string]any{"ok": true})
}

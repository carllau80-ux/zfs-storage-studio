package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type AuditRec struct {
	Username     string `json:"username"`
	NodeID       int64  `json:"node_id"`
	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceName string `json:"resource_name"`
	Params       string `json:"params"`
	Result       string `json:"result"`
	Detail       string `json:"detail"`
	Duration     int64  `json:"duration_ms"`
}

// withAudit 包装写操作:记录入参/结果/耗时,并做敏感字段脱敏。
func (a *App) withAudit(resourceType, resourceName func(r *http.Request) string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<18))
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := AuditRec{
			Username:     "anon",
			Action:       actionLabel(r.Method, r.URL.Path),
			ResourceType: resourceType(r),
			ResourceName: resourceName(r),
			Params:       string(redactParams(body)),
			Result:       "ok",
		}
		if u := userFrom(r); u != nil {
			rec.Username = u.Username
		}
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next(sw, r)
		if sw.code >= 400 {
			rec.Result = "err"
			rec.Detail = http.StatusText(sw.code)
		}
		rec.Duration = time.Since(start).Milliseconds()
		a.insertAudit(r.Context(), rec)
	}
}

// actionLabel 将 方法+路由 转为人类可读的操作名。
func actionLabel(method, path string) string {
	p := strings.TrimPrefix(path, "/api/v1")
	switch {
	// 数据集 / 卷
	case p == "/datasets" && method == "POST":
		return "创建数据集/卷"
	case p == "/datasets/clone":
		return "从快照克隆创建卷"
	case strings.HasPrefix(p, "/datasets/") && strings.HasSuffix(p, "/format"):
		return "格式化卷"
	case strings.HasPrefix(p, "/datasets/") && strings.HasSuffix(p, "/resize"):
		return "调整卷容量"
	case strings.HasPrefix(p, "/datasets/") && strings.HasSuffix(p, "/properties"):
		return "修改数据集属性"
	case strings.HasPrefix(p, "/datasets/") && strings.HasSuffix(p, "/snapshots") && method == "POST":
		return "创建快照"
	case strings.HasPrefix(p, "/datasets/") && strings.HasSuffix(p, "/rollback"):
		return "回滚到快照"
	case strings.HasPrefix(p, "/datasets/") && method == "DELETE":
		return "删除数据集/卷"
	// 快照
	case strings.HasPrefix(p, "/snapshots/") && method == "DELETE":
		return "删除快照"
	// 池
	case p == "/pools" && method == "POST":
		return "创建存储池"
	case strings.HasPrefix(p, "/pools/") && strings.HasSuffix(p, "/scrub"):
		return "池数据校验(scrub)"
	case strings.HasPrefix(p, "/pools/") && method == "DELETE":
		return "销毁存储池"
	// iSCSI
	case p == "/targets" && method == "POST":
		return "创建 iSCSI 目标"
	case strings.HasPrefix(p, "/targets/") && strings.HasSuffix(p, "/luns") && method == "POST":
		return "挂载 LUN"
	case strings.Contains(p, "/luns/") && method == "DELETE":
		return "移除 LUN"
	case strings.HasSuffix(p, "/acls") && method == "POST":
		return "添加 ACL 授权"
	case strings.Contains(p, "/acls/") && method == "DELETE":
		return "删除 ACL 授权"
	case strings.Contains(p, "/sessions/") && method == "DELETE":
		return "断开会话"
	case strings.HasPrefix(p, "/targets/") && method == "DELETE":
		return "删除 iSCSI 目标"
	// 主机 / 映射
	case p == "/hosts" && method == "POST":
		return "创建主机档案"
	case strings.HasPrefix(p, "/hosts/") && strings.HasSuffix(p, "/initiators") && method == "POST":
		return "录入 Initiator"
	case strings.Contains(p, "/initiators/") && method == "DELETE":
		return "删除 Initiator"
	case strings.HasPrefix(p, "/hosts/") && method == "PATCH":
		return "修改主机档案"
	case strings.HasPrefix(p, "/hosts/") && method == "DELETE":
		return "删除主机档案"
	case p == "/mappings" && method == "POST":
		return "映射卷到主机"
	case strings.HasPrefix(p, "/mappings/") && method == "DELETE":
		return "解除映射"
	// 共享
	case p == "/shares" && method == "POST":
		return "创建文件共享(NFS/SMB)"
	case strings.HasSuffix(p, "/toggle"):
		return "启用/停用文件共享"
	case strings.HasPrefix(p, "/shares/") && method == "DELETE":
		return "删除文件共享"
	// 用户 / 节点
	case p == "/users" && method == "POST":
		return "创建用户"
	case strings.HasPrefix(p, "/users/") && method == "PATCH":
		return "修改用户"
	case strings.HasPrefix(p, "/nodes/") && method == "DELETE":
		return "移除节点"
	case strings.HasSuffix(p, "/logout"):
		return "退出登录"
	}
	return method + " " + path
}

type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.code = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

var redactedKeys = []string{"password", "chap_secret", "secret", "token"}

func redactParams(body []byte) []byte {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	for _, k := range redactedKeys {
		if _, ok := m[k]; ok {
			m[k] = "***"
		}
	}
	b, _ := json.Marshal(m)
	if len(b) > 2048 {
		b = b[:2048]
	}
	return b
}

func (a *App) insertAudit(ctx context.Context, rec AuditRec) {
	var node any
	if rec.NodeID > 0 {
		node = rec.NodeID
	}
	_, err := a.db.Exec(ctx, `
		INSERT INTO audit_logs(username,node_id,action,resource_type,resource_name,params_json,result,detail,duration_ms)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		rec.Username, node, rec.Action, rec.ResourceType, rec.ResourceName, rec.Params,
		rec.Result, rec.Detail, rec.Duration)
	if err != nil {
		a.logf("audit insert 失败: %v", err)
	}
}

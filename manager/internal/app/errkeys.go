package app

import (
	"fmt"
	"net/http"
)

// 错误码协议(基线):信封含 code + key + params,前端按语言渲染文案。
// message 仍返回中文,保证旧客户端与排障可用。
const (
	KErrValidation   = "validation.invalid"
	KErrNotFound     = "resource.not_found"
	KErrConflict     = "resource.conflict"
	KErrUnauthorized = "auth.unauthorized"
	KErrForbidden    = "auth.forbidden"
	KErrRateLimited  = "auth.rate_limited"
	KErrNodeOffline  = "node.offline"
	KErrFcUnavail    = "transport.fc_unavailable"
	KErrConfirmName  = "confirm.require_name"
	KErrZvolOnly     = "target.zvol_only"
	KErrDuplicate    = "resource.duplicate"
	KErrAgent        = "agent.error"
	KErrInternal     = "internal.error"
)

// writeErrK 带 key/params 的错误响应;message 用中文格式串生成。
func writeErrK(w http.ResponseWriter, status int, code, key string, params map[string]any,
	format string, args ...any) {
	if params == nil {
		params = map[string]any{}
	}
	writeJSON(w, status, map[string]any{
		"ok": false,
		"error": apiErr{
			Code:    code,
			Key:     key,
			Params:  params,
			Message: fmt.Sprintf(format, args...),
		},
	})
}

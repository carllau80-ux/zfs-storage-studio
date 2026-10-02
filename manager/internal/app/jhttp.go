package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type apiErr struct {
	Code    string         `json:"code"`
	Key     string         `json:"key,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message"`
}

// writeJSON / readJSON / 错误响应等 HTTP 助手。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, format string, args ...any) {
	writeJSON(w, status, map[string]any{
		"ok": false, "error": apiErr{Code: code, Message: fmt.Sprintf(format, args...)},
	})
}

func writeOK(w http.ResponseWriter, v any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": v})
}

var ErrBadBody = errors.New("请求体解析失败")

func readJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrBadBody, err)
	}
	return nil
}

// parseID 解析路径中的数值 ID。
func parseID(r *http.Request, name string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(r.PathValue(name), "%d", &id)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("无效的 %s: %s", name, r.PathValue(name))
	}
	return id, nil
}

var (
	reName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`) // zpool/zvol/主机名
	reIQN     = regexp.MustCompile(`^iqn\.[0-9]{4}-[0-9]{2}\.[A-Za-z0-9.-]+(:[A-Za-z0-9._:-]+)?$`)
	reSize    = regexp.MustCompile(`^(?i)(\d+(\.\d+)?)([kKmMgGtTpPeE])?$`)
	reDataset = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+$`) // pool/child...
	reSnap    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
	reDisk    = regexp.MustCompile(`^/dev/[A-Za-z0-9_/-]+$`)
	reAddr    = regexp.MustCompile(`^[A-Za-z0-9.:\[\]-]+$`) // agent_addr host:port
	reMount   = regexp.MustCompile(`^/[A-Za-z0-9_./\-]+$`)  // 文件系统挂载点
)

var reWWN = regexp.MustCompile(`^(?i)(0x)?([0-9a-f]{16})$`)

// normalizeWWN 归一化 WWPN 为小写 0x 前缀形式。
func normalizeWWN(v string) (string, bool) {
	m := reWWN.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "", false
	}
	return "0x" + strings.ToLower(m[2]), true
}

// parseSize 解析 "20G"/"1.5T"/裸字节。
func parseSize(s string) (int64, error) {
	m := reSize.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("非法容量: %q (示例: 1G / 512M / 1048576)", s)
	}
	var f float64
	if _, err := fmt.Sscanf(m[1], "%f", &f); err != nil {
		return 0, err
	}
	unit := strings.ToUpper(m[3])
	mult := int64(1)
	switch unit {
	case "", "B":
	case "K":
		mult = 1 << 10
	case "M":
		mult = 1 << 20
	case "G":
		mult = 1 << 30
	case "T":
		mult = 1 << 40
	case "P":
		mult = 1 << 50
	case "E":
		mult = 1 << 60
	default:
		return 0, fmt.Errorf("非法单位: %s", unit)
	}
	return int64(f * float64(mult)), nil
}

// humanBytes 统一以 GiB 展示(一位小数,页面容量口径一致)。
func humanBytes(b int64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}

func dbColsNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

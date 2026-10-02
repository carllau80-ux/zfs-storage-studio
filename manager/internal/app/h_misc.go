package app

import (
	"encoding/json"
	"net/http"
	"strconv"

	"golang.org/x/crypto/bcrypt"
)

// handleUsersList(admin)
func (a *App) handleUsersList(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `
		SELECT id,username,role,disabled,created_at::text FROM users ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	type item struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		Role      string `json:"role"`
		Disabled  bool   `json:"disabled"`
		CreatedAt string `json:"created_at"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Username, &it.Role, &it.Disabled, &it.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		out = append(out, it)
	}
	writeOK(w, map[string]any{"users": out})
}

func (a *App) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if !reName.MatchString(body.Username) || len(body.Password) < 6 {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "用户名/口令不合规(口令 ≥ 6 位)")
		return
	}
	if _, ok := roleRank[body.Role]; !ok {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "角色须为 admin|operator|viewer")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "CRYPTO", "%v", err)
		return
	}
	var id int64
	err = a.db.QueryRow(r.Context(), `
		INSERT INTO users(username,password_hash,role) VALUES($1,$2,$3) RETURNING id`,
		body.Username, string(h), body.Role).Scan(&id)
	if err != nil {
		writeErr(w, http.StatusConflict, "CONFLICT", "用户名已存在: %v", err)
		return
	}
	writeOK(w, map[string]any{"user_id": id})
}

func (a *App) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	me := userFrom(r)
	var body struct {
		Password *string `json:"password"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	// 自我保护:允许修改自己的口令,但禁止修改自己的角色/启用状态
	if me != nil && me.ID == id && (body.Role != nil || body.Disabled != nil) {
		writeErrK(w, http.StatusConflict, "CONFLICT", KErrForbidden, nil,
			"不能修改自己的角色或账号状态")
		return
	}
	if body.Role != nil {
		if _, ok := roleRank[*body.Role]; !ok {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "角色须为 admin|operator|viewer")
			return
		}
		if _, err := a.db.Exec(r.Context(), `UPDATE users SET role=$2 WHERE id=$1`, id, *body.Role); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
	}
	if body.Password != nil {
		if len(*body.Password) < 6 {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "口令至少 6 位")
			return
		}
		h, err := bcrypt.GenerateFromPassword([]byte(*body.Password), bcrypt.DefaultCost)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "CRYPTO", "%v", err)
			return
		}
		if _, err := a.db.Exec(r.Context(), `UPDATE users SET password_hash=$2 WHERE id=$1`, id, string(h)); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
	}
	if body.Disabled != nil {
		if _, err := a.db.Exec(r.Context(), `UPDATE users SET disabled=$2 WHERE id=$1`, id, *body.Disabled); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
	}
	writeOK(w, map[string]any{"updated": id})
}

// handleDashboard 聚合总览。
func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	type cnt struct {
		n  int64
		sq string
	}
	query := func(sql string) int64 {
		var n int64
		_ = a.db.QueryRow(r.Context(), sql).Scan(&n)
		return n
	}
	nodes := query(`SELECT count(*) FROM nodes`)
	online := query(`SELECT count(*) FROM nodes WHERE status='online'`)
	pools := query(`SELECT count(*) FROM pools`)
	volumes := query(`SELECT count(*) FROM datasets WHERE type='volume'`)
	snaps := query(`SELECT count(*) FROM snapshots`)
	targets := query(`SELECT count(*) FROM targets`)
	hosts := query(`SELECT count(*) FROM hosts`)
	mappings := query(`SELECT count(*) FROM mappings`)
	a.mu.Lock()
	sessionCnt := 0
	for _, sl := range a.sessionSnap {
		sessionCnt += len(sl)
	}
	a.mu.Unlock()
	// 容量聚合
	var totalSize, totalAlloc int64
	_ = a.db.QueryRow(r.Context(),
		`SELECT COALESCE(sum(size),0), COALESCE(sum(allocated),0) FROM pools`).
		Scan(&totalSize, &totalAlloc)
	// 告警
	alerts := []map[string]any{}
	offRows, _ := a.db.Query(r.Context(), `SELECT name FROM nodes WHERE status='offline'`)
	for offRows != nil && offRows.Next() {
		var nm string
		if offRows.Scan(&nm) == nil {
			alerts = append(alerts, map[string]any{"level": "warn", "text": "节点离线: " + nm})
		}
	}
	if offRows != nil {
		offRows.Close()
	}
	badRows, _ := a.db.Query(r.Context(),
		`SELECT n.name,p.name,p.health FROM pools p JOIN nodes n ON n.id=p.node_id
		 WHERE p.health<>'ONLINE' OR p.state<>'ONLINE'`)
	for badRows != nil && badRows.Next() {
		var nn, pn, hh string
		if badRows.Scan(&nn, &pn, &hh) == nil {
			alerts = append(alerts, map[string]any{"level": "error", "text": nn + " / " + pn + " 健康状态: " + hh})
		}
	}
	if badRows != nil {
		badRows.Close()
	}
	// 最近审计
	recent := []map[string]any{}
	arows, _ := a.db.Query(r.Context(), `
		SELECT username,action,resource_name,result,created_at::text FROM audit_logs
		ORDER BY id DESC LIMIT 10`)
	for arows != nil && arows.Next() {
		var u, act, res, rn, ts string
		if arows.Scan(&u, &act, &rn, &res, &ts) == nil {
			recent = append(recent, map[string]any{
				"username": u, "action": act, "resource": rn, "result": res, "at": ts,
			})
		}
	}
	if arows != nil {
		arows.Close()
	}
	// 平台能力(静态特性 + 在线节点能力汇总)
	type nodeCap struct {
		Name    string `json:"name"`
		Online  bool   `json:"online"`
		FcOK    bool   `json:"fc_capable"`
		Mkfs    any    `json:"mkfs"`
		Archive bool   `json:"snapshot_supported"`
	}
	nodeCaps := []map[string]any{}
	if nrows, err := a.db.Query(r.Context(), `SELECT name,capabilities_json,status FROM nodes ORDER BY id`); err == nil {
		for nrows.Next() {
			var nm, caps, st string
			if nrows.Scan(&nm, &caps, &st) == nil {
				var cm map[string]any
				_ = json.Unmarshal([]byte(caps), &cm)
				if s2, ok := cm["os"].(string); ok && s2 != "" {
					_ = s2
				}
				mkfs := map[string]any{}
				if m, ok := cm["mkfs"].(map[string]any); ok {
					mkfs = m
				}
				fc := false
				reason := ""
				if f, ok := cm["fc"].(map[string]any); ok {
					fc, _ = f["fc_capable"].(bool)
					reason, _ = f["reason"].(string)
				}
				nodeCaps = append(nodeCaps, map[string]any{
					"name": nm, "online": st == "online", "fc_capable": fc, "fc_reason": reason, "mkfs": mkfs,
				})
			}
		}
		nrows.Close()
	}
	capabilities := map[string]any{
		"pool_layouts":     []string{"stripe 条带", "mirror 镜像", "raidz1", "raidz2", "raidz3", "slog(log)", "cache(L2ARC)"},
		"dataset_features": []string{"zvol 块卷", "filesystem 文件系统", "快照", "克隆(zfs clone)", "压缩 lz4/zstd/gzip", "重删 dedup on/verify", "属性继承与配置"},
		"block_protocols":  []string{"iSCSI(Target/LUN/ACL/CHAP)"},
		"file_protocols":   []string{"NFS(v3/v4,exportfs)", "SMB/CIFS(Samba)"},
		"fs_types":         []string{"ext4", "xfs", "ntfs"},
		"nodes":            nodeCaps,
	}
	writeOK(w, map[string]any{
		"capabilities": capabilities,
		"metrics":      a.metricsSnapshot(),
		"summary": map[string]any{
			"nodes": nodes, "nodes_online": online, "nodes_offline": nodes - online,
			"pools": pools, "volumes": volumes, "snapshots": snaps,
			"targets": targets, "sessions": sessionCnt, "hosts": hosts, "mappings": mappings,
			"total_size_human": humanBytes(totalSize), "allocated_human": humanBytes(totalAlloc),
		},
		"alerts": alerts, "recent_audits": recent,
	})
}

// handleAuditList 审计日志查询。
func (a *App) handleAuditList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := " WHERE id>0"
	args := []any{}
	add := func(cond string) {
		args = append(args, q.Get(cond))
		where += " AND " + cond + "=$" + itoa(len(args))
	}
	if v := q.Get("username"); v != "" {
		add("username")
	}
	if v := q.Get("result"); v != "" {
		add("result")
	}
	if v := q.Get("resource_type"); v != "" {
		add("resource_type")
	}
	if v := q.Get("node_id"); v != "" {
		add("node_id")
	}
	limit := int64(50)
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	offset := int64(0)
	if v := q.Get("offset"); v != "" {
		offset, _ = strconv.ParseInt(v, 10, 64)
	}
	rows, err := a.db.Query(r.Context(), `
		SELECT id,username,COALESCE(node_id,0),action,resource_type,resource_name,
		       params_json,result,detail,duration_ms,created_at::text
		FROM audit_logs`+where+` ORDER BY id DESC LIMIT $`+itoa(len(args)+1)+
		` OFFSET $`+itoa(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	type item struct {
		ID           int64  `json:"id"`
		Username     string `json:"username"`
		NodeID       int64  `json:"node_id"`
		Action       string `json:"action"`
		ResourceType string `json:"resource_type"`
		ResourceName string `json:"resource_name"`
		Params       string `json:"params"`
		Result       string `json:"result"`
		Detail       string `json:"detail"`
		DurationMs   int64  `json:"duration_ms"`
		CreatedAt    string `json:"created_at"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Username, &it.NodeID, &it.Action, &it.ResourceType,
			&it.ResourceName, &it.Params, &it.Result, &it.Detail, &it.DurationMs, &it.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		out = append(out, it)
	}
	var total int64
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM audit_logs`+where, args...).Scan(&total)
	writeOK(w, map[string]any{"audit_logs": out, "total": total})
}

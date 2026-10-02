package app

import (
	"encoding/json"
	"net/http"
	"time"
)

type agentRegisterReq struct {
	NodeName      string          `json:"node_name"`
	AgentAddr     string          `json:"agent_addr"`
	AdvertiseAddr string          `json:"advertise_addr"`
	AgentVer      string          `json:"agent_version"`
	ZfsVer        string          `json:"zfs_version"`
	Capabilities  json.RawMessage `json:"capabilities"`
}

// handleAgentRegister Agent 启动注册(名称幂等,重复注册即续期)。
func (a *App) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	var req agentRegisterReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if !reName.MatchString(req.NodeName) || !reAddr.MatchString(req.AgentAddr) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "node_name/agent_addr 非法")
		return
	}
	caps := string(req.Capabilities)
	if caps == "" {
		caps = "{}"
	}
	var id int64
	err := a.db.QueryRow(r.Context(), `
		INSERT INTO nodes(name,agent_addr,advertise_addr,agent_version,zfs_version,capabilities_json,status,last_seen_at)
		VALUES($1,$2,$3,$4,$5,$6,'online',now())
		ON CONFLICT (name) DO UPDATE SET
		  agent_addr=EXCLUDED.agent_addr, advertise_addr=EXCLUDED.advertise_addr,
		  agent_version=EXCLUDED.agent_version, zfs_version=EXCLUDED.zfs_version,
		  capabilities_json=EXCLUDED.capabilities_json, status='online', last_seen_at=now()
		RETURNING id`,
		req.NodeName, req.AgentAddr, req.AdvertiseAddr, req.AgentVer, req.ZfsVer, caps).Scan(&id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	a.logf("Agent 注册: %s (%s) id=%d", req.NodeName, req.AgentAddr, id)
	writeOK(w, map[string]any{"node_id": id})
}

func (a *App) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	ct, err := a.db.Exec(r.Context(),
		`UPDATE nodes SET status='online', last_seen_at=now() WHERE id=$1`, id)
	if err != nil || ct.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "节点未注册")
		return
	}
	writeOK(w, map[string]any{"server_time": time.Now().UTC().Format(time.RFC3339)})
}

// handleNodesList 节点列表(含资源计数)。
func (a *App) handleNodesList(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `
		SELECT n.id,n.name,n.agent_addr,n.advertise_addr,n.agent_version,n.zfs_version,n.status,n.last_seen_at,
		  (SELECT count(*) FROM pools p WHERE p.node_id=n.id),
		  (SELECT count(*) FROM datasets d WHERE d.node_id=n.id AND d.type='volume'),
		  (SELECT count(*) FROM targets t WHERE t.node_id=n.id)
		FROM nodes n ORDER BY n.id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	type item struct {
		ID            int64      `json:"id"`
		Name          string     `json:"name"`
		AgentAddr     string     `json:"agent_addr"`
		AdvertiseAddr string     `json:"advertise_addr"`
		AgentVer      string     `json:"agent_version"`
		ZfsVer        string     `json:"zfs_version"`
		Status        string     `json:"status"`
		LastSeenAt    *time.Time `json:"last_seen_at"`
		PoolCount     int64      `json:"pool_count"`
		VolumeCount   int64      `json:"volume_count"`
		TargetCount   int64      `json:"target_count"`
		Online        bool       `json:"online"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Name, &it.AgentAddr, &it.AdvertiseAddr, &it.AgentVer,
			&it.ZfsVer, &it.Status, &it.LastSeenAt, &it.PoolCount, &it.VolumeCount, &it.TargetCount); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		it.Online = it.LastSeenAt != nil && time.Since(*it.LastSeenAt) < onlineAfter
		out = append(out, it)
	}
	writeOK(w, map[string]any{"nodes": out})
}

func (a *App) handleNodeGet(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	var caps any = map[string]any{}
	_ = json.Unmarshal([]byte(n.CapJSON), &caps)
	writeOK(w, map[string]any{
		"id": n.ID, "name": n.Name, "agent_addr": n.AgentAddr,
		"agent_version": n.AgentVer, "zfs_version": n.ZfsVer,
		"status": n.Status, "online": n.online(), "last_seen_at": n.LastSeen,
		"capabilities": caps,
	})
}

func (a *App) handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	if n.online() {
		writeErr(w, http.StatusConflict, "CONFLICT", "节点在线,请先停用其 Agent 后再移除")
		return
	}
	_, err = a.db.Exec(r.Context(), `DELETE FROM nodes WHERE id=$1`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	a.mu.Lock()
	delete(a.sessionSnap, id)
	a.mu.Unlock()
	writeOK(w, map[string]any{"deleted": id})
}

func (a *App) handleNodeCapabilities(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	writeOK(w, map[string]any{"node_id": n.ID, "capabilities": decodeCapabilities(n.CapJSON)})
}

// nodeCapMap 解析节点能力 JSON 为 map。
func (a *App) nodeCapMap(n *NodeRow) map[string]any {
	if m, ok := decodeCapabilities(n.CapJSON).(map[string]any); ok {
		return m
	}
	return nil
}

// decodeCapabilities 兼容早期"JSON 字符串内嵌 JSON"的存储格式。
func decodeCapabilities(raw string) any {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		return m
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		var m2 map[string]any
		if json.Unmarshal([]byte(s), &m2) == nil {
			return m2
		}
	}
	return map[string]any{}
}

// handleNodeSessions 节点活动会话(实时向 Agent 采集)。
func (a *App) handleNodeSessions(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	m, err := a.agentQuery(r.Context(), n, "sessions", map[string]any{})
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	list, _ := m["sessions"].([]any)
	out := []SessionLive{}
	for _, it := range list {
		row, _ := it.(map[string]any)
		out = append(out, SessionLive{
			TargetName: strAny(row["target_name"]),
			IQN:        strAny(row["iqn"]),
			SID:        intAny(row["sid"]),
			State:      strAny(row["state"]),
			Type:       strAny(row["type"]),
			Address:    strAny(row["address"]),
			Alias:      strAny(row["alias"]),
		})
	}
	a.mu.Lock()
	a.sessionSnap[n.ID] = out
	a.mu.Unlock()
	writeOK(w, map[string]any{"sessions": out})
}

// handleNodeDisks 节点磁盘清单(建池选盘)。
func (a *App) handleNodeDisks(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	m, err := a.agentQuery(r.Context(), n, "disks", nil)
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	writeOK(w, m)
}

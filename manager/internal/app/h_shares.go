package app

import (
	"encoding/json"
	"net/http"
)

type shareRow struct {
	ID         int64  `json:"id"`
	NodeID     int64  `json:"node_id"`
	NodeName   string `json:"node_name"`
	DatasetID  int64  `json:"dataset_id"`
	Dataset    string `json:"dataset"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	ConfigJSON string `json:"-"`
	Config     any    `json:"config"`
	Enabled    bool   `json:"enabled"`
}

const shareSelect = `
	SELECT s.id,s.node_id,n.name,s.dataset_id,d.name,s.type,s.name,s.config_json,s.enabled
	FROM shares s JOIN nodes n ON n.id=s.node_id JOIN datasets d ON d.id=s.dataset_id`

func scanShare(scan func(...any) error) (shareRow, error) {
	var r shareRow
	err := scan(&r.ID, &r.NodeID, &r.NodeName, &r.DatasetID, &r.Dataset, &r.Type, &r.Name, &r.ConfigJSON, &r.Enabled)
	if err == nil {
		var cfg any
		_ = json.Unmarshal([]byte(r.ConfigJSON), &cfg)
		r.Config = cfg
	}
	return r, err
}

func (a *App) handleSharesList(w http.ResponseWriter, r *http.Request) {
	where, args := "", []any{}
	if nid := r.URL.Query().Get("node_id"); nid != "" {
		args = append(args, nid)
		where = " WHERE s.node_id=$1"
	}
	rows, err := a.db.Query(r.Context(), shareSelect+where+" ORDER BY s.id", args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	out := []shareRow{}
	for rows.Next() {
		sh, err := scanShare(rows.Scan)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		out = append(out, sh)
	}
	writeOK(w, map[string]any{"shares": out})
}

type shareCreateReq struct {
	NodeID     int64  `json:"node_id"`
	DatasetID  int64  `json:"dataset_id"`
	Type       string `json:"type"` // nfs | smb
	Name       string `json:"name"`
	Clients    string `json:"clients"`     // NFS
	Access     string `json:"access"`      // rw|ro
	SyncMode   string `json:"sync_mode"`   // sync|async
	Squash     string `json:"squash"`      // root_squash|no_root_squash
	Subtree    string `json:"subtree"`     // no_subtree_check|subtree_check
	ReadOnly   bool   `json:"read_only"`   // SMB
	GuestOK    bool   `json:"guest_ok"`    // SMB
	ValidUsers string `json:"valid_users"` // SMB
}

func (a *App) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	var req shareCreateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if req.Type != "nfs" && req.Type != "smb" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "type 须为 nfs|smb")
		return
	}
	row := a.db.QueryRow(r.Context(), dsSelect+` WHERE d.id=$1`, req.DatasetID)
	ds, err := scanDatasetRow(row.Scan)
	if err != nil || ds.NodeID != req.NodeID {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "数据集不存在或不属于该节点")
		return
	}
	if ds.Type != "filesystem" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "共享需建立在文件系统数据集上(zvol 请先格式化并挂载或改用 filesystem 类型)")
		return
	}
	name := req.Name
	if name == "" {
		name = shortName(ds.Name)
	}
	if !reName.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "共享名非法")
		return
	}
	n := a.getNodeOrErr(w, r, req.NodeID)
	if n == nil {
		return
	}
	var res map[string]any
	switch req.Type {
	case "nfs":
		if req.Clients == "" {
			req.Clients = "*"
		}
		params := map[string]any{"dataset": ds.Name, "action": "add", "clients": req.Clients,
			"access": orDefault(req.Access, "rw"), "sync_mode": orDefault(req.SyncMode, "sync"),
			"squash": orDefault(req.Squash, "root_squash"), "subtree": orDefault(req.Subtree, "no_subtree_check")}
		res, err = a.agentOp(r.Context(), n, "share_nfs_apply", params)
	case "smb":
		res, err = a.agentOp(r.Context(), n, "share_smb_apply", map[string]any{
			"dataset": ds.Name, "action": "add", "name": name,
			"read_only": req.ReadOnly, "guest_ok": req.GuestOK, "valid_users": req.ValidUsers,
		})
	}
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	cfg, _ := json.Marshal(req)
	var id int64
	err = a.db.QueryRow(r.Context(), `
		INSERT INTO shares(node_id,dataset_id,type,name,config_json) VALUES($1,$2,$3,$4,$5) RETURNING id`,
		req.NodeID, req.DatasetID, req.Type, name, string(cfg)).Scan(&id)
	if err != nil {
		writeErr(w, http.StatusConflict, "CONFLICT", "共享已存在或写入失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"share_id": id, "name": name, "detail": res})
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func (a *App) handleShareDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	sh, err := scanShare(a.db.QueryRow(r.Context(), shareSelect+` WHERE s.id=$1`, id).Scan)
	if err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "共享不存在")
		return
	}
	n := a.getNodeOrErr(w, r, sh.NodeID)
	if n == nil {
		return
	}
	if sh.Type == "nfs" {
		_, err = a.agentOp(r.Context(), n, "share_nfs_apply", map[string]any{"dataset": sh.Dataset, "action": "remove"})
	} else {
		_, err = a.agentOp(r.Context(), n, "share_smb_apply", map[string]any{"dataset": sh.Dataset, "action": "remove", "name": sh.Name})
	}
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `DELETE FROM shares WHERE id=$1`, id)
	writeOK(w, map[string]any{"deleted": id})
}

func (a *App) handleShareToggle(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	sh, err := scanShare(a.db.QueryRow(r.Context(), shareSelect+` WHERE s.id=$1`, id).Scan)
	if err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "共享不存在")
		return
	}
	var req shareCreateReq
	_ = json.Unmarshal([]byte(sh.ConfigJSON), &req)
	n := a.getNodeOrErr(w, r, sh.NodeID)
	if n == nil {
		return
	}
	action := "add"
	if !body.Enabled {
		action = "remove"
	}
	var res map[string]any
	if sh.Type == "nfs" {
		res, err = a.agentOp(r.Context(), n, "share_nfs_apply", map[string]any{"dataset": sh.Dataset, "action": action,
			"clients": orDefault(req.Clients, "*"), "access": orDefault(req.Access, "rw"),
			"sync_mode": orDefault(req.SyncMode, "sync"), "squash": orDefault(req.Squash, "root_squash"),
			"subtree": orDefault(req.Subtree, "no_subtree_check")})
	} else {
		res, err = a.agentOp(r.Context(), n, "share_smb_apply", map[string]any{"dataset": sh.Dataset, "action": action,
			"name": sh.Name, "read_only": req.ReadOnly, "guest_ok": req.GuestOK, "valid_users": req.ValidUsers})
	}
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE shares SET enabled=$2, updated_at=now() WHERE id=$1`, id, body.Enabled)
	writeOK(w, map[string]any{"share_id": id, "enabled": body.Enabled, "detail": res})
}

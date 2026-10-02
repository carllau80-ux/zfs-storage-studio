package app

import (
	"net/http"
)

// handleMappingsList 映射列表(按主机/卷双向查询)。
func (a *App) handleMappingsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := " WHERE m.id>0"
	args := []any{}
	if hid := q.Get("host_id"); hid != "" {
		args = append(args, hid)
		where += " AND m.host_id=$" + itoa(len(args))
	}
	if did := q.Get("dataset_id"); did != "" {
		args = append(args, did)
		where += " AND m.dataset_id=$" + itoa(len(args))
	}
	if nid := q.Get("node_id"); nid != "" {
		args = append(args, nid)
		where += " AND m.node_id=$" + itoa(len(args))
	}
	rows, err := a.db.Query(r.Context(), `
		SELECT m.id,m.host_id,h.name,m.node_id,n.name,m.target_id,t.target_name,t.transport,
		       m.dataset_id,d.name,m.rw,m.enabled,m.created_at,l.lun_id
		FROM mappings m
		JOIN hosts h ON h.id=m.host_id
		JOIN nodes n ON n.id=m.node_id
		JOIN targets t ON t.id=m.target_id
		JOIN datasets d ON d.id=m.dataset_id
		JOIN luns l ON l.target_id=m.target_id AND l.dataset_id=m.dataset_id
		`+where+` ORDER BY m.id`, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	type item struct {
		ID        int64  `json:"id"`
		HostID    int64  `json:"host_id"`
		Host      string `json:"host"`
		NodeID    int64  `json:"node_id"`
		NodeName  string `json:"node_name"`
		TargetID  int64  `json:"target_id"`
		IQN       string `json:"iqn"`
		Transport string `json:"transport"`
		DatasetID int64  `json:"dataset_id"`
		Dataset   string `json:"dataset"`
		RW        string `json:"rw"`
		Enabled   bool   `json:"enabled"`
		LunID     int64  `json:"lun_id"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.HostID, &it.Host, &it.NodeID, &it.NodeName,
			&it.TargetID, &it.IQN, &it.Transport, &it.DatasetID, &it.Dataset, &it.RW, &it.Enabled,
			newTimePtr(), &it.LunID); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		out = append(out, it)
	}
	writeOK(w, map[string]any{"mappings": out})
}

// handleMappingCreate 卷 → 主机 一键映射(一卷一 Target 策略):
// 复用卷所在 Target,否则自动创建;为该主机全部 Initiator 加 ACL。
func (a *App) handleMappingCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HostID    int64  `json:"host_id"`
		DatasetID int64  `json:"dataset_id"`
		RW        string `json:"rw"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if body.RW == "" {
		body.RW = "rw"
	}
	if body.RW != "rw" && body.RW != "ro" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "rw 须为 rw|ro")
		return
	}
	// 主机及其 initiator
	var hostName string
	var hostExists bool
	if err := a.db.QueryRow(r.Context(), `SELECT name FROM hosts WHERE id=$1`, body.HostID).
		Scan(&hostName); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "主机不存在")
		return
	}
	_ = hostExists
	inits, err := a.initiatorsOf(r, body.HostID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	if len(inits) == 0 {
		writeErr(w, http.StatusConflict, "CONFLICT", "Initiator 档案 %s 尚无客户端标识(IQN/WWPN),请先录入", hostName)
		return
	}
	iscsiInits := make([]initiatorRow, 0, len(inits))
	for _, it := range inits {
		if it.Transport != "fc" {
			iscsiInits = append(iscsiInits, it)
		}
	}
	if len(iscsiInits) == 0 {
		writeErrK(w, http.StatusConflict, "FC_UNAVAILABLE", KErrFcUnavail, nil,
			"该 Initiator 仅含 FC WWPN;FC 映射需目标节点具备兼容 QLogic HBA(当前不可用)")
		return
	}
	inits = iscsiInits
	// 卷
	row := a.db.QueryRow(r.Context(), dsSelect+` WHERE d.id=$1`, body.DatasetID)
	ds, err := scanDatasetRow(row.Scan)
	if err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "卷不存在")
		return
	}
	if ds.Type != "volume" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "仅 zvol 可映射")
		return
	}
	// 防重复
	var dup bool
	_ = a.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM mappings WHERE host_id=$1 AND dataset_id=$2)`,
		body.HostID, body.DatasetID).Scan(&dup)
	if dup {
		writeErrK(w, http.StatusConflict, "CONFLICT", KErrDuplicate, map[string]any{"dataset": ds.Name}, "该主机已映射此卷(每主机一卷一 Target)")
		return
	}
	n := a.getNodeOrErr(w, r, ds.NodeID)
	if n == nil {
		return
	}
	// 目标:复用该卷已挂载的 Target,否则自动建
	var targetID int64
	var iqn string
	err = a.db.QueryRow(r.Context(), `
		SELECT t.id,t.target_name FROM targets t
		JOIN luns l ON l.target_id=t.id
		WHERE t.node_id=$1 AND l.dataset_id=$2 LIMIT 1`,
		ds.NodeID, ds.ID).Scan(&targetID, &iqn)
	if err != nil {
		iqn = a.genIQN(n.Name, shortName(ds.Name)+"-h"+itoa(int(body.HostID)))
		if _, err := a.agentOp(r.Context(), n, "lio_target_create", map[string]any{
			"iqn": iqn, "dataset": ds.Name}); err != nil {
			writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
			return
		}
	} else {
		// 校验该卷在当前 Target 之外未被占(一卷一 Target)
		var extra int64
		_ = a.db.QueryRow(r.Context(), `
			SELECT count(*) FROM luns l2 JOIN targets t2 ON t2.id=l2.target_id
			WHERE l2.dataset_id=$1 AND t2.id<>$2`, ds.ID, targetID).Scan(&extra)
		if extra > 0 {
			writeErr(w, http.StatusConflict, "CONFLICT", "卷已存在于多个 Target,不满足一卷一 Target 策略")
			return
		}
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("映射后 iSCSI 同步失败: %v", err)
	}
	// 重查 target
	if targetID == 0 {
		_ = a.db.QueryRow(r.Context(),
			`SELECT id FROM targets WHERE node_id=$1 AND target_name=$2`, ds.NodeID, iqn).Scan(&targetID)
	}
	// 为该主机所有 initiator 添加 ACL(带其 CHAP)
	for _, ini := range inits {
		secret, _ := a.chapDecrypt("") // initiatorsOf 不含密文;重新取
		_ = secret
		params := map[string]any{"iqn": iqn, "initiator": ini.IQN}
		if ini.ChapUser != "" {
			var enc string
			if err := a.db.QueryRow(r.Context(),
				`SELECT chap_secret_enc FROM initiators WHERE id=$1`, ini.ID).Scan(&enc); err == nil {
				if sec, err := a.chapDecrypt(enc); err == nil && sec != "" {
					params["chap_user"] = ini.ChapUser
					params["chap_secret"] = sec
				}
			}
		}
		if _, err := a.agentOp(r.Context(), n, "lio_acl_add", params); err != nil {
			writeErr(w, http.StatusConflict, "AGENT_ERROR", "为主机 %s 加 ACL(%s)失败: %v", hostName, ini.IQN, err)
			return
		}
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("映射 ACL 后同步失败: %v", err)
	}
	var mid int64
	if err := a.db.QueryRow(r.Context(), `
		INSERT INTO mappings(host_id,node_id,target_id,dataset_id,rw) VALUES($1,$2,$3,$4,$5)
		RETURNING id`, body.HostID, ds.NodeID, targetID, ds.ID, body.RW).Scan(&mid); err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	writeOK(w, map[string]any{
		"mapping_id": mid, "host": hostName, "dataset": ds.Name,
		"iqn": iqn, "lun_id": 0,
		"portal": hostOfAddr(n.PortalHost()) + ":3260",
		"auth": map[string]any{
			"has_chap": len(inits) > 0 && inits[0].ChapUser != "",
			"user":     firstChapUser(inits),
		},
	})
}

func firstChapUser(inits []initiatorRow) string {
	for _, i := range inits {
		if i.ChapUser != "" {
			return i.ChapUser
		}
	}
	return ""
}

// hostOfAddr 从 agent_addr(host:port)提取主机 IP。
func hostOfAddr(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}

// handleMappingDelete 解除映射:摘 ACL;若 Target 专用于该映射且无其它 ACL/映射则删除 Target。
func (a *App) handleMappingDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var hostID, nodeID, targetID, datasetID int64
	var iqn, dsName string
	if err := a.db.QueryRow(r.Context(), `
		SELECT m.host_id,m.node_id,m.target_id,m.dataset_id,t.target_name,d.name
		FROM mappings m
		JOIN targets t ON t.id=m.target_id
		JOIN datasets d ON d.id=m.dataset_id
		WHERE m.id=$1`, id).Scan(&hostID, &nodeID, &targetID, &datasetID, &iqn, &dsName); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "映射不存在")
		return
	}
	inits, err := a.initiatorsOf(r, hostID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	// 摘除该主机 initiator 的 ACL(失败即中断,保证授权真实撤销)
	for _, ini := range inits {
		if _, err := a.agentOp(r.Context(), n, "lio_acl_remove", map[string]any{"iqn": iqn, "initiator": ini.IQN}); err != nil {
			writeErr(w, http.StatusConflict, "AGENT_ERROR", "撤销 ACL(%s)失败: %v", ini.IQN, err)
			return
		}
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("解映射 ACL 同步失败: %v", err)
	}
	// 若该 Target 无剩余 ACL 且无其它映射 → 删除 Target(其 LUN/backstore 一并清理)
	var otherAcls, otherMappings int64
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM acls WHERE target_id=$1`, targetID).Scan(&otherAcls)
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM mappings WHERE target_id=$1 AND id<>$2`,
		targetID, id).Scan(&otherMappings)
	if otherAcls == 0 && otherMappings == 0 {
		if _, err := a.agentOp(r.Context(), n, "lio_target_delete", map[string]any{"iqn": iqn}); err != nil {
			writeErr(w, http.StatusConflict, "AGENT_ERROR",
				"专用 Target 清理失败(可能仍有活动会话,请断开后重试): %v", err)
			return
		}
	}
	_, err = a.db.Exec(r.Context(), `DELETE FROM mappings WHERE id=$1`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("解映射后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"unmapped": dsName, "host_mapping_id": id})
}

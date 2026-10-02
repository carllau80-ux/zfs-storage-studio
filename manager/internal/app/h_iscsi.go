package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// targetLun / targetAcl / targetLive 描述 iSCSI Target 实况。
type targetDetail struct {
	ID         int64            `json:"id"`
	NodeID     int64            `json:"node_id"`
	NodeName   string           `json:"node_name"`
	TargetName string           `json:"target_name"`
	Transport  string           `json:"transport"`
	Portals    []string         `json:"portals"`
	Enabled    bool             `json:"enabled"`
	Luns       []map[string]any `json:"luns"`
	Acls       []map[string]any `json:"acls"`
	Sessions   []SessionLive    `json:"sessions"`
	CreatedAt  time.Time        `json:"created_at"`
}

// syncIscsi 将节点 LIO 实况同步进 targets/luns/acls 表,并缓存会话快照。
func (a *App) syncIscsi(ctx context.Context, node *NodeRow) error {
	m, err := a.agentQuery(ctx, node, "iscsi_targets", nil)
	if err != nil {
		return err
	}
	live, _ := m["targets"].([]any)
	seenIqn := map[string]bool{}
	var sessAll []SessionLive
	for _, it := range live {
		tg, _ := it.(map[string]any)
		iqn := strAny(tg["target_name"])
		if iqn == "" {
			continue
		}
		seenIqn[iqn] = true
		if _, err := a.db.Exec(ctx, `
			INSERT INTO targets(node_id,target_name) VALUES($1,$2)
			ON CONFLICT (node_id,target_name) DO NOTHING`, node.ID, iqn); err != nil {
			return err
		}
		var tid int64
		if err := a.db.QueryRow(ctx,
			`SELECT id FROM targets WHERE node_id=$1 AND target_name=$2`, node.ID, iqn).Scan(&tid); err != nil {
			return err
		}
		// LUN
		seenLun := map[int64]bool{}
		for _, li := range anyList(tg["luns"]) {
			lm, _ := li.(map[string]any)
			lunID := intAny(lm["lun"])
			seenLun[lunID] = true
			dsName := strAny(lm["dataset"])
			var dsID *int64
			if dsName != "" {
				var did int64
				if err := a.db.QueryRow(ctx,
					`SELECT id FROM datasets WHERE node_id=$1 AND name=$2`, node.ID, dsName).Scan(&did); err == nil {
					dsID = &did
				}
			}
			if _, err := a.db.Exec(ctx, `
				INSERT INTO luns(target_id,lun_id,dataset_id) VALUES($1,$2,$3)
				ON CONFLICT (target_id,lun_id) DO UPDATE SET dataset_id=EXCLUDED.dataset_id`,
				tid, lunID, dsID); err != nil {
				return err
			}
		}
		keys := make([]int64, 0, len(seenLun))
		for k := range seenLun {
			keys = append(keys, k)
		}
		if _, err := a.db.Exec(ctx,
			`DELETE FROM luns WHERE target_id=$1 AND NOT (lun_id = ANY($2::bigint[]))`,
			tid, keys); err != nil {
			return err
		}
		// ACL
		seenAcl := map[string]bool{}
		for _, ai := range anyList(tg["acls"]) {
			am, _ := ai.(map[string]any)
			ini := strAny(am["initiator_iqn"])
			if ini == "" {
				continue
			}
			seenAcl[ini] = true
			if _, err := a.db.Exec(ctx, `
				INSERT INTO acls(target_id,initiator_iqn,chap_user) VALUES($1,$2,$3)
				ON CONFLICT (target_id,initiator_iqn) DO UPDATE SET chap_user=EXCLUDED.chap_user`,
				tid, ini, strAny(am["chap_user"])); err != nil {
				return err
			}
		}
		akeys := make([]string, 0, len(seenAcl))
		for k := range seenAcl {
			akeys = append(akeys, k)
		}
		if _, err := a.db.Exec(ctx,
			`DELETE FROM acls WHERE target_id=$1 AND NOT (initiator_iqn = ANY($2::text[]))`,
			tid, akeys); err != nil {
			return err
		}
		// 会话
		for _, si := range anyList(tg["sessions"]) {
			sm, _ := si.(map[string]any)
			sessAll = append(sessAll, SessionLive{
				TargetName: iqn,
				IQN:        strAny(sm["iqn"]),
				SID:        intAny(sm["sid"]),
				State:      strAny(sm["state"]),
				Type:       strAny(sm["type"]),
				Address:    strAny(sm["address"]),
				Alias:      strAny(sm["alias"]),
			})
		}
	}
	// 删除失效 target(有 mapping 的保留)
	if _, err := a.db.Exec(ctx, `
		DELETE FROM targets WHERE node_id=$1 AND NOT (target_name = ANY($2::text[]))
		  AND NOT EXISTS (SELECT 1 FROM mappings m WHERE m.target_id=targets.id)`,
		node.ID, keysOf(seenIqn)); err != nil {
		return err
	}
	a.mu.Lock()
	a.sessionSnap[node.ID] = sessAll
	a.mu.Unlock()
	return nil
}

func anyList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func (a *App) liveTargets(ctx context.Context, node *NodeRow) ([]map[string]any, error) {
	m, err := a.agentQuery(ctx, node, "iscsi_targets", nil)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, it := range anyList(m["targets"]) {
		if tg, ok := it.(map[string]any); ok {
			out = append(out, tg)
		}
	}
	return out, nil
}

// syncFcTargets 同步 FC fabric 实况(仅当节点 fc_capable)。
func (a *App) syncFcTargets(ctx context.Context, node *NodeRow) error {
	if caps := a.nodeCapMap(node); caps != nil {
		if f, ok := caps["fc"].(map[string]any); ok {
			if ok2, _ := f["fc_capable"].(bool); !ok2 {
				return nil
			}
		}
	}
	m, err := a.agentQuery(ctx, node, "fc_targets", nil)
	if err != nil {
		return err
	}
	live, _ := m["targets"].([]any)
	seen := map[string]bool{}
	for _, it := range live {
		tg, _ := it.(map[string]any)
		wwn := strAny(tg["target_name"])
		if wwn == "" {
			continue
		}
		seen[wwn] = true
		if _, err := a.db.Exec(ctx, `
			INSERT INTO targets(node_id,target_name,transport) VALUES($1,$2,'fc')
			ON CONFLICT (node_id,target_name) DO UPDATE SET transport='fc'`, node.ID, wwn); err != nil {
			return err
		}
		var tid int64
		if err := a.db.QueryRow(ctx,
			`SELECT id FROM targets WHERE node_id=$1 AND target_name=$2`, node.ID, wwn).Scan(&tid); err != nil {
			return err
		}
		seenLun := map[int64]bool{}
		for _, li := range anyList(tg["luns"]) {
			lm, _ := li.(map[string]any)
			lunID := intAny(lm["lun"])
			seenLun[lunID] = true
			var dsID *int64
			if dsName := strAny(lm["dataset"]); dsName != "" {
				var did int64
				if err := a.db.QueryRow(ctx,
					`SELECT id FROM datasets WHERE node_id=$1 AND name=$2`, node.ID, dsName).Scan(&did); err == nil {
					dsID = &did
				}
			}
			if _, err := a.db.Exec(ctx, `
				INSERT INTO luns(target_id,lun_id,dataset_id) VALUES($1,$2,$3)
				ON CONFLICT (target_id,lun_id) DO UPDATE SET dataset_id=EXCLUDED.dataset_id`,
				tid, lunID, dsID); err != nil {
				return err
			}
		}
		keys := make([]int64, 0, len(seenLun))
		for k := range seenLun {
			keys = append(keys, k)
		}
		if _, err := a.db.Exec(ctx,
			`DELETE FROM luns WHERE target_id=$1 AND NOT (lun_id = ANY($2::bigint[]))`, tid, keys); err != nil {
			return err
		}
		seenAcl := map[string]bool{}
		for _, ai := range anyList(tg["acls"]) {
			am, _ := ai.(map[string]any)
			ini := strAny(am["initiator_iqn"])
			if ini == "" {
				continue
			}
			seenAcl[ini] = true
			if _, err := a.db.Exec(ctx, `
				INSERT INTO acls(target_id,initiator_iqn,chap_user) VALUES($1,$2,'')
				ON CONFLICT (target_id,initiator_iqn) DO NOTHING`, tid, ini); err != nil {
				return err
			}
		}
		akeys := make([]string, 0, len(seenAcl))
		for k := range seenAcl {
			akeys = append(akeys, k)
		}
		if _, err := a.db.Exec(ctx,
			`DELETE FROM acls WHERE target_id=$1 AND NOT (initiator_iqn = ANY($2::text[]))`, tid, akeys); err != nil {
			return err
		}
	}
	if _, err := a.db.Exec(ctx, `
		DELETE FROM targets WHERE node_id=$1 AND transport='fc'
		  AND NOT (target_name = ANY($2::text[]))
		  AND NOT EXISTS (SELECT 1 FROM mappings m WHERE m.target_id=targets.id)`,
		node.ID, keysOf(seen)); err != nil {
		return err
	}
	return nil
}

func (a *App) handleTargetsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where, args := "", []any{}
	if nid := q.Get("node_id"); nid != "" {
		where = " WHERE t.node_id=$" + itoa(len(args)+1)
		args = append(args, nid)
	}
	rows, err := a.db.Query(r.Context(), `
		SELECT t.id,t.node_id,n.name,t.target_name,t.transport,
		  (SELECT count(*) FROM luns l WHERE l.target_id=t.id),
		  (SELECT count(*) FROM acls c WHERE c.target_id=t.id),
		  (SELECT count(*) FROM mappings m WHERE m.target_id=t.id)
		FROM targets t JOIN nodes n ON n.id=t.node_id`+where+` ORDER BY t.id`, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	type item struct {
		ID           int64  `json:"id"`
		NodeID       int64  `json:"node_id"`
		NodeName     string `json:"node_name"`
		TargetName   string `json:"target_name"`
		Transport    string `json:"transport"`
		LunCount     int64  `json:"lun_count"`
		AclCount     int64  `json:"acl_count"`
		MappingCount int64  `json:"mapping_count"`
		SessionCount int    `json:"session_count"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.NodeID, &it.NodeName, &it.TargetName, &it.Transport,
			&it.LunCount, &it.AclCount, &it.MappingCount); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		out = append(out, it)
	}
	// 会话数来自最近一次轮询快照
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range out {
		cnt := 0
		for _, s := range a.sessionSnap[out[i].NodeID] {
			if s.TargetName == out[i].TargetName {
				cnt++
			}
		}
		out[i].SessionCount = cnt
	}
	writeOK(w, map[string]any{"targets": out})
}

type targetListRow struct {
	ID           int64
	NodeID       int64
	NodeName     string
	TargetName   string
	LunCount     int64
	AclCount     int64
	MappingCount int64
	SessionCount int
}

// handleTargetGet 详情:DB 行 + 实时状态合并。
func (a *App) handleTargetGet(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var td targetDetail
	var createdAt time.Time
	if err := a.db.QueryRow(r.Context(), `
		SELECT t.id,t.node_id,n.name,t.target_name,t.transport,t.created_at
		FROM targets t JOIN nodes n ON n.id=t.node_id WHERE t.id=$1`, id).
		Scan(&td.ID, &td.NodeID, &td.NodeName, &td.TargetName, &td.Transport, &createdAt); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "Target 不存在")
		return
	}
	td.CreatedAt = createdAt
	td.Luns = []map[string]any{}
	td.Acls = []map[string]any{}
	lrows, err := a.db.Query(r.Context(), `
		SELECT l.lun_id, COALESCE(d.name,'外部设备'), d.filesystem_type FROM luns l
		LEFT JOIN datasets d ON d.id=l.dataset_id WHERE l.target_id=$1 ORDER BY l.lun_id`, id)
	if err == nil {
		for lrows.Next() {
			var lun int64
			var dsName, fs string
			if lrows.Scan(&lun, &dsName, &fs) == nil {
				td.Luns = append(td.Luns, map[string]any{
					"lun_id": lun, "dataset": dsName, "filesystem_type": fs, "wwn": "",
				})
			}
		}
		lrows.Close()
	}
	// 实况 WWN(backstore 持久标识,客户端 by-id 的 wwn-0x 尾段来源)稍后由 live 合并
	arows, err := a.db.Query(r.Context(), `
		SELECT a.initiator_iqn,a.chap_user,
		  EXISTS(SELECT 1 FROM mappings m WHERE m.target_id=a.target_id)
		FROM acls a WHERE a.target_id=$1`, id)
	if err == nil {
		for arows.Next() {
			var iqn, chap string
			var managed bool
			if arows.Scan(&iqn, &chap, &managed) == nil {
				td.Acls = append(td.Acls, map[string]any{
					"initiator_iqn": iqn, "chap_user": chap, "has_chap": chap != "", "managed": managed,
				})
			}
		}
		arows.Close()
	}
	// 实时会话
	n := a.getNodeOrErr(w, r, td.NodeID)
	if n != nil && n.online() {
		live, err := a.liveTargets(r.Context(), n)
		if err == nil {
			for _, tg := range live {
				if strAny(tg["target_name"]) != td.TargetName {
					continue
				}
				td.Enabled = tg["enabled"] == true
				liveLunWwn := map[int64]string{}
				for _, li := range anyList(tg["luns"]) {
					lm, _ := li.(map[string]any)
					liveLunWwn[intAny(lm["lun"])] = strAny(lm["wwn"])
				}
				for i := range td.Luns {
					if w, ok := liveLunWwn[intAny(td.Luns[i]["lun_id"])]; ok {
						td.Luns[i]["wwn"] = w
					}
				}
				for _, p := range anyList(tg["portals"]) {
					td.Portals = append(td.Portals, fmt.Sprintf("%s:%v", strAny(mapVal(p, "ip")), intAny(mapVal(p, "port"))))
				}
				for _, si := range anyList(tg["sessions"]) {
					sm, _ := si.(map[string]any)
					td.Sessions = append(td.Sessions, SessionLive{
						TargetName: td.TargetName, IQN: strAny(sm["iqn"]),
						SID: intAny(sm["sid"]), State: strAny(sm["state"]),
						Type: strAny(sm["type"]), Address: strAny(sm["address"]),
						Alias: strAny(sm["alias"]),
					})
				}
			}
		}
	}
	writeOK(w, map[string]any{"target": td})
}

func mapVal(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	return nil
}

type targetCreateReq struct {
	NodeID    int64  `json:"node_id"`
	Transport string `json:"transport"`  // iscsi(默认) | fc
	IQN       string `json:"iqn"`        // iSCSI IQN 或 FC 目标 WWPN
	WWN       string `json:"wwn"`        // FC 目标 WWPN(别名)
	DatasetID int64  `json:"dataset_id"` // 可选:创建后直接挂载该卷为 LUN 0
}

func (a *App) genIQN(nodeName, hint string) string {
	now := time.Now()
	slug := "n" + fmt.Sprintf("%d", now.UnixNano()%100000)
	if hint != "" {
		s := make([]rune, 0, len(hint))
		for _, c := range hint {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
				s = append(s, c)
			} else {
				s = append(s, '-')
			}
		}
		slug = string(s) + "-" + slug
	}
	return fmt.Sprintf("iqn.%d-%02d.com.ustc:%s-%s", now.Year(), int(now.Month()), nodeName, slug)
}

func (a *App) handleTargetCreate(w http.ResponseWriter, r *http.Request) {
	var req targetCreateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, req.NodeID)
	if n == nil {
		return
	}
	if req.Transport == "" {
		req.Transport = "iscsi"
	}
	if req.Transport == "fc" {
		if req.WWN != "" {
			req.IQN = req.WWN
		}
		wwn, ok := normalizeWWN(req.IQN)
		if req.IQN != "" && !ok {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "目标 WWPN 非法(示例: 0x21000024ff123456)")
			return
		}
		var fcc bool
		var reason string
		if caps := a.nodeCapMap(n); caps != nil {
			if f, ok := caps["fc"].(map[string]any); ok {
				fcc, _ = f["fc_capable"].(bool)
				reason, _ = f["reason"].(string)
			}
		}
		if !fcc {
			writeErrK(w, http.StatusConflict, "FC_UNAVAILABLE", KErrFcUnavail, map[string]any{"reason": orDefault(reason, "未检测到兼容 FC HBA")},
				"FC Target 不可用: %s(需在本机检测到 QLogic 24xx/26xx 且加载 tcm_qla2xxx)", orDefault(reason, "未检测到兼容 FC HBA"))
			return
		}
		req.IQN = wwn
	} else if req.IQN != "" && !reIQN.MatchString(req.IQN) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "IQN 格式非法")
		return
	}
	var ds *datasetRow
	if req.DatasetID > 0 {
		row := a.db.QueryRow(r.Context(), dsSelect+` WHERE d.id=$1`, req.DatasetID)
		d, err := scanDatasetRow(row.Scan)
		if err != nil || d.NodeID != req.NodeID {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "卷不存在或不属于该节点")
			return
		}
		if d.Type != "volume" {
			writeErrK(w, http.StatusBadRequest, "VALIDATION", KErrZvolOnly, nil,
				"仅 zvol 可作为 LUN 载体(文件系统/pool 根请使用「共享」)")
			return
		}
		if d.Mapped {
			writeErr(w, http.StatusConflict, "CONFLICT", "卷 %s 已映射到其它 Target(一卷一 Target 策略)", d.Name)
			return
		}
		ds = &d
	}
	if req.IQN == "" {
		hint := ""
		if ds != nil {
			hint = shortName(ds.Name)
		}
		req.IQN = a.genIQN(n.Name, hint)
	}
	op := "lio_target_create"
	if req.Transport == "fc" {
		op = "fc_target_create"
	}
	params := map[string]any{"iqn": req.IQN, "wwn": req.IQN}
	if ds != nil {
		params["dataset"] = ds.Name
	}
	if _, err := a.agentOp(r.Context(), n, op, params); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("建 Target 后同步失败: %v", err)
	}
	var tid int64
	_ = a.db.QueryRow(r.Context(),
		`SELECT id FROM targets WHERE node_id=$1 AND target_name=$2`, req.NodeID, req.IQN).Scan(&tid)
	writeOK(w, map[string]any{"target_id": tid, "iqn": req.IQN})
}

func (a *App) handleTargetDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	nodeID, iqn, transport, terr := a.targetTransport(r.Context(), id)
	if terr != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "Target 不存在")
		return
	}
	var mappingCnt int64
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM mappings WHERE target_id=$1`, id).Scan(&mappingCnt)
	if mappingCnt > 0 {
		writeErr(w, http.StatusConflict, "CONFLICT", "该 Target 存在 %d 条映射记录,请先在主机详情解除映射", mappingCnt)
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, opPrefix(transport)+"target_delete", map[string]any{"iqn": iqn, "wwn": iqn}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if transport == "fc" {
		_ = a.syncFcTargets(r.Context(), n)
	} else if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("删 Target 后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"deleted": iqn})
}

// targetInfo 读取目标传输类型。
func (a *App) targetTransport(ctx context.Context, id int64) (nodeID int64, name, transport string, err error) {
	err = a.db.QueryRow(ctx, `SELECT node_id,target_name,transport FROM targets WHERE id=$1`, id).
		Scan(&nodeID, &name, &transport)
	return
}

// opPrefix 按传输给出 Agent 操作前缀。
func opPrefix(transport string) string {
	if transport == "fc" {
		return "fc_"
	}
	return "lio_"
}

func (a *App) handleLunAdd(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var body struct {
		DatasetID int64 `json:"dataset_id"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	nodeID, iqn, transport, err := a.targetTransport(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "Target 不存在")
		return
	}
	row := a.db.QueryRow(r.Context(), dsSelect+` WHERE d.id=$1`, body.DatasetID)
	d, err := scanDatasetRow(row.Scan)
	if err != nil || d.NodeID != nodeID {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "卷不存在或不属于该节点")
		return
	}
	if d.Type != "volume" {
		writeErrK(w, http.StatusBadRequest, "VALIDATION", KErrZvolOnly, nil, "仅 zvol 可挂载为 LUN(文件系统/pool 根请使用「共享」)")
		return
	}
	if d.Mapped {
		writeErr(w, http.StatusConflict, "CONFLICT", "卷 %s 已挂载到其它 Target", d.Name)
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, opPrefix(transport)+"lun_add", map[string]any{"iqn": iqn, "wwn": iqn, "dataset": d.Name}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("挂 LUN 后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"target": iqn, "dataset": d.Name})
}

func (a *App) handleLunRemove(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var lunID int64
	if _, err := fmt.Sscanf(r.PathValue("lun"), "%d", &lunID); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "lun_id 非法")
		return
	}
	var dsID *int64
	nodeID, iqn, transport, terr := a.targetTransport(r.Context(), id)
	if terr != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "Target 不存在")
		return
	}
	if err := a.db.QueryRow(r.Context(), `
		SELECT l.dataset_id FROM luns l WHERE l.target_id=$1 AND l.lun_id=$2`, id, lunID).
		Scan(&dsID); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "LUN 不存在")
		return
	}
	if dsID != nil {
		var mcnt int64
		_ = a.db.QueryRow(r.Context(), `
			SELECT count(*) FROM mappings WHERE target_id=$1 AND dataset_id=$2`, id, *dsID).Scan(&mcnt)
		if mcnt > 0 {
			writeErr(w, http.StatusConflict, "CONFLICT", "该 LUN 仍被主机映射,请先解除映射")
			return
		}
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, opPrefix(transport)+"lun_remove", map[string]any{"iqn": iqn, "wwn": iqn, "lun_id": lunID}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("摘 LUN 后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"removed": fmt.Sprintf("%s lun%d", iqn, lunID)})
}

type aclAddReq struct {
	IQN        string `json:"iqn"` // initiator
	ChapUser   string `json:"chap_user"`
	ChapSecret string `json:"chap_secret"`
}

func (a *App) handleAclAdd(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var req aclAddReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if !reIQN.MatchString(req.IQN) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "Initiator IQN 非法")
		return
	}
	if req.ChapUser == "" && req.ChapSecret != "" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "设置 CHAP 需同时提供用户名")
		return
	}
	if req.ChapUser != "" && len(req.ChapSecret) < 12 {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "CHAP 口令至少 12 字符")
		return
	}
	nodeID, iqn, transport, terr := a.targetTransport(r.Context(), id)
	if terr != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "Target 不存在")
		return
	}
	if transport == "fc" && (req.ChapUser != "" || req.ChapSecret != "") {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "FC ACL 不支持 CHAP")
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, opPrefix(transport)+"acl_add", map[string]any{
		"iqn": iqn, "wwn": iqn, "initiator": req.IQN, "chap_user": req.ChapUser, "chap_secret": req.ChapSecret}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("加 ACL 后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"target": iqn, "initiator": req.IQN})
}

func (a *App) handleAclRemove(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	ini := r.PathValue("iqn")
	nodeID, iqn, transport, terr := a.targetTransport(r.Context(), id)
	if terr != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "Target 不存在")
		return
	}
	var managed bool
	_ = a.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM mappings m JOIN acls c ON c.target_id=m.target_id
		   WHERE m.target_id=$1 AND c.initiator_iqn=$2)`, id, ini).Scan(&managed)
	if managed {
		writeErr(w, http.StatusConflict, "CONFLICT", "该 ACL 由映射记录管理,请在主机详情解除映射")
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, opPrefix(transport)+"acl_remove", map[string]any{"iqn": iqn, "wwn": iqn, "initiator": ini}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncIscsi(r.Context(), n); err != nil {
		a.logf("删 ACL 后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"removed": ini})
}

func (a *App) handleSessionLogout(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var sid int64
	if _, err := fmt.Sscanf(r.PathValue("sid"), "%d", &sid); err != nil || sid <= 0 {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "session id 非法")
		return
	}
	var nodeID int64
	var iqn string
	if err := a.db.QueryRow(r.Context(), `SELECT node_id,target_name FROM targets WHERE id=$1`, id).
		Scan(&nodeID, &iqn); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "Target 不存在")
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, "lio_session_logout", map[string]any{"iqn": iqn, "sid": sid}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	writeOK(w, map[string]any{"logged_out": sid})
}

var _ = json.Marshal

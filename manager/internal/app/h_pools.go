package app

import (
	"net/http"
)

func (a *App) handlePoolsList(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `
		SELECT p.id,p.node_id,n.name,p.name,p.guid,p.state,p.health,p.size,p.allocated,p.free,p.updated_at,
		  (SELECT count(*) FROM datasets d WHERE d.pool_id=p.id AND d.type='volume')
		FROM pools p JOIN nodes n ON n.id=p.node_id
		ORDER BY p.node_id,p.id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	type item struct {
		ID          int64  `json:"id"`
		NodeID      int64  `json:"node_id"`
		NodeName    string `json:"node_name"`
		Name        string `json:"name"`
		GUID        string `json:"guid"`
		State       string `json:"state"`
		Health      string `json:"health"`
		Size        int64  `json:"size"`
		Allocated   int64  `json:"allocated"`
		Free        int64  `json:"free"`
		SizeHuman   string `json:"size_human"`
		UsedHuman   string `json:"used_human"`
		FreeHuman   string `json:"free_human"`
		VolumeCount int64  `json:"volume_count"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.NodeID, &it.NodeName, &it.Name, &it.GUID, &it.State,
			&it.Health, &it.Size, &it.Allocated, &it.Free, newTimePtr(), &it.VolumeCount); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		it.SizeHuman = humanBytes(it.Size)
		it.UsedHuman = humanBytes(it.Allocated)
		it.FreeHuman = humanBytes(it.Free)
		out = append(out, it)
	}
	writeOK(w, map[string]any{"pools": out})
}

func (a *App) getPool(ctxReq interface{ Done() <-chan struct{} }, id int64) (map[string]any, error) {
	// 兼容性包装:直接使用 SQL 查询单池
	panic("unused")
}

func (a *App) handlePoolGet(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	row := a.db.QueryRow(r.Context(), `
		SELECT p.id,p.node_id,n.name,p.name,p.guid,p.state,p.health,p.size,p.allocated,p.free,
		  (SELECT count(*) FROM datasets d WHERE d.pool_id=p.id AND d.type='volume'),
		  (SELECT count(*) FROM datasets d WHERE d.pool_id=p.id AND d.type='filesystem')
		FROM pools p JOIN nodes n ON n.id=p.node_id WHERE p.id=$1`, id)
	var it struct {
		ID        int64  `json:"id"`
		NodeID    int64  `json:"node_id"`
		NodeName  string `json:"node_name"`
		Name      string `json:"name"`
		GUID      string `json:"guid"`
		State     string `json:"state"`
		Health    string `json:"health"`
		Size      int64  `json:"size"`
		Allocated int64  `json:"allocated"`
		Free      int64  `json:"free"`
		SizeHuman string `json:"size_human"`
		UsedHuman string `json:"used_human"`
		FreeHuman string `json:"free_human"`
		Vols      int64  `json:"volume_count"`
		Fs        int64  `json:"filesystem_count"`
	}
	var nodeName, name, guid, state, health string
	if err := row.Scan(&it.ID, &it.NodeID, &nodeName, &name, &guid, &state, &health,
		&it.Size, &it.Allocated, &it.Free, &it.Vols, &it.Fs); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "池不存在")
		return
	}
	it.NodeName, it.Name, it.GUID, it.State, it.Health = nodeName, name, guid, state, health
	it.SizeHuman, it.UsedHuman, it.FreeHuman = humanBytes(it.Size), humanBytes(it.Allocated), humanBytes(it.Free)
	writeOK(w, map[string]any{"pool": it})
}

type vdevGroup struct {
	Type  string   `json:"type"` // stripe | mirror | raidz | raidz2 | raidz3
	Disks []string `json:"disks"`
}

type poolOptions struct {
	Ashift      string `json:"ashift"`
	Compression string `json:"compression"`
	Recordsize  string `json:"recordsize"`
	Dedup       string `json:"dedup"`
}

type poolCreateReq struct {
	NodeID  int64        `json:"node_id"`
	Name    string       `json:"name"`
	Disks   []string     `json:"disks"` // 兼容:单组条带
	Layout  []vdevGroup  `json:"layout"`
	Options *poolOptions `json:"options"`
	Log     []string     `json:"log"`
	Cache   []string     `json:"cache"`
	Spare   []string     `json:"spare"`
}

func (a *App) handlePoolCreate(w http.ResponseWriter, r *http.Request) {
	var req poolCreateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if !reName.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "池名非法(仅字母数字 _ . -)")
		return
	}
	layout := req.Layout
	if len(layout) == 0 && len(req.Disks) > 0 {
		layout = []vdevGroup{{Type: "stripe", Disks: req.Disks}}
	}
	if len(layout) == 0 {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "至少配置一组数据 vdev 并选择设备")
		return
	}
	checkDisks := func(name string, devs []string) bool {
		for _, d := range devs {
			if !reDisk.MatchString(d) {
				writeErr(w, http.StatusBadRequest, "VALIDATION", "%s 磁盘路径非法: %s", name, d)
				return false
			}
		}
		return true
	}
	for i := range layout {
		if layout[i].Type == "" {
			layout[i].Type = "stripe"
		}
		if !checkDisks(layout[i].Type, layout[i].Disks) {
			return
		}
		if len(layout[i].Disks) == 0 {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "vdev 组(%s)未选择设备", layout[i].Type)
			return
		}
	}
	if !checkDisks("log", req.Log) || !checkDisks("cache", req.Cache) || !checkDisks("spare", req.Spare) {
		return
	}
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM pools WHERE node_id=$1 AND name=$2)`,
		req.NodeID, req.Name).Scan(&exists); err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	if exists {
		writeErrK(w, http.StatusConflict, "CONFLICT", KErrDuplicate, map[string]any{"pool": req.Name}, "该节点上已存在同名池 %s", req.Name)
		return
	}
	n := a.getNodeOrErr(w, r, req.NodeID)
	if n == nil {
		return
	}
	opts := map[string]any{}
	if req.Options != nil {
		if req.Options.Ashift != "" {
			opts["ashift"] = req.Options.Ashift
		}
		if req.Options.Compression != "" {
			opts["compression"] = req.Options.Compression
		}
		if req.Options.Recordsize != "" {
			opts["recordsize"] = req.Options.Recordsize
		}
		if req.Options.Dedup != "" {
			opts["dedup"] = req.Options.Dedup
		}
	}
	layoutArg := make([]map[string]any, 0, len(layout))
	for _, g := range layout {
		layoutArg = append(layoutArg, map[string]any{"type": g.Type, "disks": g.Disks})
	}
	if _, err := a.agentOp(r.Context(), n, "pool_create", map[string]any{
		"pool_name": req.Name, "layout": layoutArg, "options": opts,
		"log": req.Log, "cache": req.Cache, "spare": req.Spare}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncNodeState(r.Context(), n); err != nil {
		a.logf("建池后同步失败: %v", err)
	}
	// 返回新建池
	var pid int64
	_ = a.db.QueryRow(r.Context(), `SELECT id FROM pools WHERE node_id=$1 AND name=$2`,
		req.NodeID, req.Name).Scan(&pid)
	writeOK(w, map[string]any{"pool_id": pid, "name": req.Name})
}

// handlePoolStatus 池状态详情(zpool status JSON 归一化)。
func (a *App) handlePoolStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var nodeID int64
	var poolName string
	if err := a.db.QueryRow(r.Context(), `SELECT node_id,name FROM pools WHERE id=$1`, id).
		Scan(&nodeID, &poolName); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "池不存在")
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	m, err := a.agentQuery(r.Context(), n, "pool_status", map[string]any{"pool": poolName})
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	writeOK(w, m)
}

func (a *App) handlePoolScrub(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var body struct {
		Action string `json:"action"` // start | stop
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if body.Action != "start" && body.Action != "stop" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "action 须为 start|stop")
		return
	}
	var nodeID int64
	var poolName string
	if err := a.db.QueryRow(r.Context(), `SELECT node_id,name FROM pools WHERE id=$1`, id).
		Scan(&nodeID, &poolName); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "池不存在")
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	m, err := a.agentOp(r.Context(), n, "pool_scrub", map[string]any{"pool_name": poolName, "action": body.Action})
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	writeOK(w, m)
}

func (a *App) handlePoolDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var body struct {
		Confirm string `json:"confirm"` // 须等于池名
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var nodeID int64
	var poolName string
	if err := a.db.QueryRow(r.Context(), `SELECT node_id,name FROM pools WHERE id=$1`, id).
		Scan(&nodeID, &poolName); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "池不存在")
		return
	}
	if body.Confirm != poolName {
		writeErrK(w, http.StatusBadRequest, "VALIDATION", KErrConfirmName, map[string]any{"name": poolName}, "请完整输入池名 %q 以确认销毁", poolName)
		return
	}
	// 只统计池下的子数据集/卷(池根文件系统行不算)
	var volCnt int64
	if err := a.db.QueryRow(r.Context(), `
		SELECT count(*) FROM datasets d JOIN pools p ON p.id=d.pool_id
		WHERE p.id=$1 AND d.name<>p.name`, id).Scan(&volCnt); err == nil && volCnt > 0 {
		writeErr(w, http.StatusConflict, "CONFLICT", "池内仍有 %d 个数据集/卷,请先删除", volCnt)
		return
	}
	var lunCnt int64
	if err := a.db.QueryRow(r.Context(), `
		SELECT count(*) FROM luns l JOIN datasets d ON d.id=l.dataset_id WHERE d.pool_id=$1`, id).
		Scan(&lunCnt); err == nil && lunCnt > 0 {
		writeErr(w, http.StatusConflict, "CONFLICT", "池内卷已被映射为 LUN,请先解映射")
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, "pool_destroy", map[string]any{
		"pool_name": poolName, "confirm": poolName}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `DELETE FROM pools WHERE id=$1`, id)
	writeOK(w, map[string]any{"deleted": poolName})
}

type timePtr struct{ p *interface{} }

// newTimePtr 占位扫描目标(不读取 updated_at)。
func newTimePtr() *interface{} {
	var v interface{}
	return &v
}

package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// datasetRow 通用查询结构。
type datasetRow struct {
	ID               int64  `json:"id"`
	NodeID           int64  `json:"node_id"`
	NodeName         string `json:"node_name"`
	PoolID           int64  `json:"pool_id"`
	Pool             string `json:"pool"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	Volsize          int64  `json:"volsize"`
	VolsizeHuman     string `json:"volsize_human"`
	Used             int64  `json:"used"`
	UsedHuman        string `json:"used_human"`
	Available        int64  `json:"available"`
	AvailableHuman   string `json:"available_human"`
	Compression      string `json:"compression"`
	Origin           string `json:"origin"`
	FilesystemType   string `json:"filesystem_type"`
	FilesystemStatus string `json:"filesystem_status"`
	SnapshotCount    int64  `json:"snapshot_count"`
	Mapped           bool   `json:"mapped"`
}

const dsSelect = `
	SELECT d.id,d.node_id,n.name,COALESCE(d.pool_id,0),COALESCE(p.name,''),d.name,d.type,d.volsize,d.used,d.available,
	       d.compression,d.origin,d.filesystem_type,d.filesystem_status,
	       (SELECT count(*) FROM snapshots s WHERE s.dataset_id=d.id),
	       EXISTS(SELECT 1 FROM luns l WHERE l.dataset_id=d.id)
	FROM datasets d
	JOIN nodes n ON n.id=d.node_id
	LEFT JOIN pools p ON p.id=d.pool_id`

func scanDatasetRow(scan func(...any) error) (datasetRow, error) {
	var r datasetRow
	err := scan(&r.ID, &r.NodeID, &r.NodeName, &r.PoolID, &r.Pool, &r.Name, &r.Type,
		&r.Volsize, &r.Used, &r.Available, &r.Compression, &r.Origin,
		&r.FilesystemType, &r.FilesystemStatus, &r.SnapshotCount, &r.Mapped)
	r.VolsizeHuman = humanBytes(r.Volsize)
	r.UsedHuman = humanBytes(r.Used)
	r.AvailableHuman = humanBytes(r.Available)
	return r, err
}

func (a *App) handleDatasetsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	conds := []string{}
	args := []any{}
	add := func(col string) {
		args = append(args, q.Get(col))
		conds = append(conds, "d."+col+"=$"+itoa(len(args)))
	}
	for _, c := range []string{"node_id", "pool_id", "type"} {
		if q.Get(c) != "" {
			add(c)
		}
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + joinConds(conds)
	}
	rows, err := a.db.Query(r.Context(), dsSelect+where+" ORDER BY d.name", args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	out := []datasetRow{}
	for rows.Next() {
		r2, err := scanDatasetRow(rows.Scan)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		out = append(out, r2)
	}
	writeOK(w, map[string]any{"datasets": out})
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func joinConds(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}

type datasetCreateReq struct {
	NodeID       int64  `json:"node_id"`
	PoolID       int64  `json:"pool_id"`
	Type         string `json:"type"` // volume(默认) | filesystem
	Name         string `json:"name"`
	Size         string `json:"size"`
	Compression  string `json:"compression"`
	Dedup        string `json:"dedup"`
	Volblocksize string `json:"volblocksize"`
	Sparse       bool   `json:"sparse"`
	Mountpoint   string `json:"mountpoint"` // filesystem: none|auto|/path
}

func (a *App) handleDatasetCreate(w http.ResponseWriter, r *http.Request) {
	var req datasetCreateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if !reName.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "名称非法(仅字母数字 _ . -,1-63 字符)")
		return
	}
	isFS := req.Type == "filesystem"
	if req.Type != "" && req.Type != "volume" && !isFS {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "type 须为 volume|filesystem")
		return
	}
	var size int64
	if !isFS {
		var perr error
		size, perr = parseSize(req.Size)
		if perr != nil || size < 1<<20 {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "卷容量须 ≥ 1MiB: %v", perr)
			return
		}
	}
	var nodeID int64
	var poolName string
	if err := a.db.QueryRow(r.Context(), `SELECT node_id,name FROM pools WHERE id=$1`, req.PoolID).
		Scan(&nodeID, &poolName); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "池不存在")
		return
	}
	if nodeID != req.NodeID {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "池与节点不匹配")
		return
	}
	var dup bool
	_ = a.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM datasets WHERE node_id=$1 AND name=$2)`,
		nodeID, poolName+"/"+req.Name).Scan(&dup)
	if dup {
		writeErrK(w, http.StatusConflict, "CONFLICT", KErrDuplicate, map[string]any{"dataset": poolName + "/" + req.Name}, "已存在同名数据集 %s/%s", poolName, req.Name)
		return
	}
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if isFS {
		params := map[string]any{"pool": poolName, "name": req.Name}
		if req.Compression != "" && req.Compression != "inherit" {
			params["compression"] = req.Compression
		}
		if req.Dedup != "" && req.Dedup != "inherit" {
			params["dedup"] = req.Dedup
		}
		switch {
		case req.Mountpoint == "auto":
			params["mountpoint"] = "/" + poolName + "/" + req.Name
		case req.Mountpoint != "" && req.Mountpoint != "none":
			if !reMount.MatchString(req.Mountpoint) {
				writeErr(w, http.StatusBadRequest, "VALIDATION", "挂载点非法(须为绝对路径)")
				return
			}
			params["mountpoint"] = req.Mountpoint
		}
		if _, err := a.agentOp(r.Context(), n, "zfs_fs_create", params); err != nil {
			writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
			return
		}
	} else {
		params := map[string]any{
			"pool": poolName, "name": req.Name, "size": size,
		}
		if req.Compression != "" && req.Compression != "inherit" {
			params["compression"] = req.Compression
		}
		if req.Volblocksize != "" {
			params["volblocksize"] = req.Volblocksize
		}
		if req.Sparse {
			params["sparse"] = true
		}
		if _, err := a.agentOp(r.Context(), n, "zvol_create", params); err != nil {
			writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
			return
		}
	}
	if err := a.syncNodeState(r.Context(), n); err != nil {
		a.logf("建卷后同步失败: %v", err)
	}
	// 读取新行
	row := a.db.QueryRow(r.Context(), dsSelect+
		` WHERE d.node_id=$1 AND d.name=$2`, nodeID, poolName+"/"+req.Name)
	nr, err := scanDatasetRow(row.Scan)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "回读新卷失败: %v", err)
		return
	}
	writeOK(w, map[string]any{"dataset": nr})
}

func (a *App) datasetByID(w http.ResponseWriter, r *http.Request) (*datasetRow, bool) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return nil, false
	}
	row := a.db.QueryRow(r.Context(), dsSelect+` WHERE d.id=$1`, id)
	nr, err := scanDatasetRow(row.Scan)
	if err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "数据集不存在")
		return nil, false
	}
	return &nr, true
}

func (a *App) handleDatasetGet(w http.ResponseWriter, r *http.Request) {
	ds, ok := a.datasetByID(w, r)
	if !ok {
		return
	}
	n := a.getNodeOrErr(w, r, ds.NodeID)
	if n == nil {
		return
	}
	var props map[string]any = map[string]any{}
	var fsinfo map[string]any = map[string]any{
		"type": ds.FilesystemType, "status": ds.FilesystemStatus, "mounted": false,
	}
	if n.online() {
		// 实时属性(zvol 与 filesystem 属性集不同)
		if m, err := a.agentQuery(r.Context(), n, "dsprops", map[string]any{
			"dataset": ds.Name,
			"props":   dsPropList(ds.Type),
		}); err == nil {
			if pm, ok := m["properties"].(map[string]any); ok {
				props = pm
			}
		}
		// 文件系统状态仅对 zvol(blkid)
		if ds.Type == "volume" {
			if m, err := a.agentQuery(r.Context(), n, "fsinfo", map[string]any{"dataset": ds.Name}); err == nil {
				fsinfo = m
				fsType := strAny(fsinfo["type"])
				if fsType == "" {
					fsType = "none"
				}
				if fsType != ds.FilesystemType || strAny(fsinfo["status"]) != ds.FilesystemStatus {
					_, _ = a.db.Exec(r.Context(),
						`UPDATE datasets SET filesystem_type=$1, filesystem_status=$2 WHERE id=$3`,
						fsType, strAny(fsinfo["status"]), ds.ID)
					ds.FilesystemType = fsType
					ds.FilesystemStatus = strAny(fsinfo["status"])
				}
			}
		}
	}
	// 所在 Target
	trows, err := a.db.Query(r.Context(), `
		SELECT t.id,t.target_name,l.lun_id FROM targets t
		JOIN luns l ON l.target_id=t.id
		WHERE l.dataset_id=$1`, ds.ID)
	targets := []map[string]any{}
	if err == nil {
		for trows.Next() {
			var tid, lun int64
			var iqn string
			if trows.Scan(&tid, &iqn, &lun) == nil {
				targets = append(targets, map[string]any{"id": tid, "iqn": iqn, "lun_id": lun})
			}
		}
		trows.Close()
	}
	// 授权主机
	hosts := []map[string]any{}
	hrows, err := a.db.Query(r.Context(), `
		SELECT DISTINCT h.id,h.name FROM mappings m JOIN hosts h ON h.id=m.host_id
		WHERE m.dataset_id=$1`, ds.ID)
	if err == nil {
		for hrows.Next() {
			var hid int64
			var hname string
			if hrows.Scan(&hid, &hname) == nil {
				hosts = append(hosts, map[string]any{"id": hid, "name": hname})
			}
		}
		hrows.Close()
	}
	writeOK(w, map[string]any{
		"dataset": ds, "properties": props, "filesystem": fsinfo,
		"targets": targets, "authorized_hosts": hosts,
	})
}

func dsPropList(dsType string) []string {
	if dsType == "filesystem" {
		return []string{"compression", "dedup", "compressratio", "recordsize", "quota", "atime", "mounted", "mountpoint"}
	}
	return []string{"compression", "dedup", "compressratio", "volsize", "volblocksize", "volmode", "logicalused"}
}

type dsResizeReq struct {
	Size    string `json:"size"`
	Confirm string `json:"confirm"` // 缩容时必填:卷名
}

func (a *App) handleDatasetResize(w http.ResponseWriter, r *http.Request) {
	ds, ok := a.datasetByID(w, r)
	if !ok {
		return
	}
	if ds.Type != "volume" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "仅 zvol 支持扩容/缩容")
		return
	}
	var req dsResizeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	size, err := parseSize(req.Size)
	if err != nil || size < 1<<20 {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "容量非法(≥ 1MiB): %v", err)
		return
	}
	if size < ds.Volsize && req.Confirm != shortName(ds.Name) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "缩容为破坏性操作,请键入卷名 %q 确认", shortName(ds.Name))
		return
	}
	n := a.getNodeOrErr(w, r, ds.NodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, "zvol_resize", map[string]any{
		"dataset": ds.Name, "size": size, "confirm": req.Confirm}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncNodeState(r.Context(), n); err != nil {
		a.logf("resize 后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"dataset": ds.Name, "volsize": size, "volsize_human": humanBytes(size)})
}

func shortName(ds string) string {
	i := indexByte(ds, '/')
	if i < 0 {
		return ds
	}
	return ds[i+1:]
}

func (a *App) handleDatasetDelete(w http.ResponseWriter, r *http.Request) {
	ds, ok := a.datasetByID(w, r)
	if !ok {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if body.Confirm != shortName(ds.Name) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "请完整输入卷名 %q 以确认删除", shortName(ds.Name))
		return
	}
	if ds.SnapshotCount > 0 {
		writeErr(w, http.StatusConflict, "CONFLICT", "卷仍有 %d 个快照,请先删除全部快照", ds.SnapshotCount)
		return
	}
	if ds.Mapped {
		writeErr(w, http.StatusConflict, "CONFLICT", "卷已映射为 LUN(强校验),请先在卷详情解除映射")
		return
	}
	n := a.getNodeOrErr(w, r, ds.NodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, "dataset_destroy", map[string]any{
		"dataset": ds.Name, "confirm": body.Confirm}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `DELETE FROM datasets WHERE id=$1`, ds.ID)
	if err := a.syncNodeState(r.Context(), n); err != nil {
		a.logf("删卷后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"deleted": ds.Name})
}

// handleDatasetFormat 执行白名单 mkfs(强校验:未映射/无挂载/确认码)。
func (a *App) handleDatasetFormat(w http.ResponseWriter, r *http.Request) {
	ds, ok := a.datasetByID(w, r)
	if !ok {
		return
	}
	if ds.Type != "volume" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "仅 zvol 支持格式化")
		return
	}
	var body struct {
		Filesystem string `json:"filesystem"` // ext4 | xfs | ntfs
		Confirm    string `json:"confirm"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	body.Filesystem = strings.ToLower(strings.TrimSpace(body.Filesystem))
	switch body.Filesystem {
	case "ext4", "xfs", "ntfs":
	default:
		writeErr(w, http.StatusBadRequest, "VALIDATION", "filesystem 须为 ext4|xfs|ntfs")
		return
	}
	if body.Confirm != shortName(ds.Name) {
		writeErrK(w, http.StatusBadRequest, "VALIDATION", KErrConfirmName, map[string]any{"name": shortName(ds.Name)}, "格式化将清除卷上全部数据,请键入卷名 %q 确认", shortName(ds.Name))
		return
	}
	if ds.Mapped {
		writeErrK(w, http.StatusConflict, "CONFLICT", KErrConflict, map[string]any{"dataset": ds.Name}, "卷已映射到 LUN,禁止格式化(先解除映射并断开会话)")
		return
	}
	n := a.getNodeOrErr(w, r, ds.NodeID)
	if n == nil {
		return
	}
	res, err := a.agentOp(r.Context(), n, "zvol_format", map[string]any{
		"dataset": ds.Name, "filesystem": body.Filesystem, "confirm": body.Confirm})
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	fsType := strAny(res["filesystem_type"])
	if fsType == "" {
		fsType = body.Filesystem
	}
	_, _ = a.db.Exec(r.Context(),
		`UPDATE datasets SET filesystem_type=$1, filesystem_status='ready' WHERE id=$2`,
		fsType, ds.ID)
	writeOK(w, map[string]any{"dataset": ds.Name, "filesystem_type": fsType, "detail": res})
}

var _ = json.Marshal

// handleDatasetProps 配置数据集属性(白名单:compression / dedup)。
func (a *App) handleDatasetProps(w http.ResponseWriter, r *http.Request) {
	ds, ok := a.datasetByID(w, r)
	if !ok {
		return
	}
	var body struct {
		Property string `json:"property"`
		Value    string `json:"value"`
		Confirm  string `json:"confirm"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	switch body.Property {
	case "compression", "dedup", "mountpoint":
	default:
		writeErr(w, http.StatusBadRequest, "VALIDATION", "property 仅支持 compression|dedup|mountpoint")
		return
	}
	if body.Property == "mountpoint" {
		if body.Value == "auto" {
			body.Value = "/" + ds.Pool + "/" + shortName(ds.Name)
		} else if body.Value != "none" && body.Value != "legacy" && !reMount.MatchString(body.Value) {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "挂载点须为 auto | none | 绝对路径")
			return
		}
		if ds.Type != "filesystem" {
			writeErr(w, http.StatusBadRequest, "VALIDATION", "仅文件系统数据集可设置挂载点")
			return
		}
	}
	if body.Value == "" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "value 必填(inherit 表示继承父级)")
		return
	}
	// dedup 开启影响性能/内存,需要键入资源名确认
	if body.Property == "dedup" && body.Value != "off" && body.Value != "inherit" {
		if body.Confirm != shortName(ds.Name) {
			writeErr(w, http.StatusBadRequest, "VALIDATION",
				"开启重删有性能开销,请键入 %q 确认", shortName(ds.Name))
			return
		}
	}
	n := a.getNodeOrErr(w, r, ds.NodeID)
	if n == nil {
		return
	}
	res, err := a.agentOp(r.Context(), n, "zfs_set_prop", map[string]any{
		"dataset": ds.Name, "property": body.Property, "value": body.Value})
	if err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if body.Property == "compression" {
		v := strAny(res["compression"])
		if body.Value == "inherit" || v == "" {
			v = ""
		}
		_, _ = a.db.Exec(r.Context(),
			`UPDATE datasets SET compression=$1 WHERE id=$2`, v, ds.ID)
	}
	writeOK(w, map[string]any{"dataset": ds.Name, "property": body.Property, "value": res})
}

// handleDatasetClone 从快照克隆为可写 zvol(创建卷的"从快照克隆"模式)。
func (a *App) handleDatasetClone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceDatasetID int64  `json:"source_dataset_id"`
		Snapshot        string `json:"snapshot"`
		Name            string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if !reName.MatchString(body.Name) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "新卷名非法(仅字母数字 _ . -)")
		return
	}
	if !reSnap.MatchString(body.Snapshot) || indexByte(body.Snapshot, '@') >= 0 {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "快照名非法")
		return
	}
	row := a.db.QueryRow(r.Context(), dsSelect+` WHERE d.id=$1`, body.SourceDatasetID)
	src, err := scanDatasetRow(row.Scan)
	if err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "源卷不存在")
		return
	}
	if src.Type != "volume" {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "仅 zvol 可作为克隆源")
		return
	}
	var snapExists bool
	_ = a.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM snapshots WHERE dataset_id=$1 AND name=$2)`,
		src.ID, body.Snapshot).Scan(&snapExists)
	if !snapExists {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "快照 %s 不存在于 %s", body.Snapshot, src.Name)
		return
	}
	var dup bool
	_ = a.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM datasets WHERE node_id=$1 AND name=$2)`,
		src.NodeID, src.Pool+"/"+body.Name).Scan(&dup)
	if dup {
		writeErr(w, http.StatusConflict, "CONFLICT", "已存在同名数据集 %s/%s", src.Pool, body.Name)
		return
	}
	n := a.getNodeOrErr(w, r, src.NodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, "zfs_clone", map[string]any{
		"source": src.Name, "snapshot": body.Snapshot, "name": body.Name}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncNodeState(r.Context(), n); err != nil {
		a.logf("克隆后同步失败: %v", err)
	}
	row2 := a.db.QueryRow(r.Context(), dsSelect+` WHERE d.node_id=$1 AND d.name=$2`,
		src.NodeID, src.Pool+"/"+body.Name)
	cl, err := scanDatasetRow(row2.Scan)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "回读克隆卷失败: %v", err)
		return
	}
	writeOK(w, map[string]any{
		"dataset": cl, "cloned_from": src.Name + "@" + body.Snapshot,
	})
}

package app

import (
	"net/http"
)

type snapRow struct {
	ID        int64  `json:"id"`
	DatasetID int64  `json:"dataset_id"`
	Dataset   string `json:"dataset"`
	Name      string `json:"name"`
	Used      int64  `json:"used"`
	UsedHuman string `json:"used_human"`
	Createtxg int64  `json:"createtxg"`
	Creation  string `json:"creation"`
}

func (a *App) handleSnapList(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var dsName string
	if err := a.db.QueryRow(r.Context(), `SELECT name FROM datasets WHERE id=$1`, id).
		Scan(&dsName); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "数据集不存在")
		return
	}
	rows, err := a.db.Query(r.Context(), `
		SELECT s.id,s.dataset_id,$2,s.name,s.used,s.createtxg,s.creation
		FROM snapshots s WHERE s.dataset_id=$1 ORDER BY s.createtxg`, id, dsName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return
	}
	defer rows.Close()
	out := []snapRow{}
	for rows.Next() {
		var s snapRow
		if err := rows.Scan(&s.ID, &s.DatasetID, &s.Dataset, &s.Name, &s.Used, &s.Createtxg, &s.Creation); err != nil {
			writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
			return
		}
		s.UsedHuman = humanBytes(s.Used)
		out = append(out, s)
	}
	writeOK(w, map[string]any{"snapshots": out})
}

func (a *App) handleSnapCreate(w http.ResponseWriter, r *http.Request) {
	ds, ok := a.datasetByID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if !reSnap.MatchString(body.Name) || indexByte(body.Name, '@') >= 0 {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "快照名非法(字母数字 _ . : - 开头不能是数字之外符号,不含 @)")
		return
	}
	n := a.getNodeOrErr(w, r, ds.NodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, "snapshot_create", map[string]any{
		"dataset": ds.Name, "snapshot": body.Name}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncNodeState(r.Context(), n); err != nil {
		a.logf("快照创建后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"dataset": ds.Name, "snapshot": body.Name})
}

func (a *App) handleSnapDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	var nodeID int64
	var dsName, snapName string
	if err := a.db.QueryRow(r.Context(), `
		SELECT d.node_id,d.name,s.name FROM snapshots s
		JOIN datasets d ON d.id=s.dataset_id WHERE s.id=$1`, id).
		Scan(&nodeID, &dsName, &snapName); err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "快照不存在")
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	// 二次确认:键入快照名(可选但推荐);直接删除亦可接受,审计留痕
	_ = readJSON(r, &body)
	n := a.getNodeOrErr(w, r, nodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, "snapshot_delete", map[string]any{
		"snapshot": dsName + "@" + snapName}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `DELETE FROM snapshots WHERE id=$1`, id)
	if err := a.syncNodeState(r.Context(), n); err != nil {
		a.logf("快照删除后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"deleted": dsName + "@" + snapName})
}

// handleSnapRollback 回滚 zvol/fs 到指定快照(强校验:未映射且无会话)。
func (a *App) handleSnapRollback(w http.ResponseWriter, r *http.Request) {
	ds, ok := a.datasetByID(w, r)
	if !ok {
		return
	}
	var body struct {
		Snapshot string `json:"snapshot"` // 快照短名
		Confirm  string `json:"confirm"`  // 卷名
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if body.Confirm != shortName(ds.Name) {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "回滚将丢弃快照之后的数据,请键入卷名 %q 确认", shortName(ds.Name))
		return
	}
	var snapExists bool
	_ = a.db.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM snapshots WHERE dataset_id=$1 AND name=$2)`,
		ds.ID, body.Snapshot).Scan(&snapExists)
	if !snapExists {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "快照 %s 不存在", body.Snapshot)
		return
	}
	if ds.Mapped {
		writeErr(w, http.StatusConflict, "CONFLICT", "卷已映射为 LUN,回滚前请先解除映射并断开所有会话")
		return
	}
	n := a.getNodeOrErr(w, r, ds.NodeID)
	if n == nil {
		return
	}
	if _, err := a.agentOp(r.Context(), n, "zvol_rollback", map[string]any{
		"dataset": ds.Name, "snapshot": body.Snapshot, "confirm": body.Confirm}); err != nil {
		writeErr(w, http.StatusConflict, "AGENT_ERROR", "%v", err)
		return
	}
	if err := a.syncNodeState(r.Context(), n); err != nil {
		a.logf("回滚后同步失败: %v", err)
	}
	writeOK(w, map[string]any{"rolled_back": ds.Name + "@" + body.Snapshot})
}

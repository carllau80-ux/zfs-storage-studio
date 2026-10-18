package app

import "net/http"

// handleNodeHardware 实时采集节点硬件信息(OS/CPU/内存/磁盘/HBA),由 Agent 读取本机。
func (a *App) handleNodeHardware(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return
	}
	res, err := a.agentQuery(r.Context(), n, "hardware", nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "读取节点硬件信息失败: %v", err)
		return
	}
	writeOK(w, res)
}

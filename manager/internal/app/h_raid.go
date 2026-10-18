package app

import (
	"net/http"
	"strconv"
)

// RAID 实时管理:所有数据经节点 Agent(storcli)读取;写操作需节点显式开启
// allow_mutation,并由 Manager 二次门禁 + 确认串,双端拒绝只读节点。

func (a *App) raidCap(n *NodeRow) (available, allow bool) {
	caps := a.nodeCapMap(n)
	rm, _ := caps["raid"].(map[string]any)
	if rm == nil {
		return false, false
	}
	available, _ = rm["available"].(bool)
	allow, _ = rm["allow_mutation"].(bool)
	return
}

func (a *App) raidNode(w http.ResponseWriter, r *http.Request) *NodeRow {
	id, err := parseID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return nil
	}
	n := a.getNodeOrErr(w, r, id)
	if n == nil {
		return nil
	}
	if avail, _ := a.raidCap(n); !avail {
		writeErr(w, http.StatusConflict, "RAID_UNAVAILABLE", "节点未检测到可用的 storcli/RAID 控制器")
		return nil
	}
	return n
}

// raidWriteGate 只读节点直接 403,不触达 Agent。
func (a *App) raidWriteGate(w http.ResponseWriter, n *NodeRow) bool {
	if _, allow := a.raidCap(n); !allow {
		writeErr(w, http.StatusForbidden, "RAID_READ_ONLY",
			"节点 %s 为只读模式(生产/验证环境),RAID 写操作已禁用", n.Name)
		return false
	}
	return true
}

func (a *App) handleRaidControllers(w http.ResponseWriter, r *http.Request) {
	n := a.raidNode(w, r)
	if n == nil {
		return
	}
	res, err := a.agentQuery(r.Context(), n, "raid_controllers", nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "%v", err)
		return
	}
	_, allow := a.raidCap(n)
	writeOK(w, map[string]any{"controllers": res["controllers"], "allow_mutation": allow})
}

func (a *App) raidCtrlParam(r *http.Request) int {
	cid, _ := strconv.Atoi(r.PathValue("cid"))
	return cid
}

func (a *App) handleRaidVDs(w http.ResponseWriter, r *http.Request) {
	n := a.raidNode(w, r)
	if n == nil {
		return
	}
	cid := a.raidCtrlParam(r)
	res, err := a.agentQuery(r.Context(), n, "raid_vds", map[string]any{"controller": cid})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "%v", err)
		return
	}
	writeOK(w, res)
}

func (a *App) handleRaidPDs(w http.ResponseWriter, r *http.Request) {
	n := a.raidNode(w, r)
	if n == nil {
		return
	}
	cid := a.raidCtrlParam(r)
	res, err := a.agentQuery(r.Context(), n, "raid_pds", map[string]any{"controller": cid})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "%v", err)
		return
	}
	writeOK(w, res)
}

func (a *App) handleRaidEvents(w http.ResponseWriter, r *http.Request) {
	n := a.raidNode(w, r)
	if n == nil {
		return
	}
	cid := a.raidCtrlParam(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	res, err := a.agentQuery(r.Context(), n, "raid_events",
		map[string]any{"controller": cid, "limit": limit})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "%v", err)
		return
	}
	writeOK(w, res)
}

type raidVDCreateReq struct {
	Controller int      `json:"controller"`
	Level      string   `json:"level"`
	Drives     []string `json:"drives"`
	Name       string   `json:"name"`
	Stripesize string   `json:"stripesize"`
	PdPerArray string   `json:"pdperarray"`
	Confirm    string   `json:"confirm"`
}

func (a *App) handleRaidVDCreate(w http.ResponseWriter, r *http.Request) {
	n := a.raidNode(w, r)
	if n == nil {
		return
	}
	if !a.raidWriteGate(w, n) {
		return
	}
	var req raidVDCreateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	if req.Confirm == "" {
		writeErr(w, http.StatusBadRequest, "CONFIRM", "缺少确认串(请输入 CREATE <名称或级别>)")
		return
	}
	res, err := a.agentOp(r.Context(), n, "raid_vd_create", map[string]any{
		"controller": req.Controller, "level": req.Level, "drives": req.Drives,
		"name": req.Name, "stripesize": req.Stripesize,
		"pdperarray": req.PdPerArray, "confirm": req.Confirm,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "%v", err)
		return
	}
	writeOK(w, res)
}

func (a *App) handleRaidVDDelete(w http.ResponseWriter, r *http.Request) {
	n := a.raidNode(w, r)
	if n == nil {
		return
	}
	if !a.raidWriteGate(w, n) {
		return
	}
	cid, _ := strconv.Atoi(r.URL.Query().Get("controller"))
	res, err := a.agentOp(r.Context(), n, "raid_vd_delete", map[string]any{
		"controller": cid, "vd": r.PathValue("vd"), "confirm": r.URL.Query().Get("confirm"),
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "%v", err)
		return
	}
	writeOK(w, res)
}

type raidVDSetReq struct {
	Controller  int     `json:"controller"`
	Name        *string `json:"name"`
	WritePolicy string  `json:"writepolicy"`
	ReadPolicy  string  `json:"readpolicy"`
	IOPolicy    string  `json:"iopolicy"`
	Access      string  `json:"access"`
	Confirm     string  `json:"confirm"`
}

func (a *App) handleRaidVDSet(w http.ResponseWriter, r *http.Request) {
	n := a.raidNode(w, r)
	if n == nil {
		return
	}
	if !a.raidWriteGate(w, n) {
		return
	}
	var req raidVDSetReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION", "%v", err)
		return
	}
	res, err := a.agentOp(r.Context(), n, "raid_vd_set", map[string]any{
		"controller": req.Controller, "vd": r.PathValue("vd"), "name": req.Name,
		"writepolicy": req.WritePolicy, "readpolicy": req.ReadPolicy,
		"iopolicy": req.IOPolicy, "access": req.Access, "confirm": req.Confirm,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "%v", err)
		return
	}
	writeOK(w, res)
}

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

// raidStaticWritable 节点配置本身是否允许 RAID 写(且未开启节点级只读)。
func raidStaticWritable(n *NodeRow) bool {
	a := &App{}
	_, allow := a.raidCap(n)
	if !allow {
		return false
	}
	caps := a.nodeCapMap(n)
	if v, ok := caps["allow_mutation"].(bool); ok && !v {
		return false
	}
	return true
}

// raidWriteAllowed 综合静态能力与 Agent 实时解锁状态。
func raidWriteAllowed(n *NodeRow, state map[string]any) bool {
	if raidStaticWritable(n) {
		return true
	}
	v, _ := state["raid_effective"].(bool)
	return v
}

// raidWriteGate 只读节点 403;若节点已通过口令临时解锁,则以 Agent 实时状态为准。
func (a *App) raidWriteGate(w http.ResponseWriter, r *http.Request, n *NodeRow) bool {
	if raidStaticWritable(n) {
		return true
	}
	st, err := a.agentQuery(r.Context(), n, "mutation_state", nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AGENT", "读取节点写保护状态失败: %v", err)
		return false
	}
	if !raidWriteAllowed(n, st) {
		writeErr(w, http.StatusForbidden, "RAID_READ_ONLY",
			"节点 %s 为只读模式(生产/验证环境),请先输入密码临时解锁", n.Name)
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
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize <= 0 {
		pageSize = 50
	}
	res, err := a.agentQuery(r.Context(), n, "raid_events",
		map[string]any{"controller": cid, "page": page, "page_size": pageSize})
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
	if !a.raidWriteGate(w, r, n) {
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
	if !a.raidWriteGate(w, r, n) {
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
	if !a.raidWriteGate(w, r, n) {
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

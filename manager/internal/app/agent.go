package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// NodeRow 对应 nodes 表。
type NodeRow struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	AgentAddr     string     `json:"agent_addr"`
	AdvertiseAddr string     `json:"advertise_addr"`
	AgentVer      string     `json:"agent_version"`
	ZfsVer        string     `json:"zfs_version"`
	CapJSON       string     `json:"-"`
	Status        string     `json:"status"`
	LastSeen      *time.Time `json:"last_seen_at"`
}

// PortalHost 生成对外 Portal:优先 advertise_addr,其次 agent_addr。
func (n *NodeRow) PortalHost() string {
	if n.AdvertiseAddr != "" {
		return n.AdvertiseAddr
	}
	return n.AgentAddr
}

// SessionLive 一条在线会话(来自 Agent 实时采集)。
type SessionLive struct {
	TargetName string `json:"target_name"` // IQN
	IQN        string `json:"iqn"`
	SID        int64  `json:"sid"`
	State      string `json:"state"`
	Type       string `json:"type"`
	Address    string `json:"address"`
	Alias      string `json:"alias"`
}

// onlineSince 判定节点在线的最大心跳间隔。
const onlineAfter = 40 * time.Second

func (n *NodeRow) online() bool {
	return n.LastSeen != nil && time.Since(*n.LastSeen) < onlineAfter
}

func (a *App) getNode(ctx context.Context, id int64) (*NodeRow, error) {
	var n NodeRow
	err := a.db.QueryRow(ctx, `
		SELECT id,name,agent_addr,advertise_addr,agent_version,zfs_version,capabilities_json,status,last_seen_at
		FROM nodes WHERE id=$1`, id).
		Scan(&n.ID, &n.Name, &n.AgentAddr, &n.AdvertiseAddr, &n.AgentVer, &n.ZfsVer,
			&n.CapJSON, &n.Status, &n.LastSeen)
	return &n, err
}

func (a *App) listNodes(ctx context.Context) ([]NodeRow, error) {
	rows, err := a.db.Query(ctx, `
		SELECT id,name,agent_addr,advertise_addr,agent_version,zfs_version,capabilities_json,status,last_seen_at
		FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeRow{}
	for rows.Next() {
		var n NodeRow
		if err := rows.Scan(&n.ID, &n.Name, &n.AgentAddr, &n.AdvertiseAddr, &n.AgentVer,
			&n.ZfsVer, &n.CapJSON, &n.Status, &n.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---------- Agent HTTP 客户端 ----------

type agentResp struct {
	OK     bool `json:"ok"`
	Result any  `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *App) agentCall(ctx context.Context, node *NodeRow, path string, payload any) (any, error) {
	if !node.online() {
		return nil, fmt.Errorf("节点 %s 当前离线(心跳超时),禁止操作", node.Name)
	}
	if node.AgentAddr == "" {
		return nil, fmt.Errorf("节点 %s 未上报 Agent 地址", node.Name)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	url := "http://" + node.AgentAddr + path
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", a.cfg.AgentToken())
	cl := &http.Client{Timeout: 90 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Agent(%s) 不可达: %v", node.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Agent(%s) HTTP %d: %s", node.Name, resp.StatusCode, truncate(string(raw), 300))
	}
	var ar agentResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("Agent(%s) 响应解析失败: %v", node.Name, err)
	}
	if !ar.OK {
		if ar.Error != nil {
			return nil, fmt.Errorf("%s: %s", ar.Error.Code, ar.Error.Message)
		}
		return nil, fmt.Errorf("Agent(%s) 返回失败", node.Name)
	}
	return ar.Result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// agentQuery 向节点 Agent 发查询类请求。
func (a *App) agentQuery(ctx context.Context, node *NodeRow, kind string, params map[string]any) (map[string]any, error) {
	res, err := a.agentCall(ctx, node, "/api/v1/query", map[string]any{"kind": kind, "params": params})
	if err != nil {
		return nil, err
	}
	m, ok := res.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Agent 返回类型异常")
	}
	return m, nil
}

// agentOp 向节点 Agent 发变更类操作。
func (a *App) agentOp(ctx context.Context, node *NodeRow, op string, params map[string]any) (map[string]any, error) {
	res, err := a.agentCall(ctx, node, "/api/v1/op", map[string]any{"op": op, "params": params})
	if err != nil {
		return nil, err
	}
	m, ok := res.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Agent 返回类型异常")
	}
	return m, nil
}

// ---------- 状态轮询 ----------

type refreshStats struct{ pools, datasets, snaps, targets int }

// syncNodeState 拉取节点真实状态并同步到数据库(期望状态对账)。
func (a *App) syncNodeState(ctx context.Context, node *NodeRow) error {
	// 1. pools
	pm, err := a.agentQuery(ctx, node, "pools", nil)
	if err != nil {
		return err
	}
	poolList, _ := pm["pools"].([]any)
	seenPools := map[string]bool{}
	for _, it := range poolList {
		p, _ := it.(map[string]any)
		name := strAny(p["name"])
		seenPools[name] = true
		if _, e := a.db.Exec(ctx, `
			INSERT INTO pools(node_id,name,guid,state,health,size,allocated,free,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())
			ON CONFLICT (node_id,name) DO UPDATE SET
			  guid=EXCLUDED.guid,state=EXCLUDED.state,health=EXCLUDED.health,
			  size=EXCLUDED.size,allocated=EXCLUDED.allocated,free=EXCLUDED.free,updated_at=now()`,
			node.ID, name, strAny(p["guid"]), strAny(p["state"]), strAny(p["health"]),
			intAny(p["size"]), intAny(p["allocated"]), intAny(p["free"])); e != nil {
			return e
		}
	}
	// 删除已不存在的池(级联清理其卷/快照)
	if _, e := a.db.Exec(ctx, `
		DELETE FROM pools WHERE node_id=$1 AND NOT (name = ANY($2::text[]))`, node.ID, keysOf(seenPools)); e != nil {
		return e
	}
	// 2. datasets + snapshots
	if err := a.syncDatasets(ctx, node); err != nil {
		return err
	}
	// 3. iSCSI targets/luns/acls
	if err := a.syncIscsi(ctx, node); err != nil {
		return err
	}
	// 3b. FC targets(节点具备兼容 HBA 时)
	if err := a.syncFcTargets(ctx, node); err != nil {
		return err
	}
	return nil
}

func (a *App) syncDatasets(ctx context.Context, node *NodeRow) error {
	dm, err := a.agentQuery(ctx, node, "datasets", map[string]any{"types": "volume,filesystem"})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, it := range dsList(dm["datasets"]) {
		name := it["name"]
		seen[name] = true
		pool := poolOfName(name)
		// 池根文件系统(dataset 名 == 池名)同样归属该池行
		if pool == "" {
			pool = name
		}
		var poolID *int64
		var pid int64
		if e := a.db.QueryRow(ctx, `SELECT id FROM pools WHERE node_id=$1 AND name=$2`, node.ID, pool).Scan(&pid); e == nil {
			poolID = &pid
		}
		if _, e := a.db.Exec(ctx, `
			INSERT INTO datasets(node_id,pool_id,name,type,volsize,used,available,compression,origin,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
			ON CONFLICT (node_id,name) DO UPDATE SET
			  pool_id=EXCLUDED.pool_id,type=EXCLUDED.type,volsize=EXCLUDED.volsize,
			  used=EXCLUDED.used,available=EXCLUDED.available,compression=EXCLUDED.compression,
			  origin=EXCLUDED.origin,updated_at=now()`,
			node.ID, poolID, name, it["type"], intAny(it["volsize"]), intAny(it["used"]),
			intAny(it["available"]), it["compression"], it["origin"]); e != nil {
			return e
		}
	}
	// 清理失效行(有映射/挂载的卷保留,标记为对外可见的孤儿)
	if _, e := a.db.Exec(ctx, `
		DELETE FROM datasets WHERE node_id=$1 AND NOT (name = ANY($2::text[]))
		  AND type <> 'snapshot'
		  AND NOT EXISTS (SELECT 1 FROM luns l WHERE l.dataset_id=datasets.id)
		  AND NOT EXISTS (SELECT 1 FROM mappings m WHERE m.dataset_id=datasets.id)`,
		node.ID, keysOf(seen)); e != nil {
		return e
	}
	// snapshots
	sm, err := a.agentQuery(ctx, node, "snapshots", nil)
	if err == nil {
		dsNames := map[int64]string{}
		rows, _ := a.db.Query(ctx, `SELECT id,name FROM datasets WHERE node_id=$1`, node.ID)
		for rows.Next() {
			var id int64
			var nm string
			if rows.Scan(&id, &nm) == nil {
				dsNames[id] = nm
			}
		}
		rows.Close()
		seenSnap := map[int64][]string{} // dataset_id -> 仍存在的快照短名
		for _, it := range dsList(sm["snapshots"]) {
			parent := snapParent(it["name"])
			var dsID int64
			for id, nm := range dsNames {
				if nm == parent {
					dsID = id
					break
				}
			}
			if dsID == 0 {
				continue
			}
			seenSnap[dsID] = append(seenSnap[dsID], snapName(it["name"]))
			if _, e := a.db.Exec(ctx, `
				INSERT INTO snapshots(dataset_id,name,used,createtxg,creation)
				VALUES($1,$2,$3,$4,$5)
				ON CONFLICT (dataset_id,name) DO UPDATE SET
				  used=EXCLUDED.used,createtxg=EXCLUDED.createtxg,creation=EXCLUDED.creation`,
				dsID, snapName(it["name"]), intAny(it["used"]), intAny(it["createtxg"]), it["creation"]); e != nil {
				return e
			}
		}
		// 清理已删除的快照行(含该数据集快照全部消失的情形)
		for dsID := range dsNames {
			names := seenSnap[dsID]
			if _, e := a.db.Exec(ctx, `
				DELETE FROM snapshots WHERE dataset_id=$1 AND NOT (name = ANY($2::text[]))`,
				dsID, names); e != nil {
				return e
			}
		}
	}
	return nil
}

// ---------- 工具 ----------

func strAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func intAny(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		var n int64
		fmt.Sscanf(t, "%d", &n)
		return n
	case json.Number:
		n, _ := t.Int64()
		return n
	}
	return 0
}

func dsList(v any) []map[string]string {
	var out []map[string]string
	items, _ := v.([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		row := map[string]string{}
		for k, val := range m {
			row[k] = strAny(val)
		}
		out = append(out, row)
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// poolOfName / snapParent / snapName 拆分 dataset 名。
func poolOfName(ds string) string {
	i := indexByte(ds, '/')
	if i <= 0 {
		return ""
	}
	return ds[:i]
}

func snapParent(name string) string {
	i := indexByte(name, '@')
	if i <= 0 {
		return name
	}
	return name[:i]
}

func snapName(name string) string {
	i := indexByte(name, '@')
	if i < 0 {
		return name
	}
	return name[i+1:]
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// getNodeOrErr 通用取节点助手(未找到/已下线给出语义化错误)。
func (a *App) getNodeOrErr(w http.ResponseWriter, r *http.Request, id int64) *NodeRow {
	n, err := a.getNode(r.Context(), id)
	if err == pgx.ErrNoRows {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "节点不存在")
		return nil
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "DB", "%v", err)
		return nil
	}
	return n
}

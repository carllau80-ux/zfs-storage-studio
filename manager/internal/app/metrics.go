package app

import (
	"context"
	"time"
)

// MetricPoint 一个采样点(网络/磁盘为字节/秒)。
type MetricPoint struct {
	Ts    int64   `json:"ts"`
	CPU   float64 `json:"cpu"`
	Mem   float64 `json:"mem"`
	NetRx float64 `json:"net_rx"`
	NetTx float64 `json:"net_tx"`
	DiskR float64 `json:"disk_r"`
	DiskW float64 `json:"disk_w"`
}

const metricsRing = 60 // 保留最近 60 个采样点(轮询 12s ≈ 12 分钟)

// collectMetrics 从节点采集一次性能指标并写入环形缓冲。
func (a *App) collectMetrics(ctx context.Context, node *NodeRow) {
	m, err := a.agentQuery(ctx, node, "metrics", nil)
	if err != nil || m["valid"] != true {
		return
	}
	p := MetricPoint{
		Ts:    intAny(m["ts"]),
		CPU:   floatAny(m["cpu_pct"]),
		Mem:   floatAny(m["mem_pct"]),
		NetRx: floatAny(m["net_rx_bps"]),
		NetTx: floatAny(m["net_tx_bps"]),
		DiskR: floatAny(m["disk_r_bps"]),
		DiskW: floatAny(m["disk_w_bps"]),
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.metrics == nil {
		a.metrics = map[int64][]MetricPoint{}
	}
	ring := append(a.metrics[node.ID], p)
	if len(ring) > metricsRing {
		ring = ring[len(ring)-metricsRing:]
	}
	a.metrics[node.ID] = ring
}

func floatAny(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	}
	return 0
}

// metricsSnapshot 汇总全部节点的采样序列(供概览图表)。
func (a *App) metricsSnapshot() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	nodes, _ := a.listNodesCached()
	out := []map[string]any{}
	for _, n := range nodes {
		pts := a.metrics[n.ID]
		if len(pts) == 0 {
			continue
		}
		out = append(out, map[string]any{
			"node_id": n.ID, "name": n.Name, "online": n.online(),
			"points": pts, "updated_at": time.Now().Unix(),
		})
	}
	return out
}

// listNodesCached 用尽小数据量下直接查库(保持实现简单)。
func (a *App) listNodesCached() ([]NodeRow, error) {
	ctx, cancel := contextWithTimeout(3 * time.Second)
	defer cancel()
	return a.listNodes(ctx)
}

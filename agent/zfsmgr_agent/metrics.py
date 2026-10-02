"""本机性能指标采样(纯 /proc,无第三方依赖)。

每次调用返回"距上次调用区间"的速率:CPU%、内存、网络 RX/TX、磁盘读/写字节速率。
首次调用没有基准,返回 valid=False。
"""
from __future__ import annotations

import time


class MetricsSampler:
    def __init__(self):
        self._prev = None

    # ---------- 原始读取 ----------

    @staticmethod
    def _cpu():
        with open("/proc/stat") as f:
            parts = f.readline().split()
        v = [int(x) for x in parts[1:11]]
        idle = v[3] + (v[4] if len(v) > 4 else 0)   # idle + iowait
        total = sum(v)
        return total, idle

    @staticmethod
    def _mem():
        info = {}
        with open("/proc/meminfo") as f:
            for line in f:
                k, _, rest = line.partition(":")
                info[k] = int(rest.strip().split()[0]) * 1024  # kB -> B
        total = info.get("MemTotal", 0)
        avail = info.get("MemAvailable", info.get("MemFree", 0))
        used = max(total - avail, 0)
        pct = (used / total * 100.0) if total else 0.0
        return total, used, pct

    @staticmethod
    def _net():
        rx = tx = 0
        with open("/proc/net/dev") as f:
            for line in f.readlines()[2:]:
                name, _, rest = line.partition(":")
                if name.strip() == "lo":
                    continue
                cols = rest.split()
                rx += int(cols[0])
                tx += int(cols[8])
        return rx, tx

    @staticmethod
    def _disk():
        rd = wr = 0
        with open("/proc/diskstats") as f:
            for line in f:
                c = line.split()
                if len(c) < 10:
                    continue
                name = c[2]
                if name.startswith(("loop", "ram", "zram")):
                    continue
                # 仅统计整盘(排除分区:内核会列出 <disk><partnum>,以是否存在纯父设备近似处理)
                if len(name) > 3 and name[:-1].isalpha():
                    continue
                rd += int(c[5]) * 512
                wr += int(c[9]) * 512
        return rd, wr

    # ---------- 采样 ----------

    def sample(self) -> dict:
        now = time.time()
        cur = {
            "t": now,
            "cpu": self._cpu(),
            "mem": self._mem(),
            "net": self._net(),
            "disk": self._disk(),
        }
        prev, self._prev = self._prev, cur
        total, used, mem_pct = cur["mem"]
        base = {
            "ts": int(now),
            "mem_total": total,
            "mem_used": used,
            "mem_pct": round(mem_pct, 1),
            "cpu_pct": None,
            "net_rx_bps": None,
            "net_tx_bps": None,
            "disk_r_bps": None,
            "disk_w_bps": None,
            "valid": False,
        }
        if not prev:
            return base
        dt = max(cur["t"] - prev["t"], 0.001)
        # CPU
        d_total = cur["cpu"][0] - prev["cpu"][0]
        d_idle = cur["cpu"][1] - prev["cpu"][1]
        cpu_pct = (1.0 - d_idle / d_total) * 100.0 if d_total > 0 else 0.0
        # 网络/磁盘速率
        nrx = max(cur["net"][0] - prev["net"][0], 0) / dt
        ntx = max(cur["net"][1] - prev["net"][1], 0) / dt
        drd = max(cur["disk"][0] - prev["disk"][0], 0) / dt
        dwr = max(cur["disk"][1] - prev["disk"][1], 0) / dt
        base.update({
            "cpu_pct": round(min(max(cpu_pct, 0.0), 100.0), 1),
            "net_rx_bps": int(nrx),
            "net_tx_bps": int(ntx),
            "disk_r_bps": int(drd),
            "disk_w_bps": int(dwr),
            "valid": True,
        })
        return base

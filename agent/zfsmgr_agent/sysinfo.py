"""本机能力探测(sysinfo):ZFS 版本、JSON 支持、FC HBA、磁盘清单等。"""
from __future__ import annotations

import glob
import json
import os
import platform
import socket
import subprocess
from datetime import datetime, timezone

AGENT_VERSION = "0.4.0"


def _run(argv: list[str]) -> tuple[int, str, str]:
    try:
        p = subprocess.run(argv, capture_output=True, text=True, timeout=20)
        return p.returncode, p.stdout, p.stderr
    except Exception:
        return -1, "", ""


def zfs_version() -> str:
    rc, out, _ = _run(["zfs", "version"])
    if rc != 0:
        return ""
    return out.strip().replace("\n", " / ")


def os_info() -> str:
    try:
        with open("/etc/os-release") as f:
            for line in f:
                if line.startswith("PRETTY_NAME="):
                    return line.split("=", 1)[1].strip().strip('"')
    except OSError:
        pass
    return "linux"


def fc_detect() -> dict:
    """FC 能力探测:仅当存在 QLogic 24xx/26xx 类 HBA 且内核支持 tcm_qla2xxx 时可行。
    本平台首期实现只读探测,无兼容 HBA 即 fc_capable=false(前端置灰)。"""
    rc, _, _ = _run(["modprobe", "-n", "-v", "tcm_qla2xxx"])
    try:
        hba_dirs = glob.glob("/sys/class/fc_host/*")
    except Exception:
        hba_dirs = []
    # 厂商 1077 = QLogic;24xx/26xx 见 pci device id 段
    qlogic = False
    for h in hba_dirs:
        try:
            with open(os.path.join(h, "device", "vendor")) as f:
                if f.read().strip() in ("0x1077", "1077"):
                    qlogic = True
        except OSError:
            pass
    capable = bool(hba_dirs) and rc == 0 and qlogic
    reason = ""
    if not hba_dirs:
        reason = "未检测到 FC HBA"
    elif not qlogic:
        reason = "FC HBA 非 QLogic 24xx/26xx 系列"
    elif rc != 0:
        reason = "内核缺少 tcm_qla2xxx 模块"
    return {"fc_capable": capable, "reason": reason,
            "hba_ports": [os.path.basename(h) for h in hba_dirs]}


def os_details() -> dict:
    d = {}
    try:
        with open("/etc/os-release") as f:
            for line in f:
                if "=" in line:
                    k, v = line.split("=", 1)
                    d[k.strip()] = v.strip().strip('"')
    except OSError:
        pass
    uptime = 0
    try:
        with open("/proc/uptime") as f:
            uptime = int(float(f.read().split()[0]))
    except OSError:
        pass
    return {
        "pretty": d.get("PRETTY_NAME") or "Linux",
        "id": d.get("ID", ""),
        "version": d.get("VERSION_ID") or d.get("VERSION", ""),
        "kernel": platform.release(),
        "arch": platform.machine(),
        "hostname": socket.gethostname(),
        "uptime_s": uptime,
    }


def cpu_details() -> dict:
    try:
        with open("/proc/cpuinfo") as f:
            text = f.read()
    except OSError:
        text = ""
    blocks = [b for b in text.split("\n\n") if b.strip()]
    models, mhz = [], []
    sockets, cores = set(), set()
    for b in blocks:
        d = {}
        for line in b.splitlines():
            if ":" in line:
                k, v = line.split(":", 1)
                d[k.strip()] = v.strip()
        model = d.get("model name") or d.get("model") or d.get("Hardware") or ""
        if model:
            models.append(model)
        if d.get("cpu MHz"):
            try:
                mhz.append(float(d["cpu MHz"]))
            except ValueError:
                pass
        phys = d.get("physical id")
        core = d.get("core id")
        if phys is not None:
            sockets.add(phys)
        if phys is not None and core is not None:
            cores.add((phys, core))
    logical = len(blocks)
    physical = len(cores) or logical
    sock_count = len(sockets) or 1
    return {
        "model": models[0] if models else "",
        "logical": logical,
        "physical": physical,
        "sockets": sock_count,
        "cores_per_socket": round(physical / sock_count) if sock_count else physical,
        "mhz": round(sum(mhz) / len(mhz)) if mhz else 0,
    }


def memory_details() -> dict:
    raw = {}
    try:
        with open("/proc/meminfo") as f:
            for line in f:
                k, v = line.split(":", 1)
                raw[k] = int(v.strip().split()[0]) * 1024  # kB -> bytes
    except (OSError, ValueError):
        pass
    total = raw.get("MemTotal", 0)
    avail = raw.get("MemAvailable", raw.get("MemFree", 0))
    used = max(0, total - avail)
    return {
        "total": total,
        "available": avail,
        "free": raw.get("MemFree", 0),
        "used": used,
        "used_pct": round(used * 100 / total, 1) if total else 0,
        "swap_total": raw.get("SwapTotal", 0),
        "swap_free": raw.get("SwapFree", 0),
        "swap_used": max(0, raw.get("SwapTotal", 0) - raw.get("SwapFree", 0)),
    }


def _is_physical_disk(name: str) -> bool:
    # 排除 zvol(zd*)、网络块(nbd/rbd)、loop/ram/rom/dm 等非物理整盘
    return name.startswith(("sd", "vd", "nvme", "hd", "xvd", "mmcblk", "md"))


def capabilities() -> dict:
    return {
        "agent_version": AGENT_VERSION,
        "zfs_version": zfs_version(),
        "os": os_info(),
        "json_supported": True,
        "sparse_property": False,   # 本发行版 zfs 无 sparse 属性(用 create -s)
        "fc": fc_detect(),
        "mkfs": {"ext4": os.path.exists("/sbin/mkfs.ext4"),
                 "xfs": os.path.exists("/sbin/mkfs.xfs"),
                 "ntfs": os.path.exists("/sbin/mkfs.ntfs")},
    }


def disks() -> list[dict]:
    """lsblk JSON 读取物理整盘(建池候选 + 节点硬件展示)。"""
    rc, out, _ = _run(["lsblk", "-b", "-J", "-o",
                       "NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,TRAN,ROTA,MODEL,SERIAL,WWN,VENDOR"])
    if rc != 0:
        return []
    try:
        raw = json.loads(out)
    except Exception:
        return []
    res = []

    def walk(dev: dict, is_root: bool):
        name = dev.get("name", "")
        if (is_root and dev.get("type") == "disk" and _is_physical_disk(name)
                and int(dev.get("size", 0) or 0) > 0):
            mps = [m for m in (dev.get("mountpoints") or []) if m]
            rota = bool(dev.get("rota"))
            res.append({
                "path": "/dev/" + name,
                "name": name,
                "size": int(dev.get("size", 0) or 0),
                "fs": dev.get("fstype") or "",
                "mounted": bool(mps),
                "mountpoints": mps,
                "children": len(dev.get("children", []) or []),
                "model": (dev.get("model") or "").strip(),
                "serial": (dev.get("serial") or "").strip(),
                "wwn": dev.get("wwn") or "",
                "vendor": (dev.get("vendor") or "").strip(),
                "tran": dev.get("tran") or "",
                "rota": rota,
                "media": "HDD" if rota else "SSD",
            })
        for c in dev.get("children", []) or []:
            walk(c, False)

    for d in raw.get("blockdevices", []):
        walk(d, True)
    return res


def _read_sys(path: str) -> str:
    try:
        with open(path) as f:
            return f.read().strip()
    except OSError:
        return ""


def fc_ports() -> list[dict]:
    out = []
    for h in sorted(glob.glob("/sys/class/fc_host/*")):
        dev = os.path.join(h, "device")
        try:
            pci = os.path.basename(os.path.realpath(dev))
            driver = os.path.basename(os.path.realpath(os.path.join(dev, "driver")))
        except OSError:
            pci, driver = "", ""
        out.append({
            "host": os.path.basename(h),
            "port_name": _read_sys(os.path.join(h, "port_name")),
            "node_name": _read_sys(os.path.join(h, "node_name")),
            "port_state": _read_sys(os.path.join(h, "port_state")),
            "speed": _read_sys(os.path.join(h, "speed")),
            "symbolic_name": _read_sys(os.path.join(h, "symbolic_name")),
            "fabric_name": _read_sys(os.path.join(h, "fabric_name")),
            "driver": driver,
            "pci": pci,
        })
    return out


def pci_storage_adapters() -> list[dict]:
    """从 lspci 提取 FC/SAS/SCSI HBA(RAID 卡由 storcli 单独展示)。"""
    rc, out, _ = _run(["lspci", "-Dnnk"])
    if rc != 0:
        return []
    wanted = ("fibre channel", "serial attached scsi", "scsi storage controller")
    res = []
    cur = None
    for line in out.splitlines():
        if line and not line.startswith("\t"):
            cur = None
            try:
                slot, rest = line.split(" ", 1)
                cls = rest.split(":", 1)[0].split("[")[0].strip()
                desc = rest.split(":", 1)[1].strip()
            except (ValueError, IndexError):
                continue
            if any(w in cls.lower() for w in wanted):
                cur = {"slot": slot, "class": cls, "description": desc, "driver": ""}
                res.append(cur)
        elif cur is not None and "Kernel driver in use:" in line:
            cur["driver"] = line.split(":", 1)[1].strip()
    return res


def hba_info() -> dict:
    return {"fc_ports": fc_ports(), "adapters": pci_storage_adapters()}


def hardware() -> dict:
    return {
        "os": os_details(),
        "cpu": cpu_details(),
        "memory": memory_details(),
        "disks": disks(),
        "hba": hba_info(),
        "collected_at": datetime.now(timezone.utc).isoformat(),
    }


def mounted_zvols() -> list[str]:
    """返回本机已挂载的 zvol dataset 列表(格式化/销毁护栏)。"""
    out = []
    try:
        with open("/proc/mounts") as f:
            for line in f:
                dev = line.split(" ")[0]
                if dev.startswith("/dev/zvol/"):
                    out.append(dev[len("/dev/zvol/"):])
    except OSError:
        pass
    return out

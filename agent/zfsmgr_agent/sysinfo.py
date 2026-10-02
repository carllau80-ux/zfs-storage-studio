"""本机能力探测(sysinfo):ZFS 版本、JSON 支持、FC HBA、磁盘清单等。"""
from __future__ import annotations

import glob
import json
import os
import subprocess

AGENT_VERSION = "0.1.0"


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
    """lsblk JSON 读取候选整盘(建池用)。"""
    rc, out, _ = _run(["lsblk", "-b", "-J", "-o", "NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,TRAN"])
    if rc != 0:
        return []
    try:
        raw = json.loads(out)
    except Exception:
        return []
    res = []

    def walk(dev: dict, is_root: bool):
        name = dev.get("name", "")
        if is_root and dev.get("type") == "disk":
            parts = name.startswith("sd") or name.startswith("vd") or name.startswith("nvme")
            res.append({
                "path": "/dev/" + name,
                "name": name,
                "size": int(dev.get("size", 0) or 0),
                "fs": dev.get("fstype") or "",
                "mounted": bool(dev.get("mountpoints")),
                "children": len(dev.get("children", []) or []),
            })
        for c in dev.get("children", []) or []:
            walk(c, False)

    for d in raw.get("blockdevices", []):
        walk(d, True)
    return res


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

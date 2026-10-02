"""卷文件系统格式化(FormatService):白名单 mkfs + blkid 回读校验。"""
from __future__ import annotations

import os
import re
import subprocess

MKFS = {
    "ext4": "/sbin/mkfs.ext4",
    "xfs": "/sbin/mkfs.xfs",
    "ntfs": "/sbin/mkfs.ntfs",
}


class FmtError(Exception):
    def __init__(self, message: str, code: str = "FMT_ERROR"):
        super().__init__(message)
        self.code = code


def zvol_dev(dataset: str) -> str:
    d = "/dev/zvol/" + dataset
    return d if os.path.exists(d) else ""


def is_mounted(dataset: str) -> bool:
    """检查该 zvol 是否被本机挂载(/proc/mounts)。"""
    real = ""
    d = "/dev/zvol/" + dataset
    if os.path.exists(d):
        real = os.path.realpath(d)
    try:
        with open("/proc/mounts") as f:
            for line in f:
                dev = line.split(" ")[0]
                if dev == d or dev == real or (real and dev == real) or \
                   (dev.startswith("/dev/zvol/") and dataset in dev):
                    return True
    except OSError:
        pass
    return False


def blkid_type(dataset: str) -> str:
    """blkid 只读探测文件系统类型;无则 'none'。"""
    dev = zvol_dev(dataset)
    if not dev:
        return "unknown"
    try:
        out = subprocess.run(["blkid", "-o", "value", "-s", "TYPE", dev],
                             capture_output=True, text=True, timeout=20)
        if out.returncode == 0:
            return out.stdout.strip() or "none"
        return "none"
    except Exception:
        return "unknown"


class FormatService:
    """仅允许白名单绝对路径,格式化前强校验:未被 backstore 引用、无本机挂载。"""

    def __init__(self, backstore_devs: callable):
        self._backstore_devs = backstore_devs  # lio.backstore_devs

    def format(self, dataset: str, fs: str, confirm: str = "") -> dict:
        fs = (fs or "").strip().lower()   # 文件系统标识统一小写(ext4/xfs/ntfs)
        if fs not in MKFS:
            raise FmtError(f"filesystem 须为 {'|'.join(MKFS)}", code="VALIDATION")
        if confirm != dataset.rsplit("/", 1)[-1]:
            raise FmtError("格式化将清除全部数据,请键入卷名确认", code="CONFIRM")
        dev = zvol_dev(dataset)
        if not dev:
            raise FmtError(f"块设备不存在: /dev/zvol/{dataset}", code="NOT_FOUND")
        if is_mounted(dataset):
            raise FmtError("卷已被本机挂载,请先卸载", code="CONFLICT")
        for bdev in self._backstore_devs():
            from .lio import dataset_of_dev
            if bdev and (bdev == dev or dataset_of_dev(bdev) == dataset):
                raise FmtError("卷已被 iSCSI backstore 引用(已映射),禁止格式化", code="CONFLICT")
        binp = MKFS[fs]
        if not os.path.exists(binp):
            raise FmtError(f"mkfs 工具缺失: {binp}", code="NOT_FOUND")
        try:
            p = subprocess.run([binp, "-F", dev],
                               capture_output=True, text=True, timeout=300)
        except FileNotFoundError:
            raise FmtError(f"mkfs 工具不可执行: {binp}", code="NOT_FOUND")
        if p.returncode != 0:
            raise FmtError(f"{fs} 格式化失败: {(p.stderr or p.stdout).strip()[:300]}")
        actual = blkid_type(dataset)
        if actual == "unknown":
            actual = fs
        return {"filesystem_type": str(actual).lower(), "status": "ready", "device": dev}

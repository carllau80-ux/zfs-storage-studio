"""ZFS 2.3+ JSON 读取适配层(ZfsJsonClient)。

原则:
- 一律 argv 数组执行,禁止 shell 拼接;
- 读命令统一 -j -p --json-int;
- 解析函数为纯函数,便于用真实输出 fixture 做单元测试;
- 写操作白名单 argv 执行 + 操作后 JSON 回读校验(7.6)。
"""
from __future__ import annotations

import os
import re
import subprocess
from datetime import datetime, timezone

NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$")
SNAP_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$")
SIZE_RE = re.compile(r"(?i)^(\d+(\.\d+)?)([kKmMgGtTpPeE])?$")


class ZfsError(Exception):
    def __init__(self, message: str, code: str = "ZFS_ERROR", stderr: str = ""):
        super().__init__(message)
        self.code = code
        self.stderr = (stderr or "").strip()[:500]


class ZfsJsonClient:
    def __init__(self, timeout: float = 90.0):
        self.timeout = timeout

    # ---------- 底层执行 ----------

    def run(self, argv: list[str], timeout: float | None = None) -> tuple[int, str, str]:
        """执行 argv,返回 (rc, stdout, stderr)。"""
        try:
            p = subprocess.run(argv, capture_output=True, text=True,
                               timeout=timeout or self.timeout)
        except FileNotFoundError:
            raise ZfsError(f"命令不存在: {argv[0]}", code="NOT_FOUND")
        except subprocess.TimeoutExpired:
            raise ZfsError(f"命令超时: {' '.join(argv[:3])}...", code="TIMEOUT")
        return p.returncode, p.stdout, p.stderr

    def json_cmd(self, argv: list[str]) -> dict:
        rc, out, err = self.run(argv)
        if rc != 0:
            raise ZfsError(err or f"{argv[0]} 返回 {rc}", stderr=err)
        try:
            import json
            return json.loads(out)
        except Exception:
            raise ZfsError(f"{argv[0]} 输出非 JSON", code="PARSE")

    def check_version(self) -> str:
        rc, out, _ = self.run(["zfs", "version"])
        if rc != 0:
            raise ZfsError("无法执行 zfs version", code="ZFS_MISSING")
        return out.strip().replace("\n", " / ")

    # ---------- 版本校验(7.2 契约) ----------

    @staticmethod
    def check_output_version(raw: dict, command: str) -> None:
        ov = raw.get("output_version", {})
        if ov.get("command") != command or int(ov.get("vers_major", -1)) != 0:
            raise ZfsError(
                f"不支持的 ZFS JSON 契约 {ov} (期望 {command} v0.x)",
                code="JSON_CONTRACT")

    # ---------- 读取 ----------

    def zpool_list(self) -> list[dict]:
        raw = self.json_cmd(["zpool", "list", "-j", "-p", "--json-int"])
        self.check_output_version(raw, "zpool list")
        return parse_pools(raw)

    def zpool_status(self, pool: str = "") -> dict:
        argv = ["zpool", "status", "-j", "-p", "--json-int"]
        if pool:
            argv.append(pool)
        raw = self.json_cmd(argv)
        self.check_output_version(raw, "zpool status")
        return parse_pool_status(raw)

    def datasets(self, types: str = "volume,filesystem") -> list[dict]:
        raw = self.json_cmd(["zfs", "list", "-j", "-p", "--json-int",
                             "-t", types,
                             "-o", "name,type,used,available,volsize,compression,origin"])
        self.check_output_version(raw, "zfs list")
        return parse_datasets(raw)

    def snapshots(self) -> list[dict]:
        raw = self.json_cmd(["zfs", "list", "-j", "-p", "--json-int",
                             "-t", "snapshot",
                             "-o", "name,used,createtxg,creation"])
        self.check_output_version(raw, "zfs list")
        return parse_snapshots(raw)

    def volprops(self, dataset: str) -> dict:
        return self.dsprops(dataset, self.GET_PROPS_VOL)

    def fsprops(self, dataset: str) -> dict:
        return self.dsprops(dataset, self.GET_PROPS_FS)

    def prop(self, dataset: str, prop_name: str) -> str:
        raw = self.json_cmd(["zfs", "get", "-j", "-p", "--json-int", prop_name, dataset])
        props = parse_props(raw)
        return props.get(prop_name, "")

    # ---------- 属性配置白名单 ----------

    ALLOW_SET = {
        "compression": r"^(on|off|lz4|zstd|zstd-[1-9]|zstd-fast(-[0-9]+)?|lzjb|gzip(-[1-9])?|zle)$",
        "dedup": r"^(on|off|verify)$",
        "mountpoint": r"^(none|legacy|/[A-Za-z0-9_./\-]+)$",
    }
    GET_PROPS_VOL = ["volsize", "volblocksize", "volmode", "compression", "dedup",
                     "compressratio", "logicalused"]
    GET_PROPS_FS = ["compression", "dedup", "compressratio", "recordsize", "quota",
                    "refquota", "atime", "mounted", "mountpoint"]

    def fs_create(self, pool: str, name: str, compression: str = "",
                  dedup: str = "", mountpoint: str = "none") -> None:
        if not NAME_RE.match(name):
            raise ZfsError("数据集名非法")
        ds = f"{pool}/{name}"
        argv = ["zfs", "create", "-o", f"mountpoint={mountpoint}"]
        if compression and compression != "inherit":
            argv += ["-o", f"compression={compression}"]
        if dedup and dedup != "inherit":
            argv += ["-o", f"dedup={dedup}"]
        argv.append(ds)
        rc, _, err = self.run(argv, timeout=120)
        if rc != 0:
            raise ZfsError(err or "zfs create 失败", stderr=err)
        if not any(d["name"] == ds for d in self.datasets("filesystem")):
            raise ZfsError("回读校验失败: filesystem 未按预期创建", code="READBACK")

    def clone_zvol(self, source_dataset: str, snapshot: str, new_name: str) -> str:
        """zfs clone:以源卷快照克隆出可写新卷(同池)。"""
        if not NAME_RE.match(new_name):
            raise ZfsError("新卷名非法")
        pool = source_dataset.split("/", 1)[0]
        target = f"{pool}/{new_name}"
        rc, _, err = self.run(
            ["zfs", "clone", f"{source_dataset}@{snapshot}", target], timeout=300)
        if rc != 0:
            raise ZfsError(err or "zfs clone 失败", stderr=err)
        vols = self.datasets("volume")
        hit = [d for d in vols if d["name"] == target]
        if not hit:
            raise ZfsError("回读校验失败: 克隆卷未出现", code="READBACK")
        return target

    def set_prop(self, dataset: str, prop: str, value: str) -> dict:
        if prop not in self.ALLOW_SET:
            raise ZfsError(f"属性 {prop} 不在可配置白名单", code="VALIDATION")
        if value == "inherit":
            rc, _, err = self.run(["zfs", "inherit", prop, dataset])
            if rc != 0:
                raise ZfsError(err or "zfs inherit 失败", stderr=err)
        else:
            if not re.match(self.ALLOW_SET[prop], value):
                raise ZfsError(f"属性 {prop} 的值非法: {value}", code="VALIDATION")
            rc, _, err = self.run(["zfs", "set", f"{prop}={value}", dataset], timeout=300)
            if rc != 0:
                raise ZfsError(err or f"zfs set {prop} 失败", stderr=err)
        if prop == "mountpoint" and value not in ("inherit", "none", "legacy"):
            os.makedirs(value, exist_ok=True) if hasattr(os, "makedirs") else None
            self.run(["zfs", "mount", dataset])   # 幂等:已挂载返回非零,忽略
        # 回读校验
        raw = self.json_cmd(["zfs", "get", "-j", "-p", "--json-int", prop, dataset])
        got = parse_props(raw).get(prop, "")
        if value == "inherit":
            return {prop: "inherit"}
        if str(got) != value:
            raise ZfsError(f"回读校验失败: {prop} 期望 {value} 实得 {got}", code="READBACK")
        return {prop: got}

    def dsprops(self, dataset: str, props: list[str]) -> dict:
        for p_ in props:
            if p_ not in self.GET_PROPS_VOL + self.GET_PROPS_FS:
                raise ZfsError(f"不可读取的属性: {p_}", code="VALIDATION")
        raw = self.json_cmd(["zfs", "get", "-j", "-p", "--json-int",
                             ",".join(props), dataset])
        return parse_props(raw)

    # ---------- 变更操作(白名单 + 回读) ----------

    VDEV_TYPES = {"stripe", "mirror", "raidz", "raidz1", "raidz2", "raidz3"}
    CREATE_OPTS = {
        "ashift": r"^[0-9]+$",
        "compression": r"^(on|off|lz4|zstd|zstd-[1-9]|zstd-fast(-[0-9]+)?|gzip(-[1-9])?|lzjb|zle)$",
        "recordsize": r"(?i)^(4k|8k|16k|32k|64k|128k|256k|512k|1m)$",
        "dedup": r"^(on|off|verify)$",
        "mountpoint": r"^(none|legacy|/[A-Za-z0-9_./\-]+)$",
    }

    def pool_create(self, pool_name: str, layout: list[dict], options: dict | None = None,
                    log: list[str] | None = None, cache: list[str] | None = None,
                    spare: list[str] | None = None, force: bool = False) -> None:
        """zpool create:支持多 vdev 组(stripe/mirror/raidz*)与 log/cache/spare。

        layout: [{"type": "mirror", "disks": ["/dev/sdX", ...]}, ...]
        options: {"ashift": "12", "compression": "lz4", "recordsize": "16k", ...}
        """
        if not NAME_RE.match(pool_name):
            raise ZfsError("池名非法")
        if not layout or not layout[0].get("disks"):
            raise ZfsError("至少需要一组数据 vdev 并选择设备", code="VALIDATION")
        all_devs: list[str] = []

        def _chk(dev: str) -> None:
            if not re.match(r"^/dev/[A-Za-z0-9_/-]+$", dev):
                raise ZfsError(f"设备路径非法: {dev}")
            all_devs.append(dev)

        vdev_args: list[str] = []
        for g in layout:
            gtype = str(g.get("type") or "stripe").lower()
            disks = [str(x) for x in (g.get("disks") or [])]
            if gtype not in self.VDEV_TYPES:
                raise ZfsError(f"不支持的 vdev 类型: {gtype}", code="VALIDATION")
            if not disks:
                raise ZfsError(f"vdev 组({gtype})未选择设备", code="VALIDATION")
            for d in disks:
                _chk(d)
            if gtype == "stripe":
                vdev_args += disks
            else:
                vdev_args += [gtype] + disks
        for label, lst in (("log", log), ("cache", cache), ("spare", spare)):
            devs = [str(x) for x in (lst or [])]
            if devs:
                for d in devs:
                    _chk(d)
                vdev_args += [label] + devs

        if len(set(all_devs)) != len(all_devs):
            raise ZfsError("同一设备被重复使用(每组/每类只能出现一次)", code="VALIDATION")

        # 校验并区分:ashift 是池属性(-o);compression/recordsize/dedup 是
        # 数据集属性,zpool create 不接受,建池后以 zfs set 施加到池根。
        fs_opts: list[tuple[str, str]] = []
        argv = ["zpool", "create"]
        if force:
            argv.append("-f")
        for k, v in (options or {}).items():
            if v in (None, "", "inherit", "default"):
                continue
            rule = self.CREATE_OPTS.get(k)
            if rule is None:
                raise ZfsError(f"不支持的建池参数: {k}", code="VALIDATION")
            if not re.match(rule, str(v)):
                raise ZfsError(f"建池参数 {k} 的值非法: {v}", code="VALIDATION")
            if k == "ashift":
                argv += ["-o", f"{k}={v}"]
            else:
                fs_opts.append((k, str(v)))
        argv += [pool_name] + vdev_args
        rc, _, err = self.run(argv, timeout=600)
        if rc != 0:
            raise ZfsError(err or "zpool create 失败", stderr=err)
        # 池根数据集属性
        for k, v in fs_opts:
            rc, _, err = self.run(["zfs", "set", f"{k}={v}", pool_name], timeout=120)
            if rc != 0:
                raise ZfsError(f"设置 {k}={v} 失败: {err or 'zfs set 错误'}", stderr=err)
        self._pool_exists(pool_name)

    def pool_destroy(self, pool_name: str) -> None:
        rc, _, err = self.run(["zpool", "destroy", pool_name], timeout=300)
        if rc != 0:
            raise ZfsError(err or "zpool destroy 失败", stderr=err)

    def pool_scrub(self, pool_name: str, action: str) -> None:
        argv = ["zpool", "scrub"] if action == "start" else ["zpool", "scrub", "-s"]
        argv.append(pool_name)
        rc, _, err = self.run(argv)
        if rc != 0:
            raise ZfsError(err or "scrub 失败", stderr=err)

    def zvol_create(self, pool: str, name: str, size_bytes: int, compression: str = "",
                    volblocksize: str = "", sparse: bool = False) -> None:
        if not NAME_RE.match(name):
            raise ZfsError("卷名非法")
        if size_bytes <= 0:
            raise ZfsError("容量非法")
        ds = f"{pool}/{name}"
        argv = ["zfs", "create", "-V", str(size_bytes)]
        if sparse:
            argv.append("-s")
        if volblocksize:
            argv += ["-b", volblocksize]
        if compression:
            argv += ["-o", f"compression={compression}"]
        argv.append(ds)
        rc, _, err = self.run(argv, timeout=300)
        if rc != 0:
            raise ZfsError(err or "zfs create 失败", stderr=err)
        # 回读校验
        if not any(d["name"] == ds and d["volsize"] == size_bytes for d in self.datasets("volume")):
            raise ZfsError("回读校验失败: zvol 未按预期创建", code="READBACK")

    def zvol_resize(self, dataset: str, size_bytes: int, confirm: str = "") -> None:
        cur = self.volsize(dataset)
        if size_bytes < cur:
            if confirm != dataset.rsplit("/", 1)[-1]:
                raise ZfsError("缩容需以卷名二次确认(破坏性操作)", code="CONFIRM")
        rc, _, err = self.run(["zfs", "set", f"volsize={size_bytes}", dataset], timeout=300)
        if rc != 0:
            raise ZfsError(err or "zfs set volsize 失败", stderr=err)
        if self.volsize(dataset) != size_bytes:
            raise ZfsError("回读校验失败: volsize 未生效", code="READBACK")

    def dataset_destroy(self, dataset: str, confirm: str, recursive: bool = False) -> None:
        if confirm != dataset.rsplit("/", 1)[-1]:
            raise ZfsError("请键入卷名以确认删除", code="CONFIRM")
        snaps = [s for s in self.snapshots() if s["dataset"] == dataset]
        if snaps and not recursive:
            raise ZfsError(f"存在 {len(snaps)} 个快照,请先删除(或显式 recursive)", code="CONFLICT")
        argv = ["zfs", "destroy"]
        if recursive:
            argv.append("-r")
        argv.append(dataset)
        rc, _, err = self.run(argv, timeout=300)
        if rc != 0:
            raise ZfsError(err or "zfs destroy 失败", stderr=err)

    def snapshot_create(self, dataset: str, snapshot: str) -> None:
        if not SNAP_RE.match(snapshot) or "@" in snapshot:
            raise ZfsError("快照名非法")
        full = f"{dataset}@{snapshot}"
        rc, _, err = self.run(["zfs", "snapshot", full])
        if rc != 0:
            raise ZfsError(err or "zfs snapshot 失败", stderr=err)

    def snapshot_delete(self, snapshot: str) -> None:
        if "@" not in snapshot:
            raise ZfsError("快照全名须含 @")
        rc, _, err = self.run(["zfs", "destroy", snapshot])
        if rc != 0:
            raise ZfsError(err or "zfs destroy snapshot 失败", stderr=err)

    def rollback(self, dataset: str, snapshot: str, confirm: str = "") -> None:
        if confirm != dataset.rsplit("/", 1)[-1]:
            raise ZfsError("回滚需键入卷名二次确认", code="CONFIRM")
        snaps = sorted([s for s in self.snapshots() if s["dataset"] == dataset],
                       key=lambda s: s["createtxg"])
        if not snaps or snaps[-1]["snap"] != snapshot:
            raise ZfsError("仅允许回滚到最新快照(请先删除其后快照)", code="CONFLICT")
        full = f"{dataset}@{snapshot}"
        rc, _, err = self.run(["zfs", "rollback", full], timeout=300)
        if rc != 0:
            raise ZfsError(err or "zfs rollback 失败", stderr=err)

    # ---------- 内部 ----------

    def volsize(self, dataset: str) -> int:
        for d in self.datasets("volume"):
            if d["name"] == dataset:
                return int(d["volsize"] or 0)
        raise ZfsError(f"卷不存在: {dataset}", code="NOT_FOUND")

    def _pool_exists(self, pool_name: str) -> None:
        if not any(p["name"] == pool_name for p in self.zpool_list()):
            raise ZfsError(f"回读校验失败: 池 {pool_name} 未出现", code="READBACK")


# ================= 纯解析函数(输入为真实 JSON dict) =================

def _num(v) -> int:
    """属性值可能是 int(JSON 整数)或数字字符串,或 '-'。"""
    if v is None:
        return 0
    if isinstance(v, bool):
        return int(v)
    if isinstance(v, (int, float)):
        return int(v)
    s = str(v).strip()
    if s in ("", "-"):
        return 0
    try:
        return int(float(s))
    except ValueError:
        return 0


def parse_pools(raw: dict) -> list[dict]:
    ZfsJsonClient.check_output_version(raw, "zpool list")
    out = []
    for name, p in raw.get("pools", {}).items():
        props = p.get("properties", {})
        pv = lambda k: (props.get(k) or {}).get("value")
        out.append({
            "name": name,
            "state": str(p.get("state", "")),
            "guid": str(p.get("pool_guid", "")),
            "size": _num(pv("size")),
            "allocated": _num(pv("allocated")),
            "free": _num(pv("free")),
            "health": str(pv("health") or ""),
        })
    return out


def _size_to_bytes(v: str) -> int:
    """把 zpool status 的 '31.5G'/'222K' 转成字节。"""
    s = (v or "").strip()
    if not s or s == "-":
        return 0
    mult = {"": 1, "B": 1, "K": 1 << 10, "M": 1 << 20,
            "G": 1 << 30, "T": 1 << 40, "P": 1 << 50}
    for suf, m in mult.items():
        if s.endswith(suf) and (suf == "" or len(s) > len(suf)):
            try:
                return int(float(s[: len(s) - len(suf)]) * m)
            except ValueError:
                return 0
    try:
        return int(float(s))
    except ValueError:
        return 0


def parse_pool_status(raw: dict) -> dict:
    """zpool status -j 结构化解析:vdev 拍平成带层级的行,供表格渲染。"""
    ZfsJsonClient.check_output_version(raw, "zpool status")
    pools = raw.get("pools", {})
    out = {}

    def _scalars(node: dict) -> dict:
        return {k: v for k, v in node.items()
                if not isinstance(v, (dict, list))}

    def _walk(vdev: dict, level: int, rows: list) -> None:
        row = {
            "name": str(vdev.get("name", "")),
            "type": str(vdev.get("vdev_type", "")),
            "state": str(vdev.get("state", "")),
            "path": str(vdev.get("path", "")),
            "alloc": str(vdev.get("alloc_space", "")),
            "alloc_bytes": _size_to_bytes(str(vdev.get("alloc_space", ""))),
            "total_bytes": _size_to_bytes(str(vdev.get("total_space", ""))),
            "read": str(vdev.get("read_errors", "")),
            "write": str(vdev.get("write_errors", "")),
            "cksum": str(vdev.get("checksum_errors", "")),
            "level": level,
        }
        rows.append(row)
        kids = vdev.get("vdevs")
        if isinstance(kids, dict):
            for k, v in kids.items():
                if isinstance(v, dict) and ("name" in v or "vdev_type" in v):
                    _walk(v, level + 1, rows)

    for name, p in pools.items():
        rows: list[dict] = []
        root_vdevs = p.get("vdevs", {})
        if isinstance(root_vdevs, dict):
            for k, v in root_vdevs.items():
                if isinstance(v, dict):
                    _walk(v, 0, rows)
        scan = p.get("scan")
        scan_info = None
        if isinstance(scan, dict):
            scan_info = _scalars(scan)
        out[name] = {
            "name": name,
            "state": str(p.get("state", "")),
            "error_count": str(p.get("error_count", "")),
            "scan": scan_info,
            "vdev_rows": rows,
            "raw": raw.get("pools", {}).get(name, {}),
        }
    return out


def parse_datasets(raw: dict) -> list[dict]:
    ZfsJsonClient.check_output_version(raw, "zfs list")
    out = []
    for name, d in raw.get("datasets", {}).items():
        props = d.get("properties", {})
        pv = lambda k: (props.get(k) or {}).get("value")
        out.append({
            "name": name,
            "type": str(d.get("type", "")).lower(),
            "used": _num(pv("used")),
            "available": _num(pv("available")),
            "volsize": _num(pv("volsize")),
            "compression": str(pv("compression") or ""),
            "origin": str(pv("origin") or ""),
        })
    return out


def parse_snapshots(raw: dict) -> list[dict]:
    ZfsJsonClient.check_output_version(raw, "zfs list")
    out = []
    for name, d in raw.get("datasets", {}).items():
        props = d.get("properties", {})
        pv = lambda k: (props.get(k) or {}).get("value")
        creation = pv("creation")
        if _num(creation) > 0:  # 时间戳
            try:
                creation = datetime.fromtimestamp(_num(creation), tz=timezone.utc).strftime("%Y-%m-%d %H:%M:%S")
            except Exception:
                pass
        ds, _, snap = name.partition("@")
        out.append({
            "name": name,
            "dataset": ds,
            "snap": snap,
            "used": _num(pv("used")),
            "createtxg": _num(pv("createtxg")),
            "creation": str(creation),
        })
    return out


def parse_props(raw: dict) -> dict:
    ZfsJsonClient.check_output_version(raw, "zfs get")
    out = {}
    for name, d in raw.get("datasets", {}).items():
        for k, v in d.get("properties", {}).items():
            val = v.get("value")
            out[k] = _num(val) if _num(val) != 0 or str(val).isdigit() else str(val)
    return out

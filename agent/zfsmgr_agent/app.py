"""storman 存储管理平台 Node Agent —— FastAPI 服务与操作路由(ZFS + RAID)。

对外(仅 Manager 可访问,X-Agent-Token 校验):
  POST /api/v1/query {kind, params}
  POST /api/v1/op     {op, params}

主动上行(Agent 发起):Manager /api/v1/agent/register、/nodes/{id}/heartbeat
"""
from __future__ import annotations

import argparse
import json
from datetime import datetime, timezone
import logging
import os
import threading
import time
import urllib.request
from typing import Optional

from fastapi import Depends, FastAPI, Header, HTTPException, Request
from fastapi.responses import JSONResponse

from . import sysinfo
from .config import Config, State
from .fmt import FmtError, FormatService, blkid_type, is_mounted
from .lio import LioClient, LioError
from . import shares as sharemgr
from .metrics import MetricsSampler
from .raid import RaidClient, RaidError
from .zfs import ZfsError, ZfsJsonClient

log = logging.getLogger("zfsmgr_agent")

zfs = ZfsJsonClient()


class AgentRuntime:
    def __init__(self, cfg: Config, state: State, lio: LioClient, fmt: FormatService):
        self.cfg = cfg
        self.state = state
        self.lio = lio
        self.fmt = fmt
        self.raid = RaidClient(cfg.raid_storcli_path, bool(cfg.raid_allow_mutation and cfg.allow_mutation))
        self.caps = sysinfo.capabilities()
        self.caps["raid"] = self.raid.capabilities()
        self.caps["allow_mutation"] = bool(cfg.allow_mutation)
        # LIO 依赖 rtslib;缺失时跳过目标同步(节点仅做 ZFS/RAID 管理)
        from .lio import RTSRoot
        self.caps["lio_available"] = RTSRoot is not None
        self.metrics = MetricsSampler()
        self.lock = threading.RLock()
        # 临时解锁(仅内存态,TTL 后自动回锁;重启即恢复只读)
        self._mutation_until = 0.0

    def mutation_unlocked(self) -> bool:
        return time.time() < self._mutation_until

    def mutation_state(self) -> dict:
        unlocked = self.mutation_unlocked()
        remaining = max(0, int(self._mutation_until - time.time())) if unlocked else 0
        exp = ""
        if unlocked:
            exp = datetime.fromtimestamp(self._mutation_until, tz=timezone.utc).isoformat()
        return {
            "config_allow": bool(self.cfg.allow_mutation),
            "raid_config_allow": bool(self.cfg.raid_allow_mutation),
            "unlocked": unlocked,
            "expires_at": exp,
            "remaining_s": remaining,
            "effective": bool(self.cfg.allow_mutation or unlocked),
            "raid_effective": bool(self.cfg.raid_allow_mutation or unlocked),
        }

    def raid_writable(self) -> bool:
        return bool(self.cfg.raid_allow_mutation or self.mutation_unlocked())


def make_app(cfg: Config) -> FastAPI:
    state = State(cfg.state_file)
    lio = LioClient()
    fmt = FormatService(lio.backstore_devs)
    rt = AgentRuntime(cfg=cfg, state=state, lio=lio, fmt=fmt)

    app = FastAPI(title="storman-agent", docs_url=None, redoc_url=None)
    app.state.rt = rt

    async def check_token(x_agent_token: Optional[str] = Header(default=None)):
        if x_agent_token != cfg.agent_token:
            raise HTTPException(status_code=401, detail="token invalid")
        return True

    def err(e: Exception) -> JSONResponse:
        code, msg = "AGENT_ERROR", str(e)
        if isinstance(e, ZfsError):
            code, msg = e.code, str(e)
        elif isinstance(e, LioError):
            code, msg = e.code, str(e)
        elif isinstance(e, FmtError):
            code, msg = e.code, str(e)
        elif isinstance(e, RaidError):
            code, msg = e.code, str(e)
        log.warning("agent error %s: %s", code, msg)
        return JSONResponse(status_code=200,
                            content={"ok": False, "error": {"code": code, "message": msg}})

    @app.post("/api/v1/query")
    async def query(req: Request, _auth: bool = Depends(check_token)):
        body = await req.json()
        kind = body.get("kind", "")
        params = body.get("params") or {}
        try:
            return {"ok": True, "result": handle_query(rt, kind, params)}
        except Exception as e:
            return err(e)

    @app.post("/api/v1/op")
    async def op(req: Request, _auth: bool = Depends(check_token)):
        body = await req.json()
        o = body.get("op", "")
        params = body.get("params") or {}
        try:
            with rt.lock:
                if o not in ("mutation_unlock", "mutation_lock"):
                    ensure_writable(rt.cfg, rt.mutation_unlocked())
                res = handle_op(rt, o, params)
            return {"ok": True, "result": res or {}}
        except Exception as e:
            return err(e)

    @app.get("/api/v1/ping")
    async def ping(_auth: bool = Depends(check_token)):
        return {"ok": True, "agent": sysinfo.AGENT_VERSION, "node_id": rt.state.node_id}

    return app


def ensure_writable(cfg: Config, unlocked: bool = False) -> None:
    """节点写保护:只读节点仅允许查询;输入密码临时解锁的窗口期内放行。"""
    if not cfg.allow_mutation and not unlocked:
        raise ZfsError("当前节点为只读模式(生产环境),所有变更操作已禁用", code="READ_ONLY")


# ---------------- 查询分发 ----------------

def handle_query(rt: AgentRuntime, kind: str, params: dict) -> dict:
    if kind == "pools":
        return {"pools": zfs.zpool_list()}
    if kind == "pool_status":
        return {"status": zfs.zpool_status(params.get("pool", ""))}
    if kind == "datasets":
        return {"datasets": zfs.datasets(params.get("types", "volume,filesystem"))}
    if kind == "snapshots":
        return {"snapshots": zfs.snapshots()}
    if kind == "volprops":
        return {"properties": zfs.volprops(params.get("dataset", ""))}
    if kind == "fsprops":
        return {"properties": zfs.fsprops(params.get("dataset", ""))}
    if kind == "dsprops":
        return {"properties": zfs.dsprops(params.get("dataset", ""),
                                          [str(x) for x in params.get("props", [])])}
    if kind == "fsinfo":
        ds = params.get("dataset", "")
        fs_type = blkid_type(ds)
        return {"type": fs_type,
                "status": "ready" if fs_type not in ("none", "unknown") else
                          ("unformatted" if fs_type == "none" else "unknown"),
                "mounted": is_mounted(ds)}
    if kind == "iscsi_targets":
        return {"targets": rt.lio.iscsi_targets()}
    if kind == "fc_targets":
        return {"targets": rt.lio.fc_targets()}
    if kind == "sessions":
        return {"sessions": rt.lio.sessions()}
    if kind == "disks":
        return {"disks": sysinfo.disks()}
    if kind == "hba":
        return {"fc": rt.caps.get("fc", {})}
    if kind == "capabilities":
        return rt.caps
    if kind == "metrics":
        return rt.metrics.sample()
    if kind == "share_status":
        typ = params.get("type", "nfs")
        if typ == "nfs":
            return sharemgr.nfs_status(params.get("dataset", ""))
        return sharemgr.smb_status(params.get("name", ""))
    # ---- RAID(只读) ----
    if kind == "raid_controllers":
        return {"controllers": rt.raid.controllers()}
    if kind == "raid_vds":
        return {"vds": rt.raid.vds(int(params.get("controller", 0)))}
    if kind == "raid_pds":
        return {"pds": rt.raid.pds(int(params.get("controller", 0)))}
    if kind == "raid_events":
        return rt.raid.events(int(params.get("controller", 0)),
                              int(params.get("page", 1)),
                              int(params.get("page_size", 50)))
    if kind == "mutation_state":
        with rt.lock:
            return rt.mutation_state()
    raise ZfsError(f"未知查询: {kind}", code="VALIDATION")


# ---------------- 操作分发 ----------------


def norm_out(v):
    from .lio import norm_wwn
    try:
        return norm_wwn(v)
    except Exception:
        return v


def _reject_used_disks(disks: list) -> None:
    """建池护栏:拒绝含文件系统 / 已分区 / 已挂载的设备(前端已过滤,后端兜底)。"""
    known = {d["path"]: d for d in sysinfo.disks()}
    for p in disks:
        info = known.get(p)
        if info is None:
            continue  # loop/file 等非 lsblk disk 设备交由 zpool 自身校验
        problems = []
        if info.get("fs"):
            problems.append(f"含文件系统({info['fs']})")
        if info.get("children"):
            problems.append(f"已分区({info['children']} 个分区)")
        if info.get("mounted"):
            problems.append("已挂载")
        if problems:
            raise ZfsError(f"{p} 不可用于建池: {'; '.join(problems)}(force 可跳过)", code="VALIDATION")


def handle_op(rt: AgentRuntime, op: str, p: dict) -> dict:
    ds = p.get("dataset", "")
    if op == "pool_create":
        layout = p.get("layout") or []
        if not layout and p.get("disks"):
            layout = [{"type": "stripe", "disks": p["disks"]}]
        if not bool(p.get("force")):
            used = []
            for g in layout:
                used += [str(x) for x in (g.get("disks") or [])]
            used += [str(x) for x in (p.get("log") or [])]
            used += [str(x) for x in (p.get("cache") or [])]
            used += [str(x) for x in (p.get("spare") or [])]
            _reject_used_disks(used)
        zfs.pool_create(p["pool_name"], layout, p.get("options") or {},
                        p.get("log") or [], p.get("cache") or [],
                        p.get("spare") or [], bool(p.get("force")))
        return {"pool": p["pool_name"]}
    if op == "pool_destroy":
        if p.get("confirm") != p["pool_name"]:
            raise ZfsError("请键入池名确认销毁", code="CONFIRM")
        if any(d["name"].startswith(p["pool_name"] + "/")
               for d in zfs.datasets("volume,filesystem")):
            raise ZfsError("池内仍有数据集,请先删除", code="CONFLICT")
        zfs.pool_destroy(p["pool_name"])
        return {"destroyed": p["pool_name"]}
    if op == "pool_scrub":
        try:
            zfs.pool_scrub(p["pool_name"], p.get("action", "start"))
        except ZfsError as e:
            if p.get("action") == "stop" and ("no active scrub" in str(e).lower() or "no scan" in str(e).lower()):
                return {"pool": p["pool_name"], "action": "stop", "note": "no scan in progress"}
            raise
        return {"pool": p["pool_name"], "action": p.get("action")}
    if op == "zvol_create":
        zfs.zvol_create(p["pool"], p["name"], int(p["size"]),
                        str(p.get("compression", "")), str(p.get("volblocksize", "")),
                        bool(p.get("sparse")))
        return {"dataset": f"{p['pool']}/{p['name']}"}
    if op == "zfs_fs_create":
        zfs.fs_create(p["pool"], p["name"], str(p.get("compression", "")),
                      str(p.get("dedup", "")), str(p.get("mountpoint", "none")))
        return {"dataset": f"{p['pool']}/{p['name']}"}
    if op == "zfs_set_prop":
        return zfs.set_prop(p["dataset"], p["property"], p["value"])
    if op == "zfs_clone":
        target = zfs.clone_zvol(p["source"], p["snapshot"], p["name"])
        return {"dataset": target}
    if op == "zvol_resize":
        zfs.zvol_resize(ds, int(p["size"]), str(p.get("confirm", "")))
        return {"dataset": ds, "volsize": int(p["size"])}
    if op == "dataset_destroy":
        dev = "/dev/zvol/" + ds
        for bdev in rt.lio.backstore_devs():
            if bdev == dev:
                raise ZfsError("卷被 iSCSI backstore 引用(已映射),请先解映射", code="CONFLICT")
        if is_mounted(ds):
            raise ZfsError("卷已被本机挂载,请先卸载", code="CONFLICT")
        zfs.dataset_destroy(ds, str(p.get("confirm", "")), bool(p.get("recursive")))
        return {"destroyed": ds}
    if op == "snapshot_create":
        zfs.snapshot_create(ds, p["snapshot"])
        return {"snapshot": f"{ds}@{p['snapshot']}"}
    if op == "snapshot_delete":
        zfs.snapshot_delete(p["snapshot"])
        return {"deleted": p["snapshot"]}
    if op == "zvol_rollback":
        zfs.rollback(ds, p["snapshot"], str(p.get("confirm", "")))
        return {"rolled_back": f"{ds}@{p['snapshot']}"}
    if op == "zvol_format":
        return rt.fmt.format(ds, p["filesystem"], str(p.get("confirm", "")))
    # ---- LIO ----
    if op == "lio_target_create":
        rt.lio.target_create(p["iqn"], str(p.get("dataset", "")))
        return {"iqn": p["iqn"]}
    if op == "lio_target_delete":
        rt.lio.target_delete(p["iqn"])
        return {"deleted": p["iqn"]}
    if op == "fc_target_create":
        rt.lio.fc_target_create(p.get("wwn") or p.get("iqn"), str(p.get("dataset", "")))
        return {"wwn": norm_out(p.get("wwn") or p.get("iqn"))}
    if op == "fc_target_delete":
        rt.lio.fc_target_delete(p.get("wwn") or p.get("iqn"))
        return {"deleted": p.get("wwn") or p.get("iqn")}
    if op == "fc_lun_add":
        lun = rt.lio.fc_lun_add(p.get("wwn") or p.get("iqn"), p["dataset"])
        return {"lun_id": lun}
    if op == "fc_lun_remove":
        rt.lio.fc_lun_remove(p.get("wwn") or p.get("iqn"), int(p["lun_id"]))
        return {"lun_id": int(p["lun_id"])}
    if op == "fc_acl_add":
        rt.lio.fc_acl_add(p.get("wwn") or p.get("iqn"), p["initiator"])
        return {"initiator": p["initiator"]}
    if op == "fc_acl_remove":
        rt.lio.fc_acl_remove(p.get("wwn") or p.get("iqn"), p["initiator"])
        return {"initiator": p["initiator"]}
    if op == "lio_lun_add":
        lun = rt.lio.lun_add(p["iqn"], p["dataset"])
        return {"iqn": p["iqn"], "lun_id": lun}
    if op == "lio_lun_remove":
        rt.lio.lun_remove(p["iqn"], int(p["lun_id"]))
        return {"iqn": p["iqn"], "lun_id": int(p["lun_id"])}
    if op == "lio_acl_add":
        rt.lio.acl_add(p["iqn"], p["initiator"],
                       str(p.get("chap_user", "")), str(p.get("chap_secret", "")))
        return {"iqn": p["iqn"], "initiator": p["initiator"]}
    if op == "lio_acl_remove":
        rt.lio.acl_remove(p["iqn"], p["initiator"])
        return {"iqn": p["iqn"], "initiator": p["initiator"]}
    if op == "lio_session_logout":
        rt.lio.session_logout(p["iqn"], int(p["sid"]))
        return {"sid": int(p["sid"])}
    if op == "share_nfs_apply":
        return sharemgr.nfs_apply(
            p["dataset"], p.get("action", "add"), str(p.get("clients", "")),
            str(p.get("access", "rw")), str(p.get("sync_mode", "sync")),
            str(p.get("squash", "root_squash")), str(p.get("subtree", "no_subtree_check")))
    if op == "share_smb_apply":
        return sharemgr.smb_apply(
            p["dataset"], p.get("action", "add"), str(p.get("name", "")),
            bool(p.get("read_only")), bool(p.get("guest_ok")), str(p.get("valid_users", "")))
    if op == "lio_config_save":
        rt.lio.save()
        return {"saved": True}
    # ---- 临时解锁(仅内存态;由 Manager 在校验管理员口令后调用) ----
    if op == "mutation_unlock":
        ttl = int(p.get("ttl_s", 900) or 900)
        ttl = max(60, min(ttl, 3600))
        rt._mutation_until = time.time() + ttl
        rt.raid.allow_mutation = rt.raid_writable()
        log.warning("节点临时解锁写操作: ttl=%ss", ttl)
        return rt.mutation_state()
    if op == "mutation_lock":
        rt._mutation_until = 0
        rt.raid.allow_mutation = rt.raid_writable()
        log.warning("节点写操作已重新锁定")
        return rt.mutation_state()
    # ---- RAID(默认只读;开启 allow_mutation 后仍需确认串) ----
    if op.startswith("raid_"):
        rt.raid.allow_mutation = rt.raid_writable()
    if op == "raid_vd_create":
        return rt.raid.create_vd(int(p.get("controller", 0)), str(p.get("level", "")),
                                 p.get("drives") or [], str(p.get("name", "")),
                                 str(p.get("stripesize", "")), str(p.get("pdperarray", "")),
                                 str(p.get("confirm", "")))
    if op == "raid_vd_delete":
        return rt.raid.delete_vd(int(p.get("controller", 0)), str(p.get("vd", "")),
                                 str(p.get("confirm", "")))
    if op == "raid_vd_set":
        return rt.raid.set_vd(int(p.get("controller", 0)), str(p.get("vd", "")),
                              p.get("name"), str(p.get("writepolicy", "")),
                              str(p.get("readpolicy", "")), str(p.get("iopolicy", "")),
                              str(p.get("access", "")), str(p.get("confirm", "")))
    raise ZfsError(f"未知操作: {op}", code="VALIDATION")


# ---------------- 上行:注册 + 心跳 ----------------

def post_json(url: str, token: str, payload: dict, timeout: float = 15) -> dict:
    req = urllib.request.Request(url, data=json.dumps(payload).encode(),
                                 headers={"Content-Type": "application/json",
                                          "X-Agent-Token": token})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode())


def register_loop(cfg: Config, rt: AgentRuntime):
    """启动即注册,之后周期性心跳。失败自动重试。"""
    body = {
        "node_name": cfg.node_name,
        "agent_addr": cfg.listen,
        "advertise_addr": cfg.advertise_addr,
        "agent_version": sysinfo.AGENT_VERSION,
        "zfs_version": rt.caps.get("zfs_version", ""),
        "capabilities": rt.caps,
    }
    base = cfg.manager_url.rstrip("/")
    fresh = True  # 每次 Agent 启动都重新注册,刷新能力(配置可能变更)
    while True:
        try:
            if fresh or rt.state.node_id is None:
                res = post_json(base + "/api/v1/agent/register", cfg.agent_token, body)
                nid = res["data"]["node_id"]
                rt.state.set("node_id", nid)
                fresh = False
                log.info("注册成功 node_id=%s", nid)
            nid = rt.state.node_id
            post_json(f"{base}/api/v1/agent/nodes/{nid}/heartbeat", cfg.agent_token,
                      {"node_id": nid, "uptime_s": int(time.time())})
        except Exception as e:
            log.warning("注册/心跳失败(稍后重试): %s", e)
        time.sleep(cfg.heartbeat_interval_s)


def main() -> None:
    logging.basicConfig(level=logging.INFO,
                        format="%(asctime)s %(name)s %(levelname)s %(message)s")
    ap = argparse.ArgumentParser()
    ap.add_argument("-c", "--config",
                    default=os.environ.get("ZFSMGR_AGENT_CONFIG",
                                           "/etc/zfs-platform/agent.json"))
    args = ap.parse_args()
    cfg = Config.load(args.config)

    # 恢复 LIO 持久化配置(重启后保持 target 结构)
    LioClient().restore()

    import uvicorn
    host, _, port = cfg.listen.rpartition(":")
    app = make_app(cfg)
    t = threading.Thread(target=register_loop, args=(cfg, app.state.rt), daemon=True)
    t.start()
    log.info("Agent 监听 %s (Manager: %s)", cfg.listen, cfg.manager_url)
    uvicorn.run(app, host=host, port=int(port), log_level="warning")


if __name__ == "__main__":
    main()

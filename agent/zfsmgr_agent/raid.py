"""RAID 管理(通过 storcli64,只读优先)。

- 默认 `allow_mutation=false`:生产/验证节点仅允许读取(控制器/PD/VD/日志);
- 写操作(创建/删除/改属性)需显式开启并携带确认串,所有命令无 shell 拼接;
- 仅使用 storcli JSON 输出解析。
"""
from __future__ import annotations

import json
import os
import re
import subprocess

DRIVE_RE = re.compile(r"^\d+:\d+(-\d+)?$")
NAME_RE = re.compile(r"^[A-Za-z0-9_.-]{0,15}$")
LEVELS = {"0", "1", "5", "6", "10", "50", "60"}


class RaidError(Exception):
    def __init__(self, msg: str, code: str = "RAID_ERROR"):
        super().__init__(msg)
        self.code = code


class RaidClient:
    def __init__(self, storcli_path: str, allow_mutation: bool = False, timeout: int = 60):
        self.path = storcli_path
        self.allow_mutation = allow_mutation
        self.timeout = timeout

    # ---------- 基础 ----------
    def available(self) -> bool:
        return os.path.isfile(self.path) and os.access(self.path, os.X_OK)

    def _run(self, args: list[str], timeout: int | None = None) -> str:
        if not self.available():
            raise RaidError(f"storcli 不可用: {self.path}", code="RAID_UNAVAILABLE")
        try:
            p = subprocess.run([self.path] + args, capture_output=True, text=True,
                               timeout=timeout or self.timeout)
        except subprocess.TimeoutExpired:
            raise RaidError("storcli 命令超时", code="TIMEOUT")
        if p.returncode != 0:
            raise RaidError((p.stderr or p.stdout).strip()[:400] or "storcli 执行失败")
        return p.stdout

    def _json(self, args: list[str]) -> dict:
        out = self._run(args)
        try:
            return json.loads(out)
        except Exception:
            raise RaidError("storcli 输出非 JSON", code="PARSE")

    @staticmethod
    def _resp(doc: dict) -> dict:
        ctrls = doc.get("Controllers") or []
        if not ctrls:
            raise RaidError("未发现 RAID 控制器", code="NOT_FOUND")
        return ctrls[0].get("Response Data") or {}

    def _need_mutation(self):
        if not self.allow_mutation:
            raise RaidError("当前节点为只读模式(生产/验证环境),RAID 写操作已禁用", code="READ_ONLY")

    # ---------- 只读 ----------
    def capabilities(self) -> dict:
        return {"available": self.available(), "storcli": self.path,
                "allow_mutation": self.allow_mutation}

    def controllers(self) -> list[dict]:
        if not self.available():
            return []
        overview = self._json(["show", "J"])
        sysrows = (self._resp(overview).get("System Overview") or [])
        out = []
        for row in sysrows:
            cid = int(row.get("Ctl", row.get("Controller", 0)) or 0)
            detail = {}
            try:
                detail = self._resp(self._json([f"/c{cid}", "show", "J"]))
            except Exception:
                pass
            out.append({
                "id": cid,
                "model": detail.get("Product Name") or row.get("Model", ""),
                "ports": row.get("Ports"),
                "pds": row.get("PDs"),
                "dgs": row.get("DGs"),
                "vds": row.get("VDs"),
                "bbu": row.get("BBU") or detail.get("BBU", ""),
                "health": row.get("Hlth") or detail.get("Controller Status", {}).get("Controller Status", ""),
                "serial": detail.get("Serial Number", ""),
                "sas_address": str(detail.get("SAS Address", "")).strip(),
                "pci": detail.get("PCI Address", ""),
                "fw": detail.get("FW Version", ""),
                "fw_build": detail.get("FW Package Build", ""),
                "bios": detail.get("BIOS Version", ""),
                "driver": detail.get("Driver Name", ""),
                "driver_ver": detail.get("Driver Version", ""),
                "memory": detail.get("Memory", ""),
                "bbu_detail": detail.get("BBU", ""),
                "alarm": detail.get("Alarm", ""),
                "temperature": detail.get("ROC Temperature", ""),
                "raw": {k: detail.get(k) for k in
                        ("Product Name", "Serial Number", "FW Version", "BBU", "Alarm",
                         "ROC Temperature", "Controller Status", "HwCfg", "Supported Adapter")},
            })
        return out

    def vds(self, ctrl: int) -> list[dict]:
        data = self._resp(self._json([f"/c{ctrl}", "/vall", "show", "J"]))
        rows = data.get("Virtual Drives") or []
        out = []
        for r in rows:
            dgvd = str(r.get("DG/VD", ""))
            dg, _, vd = dgvd.partition("/")
            out.append({
                "dg_vd": dgvd,
                "dg": dg, "vd": vd,
                "type": r.get("TYPE", ""),
                "state": r.get("State", ""),
                "access": r.get("Access", ""),
                "consist": r.get("Consist", ""),
                "cache": r.get("Cache", ""),
                "cac": r.get("Cac", ""),
                "name": r.get("Name", ""),
                "size": r.get("Size", ""),
                "raw": r,
            })
        return out

    def pds(self, ctrl: int) -> list[dict]:
        data = self._resp(self._json([f"/c{ctrl}", "/eall/sall", "show", "J"]))
        rows = data.get("Drive Information") or []
        out = []
        for r in rows:
            out.append({
                "eid_slot": r.get("EID:Slt", ""),
                "did": r.get("DID"),
                "state": r.get("State", ""),
                "dg": r.get("DG", ""),
                "size": r.get("Size", ""),
                "intf": r.get("Intf", ""),
                "media": r.get("Med", ""),
                "sed": r.get("SED", ""),
                "pi": r.get("PI", ""),
                "sector": r.get("SeSz", ""),
                "model": r.get("Model", ""),
                "spare": r.get("Sp", ""),
                "raw": r,
            })
        return out

    def events(self, ctrl: int, limit: int = 100) -> dict:
        limit = max(1, min(int(limit or 100), 500))
        text = self._run([f"/c{ctrl}", "show", "events"], timeout=45)
        # 截掉 storcli 尾部 "Controller = ... Status = ..." 汇总表
        text = re.split(r"\nController\s*=\s*\d+", text)[0]
        blocks = [b.strip() for b in re.split(r"\n(?=seqNum:)", text) if "seqNum:" in b]
        entries = []
        for b in blocks[-limit:]:
            seq = re.search(r"seqNum:\s*(\S+)", b)
            t = re.search(r"^Time:\s*(.+)$", b, re.M)
            boot = re.search(r"Seconds since last reboot:\s*(\d+)", b)
            desc = re.search(r"Event Description:\s*(.+)$", b, re.M)
            entries.append({
                "seq": seq.group(1) if seq else "",
                "time": t.group(1).strip() if t else (f"启动后 {boot.group(1)} 秒" if boot else ""),
                "event": desc.group(1).strip() if desc else "",
                "raw": b,
            })
        return {"total": len(blocks), "returned": len(entries), "entries": entries}

    # ---------- 写操作(默认禁用) ----------
    def create_vd(self, ctrl: int, level: str, drives: list[str], name: str = "",
                  stripesize: str = "", pdperarray: str = "", confirm: str = "") -> dict:
        self._need_mutation()
        level = str(level).replace("raid", "")
        if level not in LEVELS:
            raise RaidError("RAID 级别仅支持 0/1/5/6/10/50/60", code="VALIDATION")
        drives = [str(d).strip() for d in drives]
        if not drives or not all(DRIVE_RE.match(d) for d in drives):
            raise RaidError("磁盘格式非法(应为 EID:Slt,如 252:0)", code="VALIDATION")
        if name and not NAME_RE.match(name):
            raise RaidError("VD 名称非法(≤15 位字母数字 _. -)", code="VALIDATION")
        if confirm != f"CREATE {name or level}":
            raise RaidError("创建确认串不匹配", code="CONFIRM")
        argv = [f"/c{ctrl}", "add", "vd", f"type=raid{level}",
                f"drives={','.join(drives)}"]
        if name:
            argv.append(f"name={name}")
        if stripesize:
            if stripesize not in ("64", "128", "256", "512", "1024"):
                raise RaidError("stripesize 非法", code="VALIDATION")
            argv.append(f"stripesize={stripesize}")
        if pdperarray:
            if not str(pdperarray).isdigit():
                raise RaidError("pdperarray 非法", code="VALIDATION")
            argv.append(f"pdperarray={pdperarray}")
        before = len(self.vds(ctrl))
        self._run(argv, timeout=300)
        after = self.vds(ctrl)
        if len(after) <= before:
            raise RaidError("创建后回读校验失败:VD 数量未增加", code="READBACK")
        return {"before": before, "after": len(after), "vds": after}

    def delete_vd(self, ctrl: int, vd: str, confirm: str = "") -> dict:
        self._need_mutation()
        if not str(vd).isdigit():
            raise RaidError("VD 编号非法", code="VALIDATION")
        if confirm != f"VD{vd}":
            raise RaidError("删除确认串不匹配(请输入 VD<编号>)", code="CONFIRM")
        before = self.vds(ctrl)
        self._run([f"/c{ctrl}", f"/v{vd}", "del", "force"], timeout=300)
        after = self.vds(ctrl)
        if any(x["vd"] == str(vd) for x in after):
            raise RaidError("删除后回读校验失败:VD 仍存在", code="READBACK")
        return {"before": len(before), "after": len(after), "vds": after}

    def set_vd(self, ctrl: int, vd: str, name: str | None = None,
               writepolicy: str = "", readpolicy: str = "", iopolicy: str = "",
               access: str = "", confirm: str = "") -> dict:
        self._need_mutation()
        if not str(vd).isdigit():
            raise RaidError("VD 编号非法", code="VALIDATION")
        if confirm != f"VD{vd}":
            raise RaidError("属性修改确认串不匹配(请输入 VD<编号>)", code="CONFIRM")
        args = [f"/c{ctrl}", f"/v{vd}", "set"]
        if name is not None:
            if not NAME_RE.match(name):
                raise RaidError("VD 名称非法", code="VALIDATION")
            args.append(f"name={name}")
        if writepolicy:
            if writepolicy not in ("WT", "WB"):
                raise RaidError("writepolicy 仅 WT/WB", code="VALIDATION")
            args.append(f"writepolicy={writepolicy}")
        if readpolicy:
            if readpolicy not in ("NoReadAhead", "ReadAhead", "Adaptive"):
                raise RaidError("readpolicy 非法", code="VALIDATION")
            args.append(f"readpolicy={readpolicy}")
        if iopolicy:
            if iopolicy not in ("Direct", "Cached"):
                raise RaidError("iopolicy 仅 Direct/Cached", code="VALIDATION")
            args.append(f"iopolicy={iopolicy}")
        if access:
            if access not in ("RW", "RO"):
                raise RaidError("access 仅 RW/RO", code="VALIDATION")
            args.append(f"access={access}")
        if len(args) == 3:
            raise RaidError("没有需要修改的属性", code="VALIDATION")
        self._run(args, timeout=120)
        rows = self.vds(ctrl)
        row = next((x for x in rows if x["vd"] == str(vd)), None)
        if row is None:
            raise RaidError("回读校验失败:VD 不存在", code="READBACK")
        return {"vd": row}

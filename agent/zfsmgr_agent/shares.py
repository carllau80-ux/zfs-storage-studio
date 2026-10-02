"""文件共享管理:NFS(exportfs)与 SMB/CIFS(Samba)。

- 共享创建在指定 pool 内的文件系统数据集(zfs filesystem)上,导出其本机挂载点;
- NFS:维护 /etc/exports.d/zfs-platform.exports 中的受管块 + exportfs -ra;
- SMB:维护 /etc/samba/zfs-platform.conf 的受管段 + 主配置 include 一次,reload smbd;
- 所有变更以 exportfs -s / testparm 回读校验为准。
"""
from __future__ import annotations

import os
import re
import subprocess

NFS_FILE = "/etc/exports.d/zfs-platform.exports"
SMB_FILE = "/etc/samba/zfs-platform.conf"
SMB_MAIN = "/etc/samba/smb.conf"
SMB_INCLUDE = "include = /etc/samba/zfs-platform.conf"

NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$")
CLIENTS_RE = re.compile(r"^[A-Za-z0-9._,:/*?\[\]-]+$")


class ShareError(Exception):
    def __init__(self, msg: str, code: str = "SHARE_ERROR"):
        super().__init__(msg)
        self.code = code


def _run(argv: list[str], timeout: int = 60) -> tuple[int, str, str]:
    try:
        p = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        return p.returncode, p.stdout, p.stderr
    except FileNotFoundError:
        raise ShareError(f"命令不存在: {argv[0]}(请安装对应服务)", code="NOT_FOUND")
    except subprocess.TimeoutExpired:
        raise ShareError("命令超时", code="TIMEOUT")


def _ensure_mount(dataset: str) -> str:
    """确保数据集已挂载并返回挂载点(不支持 none/missing)。"""
    rc, out, _ = _run(["zfs", "get", "-H", "-o", "value", "mountpoint", dataset])
    mp = out.strip()
    if rc != 0 or not mp or mp in ("none", "legacy", "-"):
        raise ShareError(f"数据集 {dataset} 的 mountpoint={mp or '未设置'},"
                         "共享需先设置可挂载路径(如 /pool/name)", code="VALIDATION")
    if not os.path.isdir(mp):
        _run(["zfs", "mount", dataset])          # 幂等:已挂载时返回非零,忽略
        if not os.path.isdir(mp):
            raise ShareError(f"挂载点不存在: {mp}", code="CONFLICT")
    return mp


# ---------------- NFS ----------------

def _read(path: str) -> str:
    try:
        with open(path) as f:
            return f.read()
    except FileNotFoundError:
        return ""


def _write(path: str, text: str) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        f.write(text)
    os.replace(tmp, path)


def _strip_block(text: str, marker: str) -> str:
    lines, out, skipping = text.splitlines(), [], False
    for ln in lines:
        if ln.strip() == f"# zfs-platform: {marker}":
            skipping = True
            continue
        if skipping and ln.strip() == "# zfs-platform: end":
            skipping = False
            continue
        if not skipping:
            out.append(ln)
    return "\n".join(out).strip() + ("\n" if out else "")


def nfs_apply(dataset: str, action: str, clients: str = "", access: str = "rw",
              sync_mode: str = "sync", squash: str = "root_squash",
              subtree: str = "no_subtree_check") -> dict:
    """新增/移除 NFS 导出。action=add|remove。"""
    mountpoint = _ensure_mount(dataset)
    text = _strip_block(_read(NFS_FILE), dataset)
    if action == "add":
        if not clients or not CLIENTS_RE.match(clients.replace(" ", "")):
            raise ShareError("客户端网段非法(示例: 10.0.0.0/24 或 *)", code="VALIDATION")
        if access not in ("rw", "ro"):
            raise ShareError("access 须为 rw|ro", code="VALIDATION")
        opts = [access, sync_mode if sync_mode in ("sync", "async") else "sync",
                subtree if subtree in ("no_subtree_check", "subtree_check") else "no_subtree_check",
                squash if squash in ("root_squash", "no_root_squash") else "root_squash"]
        block = (f"# zfs-platform: {dataset}\n"
                 f"{mountpoint} {clients}({','.join(opts)})\n"
                 f"# zfs-platform: end\n")
        _write(NFS_FILE, text + block)
    elif action == "remove":
        _write(NFS_FILE, text)
    else:
        raise ShareError("action 须为 add|remove", code="VALIDATION")
    rc, _, err = _run(["exportfs", "-ra"], timeout=120)
    if rc != 0:
        raise ShareError(f"exportfs -ra 失败: {err.strip()[:300]}", code="SHARE_ERROR")
    # 回读校验(exportfs 输出含多个空格分隔,按正则容错)
    rc, out, _ = _run(["exportfs", "-s"])
    pat = re.compile(rf"^{re.escape(mountpoint)}\s+{re.escape(clients)}", re.M)
    mp_line = re.compile(rf"^{re.escape(mountpoint)}\s", re.M)
    active = bool(pat.search(out))
    if action == "add" and not active:
        raise ShareError("NFS 导出回读校验失败(exportfs 未列出该共享)", code="READBACK")
    if action == "remove" and mp_line.search(out):
        raise ShareError("NFS 导出移除回读校验失败", code="READBACK")
    return {"dataset": dataset, "mountpoint": mountpoint, "clients": clients,
            "options": ",".join(opts) if action == "add" else "", "active": active}


def nfs_status(dataset: str) -> dict:
    mp = ""
    try:
        mp = _ensure_mount(dataset)
    except ShareError:
        return {"dataset": dataset, "active": False, "raw": ""}
    rc, out, _ = _run(["exportfs", "-s"])
    return {"dataset": dataset, "mountpoint": mp,
            "active": bool(re.search(rf"^{re.escape(mp)}\s", out, re.M)), "raw": out}


# ---------------- SMB / CIFS ----------------

def _smb_sections(text: str) -> dict[str, str]:
    """解析 [name] 段。"""
    secs, cur, buf = {}, None, []
    for ln in text.splitlines():
        m = re.match(r"^\[(.+)\]$", ln.strip())
        if m:
            if cur:
                secs[cur] = "\n".join(buf).strip()
            cur, buf = m.group(1), []
        elif cur is not None:
            buf.append(ln)
    if cur:
        secs[cur] = "\n".join(buf).strip()
    return secs


def _smb_render(secs: dict[str, str]) -> str:
    out = ["# Managed by ZFS Storage Platform — do not edit manually", ""]
    for name, body in secs.items():
        out.append(f"[{name}]")
        out.append(body)
        out.append("")
    return "\n".join(out)


def _ensure_include() -> None:
    txt = _read(SMB_MAIN)
    if SMB_INCLUDE not in txt:
        _write(SMB_MAIN, txt.rstrip() + ("\n\n" if txt.strip() else "") + SMB_INCLUDE + "\n")


def smb_apply(dataset: str, action: str, name: str = "", read_only: bool = False,
              guest_ok: bool = False, valid_users: str = "") -> dict:
    """新增/移除 SMB 共享(段名 = name)。"""
    if action not in ("add", "remove"):
        raise ShareError("action 须为 add|remove", code="VALIDATION")
    if not NAME_RE.match(name):
        raise ShareError("共享名非法(字母数字 _ . -,1-63 位)", code="VALIDATION")
    mountpoint = _ensure_mount(dataset)
    _ensure_include()
    secs = _smb_sections(_read(SMB_FILE))
    if action == "add":
        body = [f"   path = {mountpoint}", "   browseable = yes",
                f"   read only = {'yes' if read_only else 'no'}"]
        if guest_ok:
            body.append("   guest ok = yes")
            body.append("   force user = nobody")   # 匿名写权限收敛
        if valid_users:
            if not all(NAME_RE.match(u) for u in valid_users.split(",")):
                raise ShareError("valid_users 非法", code="VALIDATION")
            body.append(f"   valid users = {valid_users}")
        secs[name] = "\n".join(body)
    else:
        secs.pop(name, None)
    _write(SMB_FILE, _smb_render(secs))
    # reload(优先 reload,失败则 restart)
    rc, _, err = _run(["smbcontrol", "all", "reload-config"])
    if rc != 0:
        rc2, _, err2 = _run(["systemctl", "reload", "smbd"])
        if rc2 != 0:
            rc3, _, err3 = _run(["systemctl", "restart", "smbd"])
            if rc3 != 0:
                raise ShareError(f"Samba 重载失败: {(err or err2 or err3).strip()[:200]}", code="SHARE_ERROR")
    # 回读校验(testparm)
    rc, out, _ = _run(["testparm", "-s"], timeout=60)
    exists = f"[{name}]" in out
    if action == "add" and not exists:
        raise ShareError("SMB 共享回读校验失败(testparm 未见该段)", code="READBACK")
    if action == "remove" and exists:
        raise ShareError("SMB 共享移除回读校验失败", code="READBACK")
    return {"dataset": dataset, "mountpoint": mountpoint, "name": name,
            "read_only": read_only, "guest_ok": guest_ok, "active": exists}


def smb_status(name: str) -> dict:
    secs = _smb_sections(_read(SMB_FILE))
    return {"name": name, "active": name in secs, "raw": secs.get(name, "")}

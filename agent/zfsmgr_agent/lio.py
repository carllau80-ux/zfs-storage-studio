"""LIO 控制层(LioClient)—— 基于 python3-rtslib-fb 进程内 API。

探测结论(2026-09 于 Debian 13 / rtslib 2.1.76 实测):
- rtslib_fb.RTSRoot(): fabric_modules 为生成器,按 .name 取 "iscsi";
- Target(fabric_module, wwn) / TPG(target, '1');新建 TPG 需
  set_attribute('authentication', 0) 并置 enable=True,否则拒绝无 CHAP 登录;
- 门户:NetworkPortal(tpg, '0.0.0.0', 3260);
- ACL:NodeACL(tpg, initiator_iqn) 后设 chap_userid/chap_password;
- backstore:BlockStorageObject(name=..., dev='/dev/zvol/...');
- LUN:LUN(tpg, lun_id, storage_object);
- 会话:root.sessions 生成器产出带 .session dict 的 NodeACL(有会话时);
  登出 = os.rmdir(tpg.path + '/sessions/<sid>')。
"""
from __future__ import annotations

import logging
import os
import re
import time

log = logging.getLogger("zfsmgr_agent.lio")

try:
    from rtslib_fb import RTSLibError
    from rtslib_fb import RTSRoot
except Exception as e:  # 缺少内核模块/未安装时给出清晰错误
    RTSLibError = Exception
    RTSRoot = None
    log.warning("rtslib 不可用: %s", e)

IQN_RE = re.compile(r"^iqn\.[0-9]{4}-[0-9]{2}\.[A-Za-z0-9.-]+(:[A-Za-z0-9._:-]+)?$")
WWN_RE = re.compile(r"(?i)^(0x)?[0-9a-f]{16}$")


def norm_wwn(v: str) -> str:
    m = WWN_RE.match((v or "").strip())
    if not m:
        raise LioError("WWPN 非法(示例: 0x10000000c9a1b2c3)", code="VALIDATION")
    return "0x" + m.group(2).lower()


class LioError(Exception):
    def __init__(self, message: str, code: str = "LIO_ERROR"):
        super().__init__(message)
        self.code = code


def _root() -> RTSRoot:
    if RTSRoot is None:
        raise LioError("rtslib 不可用(内核 target_core_mod/configfs?)", code="LIO_UNAVAILABLE")
    return RTSRoot()


def bs_name_for(dataset: str) -> str:
    return "bs_" + re.sub(r"[^A-Za-z0-9_]", "_", dataset)[:50]


def zvol_path(dataset: str) -> str:
    return "/dev/zvol/" + dataset


def dataset_of_dev(dev: str) -> str:
    """/dev/zvol/tank/vol-a -> tank/vol-a;非 zvol 设备返回 ''。"""
    p = "/dev/zvol/"
    if dev.startswith(p):
        return dev[len(p):].rstrip("/")
    return ""


class LioClient:
    """目标/backstore/LUN/ACL/会话的进程内控制。"""

    def __init__(self, save_file: str | None = None):
        self.save_file = save_file  # None => rtslib 发行版默认路径

    # ---------- 只读发现 ----------

    def _iter_iscsi_targets(self):
        rt = _root()
        for fm in rt.fabric_modules:
            if fm.name == "iscsi":
                for tgt in fm.targets:
                    yield tgt

    def _iter_tpgs(self):
        for tgt in self._iter_iscsi_targets():
            for tpg in tgt.tpgs:
                yield tgt, tpg

    def iscsi_targets(self) -> list[dict]:
        rt = _root()
        out = []
        for tgt in self._iter_iscsi_targets():
            tpg = next(iter(tgt.tpgs), None)
            entry = {
                "target_name": tgt.wwn,
                "enabled": bool(getattr(tpg, "enable", False)) if tpg else False,
                "portals": [],
                "luns": [],
                "acls": [],
                "sessions": [],
            }
            if tpg is None:
                out.append(entry)
                continue
            for np in tpg.network_portals:
                entry["portals"].append({"ip": np.ip_address, "port": np.port})
            for l in tpg.luns:
                dev = ""
                so_name = ""
                if l.storage_object is not None:
                    so_name = l.storage_object.name
                    try:
                        dev = str(getattr(l.storage_object, "udev_path", ""))
                        if not dev:
                            dev = str(l.storage_object.dev)
                    except Exception:
                        dev = ""
                entry["luns"].append({
                    "lun": l.lun,
                    "storage_object": so_name,
                    "device": dev,
                    "dataset": dataset_of_dev(dev),
                    "wwn": str(getattr(l.storage_object, "wwn", "") or ""),
                })
            for a in tpg.node_acls:
                chap = str(getattr(a, "chap_userid", "") or "")
                entry["acls"].append({
                    "initiator_iqn": a.node_wwn,
                    "chap_user": chap,
                    "has_chap": bool(chap),
                })
            out.append(entry)
        # 会话:root.sessions 元素可能是 NodeACL(带 .session)或 dict(带 parent_nodeacl)
        try:
            for it in rt.sessions:
                acl = None
                sess = None
                if isinstance(it, dict):
                    acl = it.get("parent_nodeacl")
                    sess = it
                else:
                    acl = it
                    sess = getattr(it, "session", None)
                if acl is None or not sess:
                    continue
                target_iqn = ""
                pt = getattr(acl, "parent_tpg", None)
                if pt is not None:
                    p2 = getattr(pt, "parent_target", None)
                    if p2 is not None:
                        target_iqn = p2.wwn
                conns = sess.get("connections") or [{}]
                for e in out:
                    if e["target_name"] == target_iqn:
                        e["sessions"].append({
                            "iqn": getattr(acl, "node_wwn", ""),
                            "sid": sess.get("id", 0),
                            "state": sess.get("state", ""),
                            "type": sess.get("type", ""),
                            "address": conns[0].get("address", ""),
                            "alias": sess.get("alias", ""),
                        })
        except Exception as ex:
            log.warning("会话枚举异常: %s", ex)
        return out

    def sessions(self) -> list[dict]:
        out = []
        for t in self.iscsi_targets():
            for s in t["sessions"]:
                s["target_name"] = t["target_name"]
                out.append(s)
        return out

    # ---------- FC(target 模式,需 QLogic + tcm_qla2xxx) ----------

    FC_FABRIC = "qla2xxx"

    def _fc_module(self, rt):
        for fm in rt.fabric_modules:
            if fm.name == self.FC_FABRIC:
                return fm
        raise LioError("FC target 不可用:内核未提供 qla2xxx LIO fabric(需 QLogic 24xx/26xx + tcm_qla2xxx)",
                       code="FC_UNAVAILABLE")

    def fc_targets(self) -> list[dict]:
        rt = _root()
        try:
            fm = self._fc_module(rt)
        except LioError:
            return []
        out = []
        sessions_by_target = {}
        try:
            for acl in rt.sessions:
                sess = getattr(acl, "session", None)
                if not sess:
                    continue
                pt = getattr(getattr(acl, "parent_tpg", None), "parent_target", None)
                if pt is None:
                    continue
                sessions_by_target.setdefault(pt.wwn, []).append({
                    "iqn": getattr(acl, "node_wwn", ""), "sid": sess.get("id", 0),
                    "state": sess.get("state", ""), "type": sess.get("type", ""),
                    "address": (sess.get("connections") or [{}])[0].get("address", ""),
                    "alias": sess.get("alias", ""),
                })
        except Exception:
            pass
        for tgt in fm.targets:
            tpg = next(iter(tgt.tpgs), None)
            entry = {"target_name": tgt.wwn, "enabled": bool(getattr(tpg, "enable", False)) if tpg else False,
                     "portals": [], "luns": [], "acls": [], "sessions": sessions_by_target.get(tgt.wwn, [])}
            if tpg is None:
                out.append(entry); continue
            for l in tpg.luns:
                dev, so_name = "", ""
                if l.storage_object is not None:
                    so_name = l.storage_object.name
                    dev = str(getattr(l.storage_object, "udev_path", "") or "")
                entry["luns"].append({"lun": l.lun, "storage_object": so_name, "device": dev,
                                      "dataset": dataset_of_dev(dev),
                                      "wwn": str(getattr(l.storage_object, "wwn", "") or "")})
            for a in tpg.node_acls:
                entry["acls"].append({"initiator_iqn": a.node_wwn, "chap_user": "", "has_chap": False})
            out.append(entry)
        return out

    def fc_target_create(self, wwn: str, dataset: str = "") -> None:
        wwn = norm_wwn(wwn)
        rt = _root()
        fm = self._fc_module(rt)
        if any(t.wwn.lower() == wwn for t in fm.targets):
            raise LioError(f"FC Target 已存在: {wwn}", code="CONFLICT")
        from rtslib_fb import Target
        tgt = Target(fm, wwn)
        tpg = self._find_or_make_tpg(tgt)
        tpg.enable = True
        if dataset:
            self._attach(tpg, dataset)
        self.save()

    def fc_target_delete(self, wwn: str) -> None:
        wwn = norm_wwn(wwn)
        rt = _root()
        fm = self._fc_module(rt)
        for tgt in [t for t in fm.targets if t.wwn.lower() == wwn]:
            self._drop_target(tgt)
            self.save()
            return
        raise LioError(f"FC Target 不存在: {wwn}", code="NOT_FOUND")

    def fc_lun_add(self, wwn: str, dataset: str) -> int:
        tpg = self._fc_tpg(wwn)
        self._attach(tpg, dataset)
        for a in tpg.node_acls:
            self._sync_acl_luns(a, tpg)
        self.save()
        return self._lun_id_of(tpg, dataset)

    def fc_lun_remove(self, wwn: str, lun_id: int) -> None:
        tpg = self._fc_tpg(wwn)
        for l in list(tpg.luns):
            if int(l.lun) == int(lun_id):
                if self._sessions_on(tpg):
                    raise LioError("该 Target 存在活动会话,请先断开", code="CONFLICT")
                l.delete(); self.save(); return
        raise LioError(f"LUN {lun_id} 不存在", code="NOT_FOUND")

    def fc_acl_add(self, wwn: str, initiator: str) -> None:
        ini = norm_wwn(initiator)
        tpg = self._fc_tpg(wwn)
        if any(a.node_wwn.lower() == ini for a in tpg.node_acls):
            raise LioError("ACL 已存在", code="CONFLICT")
        from rtslib_fb import NodeACL
        acl = NodeACL(tpg, ini)
        self._sync_acl_luns(acl, tpg)
        self.save()

    def fc_acl_remove(self, wwn: str, initiator: str) -> None:
        ini = norm_wwn(initiator)
        tpg = self._fc_tpg(wwn)
        for a in list(tpg.node_acls):
            if a.node_wwn.lower() == ini:
                a.delete(); self.save(); return
        raise LioError(f"ACL 不存在: {ini}", code="NOT_FOUND")

    def _fc_tpg(self, wwn: str):
        wwn = norm_wwn(wwn)
        rt = _root()
        fm = self._fc_module(rt)
        for tgt in fm.targets:
            if tgt.wwn.lower() == wwn:
                return self._find_or_make_tpg(tgt)
        raise LioError(f"FC Target 不存在: {wwn}", code="NOT_FOUND")

    def backstore_devs(self) -> list[str]:
        """返回全部 backstore 的 dev 路径(判断 zvol 是否被引用)。"""
        rt = _root()
        try:
            out = []
            for so in rt.storage_objects:
                dev = str(getattr(so, "udev_path", "") or "")
                out.append(dev or str(getattr(so, "dev", "")))
            return out
        except Exception:
            return []

    # ---------- 变更 ----------

    def target_create(self, iqn: str, dataset: str = "") -> None:
        if not IQN_RE.match(iqn):
            raise LioError("IQN 非法", code="VALIDATION")
        rt = _root()
        fm = self._iscsi_module(rt)
        if any(t.wwn == iqn for t in fm.targets):
            raise LioError(f"Target 已存在: {iqn}", code="CONFLICT")
        from rtslib_fb import Target
        tgt = Target(fm, iqn)
        tpg = self._find_or_make_tpg(tgt)
        try:
            tpg.set_attribute("authentication", 0)  # 无 CHAP 的 ACL 可登录;有 CHAP 的 ACL 仍强制
        except Exception:
            pass
        tpg.enable = True
        self._ensure_portal(tpg)
        if dataset:
            self._attach(tpg, dataset)
        self.save()

    def target_delete(self, iqn: str) -> None:
        rt = _root()
        fm = self._iscsi_module(rt)
        for tgt in [t for t in fm.targets if t.wwn == iqn]:
            self._drop_target(tgt)
            self.save()
            return
        raise LioError(f"Target 不存在: {iqn}", code="NOT_FOUND")

    def lun_add(self, iqn: str, dataset: str) -> int:
        tpg = self._tpg_of(iqn)
        self._attach(tpg, dataset)
        for a in tpg.node_acls:
            self._sync_acl_luns(a, tpg)
        self.save()
        return self._lun_id_of(tpg, dataset)

    def lun_remove(self, iqn: str, lun_id: int) -> None:
        tpg = self._tpg_of(iqn)
        for l in list(tpg.luns):
            if int(l.lun) == int(lun_id):
                # 校验:仍有活动会话引用该 LUN 时拒绝
                if self._sessions_on(tpg):
                    raise LioError("该 Target 存在活动会话,请先断开", code="CONFLICT")
                l.delete()
                self.save()
                return
        raise LioError(f"LUN {lun_id} 不存在", code="NOT_FOUND")

    def acl_add(self, iqn: str, initiator: str, chap_user: str = "",
                chap_secret: str = "") -> None:
        if not IQN_RE.match(initiator):
            raise LioError("Initiator IQN 非法", code="VALIDATION")
        if chap_user and len(chap_secret) < 12:
            raise LioError("CHAP 口令至少 12 字符", code="VALIDATION")
        if chap_secret and not chap_user:
            raise LioError("CHAP 口令需配套用户名", code="VALIDATION")
        tpg = self._tpg_of(iqn)
        existing = {a.node_wwn: a for a in tpg.node_acls}
        if initiator in existing:
            a = existing[initiator]
            if not chap_user:
                # 幂等语义:无凭据 ACL 已存在即期望状态已满足
                if str(getattr(a, "chap_userid", "") or ""):
                    raise LioError(
                        "该 ACL 已配置 CHAP 凭据;如需免认证请先删除该 ACL 再重新映射",
                        code="CONFLICT")
                self._sync_acl_luns(a, tpg)
                self._sync_auth_attr(tpg)
                self.save()
                return
            a.chap_userid = chap_user
            a.chap_password = chap_secret
        else:
            # 无 CHAP 的 ACL 与带 CHAP 的 ACL 不能共处同一 Target(认证属性互斥)
            if not chap_user and any(a.chap_userid for a in tpg.node_acls):
                raise LioError("该 Target 已有 CHAP ACL,新 ACL 必须携带 CHAP 凭据",
                               code="CONFLICT")
            from rtslib_fb import NodeACL
            acl = NodeACL(tpg, initiator)
            if chap_user:
                acl.chap_userid = chap_user
                acl.chap_password = chap_secret
            self._sync_acl_luns(acl, tpg)
        if initiator in existing:
            # 已存在(带 CHAP 刷新):补齐可能缺失的 mapped lun
            self._sync_acl_luns(existing[initiator], tpg)
        self._sync_auth_attr(tpg)
        self.save()

    def _sync_acl_luns(self, acl, tpg) -> None:
        """为该 ACL 补齐 TPG 当前全部 LUN 的映射(MappedLUN),
        本内核不会在 ACL 后建时自动生成 mapped lun。"""
        try:
            have = {int(m.mapped_lun) for m in acl.mapped_luns}
            for l in tpg.luns:
                if int(l.lun) not in have:
                    from rtslib_fb import MappedLUN
                    MappedLUN(acl, int(l.lun), int(l.lun))
        except Exception as ex:
            log.warning("ACL LUN 映射同步失败: %s", ex)

    def _sync_auth_attr(self, tpg) -> None:
        """按 ACL 凭据同步 TPG authentication 属性:
        任一 ACL 带 CHAP → 置 1(强制认证,CHAP 可协商);
        全部 ACL 无凭据 → 置 0(允许免认证登录)。"""
        try:
            any_cred = any(str(getattr(a, "chap_userid", "") or "") for a in tpg.node_acls)
            tpg.set_attribute("authentication", 1 if any_cred else 0)
        except Exception:
            pass

    def acl_remove(self, iqn: str, initiator: str) -> None:
        tpg = self._tpg_of(iqn)
        for a in list(tpg.node_acls):
            if a.node_wwn == initiator:
                if any(s["iqn"] == initiator and s["target_name"] == iqn
                       for s in self.sessions()):
                    raise LioError(f"Initiator {initiator} 存在活动会话,请先断开",
                                   code="CONFLICT")
                a.delete()
                self._sync_auth_attr(tpg)
                self.save()
                return
        raise LioError(f"ACL 不存在: {initiator}", code="NOT_FOUND")

    def session_logout(self, iqn: str, sid: int) -> None:
        tpg = self._tpg_of(iqn)
        # 会话目录在本内核为 dynamic_sessions(部分版本为 sessions)
        sdir = ""
        for cand in ("dynamic_sessions", "sessions"):
            p = os.path.join(tpg.path, cand)
            if os.path.isdir(p):
                sdir = p
                break
        if not sdir:
            raise LioError(
                "本内核不暴露会话 configfs 目录(target 端无法强踢),"
                "请在发起端执行 iscsiadm --logout 或等其断线超时",
                code="SESSION_KICK_UNSUPPORTED")
        target = os.path.join(sdir, str(sid))
        if not os.path.isdir(target):
            raise LioError(f"会话 {sid} 不存在", code="NOT_FOUND")
        # 先清空会话目录内连接(rmdir 需空目录)
        for root, dirs, files in os.walk(target, topdown=False):
            for f in files:
                try:
                    os.remove(os.path.join(root, f))
                except OSError:
                    pass
            for d in dirs:
                try:
                    os.rmdir(os.path.join(root, d))
                except OSError:
                    pass
        try:
            os.rmdir(target)
        except OSError as e:
            raise LioError(f"断开会话失败: {e}", code="CONFLICT")
        log.info("会话 %s/%s 已断开", iqn, sid)

    def save(self) -> None:
        try:
            rt = _root()
            if self.save_file:
                rt.save_to_file(self.save_file)
            else:
                rt.save_to_file()
        except Exception as e:
            log.warning("保存 LIO 配置失败(不影响本次生效): %s", e)

    def restore(self) -> None:
        """Agent 启动时从持久化文件恢复 LIO 配置(幂等,缺失则跳过)。"""
        try:
            rt = _root()
            if self.save_file and os.path.exists(self.save_file):
                rt.restore_from_file(self.save_file)
                log.info("已从 %s 恢复 LIO 配置", self.save_file)
        except Exception as e:
            log.warning("恢复 LIO 配置失败(将按空配置继续): %s", e)

    # ---------- 内部 ----------

    def _iscsi_module(self, rt):
        for fm in rt.fabric_modules:
            if fm.name == "iscsi":
                return fm
        raise LioError("本机无 iscsi fabric 模块(内核 iscsi_target_mod?)", code="LIO_UNAVAILABLE")

    def _find_or_make_tpg(self, tgt):
        for tpg in tgt.tpgs:
            return tpg
        from rtslib_fb import TPG
        return TPG(tgt, "1")

    def _ensure_portal(self, tpg) -> None:
        if any(True for _ in tpg.network_portals):
            return
        from rtslib_fb import NetworkPortal
        NetworkPortal(tpg, "0.0.0.0", 3260)

    def _attach(self, tpg, dataset: str) -> None:
        path = zvol_path(dataset)
        if not os.path.exists(path):
            raise LioError(f"块设备不存在: {path}(卷未生效?)", code="NOT_FOUND")
        rt = _root()
        bs = None
        want = bs_name_for(dataset)
        for so in rt.storage_objects:
            if so.name == want:
                bs = so
                break
        if bs is None:
            from rtslib_fb import BlockStorageObject
            bs = BlockStorageObject(name=want, dev=path)
        elif str(getattr(bs, "udev_path", "")) != path:
            raise LioError(f"backstore {want} 指向其它设备", code="CONFLICT")
        if self._lun_id_of(tpg, dataset) is not None:
            raise LioError(f"卷 {dataset} 已挂载到该 Target", code="CONFLICT")
        used = sorted(int(l.lun) for l in tpg.luns)
        lun_id = 0
        while lun_id in used:
            lun_id += 1
        from rtslib_fb import LUN
        LUN(tpg, lun_id, bs)

    def _lun_id_of(self, tpg, dataset: str) -> int | None:
        path = zvol_path(dataset)
        for l in tpg.luns:
            so = l.storage_object
            if so is not None:
                try:
                    dev = str(getattr(so, "udev_path", "") or "")
                    if dev == path or dataset_of_dev(dev) == dataset:
                        return int(l.lun)
                except Exception:
                    pass
        return None

    def _tpg_of(self, iqn: str) -> object:
        for tgt in self._iter_iscsi_targets():
            if tgt.wwn == iqn:
                tpg = self._find_or_make_tpg(tgt)
                self._ensure_portal(tpg)
                return tpg
        raise LioError(f"Target 不存在: {iqn}", code="NOT_FOUND")

    def _sessions_on(self, tpg) -> bool:
        """该 TPG(即所属 Target)是否存在活动会话。"""
        tgt = getattr(tpg, "parent_target", None)
        if tgt is None:
            return False
        return self._sessions_on_target(tgt)

    def _drop_target(self, tgt) -> None:
        if self._sessions_on_target(tgt):
            raise LioError(f"Target {tgt.wwn} 存在活动会话,请先断开", code="CONFLICT")
        for tpg in list(tgt.tpgs):
            for l in list(tpg.luns):
                try:
                    l.delete()
                except Exception:
                    pass
            for a in list(tpg.node_acls):
                try:
                    a.delete()
                except Exception:
                    pass
            for np in list(tpg.network_portals):
                try:
                    np.delete()
                except Exception:
                    pass
            try:
                tpg.delete()
            except Exception:
                pass
        # 清理不再被引用的 backstore
        self._sweep_orphan_backstores()
        tgt.delete()

    def _sessions_on_target(self, tgt) -> bool:
        return any(s["target_name"] == tgt.wwn for s in self.sessions())

    def _sweep_orphan_backstores(self) -> None:
        rt = _root()
        referenced = set()
        for fm in rt.fabric_modules:
            if fm.name != "iscsi":
                continue
            for t in fm.targets:
                for tpg in t.tpgs:
                    for l in tpg.luns:
                        so = l.storage_object
                        if so is not None:
                            referenced.add(so.name)
        for so in list(rt.storage_objects):
            if so.name not in referenced:
                try:
                    so.delete()
                except Exception:
                    pass

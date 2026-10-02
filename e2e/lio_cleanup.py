#!/usr/bin/env python3
"""e2e 专用:LIO 层残留清理(幂等)。

删除名称含 vol-a / e2epool 的 iSCSI Target(含其 TPG/LUN/ACL/Portal),
随后清理失去引用的 bs_* backstore。仅用于测试环境恢复。
"""
import sys

import rtslib_fb as r

MARK = ("vol-a", "e2epool")
rt = r.RTSRoot()
removed = []

for fm in rt.fabric_modules:
    if fm.name != "iscsi":
        continue
    for tgt in list(fm.targets):
        if not any(m in tgt.wwn for m in MARK):
            continue
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
        try:
            tgt.delete()
            removed.append(tgt.wwn)
        except Exception as e:
            print(f"删除 Target {tgt.wwn} 失败: {e}", file=sys.stderr)

# 清理孤儿 backstore(仅本平台命名 bs_ 且未被任何 TPG LUN 引用)
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
    if so.name.startswith("bs_") and so.name not in referenced:
        try:
            so.delete()
            removed.append("backstore:" + so.name)
        except Exception as e:
            print(f"删除 backstore {so.name} 失败: {e}", file=sys.stderr)

print("removed:", removed)

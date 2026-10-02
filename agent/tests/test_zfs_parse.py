"""解析层单元测试:使用远端真实 ZFS JSON 输出 fixture。"""
import json
from pathlib import Path

from zfsmgr_agent.zfs import (parse_pools, parse_datasets, parse_snapshots,
                              parse_props, parse_pool_status, ZfsJsonClient)

FIX = Path(__file__).parent / "fixtures"


def load(name: str) -> dict:
    return json.loads((FIX / name).read_text())


def test_parse_pools():
    pools = parse_pools(load("01_zpool_list.json"))
    assert pools, "至少应解析出池"
    tank = [p for p in pools if p["name"] == "tank"][0]
    assert tank["state"] == "ONLINE"
    assert tank["health"] == "ONLINE"
    assert tank["size"] > 0 and tank["free"] > 0
    assert tank["guid"]


def test_parse_datasets():
    ds = parse_datasets(load("03_zfs_list.json"))
    vols = [d for d in ds if d["type"] == "volume"]
    assert vols, "应解析出 zvol"
    v = vols[0]
    assert v["name"].startswith("tank/")
    assert v["volsize"] > 0
    assert v["compression"] in ("lz4", "off", "on", "")


def test_parse_snapshots():
    snaps = parse_snapshots(load("04_zfs_snapshots.json"))
    assert snaps
    s = snaps[0]
    assert "@" in s["name"]
    assert s["snap"]
    assert s["creation"], "creation 应转成可读时间"


def test_parse_props():
    props = parse_props(load("09_zvol_get.json"))
    assert int(props.get("volblocksize", 0)) == 16384
    assert props.get("volmode") == "default"


def test_parse_pool_status():
    st = parse_pool_status(load("08_zpool_status_default.json"))
    tank = st["tank"]
    assert tank["state"] == "ONLINE"
    assert len(tank["vdev_rows"]) >= 2
    assert tank["vdev_rows"][0]["name"] == "tank"
    assert tank["vdev_rows"][-1]["type"] == "disk"


def test_contract_rejects_bad_version():
    raw = load("01_zpool_list.json")
    raw["output_version"]["vers_major"] = 9
    try:
        parse_pools(raw)
        assert False, "应当拒绝未知契约版本"
    except Exception:
        pass

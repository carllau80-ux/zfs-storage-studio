"""RAID storcli 适配:只读保护、参数校验与解析。"""
import pytest

from zfsmgr_agent.config import Config
from zfsmgr_agent.raid import RaidClient, RaidError
from zfsmgr_agent.app import ensure_writable
from zfsmgr_agent.zfs import ZfsError


def test_read_only_blocks_all_writes():
    c = RaidClient("/nonexistent/storcli64", allow_mutation=False)
    with pytest.raises(RaidError) as e:
        c.create_vd(0, "5", ["252:0", "252:1"], "", confirm="CREATE 5")
    assert e.value.code == "READ_ONLY"
    with pytest.raises(RaidError) as e:
        c.delete_vd(0, "0", confirm="VD0")
    assert e.value.code == "READ_ONLY"
    with pytest.raises(RaidError) as e:
        c.set_vd(0, "0", name="x", confirm="VD0")
    assert e.value.code == "READ_ONLY"


def test_write_validation_happens_before_command(monkeypatch):
    c = RaidClient("/x", allow_mutation=True)
    monkeypatch.setattr(c, "available", lambda: True)

    def no_run(*a, **k):
        raise AssertionError("校验失败时不应执行 storcli")
    monkeypatch.setattr(c, "_run", no_run)
    monkeypatch.setattr(c, "vds", lambda cid: [])

    with pytest.raises(RaidError) as e:
        c.create_vd(0, "3", ["252:0"], confirm="CREATE 3")
    assert e.value.code == "VALIDATION"
    with pytest.raises(RaidError) as e:
        c.create_vd(0, "5", ["252:0;reboot"], confirm="CREATE 5")
    assert e.value.code == "VALIDATION"
    with pytest.raises(RaidError) as e:
        c.delete_vd(0, "0", confirm="yes")
    assert e.value.code == "CONFIRM"
    with pytest.raises(RaidError) as e:
        c.set_vd(0, "0", access="XX", confirm="VD0")
    assert e.value.code == "VALIDATION"


def test_vds_parse(monkeypatch):
    c = RaidClient("/x")
    monkeypatch.setattr(c, "available", lambda: True)
    monkeypatch.setattr(c, "_json", lambda args: {"Controllers": [{"Response Data": {
        "Virtual Drives": [
            {"DG/VD": "0/0", "TYPE": "RAID5", "State": "Optl", "Access": "RW",
             "Consist": "Yes", "Cache": "RWTD", "Name": "data", "Size": "1.0 TB"},
        ]}}]})
    rows = c.vds(0)
    assert rows[0]["dg"] == "0" and rows[0]["vd"] == "0"
    assert rows[0]["type"] == "RAID5" and rows[0]["name"] == "data"


def test_pds_parse(monkeypatch):
    c = RaidClient("/x")
    monkeypatch.setattr(c, "available", lambda: True)
    monkeypatch.setattr(c, "_json", lambda args: {"Controllers": [{"Response Data": {
        "Drive Information": [
            {"EID:Slt": "252:0", "DID": 8, "State": "Onln", "DG": "0",
             "Size": "558.911 GB", "Intf": "SATA", "Med": "HDD", "Model": "ST600MM0006"},
        ]}}]})
    rows = c.pds(0)
    assert rows[0]["eid_slot"] == "252:0" and rows[0]["state"] == "Onln"
    assert rows[0]["model"] == "ST600MM0006"


def test_events_tail(monkeypatch):
    c = RaidClient("/x")
    monkeypatch.setattr(c, "available", lambda: True)
    text = "\n".join(f"seqNum: {i}\nTime: 2026-10-18 10:00:0{i}\nEvent Description: test {i}\n"
                     for i in range(5))
    monkeypatch.setattr(c, "_run", lambda args, timeout=None: text)
    r = c.events(0, limit=2)
    assert r["total"] == 5 and r["returned"] == 2
    assert r["entries"][0]["seq"] == "3" and r["entries"][-1]["seq"] == "4"
    assert r["entries"][-1]["event"] == "test 4"
    assert "seqNum: 4" in r["entries"][-1]["raw"]


def test_node_read_only_guard():
    ensure_writable(Config(allow_mutation=True))
    with pytest.raises(ZfsError) as e:
        ensure_writable(Config(allow_mutation=False))
    assert e.value.code == "READ_ONLY"

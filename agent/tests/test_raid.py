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
    text = "\n".join(f"seqNum: {i}\nTime: 2026-10-18 10:00:00\nEvent Description: test {i}\n"
                     for i in range(25))
    monkeypatch.setattr(c, "_run", lambda args, timeout=None: text)
    r = c.events(0, page=2, page_size=10)
    assert r["total"] == 25 and r["pages"] == 3 and r["returned"] == 10
    assert r["entries"][0]["seq"] == "10" and r["entries"][-1]["seq"] == "19"
    assert r["entries"][-1]["event"] == "test 19"
    assert "seqNum: 19" in r["entries"][-1]["raw"]


def test_node_read_only_guard():
    ensure_writable(Config(allow_mutation=True))
    ensure_writable(Config(allow_mutation=False), unlocked=True)
    with pytest.raises(ZfsError) as e:
        ensure_writable(Config(allow_mutation=False))
    assert e.value.code == "READ_ONLY"


def test_controller_hw_fields(monkeypatch):
    c = RaidClient("/x")
    monkeypatch.setattr(c, "available", lambda: True)

    def fake_json(args):
        if args == ["show", "J"]:
            return {"Controllers": [{"Response Data": {"System Overview": [
                {"Ctl": 0, "Model": "MegaRAID", "Ports": 8, "PDs": 5, "DGs": 2,
                 "VDs": 2, "BBU": "Msng", "Hlth": "Opt"}]}}]}
        return {"Controllers": [{"Response Data": {
            "Basics": {"Model": "AVAGO MegaRAID SAS 9364-8i", "Serial Number": "SK1",
                       "SAS Address": "5006", "PCI Address": "00:02:00:00"},
            "Status": {"Controller Status": "Optimal", "BBU Status": "NA",
                       "Memory Correctable Errors": 0, "Memory Uncorrectable Errors": 0},
            "HwCfg": {"BBU": "Absent", "Alarm": "On",
                      "On Board Memory Size": "1024MB",
                      "Current Size of FW Cache (MB)": 629,
                      "ROC temperature(Degree Celsius)": 71},
            "Version": {"Firmware Version": "4.680", "Bios Version": "6.36",
                        "Driver Name": "megaraid_sas", "Driver Version": "07.7"},
        }}]}
    monkeypatch.setattr(c, "_json", fake_json)
    row = c.controllers()[0]
    assert row["model"] == "AVAGO MegaRAID SAS 9364-8i"
    assert row["bbu"] == "Absent" and row["bbu_status"] == "NA"
    assert row["temperature"] == 71 and row["memory"] == "1024MB"
    assert row["memory_correctable"] == 0
    assert row["health"] == "Optimal"

"""节点硬件采集(OS/CPU/内存/磁盘/HBA)。"""
from zfsmgr_agent import sysinfo


def test_hardware_inventory_shape():
    hw = sysinfo.hardware()
    assert hw["os"]["kernel"]
    assert hw["os"]["hostname"]
    assert hw["cpu"]["logical"] >= 1
    assert hw["cpu"]["physical"] >= 1
    assert hw["memory"]["total"] > 0
    assert 0 <= hw["memory"]["used_pct"] <= 100
    assert isinstance(hw["disks"], list)
    assert isinstance(hw["hba"].get("adapters"), list)
    assert isinstance(hw["hba"].get("fc_ports"), list)


def test_disks_exclude_virtual_block_devices():
    names = [d["name"] for d in sysinfo.disks()]
    assert all(not n.startswith(("zd", "nbd", "loop", "ram", "sr")) for n in names)
    for d in sysinfo.disks():
        assert d["size"] > 0
        assert "model" in d and "wwn" in d and "media" in d

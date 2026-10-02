"""名称/容量校验与路径安全。"""
import pytest

from zfsmgr_agent.zfs import ZfsError, NAME_RE, SNAP_RE, ZfsJsonClient

zfs = ZfsJsonClient()


def test_name_re():
    assert NAME_RE.match("vol-a_1.2")
    assert not NAME_RE.match("vol/a")
    assert not NAME_RE.match("-bad")
    assert not NAME_RE.match("sp ace")


def test_snap_re():
    assert SNAP_RE.match("snap-2026.09:01")
    assert not SNAP_RE.match("has@at")


def test_pool_create_rejects_bad_disk(monkeypatch):
    def fake_run(argv, timeout=None):
        raise AssertionError("不应执行")
    monkeypatch.setattr(zfs, "run", fake_run)
    with pytest.raises(ZfsError):
        zfs.pool_create("tank", [{"type": "stripe", "disks": ["/dev/sdb; rm -rf /"]}])


def test_pool_create_disk_path_escaping():
    with pytest.raises(ZfsError):
        zfs.pool_create("tank", [{"type": "stripe", "disks": ["../../etc/passwd"]}])

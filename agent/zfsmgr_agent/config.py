"""RunStor Node Agent 配置(TOML;敏感项仅引用 0600 文件)。

支持从旧版 agent.json 一次性迁移:
  agent.json → agent.toml + agent.token(0600,与 Manager 共用同一文件)
"""
from __future__ import annotations

import json
import os
import secrets
import stat
from dataclasses import dataclass, field
from pathlib import Path

try:
    import tomllib  # Python 3.11+
except ImportError:  # pragma: no cover
    tomllib = None

DEFAULT_PATH = Path("/etc/zfs-platform/agent.toml")


def _read_secret(path: str, what: str) -> str:
    st = os.stat(path)
    if stat.S_IMODE(st.st_mode) & 0o077:
        raise RuntimeError(f"{what} 文件权限过宽({path} 现为 {oct(stat.S_IMODE(st.st_mode))}),应为 0600")
    val = Path(path).read_text().strip()
    if not val:
        raise RuntimeError(f"{what} 文件为空: {path}")
    return val


@dataclass
class Config:
    listen: str = "127.0.0.1:9090"
    manager_url: str = "http://127.0.0.1:8080"
    node_name: str = "node1"
    advertise_addr: str = ""
    heartbeat_interval_s: int = 10
    state_file: str = "/var/lib/runstor/agent-state.json"
    agent_token_file: str = "/etc/zfs-platform/agent.token"
    # 解析后(不落盘打印)
    agent_token: str = field(default="", repr=False)

    @staticmethod
    def load(path: Path | str = DEFAULT_PATH) -> "Config":
        p = Path(path)
        if not p.exists():
            legacy = p.with_suffix(".json")
            if legacy.exists():
                _migrate_legacy(legacy, p)
            else:
                raise RuntimeError(f"配置文件不存在: {p}(请提供 TOML 或旧版 JSON 以便迁移)")
        data = tomllib.loads(p.read_text())
        known = set(Config.__dataclass_fields__) - {"agent_token"}
        unknown = set(data) - known
        if unknown:
            raise RuntimeError(f"配置存在未知键(已拒绝启动): {', '.join(sorted(unknown))}")
        c = Config(**{k: v for k, v in data.items() if k in known})
        if not c.agent_token_file or not os.path.exists(c.agent_token_file):
            raise RuntimeError("缺少 agent_token_file(与 Manager 共用的共享令牌文件)")
        c.agent_token = _read_secret(c.agent_token_file, "agent_token")
        if not c.manager_url:
            raise RuntimeError("缺少 manager_url")
        return c


def _migrate_legacy(legacy: Path, toml_path: Path) -> None:
    d = json.loads(legacy.read_text())
    token = d.get("agent_token") or secrets.token_hex(16)
    tok_file = d.get("agent_token_file") or "/etc/zfs-platform/agent.token"
    tf = Path(tok_file)
    if not tf.exists():
        tf.write_text(token + "\n")
        os.chmod(tf, 0o600)
    body = [
        "# RunStor Node Agent 配置(由旧 agent.json 迁移生成 2026-09-23)",
        f'listen = "{d.get("listen", "127.0.0.1:9090")}"',
        f'manager_url = "{d.get("manager_url", "http://127.0.0.1:8080")}"',
        f'node_name = "{d.get("node_name", "node1")}"',
        f'advertise_addr = "{d.get("advertise_addr", "")}"',
        f'heartbeat_interval_s = {int(d.get("heartbeat_interval_s", 10) or 10)}',
        f'state_file = "{d.get("state_file", "/var/lib/runstor/agent-state.json")}"',
        f'agent_token_file = "{tok_file}"',
        "",
    ]
    toml_path.write_text("\n".join(body))
    os.chmod(toml_path, 0o600)
    legacy.rename(str(legacy) + ".bak")


class State:
    """持久化 node_id 等。"""

    def __init__(self, path: str):
        self.path = Path(path)
        self.data: dict = {}
        if self.path.exists():
            try:
                self.data = json.loads(self.path.read_text())
            except Exception:
                self.data = {}
        else:
            # 兼容旧状态文件位置
            old = Path("/var/lib/zfs-platform/agent-state.json")
            if old.exists():
                try:
                    self.data = json.loads(old.read_text())
                    self.path.parent.mkdir(parents=True, exist_ok=True)
                    self.path.write_text(json.dumps(self.data, indent=2))
                except Exception:
                    self.data = {}

    def get(self, key: str, default=None):
        return self.data.get(key, default)

    def set(self, key: str, value):
        self.data[key] = value
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.path.write_text(json.dumps(self.data, indent=2))

    @property
    def node_id(self) -> int | None:
        v = self.data.get("node_id")
        return int(v) if v else None

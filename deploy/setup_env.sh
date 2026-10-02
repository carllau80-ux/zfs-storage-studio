#!/usr/bin/env bash
# 环境初始化(幂等):/etc/zfs-platform 配置生成 + systemd 单元安装。
# 用法:bash deploy/setup_env.sh   (root)
set -euo pipefail

ETC=/etc/zfs-platform
BIN=/opt/zfs-platform/bin
mkdir -p "$ETC" /var/lib/zfs-platform "$BIN"
chmod 700 /var/lib/zfs-platform

# 1) 读取数据库口令(Phase0 生成)
DBPW_FILE=/root/.zfsmgr_pgpass
if [ ! -s "$DBPW_FILE" ]; then
  echo "[setup] 未找到 $DBPW_FILE —— 请先执行 Phase0 建库(PG role zfsmgr)" >&2
  exit 1
fi
DBPW=$(cat "$DBPW_FILE")

# 2) Agent 共享令牌(两配置必须一致;首装随机生成并复用)
TOKEN_FILE="$ETC/.agent_token"
if [ ! -s "$TOKEN_FILE" ]; then
  openssl rand -hex 16 > "$TOKEN_FILE"
  chmod 600 "$TOKEN_FILE"
fi
TOKEN=$(cat "$TOKEN_FILE")

NODE_NAME=${NODE_NAME:-node1}
LISTEN_IP=$(hostname -I | awk '{print $1}')
AGENT_PORT=${AGENT_PORT:-9090}

# 3) 敏感项 0600 文件(引用式;不进 TOML)
umask 077
[ -s "$ETC/pg.dsn" ] || printf 'postgres://zfsmgr:%s@127.0.0.1:5432/zfsmgr\n' "$DBPW" > "$ETC/pg.dsn"
[ -s "$ETC/agent.token" ] || cp "$TOKEN_FILE" "$ETC/agent.token"
[ -s "$ETC/data.key" ] || openssl rand -hex 32 > "$ETC/data.key"
chmod 600 "$ETC/pg.dsn" "$ETC/agent.token" "$ETC/data.key"

# 4) manager.toml(基线:仅回环监听,由 Caddy/nginx 反代)
if [ ! -f "$ETC/manager.toml" ]; then
  cat > "$ETC/manager.toml" <<EOF
# RunStor Manager 配置
listen = "127.0.0.1:8080"
db_url_file = "$ETC/pg.dsn"
agent_token_file = "$ETC/agent.token"
data_key_file = "$ETC/data.key"
poll_seconds = 12
static_dir = ""
EOF
  chmod 600 "$ETC/manager.toml"
  echo "[setup] 生成 manager.toml"
fi

# 5) agent.toml
if [ ! -f "$ETC/agent.toml" ] && [ "${INSTALL_AGENT:-1}" = 1 ]; then
  cat > "$ETC/agent.toml" <<EOF
# RunStor Node Agent 配置
listen = "127.0.0.1:${AGENT_PORT}"
manager_url = "http://127.0.0.1:8080"
node_name = "${NODE_NAME}"
advertise_addr = "${LISTEN_IP}:${AGENT_PORT}"
heartbeat_interval_s = 10
state_file = "/var/lib/runstor/agent-state.json"
agent_token_file = "$ETC/agent.token"
EOF
  chmod 600 "$ETC/agent.toml"
  echo "[setup] 生成 agent.toml"
fi

# 5) systemd 单元
cp /opt/zfs-platform/deploy/zfs-manager.service /etc/systemd/system/
cp /opt/zfs-platform/deploy/zfs-agent.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable zfs-manager zfs-agent >/dev/null 2>&1 || true
echo "[setup] 完成。节点名=$NODE_NAME 监听=$LISTEN_IP:8080(UI/API), Agent=127.0.0.1:$AGENT_PORT"

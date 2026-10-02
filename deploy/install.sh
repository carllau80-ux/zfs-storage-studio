#!/usr/bin/env bash
# ZFS 存储管理平台 · 一键安装/升级脚本(bundle 内执行,root)
# 环境变量(带缺省):PREFIX MGR_PORT AGENT_PORT DB_NAME DB_USER DB_PASS DB_URL
#                  INSTALL_AGENT NO_START UNIT_SUFFIX KEEP_OLD_CONFIG
set -uo pipefail

PREFIX=${PREFIX:-/opt/zfs-platform}
MGR_PORT=${MGR_PORT:-8080}
AGENT_PORT=${AGENT_PORT:-9090}
DB_NAME=${DB_NAME:-zfsmgr}
DB_USER=${DB_USER:-zfsmgr}
DB_PASS=${DB_PASS:-$(openssl rand -hex 12)}
INSTALL_AGENT=${INSTALL_AGENT:-1}
NO_START=${NO_START:-0}
UNIT_SUFFIX=${UNIT_SUFFIX:-}
ETC=${CFG_DIR:-/etc/zfs-platform}
SRC="$(cd "$(dirname "$0")/.." && pwd)"   # bundle 根
log(){ echo "[install] $*"; }
die(){ echo "[install] 错误: $*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "请以 root 运行"

# ---------- 1. 依赖 ----------
if command -v apt-get >/dev/null 2>&1; then
  DEBIAN_FRONTEND=noninteractive
  PKGS="python3 jq curl openssl ca-certificates"
  [ "$INSTALL_AGENT" = 1 ] && PKGS="$PKGS python3-fastapi python3-uvicorn python3-rtslib-fb targetcli-fb zfsutils-linux nfs-kernel-server samba"
  if command -v psql >/dev/null 2>&1; then :; else PKGS="$PKGS postgresql"; fi
  log "安装依赖: $PKGS"
  DEBIAN_FRONTEND=noninteractive apt-get update -qq >/dev/null 2>&1 || true
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $PKGS >/dev/null 2>&1 \
    || log "部分依赖安装失败,请手工 apt install($PKGS)"
fi

# ---------- 2. 拷贝程序 ----------
mkdir -p "$PREFIX/bin" "$ETC" /var/lib/runstor
log "部署到 $PREFIX ..."
[ -f "$SRC/bin/zfs-manager" ] || die "缺少 $SRC/bin/zfs-manager(请先构建/解压完整 bundle)"
cp -f "$SRC/bin/zfs-manager" "$PREFIX/bin/"
rm -rf "$PREFIX/agent"; cp -r "$SRC/agent" "$PREFIX/agent"
mkdir -p "$PREFIX/deploy"; cp -f "$SRC/deploy/schema.sql" "$PREFIX/deploy/" 2>/dev/null || true
chmod -R a+rX "$PREFIX"; chmod +x "$PREFIX/bin/zfs-manager"

# ---------- 3. 数据库(本机 PG;可用 DB_URL 跳过) ----------
if [ -z "${DB_URL:-}" ]; then
  if command -v sudo >/dev/null && sudo -u postgres psql -tAc 'SELECT 1' >/dev/null 2>&1; then
    sudo -u postgres psql -q <<SQL 2>/dev/null || true
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='$DB_USER') THEN
    CREATE ROLE $DB_USER LOGIN PASSWORD '$DB_PASS';
  ELSE
    ALTER ROLE $DB_USER WITH LOGIN PASSWORD '$DB_PASS';
  END IF;
END \$\$;
SQL
    sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='$DB_NAME'" | grep -q 1 \
      || sudo -u postgres createdb -O "$DB_USER" "$DB_NAME"
    log "数据库就绪: $DB_NAME(owner $DB_USER)"
  else
    die "未检测到可用的本机 PostgreSQL;请手工建库后设置 DB_URL 重跑"
  fi
  DB_URL="postgres://$DB_USER:${DB_PASS}@127.0.0.1:5432/$DB_NAME"
fi

# ---------- 4. 配置(幂等:已存在则保留,避免破坏运行中实例) ----------
TOKEN_FILE="$ETC/.agent_token"
if [ ! -s "$TOKEN_FILE" ]; then openssl rand -hex 16 > "$TOKEN_FILE"; chmod 600 "$TOKEN_FILE"; fi
TOKEN=$(cat "$TOKEN_FILE")
DATA_KEY="$ETC/.data_key_hex"
if [ ! -s "$DATA_KEY" ]; then openssl rand -hex 32 > "$DATA_KEY"; chmod 600 "$DATA_KEY"; fi

umask 077
[ -s "$ETC/pg.dsn" ] || printf '%s\n' "$DB_URL" > "$ETC/pg.dsn"
[ -s "$ETC/agent.token" ] || cp "$TOKEN_FILE" "$ETC/agent.token"
[ -s "$ETC/data.key" ] || cp "$DATA_KEY" "$ETC/data.key"
chmod 600 "$ETC/pg.dsn" "$ETC/agent.token" "$ETC/data.key"
if [ ! -f "$ETC/manager.toml" ]; then
  cat > "$ETC/manager.toml" <<EOF
listen = "127.0.0.1:${MGR_PORT}"
db_url_file = "$ETC/pg.dsn"
agent_token_file = "$ETC/agent.token"
data_key_file = "$ETC/data.key"
poll_seconds = 12
static_dir = ""
EOF
  chmod 600 "$ETC/manager.toml"
fi
if [ ! -f "$ETC/agent.toml" ] && [ "$INSTALL_AGENT" = 1 ]; then
  ADV_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
  [ -n "$ADV_IP" ] || ADV_IP=127.0.0.1
  cat > "$ETC/agent.toml" <<EOF
listen = "127.0.0.1:${AGENT_PORT}"
manager_url = "http://127.0.0.1:${MGR_PORT}"
node_name = "$(hostname -s | tr -c 'A-Za-z0-9_.-' '_' | head -c 40)"
advertise_addr = "${ADV_IP}:${AGENT_PORT}"
heartbeat_interval_s = 10
state_file = "/var/lib/runstor/agent-state.json"
agent_token_file = "$ETC/agent.token"
EOF
  chmod 600 "$ETC/agent.toml"
fi
log "配置: $ETC/{manager,agent}.toml(敏感项引用 0600 文件)"

# ---------- 5. systemd ----------
MGR_UNIT=zfs-manager${UNIT_SUFFIX}.service
AGENT_UNIT=zfs-agent${UNIT_SUFFIX}.service
cat > /etc/systemd/system/$MGR_UNIT <<EOF
[Unit]
Description=ZFS Storage Platform Manager (Go)
After=network.target postgresql.service
Wants=network.target

[Service]
Type=simple
ExecStart=$PREFIX/bin/zfs-manager -config $ETC/manager.toml
Restart=always
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
if [ "$INSTALL_AGENT" = 1 ]; then
  cat > /etc/systemd/system/$AGENT_UNIT <<EOF
[Unit]
Description=ZFS Storage Platform Node Agent (Python)
After=network.target
Wants=network.target

[Service]
Type=simple
ExecStart=/usr/bin/python3 -m zfsmgr_agent.app -c $ETC/agent.toml
WorkingDirectory=$PREFIX/agent
Restart=always
RestartSec=3
Environment=PYTHONUNBUFFERED=1

[Install]
WantedBy=multi-user.target
EOF
fi
systemctl daemon-reload
systemctl enable $MGR_UNIT >/dev/null 2>&1 || true
[ "$INSTALL_AGENT" = 1 ] && systemctl enable $AGENT_UNIT >/dev/null 2>&1 || true

# ---------- 6. 启动与验证 ----------
if [ "$NO_START" = 1 ]; then
  log "NO_START=1:安装完成,未启动服务"
  exit 0
fi
systemctl restart $MGR_UNIT
[ "$INSTALL_AGENT" = 1 ] && systemctl restart $AGENT_UNIT
sleep 5
if curl -sf -o /dev/null "http://127.0.0.1:${MGR_PORT}/"; then
  log "✔ Manager 已启动: http://<本机IP>:${MGR_PORT}  (默认账号 admin/admin123,登录后请修改)"
else
  log "⚠ Manager 启动异常,请查看: journalctl -u $MGR_UNIT -n 50"
fi
[ "$INSTALL_AGENT" = 1 ] && log "Agent 单元: $AGENT_UNIT(端口 127.0.0.1:${AGENT_PORT})"
log "完成。存储池请到 Web「存储池 → 新建池」创建(设备会过滤含文件系统/已分区盘)。"

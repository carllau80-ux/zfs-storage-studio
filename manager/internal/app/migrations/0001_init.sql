-- ZFS 存储管理平台 元数据库 schema v1 (PostgreSQL)
-- 说明:Manager 启动时自动执行(幂等);生产环境需授权 zfsmgr 角色。

CREATE TABLE IF NOT EXISTS users (
  id            BIGSERIAL PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin','operator','viewer')),
  disabled      BOOLEAN NOT NULL DEFAULT FALSE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tokens (
  token      TEXT PRIMARY KEY,
  user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
  id               BIGSERIAL PRIMARY KEY,
  name             TEXT NOT NULL UNIQUE,
  agent_addr       TEXT NOT NULL DEFAULT '',          -- agent 监听地址 host:port(Manager 可达)
  advertise_addr   TEXT NOT NULL DEFAULT '',          -- 存储网对外地址 host:port(生成 Portal 用)
  agent_version    TEXT NOT NULL DEFAULT '',
  zfs_version      TEXT NOT NULL DEFAULT '',
  capabilities_json TEXT NOT NULL DEFAULT '{}',
  status           TEXT NOT NULL DEFAULT 'offline',   -- online | offline
  last_seen_at     TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pools (
  id         BIGSERIAL PRIMARY KEY,
  node_id    BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  guid       TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT '',
  health     TEXT NOT NULL DEFAULT '',
  size       BIGINT NOT NULL DEFAULT 0,
  allocated  BIGINT NOT NULL DEFAULT 0,
  free       BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (node_id, name)
);

CREATE TABLE IF NOT EXISTS datasets (
  id               BIGSERIAL PRIMARY KEY,
  node_id          BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  pool_id          BIGINT REFERENCES pools(id) ON DELETE CASCADE,
  name             TEXT NOT NULL,                     -- pool/dataset 全名
  type             TEXT NOT NULL DEFAULT 'volume' CHECK (type IN ('volume','filesystem')),
  volsize          BIGINT NOT NULL DEFAULT 0,
  used             BIGINT NOT NULL DEFAULT 0,
  available        BIGINT NOT NULL DEFAULT 0,
  compression      TEXT NOT NULL DEFAULT '',
  origin           TEXT NOT NULL DEFAULT '',
  filesystem_type  TEXT NOT NULL DEFAULT 'none' CHECK (filesystem_type IN ('none','ext4','xfs','ntfs','unknown')),
  filesystem_status TEXT NOT NULL DEFAULT 'unformatted' CHECK (filesystem_status IN ('unformatted','ready','unknown')),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (node_id, name)
);

CREATE TABLE IF NOT EXISTS snapshots (
  id         BIGSERIAL PRIMARY KEY,
  dataset_id BIGINT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  used       BIGINT NOT NULL DEFAULT 0,
  createtxg  BIGINT NOT NULL DEFAULT 0,
  creation   TEXT NOT NULL DEFAULT '',
  UNIQUE (dataset_id, name)
);

CREATE TABLE IF NOT EXISTS targets (
  id          BIGSERIAL PRIMARY KEY,
  node_id     BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  target_name TEXT NOT NULL,                          -- iSCSI IQN
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (node_id, target_name)
);

CREATE TABLE IF NOT EXISTS luns (
  id         BIGSERIAL PRIMARY KEY,
  target_id  BIGINT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
  dataset_id BIGINT REFERENCES datasets(id) ON DELETE SET NULL,
  lun_id     INT NOT NULL,
  UNIQUE (target_id, lun_id)
);

CREATE TABLE IF NOT EXISTS acls (
  id            BIGSERIAL PRIMARY KEY,
  target_id     BIGINT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
  initiator_iqn TEXT NOT NULL,
  chap_user     TEXT NOT NULL DEFAULT '',
  UNIQUE (target_id, initiator_iqn)
);

CREATE TABLE IF NOT EXISTS hosts (
  id          BIGSERIAL PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  os_type     TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS initiators (
  id              BIGSERIAL PRIMARY KEY,
  host_id         BIGINT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  iqn             TEXT NOT NULL UNIQUE,
  chap_user       TEXT NOT NULL DEFAULT '',
  chap_secret_enc TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mappings (
  id         BIGSERIAL PRIMARY KEY,
  host_id    BIGINT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  node_id    BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  target_id  BIGINT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
  dataset_id BIGINT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
  rw         TEXT NOT NULL DEFAULT 'rw',
  enabled    BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (host_id, dataset_id)
);

CREATE TABLE IF NOT EXISTS jobs (
  id         BIGSERIAL PRIMARY KEY,
  node_id    BIGINT REFERENCES nodes(id) ON DELETE CASCADE,
  type       TEXT NOT NULL,
  state      TEXT NOT NULL DEFAULT 'pending',
  params_json TEXT NOT NULL DEFAULT '{}',
  result_json TEXT NOT NULL DEFAULT '{}',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id            BIGSERIAL PRIMARY KEY,
  username      TEXT NOT NULL DEFAULT '',
  node_id       BIGINT,
  action        TEXT NOT NULL,
  resource_type TEXT NOT NULL DEFAULT '',
  resource_name TEXT NOT NULL DEFAULT '',
  params_json   TEXT NOT NULL DEFAULT '{}',
  result        TEXT NOT NULL DEFAULT 'ok',           -- ok | err
  detail        TEXT NOT NULL DEFAULT '',
  duration_ms   BIGINT NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs (username);
CREATE INDEX IF NOT EXISTS idx_audit_node ON audit_logs (node_id);
CREATE INDEX IF NOT EXISTS idx_datasets_node ON datasets (node_id);
CREATE INDEX IF NOT EXISTS idx_targets_node ON targets (node_id);

-- v2 增量:节点对外(存储网)地址(旧库升级用,幂等)
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS advertise_addr TEXT NOT NULL DEFAULT '';

-- v3 增量:文件共享(NFS / SMB-CIFS)
CREATE TABLE IF NOT EXISTS shares (
  id          BIGSERIAL PRIMARY KEY,
  node_id     BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  dataset_id  BIGINT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
  type        TEXT NOT NULL CHECK (type IN ('nfs','smb')),
  name        TEXT NOT NULL DEFAULT '',
  config_json TEXT NOT NULL DEFAULT '{}',
  enabled     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (node_id, type, name)
);

-- v4 增量:传输类型(iSCSI / FC)
ALTER TABLE targets ADD COLUMN IF NOT EXISTS transport TEXT NOT NULL DEFAULT 'iscsi';
ALTER TABLE initiators ADD COLUMN IF NOT EXISTS transport TEXT NOT NULL DEFAULT 'iscsi';

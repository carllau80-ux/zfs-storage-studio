# RunStor 存储管理平台 v0.91 发布说明

发布日期:2026-10-02 · 标签:`v0.91`

## 本版包含
- **Manager(Go 单二进制,内嵌前端)**:`/api/v1` REST + RBAC(admin/operator/viewer)+ 全量审计;
  TOML 配置与引用式密钥(0600 校验)、登录令牌 SHA-256 哈希、登录失败限速;
  `/healthz` `/readyz` `/version` `/metrics`;优雅退出(20s drain);版本化迁移(`migrations/000N`);
  JSON 结构化日志 + `X-Request-Id`;错误信封含 `code/key/params`。
- **Node Agent(Python/FastAPI)**:ZFS 2.3 JSON 适配层(契约校验/回读验证);LIO 控制(rtslib):
  iSCSI/FC Target、LUN、ACL(CHAP)、会话;mkfs 白名单(ext4/xfs/ntfs)+ blkid 回读;
  NFS(exportfs)/SMB(Samba)共享;性能指标采集(/proc)。
- **Web(Vue3 + Element Plus)**:节点/存储池(多 vdev 组、raidz、slog/cache、ashift/compression/recordsize/dedup)/
  卷与文件系统(快照、回滚、克隆、CRUD、挂载点)/Target(iSCSI+FC,按能力置灰)/Initiator(IQN+WWPN)/映射/
  共享(NFS/SMB)/审计/用户;Dashboard 聚合与实时监控(CPU/内存/网络/磁盘 IO 曲线);中英双语;亮/暗/跟随系统主题。
- **部署**:systemd 单元、nginx TLS 入口示例(8443,自签+HSTS/CSP+gzip)、一键安装器 `deploy/install.sh`、
  打包脚本 `scripts/package.sh`(输出 `zfs-platform-<ver>.tar.gz`)。
- **质量**:e2e 冒烟 41 项断言、Agent 单元测试;基线符合性检查与 ADR 文档齐全。

## 资产
- `zfs-platform-0.91.tar.gz`:一键部署包(解压后 `./deploy/install.sh`)。

## 说明
- 历史标签 `v1.0.0` 为内部迭代标记,本版修订号按发布序列改为 **0.91** 起算。
- TLS 当前使用自签证书;生产请替换受信证书或启用 Caddy 自动证书。

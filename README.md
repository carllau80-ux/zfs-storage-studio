# ZFS 存储管理平台(P0 核心闭环实现)

对应《ZFS 存储管理 Web 平台设计方案 v0.13》的 **P0 垂直切片**:在单台 Debian 13 存储节点上实现
Manager(Go)+ Node Agent(Python)+ PostgreSQL + Vue3 前端 的完整闭环,并经真实 iSCSI
(本机 Initiator ↔ LIO Target,CHAP)读写验证。

## 部署环境(192.168.13.38)

| 项 | 值 |
|---|---|
| OS | Debian 13 trixie,内核 6.12.57,KVM(2 vCPU / 1.9G + 2G swap) |
| apt 源 | 中科大 mirrors.ustc.edu.cn(含 backports/security) |
| ZFS | zfsutils 2.3.2-2(池 tank = 整盘 /dev/sdb,lz4) |
| LIO | 内核 LIO + python3-rtslib-fb 2.1.76(配置持久化 /etc/rtslib-fb-target/saveconfig.json) |
| PostgreSQL | 17.6,本机 127.0.0.1:5432,库/角色 `zfsmgr`(口令存 /root/.zfsmgr_pgpass) |
| 服务 | systemd:`zfs-manager`(:8080,UI+API)、`zfs-agent`(127.0.0.1:9090) |
| 目录 | 代码 /opt/zfs-platform;配置 /etc/zfs-platform/{manager,agent}.json |

## 文档与交付

- 使用手册(Word):docs/使用手册.docx
- 开发文档:docs/开发文档.md(架构/API/测试/踩坑)
- 部署文档:docs/部署文档.md(一键安装/手工部署/升级/排障)
- 一键安装包:releases/zfs-platform-1.0.0.tar.gz
  ```bash
  tar -xzf zfs-platform-1.0.0.tar.gz -C /opt && cd /opt/zfs-platform-1.0.0
  ./deploy/install.sh          # root;可选 PREFIX/MGR_PORT/AGENT_PORT/DB_NAME/DB_URL/NO_START=1
  ```
  打包:`bash scripts/package.sh 1.0.0`(需目标机具备 go/node 构建工具)

## 配置与访问(基线)

- 配置为 **TOML**(`/etc/zfs-platform/{manager,agent}.toml`),敏感项仅存引用文件(`pg.dsn`/`agent.token`/`data.key`,0600);
- 应用**仅监听回环**(`127.0.0.1:8080` / `127.0.0.1:9090`),对外由入口代理承担:入口用 nginx **TLS 8443**(自签+安全头+`https://IP:8443`;`deploy/nginx-runstor.conf.example`),Caddy 为可选替代;
- 登录令牌在数据库仅存 `sha256:` 哈希;旧会话平滑迁移不失效。

## 访问

- 平台 http://192.168.13.38:8080(或 http://127.0.0.1:8080)
- 初始管理员:用户名 `admin`,口令由部署时生成并保存在 **/etc/zfs-platform/admin.secret(0600)**;首次登录后请立即修改。operator/viewer 由管理员在「系统 → 用户」按需创建。

## 架构与代码

```
manager/  Go 1.24 单二进制(内部内嵌前端 dist;仅依赖 pgx/x-crypto)
agent/    Python FastAPI:ZfsJsonClient(zfs/zpool -j 适配,契约版本校验)
          + LioClient(rtslib 进程内)+ FormatService(白名单 mkfs + blkid)
web/      Vue3 + Element Plus + Vite(构建产物内嵌进 Manager)
e2e/      smoke.sh 全链路冒烟 + lio_cleanup.py(测试残留清理)
deploy/   schema.sql、systemd 单元、setup_env.sh(幂等初始化)
```

数据流:前端/API → Manager(PostgreSQL 元数据、审计、RBAC、轮询对账 12s)
→ Agent(本机 zfs/zpool 与 LIO 执行,回读校验)→ 内核。一期 1 Manager + 1 Agent(架构带 node_id 维度)。

## 功能范围(已验证)

节点注册/心跳/能力上报;池(建/scrub/销毁/状态 JSON 展示);zvol(建/扩容缩容/删,sparse 走
`zfs create -s`);格式化 ext4/xfs/ntfs(二次确认、已映射/已挂载拦截、blkid 回读);
快照(建/列/回滚(仅最新)/删,被映射禁回滚);
**zfs 文件系统 dataset**(mountpoint=none 托管创建/删除/快照);**压缩与重删属性**:池根/卷/文件系统的
compression(off/lz4/zstd/zstd-N/gzip/lzjb/zle/inherit)与 dedup(off/on/verify,需二次确认)实时显示(含
compressratio/recordsize 等)并可配置(白名单 + zfs set 回读校验);**Target / Initiator 双传输**:Target 页统一管理 iSCSI IQN 与 FC 目标 WWPN(FC 无兼容 QLogic HBA 时按能力置灰并给出原因);Initiator 页管理 iSCSI IQN 与 FC WWPN 客户端(FC 无 CHAP);映射记录标注传输类型(当前 FC 映射需硬件就绪,接口给出明确提示)。**文件共享 NFS / SMB-CIFS**:在池内文件系统数据集上创建 NFS(exportfs,支持客户端网段/rw-ro/sync-async/root_squash)与 SMB(Samba,支持只读/匿名/允许用户)共享,启用停用与删除,exportfs/testparm 回读校验;**首页"支持的存储能力"**卡片展示池布局/数据集特性/块与文件协议/卷文件系统及各节点能力(FC/mkfs);
**详细输出格式化**:池状态按
vdev 层级表格 + scan/错误计数展示(原始 JSON 折叠),会话/属性均结构化输出;iSCSI Target(自动 IQN)/LUN(一卷一 Target)/ACL
(CHAP,≥12 位)/会话实时查看与断开;主机档案与 Initiator(CHAP 凭据 AES-GCM 静态加密);
一键映射/解映射(自动建删专用 Target);审计全量留痕;RBAC(admin/operator/viewer);
Dashboard 聚合;存量池自动发现入库;Agent 重启后 LIO 配置自动恢复。

## 验证结果

`bash /opt/zfs-platform/e2e/smoke.sh` → **PASS=41 FAIL=0**,含:
- RBAC:viewer/operator 越权均 403
- 真实块链路:建池(loop 盘)→ 建卷 2G → mkfs.ext4 → 扩容 3G → 快照/回滚/删 → 主机+CHAP
  映射 → **本机 Initiator 登录本机 Target**(错误口令被拒、正确口令成功)→ 挂载写入 8M 随机数据
  → sha1 往返一致 → 会话在线可见 → 登出 → 解映射自动清理 Target → 审计可查
- Agent 单元测试 10/10(解析层基于真实 ZFS JSON 输出 fixture)
- 层间强校验:已映射卷禁止格式化/删除/回滚(409);确认码不符拒绝(400)

## 开发/部署命令(在代码工作区执行)

```bash
make sync           # rsync → 192.168.13.38:/opt/zfs-platform
make agent-test     # pytest
make manager-build  # go vet+build(远端,GOPROXY=goproxy.cn)
make web-build      # npm install + vite build + 内嵌拷贝
make deploy         # 配置生成 + systemd 安装/重启
make e2e            # 全链路冒烟
```

注意:rsync 排除 `bin/`、`node_modules`、`web/dist`、`manager/internal/app/static/dist`
(构建产物在远端生成,勿用本地占位文件覆盖)。

## 真机客户端验证(192.168.13.39 = openEuler 20.03 Initiator)

在平台建卷 tank/vol-cli39(1G,zstd)→ 录入主机 client39(IQN iqn.2012-01.com.openeuler:…)→ 一键映射。
.39 上 open-iscsi 发现/登录 192.168.13.38:3260 → LUN 设备出现(1G)→ mkfs.ext4 → 挂载 →
**96M 随机数据 sha1 往返一致**(df 显示 974M)→ 平台会话页实时显示发起端
`iqn.…openeuler:de6f2d795c0 / 192.168.13.39 / LOGGED_IN` → 登出后会话清零 → 解映射自动清理
专用 Target → 卷/主机清理,双方无残留。14 项断言 13 直通 + 1 项脚本挂载时序误报(重测通过)。

## 重要:e2e 脚本清理范围

`e2e/smoke.sh` 开始与结束时仅清理 **e2e 自己的命名空间**(主机 e2e-host、卷 tank/vol-a、
池 e2epool、对应 Target/ACL/映射),**不会**删除其它主机/卷/映射等用户数据
(2026-09-06 曾因全量复位误清用户主机档案,已修复为白名单式清理并经回归验证)。

## 已知边界与后续

- FC、克隆/promote、zvol↔IQN 关联、跨节点复制、客户端自动开通 API:本期未实现(P1+,架构预留)。
- 目标端“强制断开会话”:本内核 LIO 不暴露会话 configfs 目录(仅 dynamic_sessions 属性),接口返回
  明确提示需在发起端 iscsiadm --logout(会话实时查看不受影响)。
- sparse 以 `zfs create -s` 支持;本发行版无 sparse 属性(列表不显示)。
- CHAP 语义:带凭据 ACL 的 Target 自动置 `authentication=1`,纯免认证 Target 置 0;
  两种 ACL 不混布在同一 Target(接口返回明确冲突)。
- Manager 以 root 运行于开发环境;生产建议降权 + HTTPS/mTLS(文档 §10)。
- 默认只读用户 viewer;敏感操作均需键入资源名二次确认并写审计。

## 现场修复记录(踩坑)

1. zfs JSON 契约实测差异:`sparse`/`-o` 组合属性会被拒、`pool_guid` 而非 `guid`,
   `creation` 输出 epoch(适配层已按实测处理)。
2. rtslib TPG `authentication` 属性默认 1(无凭据 ACL 无法登录)与 CHAP 协商语义
   (带凭据 ACL 需置 1),以及 ACL 后建 LUN 时内核不自动生成 mapped_lun ——
   Agent 在 acl_add/lun_add 时显式同步 MappedLUN 与认证属性。
3. `rt.sessions` 元素形态随 rtslib 版本在 NodeACL/dict 间变化 —— 双形态兼容。
4. 会话登出采用 configfs `sessions/<sid>` rmdir(会话 dict 无删除接口)。

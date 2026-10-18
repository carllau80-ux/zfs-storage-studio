# storman 存储管理平台

storman 统一管理 **ZFS 存储池**与 **RAID 控制器**:Manager(Go)+ Node Agent(Python/FastAPI)+
PostgreSQL + Vue 3(Material You 亮/暗主题,中英双语),单二进制内嵌前端,支持多节点 Agent 注册、
能力上报与实时/对账双通道状态。

- **ZFS**:池(1..n vdev、raidz/mirror/stripe、slog/cache/spare)、zvol/文件系统、格式化
  ext4/xfs/ntfs、快照/克隆/回滚、compression/dedup/recordsize/ashift 等属性、iSCSI/FC Target、
  Initiator/CHAP 映射、NFS/SMB 共享。
- **RAID**(storcli):控制器信息与主要设置、VD(虚拟磁盘)与 PD(物理磁盘)状态、RAID 0/1/5/6/10/50/60
  创建、删除、属性(名称/读写/IO/访问策略)修改、RAID 事件日志;**默认只读**,写操作需节点显式开启
  并二次确认,生产/验证节点可完全禁止变更。
- **安全基线**:TOML 配置 + 0600 密钥文件、令牌 SHA-256 哈希、CHAP 凭据 AES-GCM 加密、RBAC
  (admin/operator/viewer)、审计留痕、登录限速、TLS 8443 入口、健康检查/Prometheus 指标。

## 验证环境

| 环境 | 说明 |
|---|---|
| 192.168.13.38(Debian 13) | 开发/回归环境:ZFS 池 `tank`、LIO/iSCSI、PostgreSQL,服务 `zfs-manager`/`zfs-agent`(历史单元名保留) |
| 192.168.2.128(openEuler 24.03) | **生产验证节点(只读)**:MegaRAID SAS9364-8i + storcli64,ZFS 池 `runstor`;Agent 单元 `storman-agent`,配置 `allow_mutation=false`、`raid_allow_mutation=false` |

生产验证节点仅执行查询;Agent 在**所有写操作入口**统一拒绝(`READ_ONLY`),Manager 对 RAID 写接口
额外返回 403(`RAID_READ_ONLY`)。部署过程中未对生产池/VD/PD 做任何创建、删除或属性修改。

## 文档与交付

- 使用手册(Word):`docs/使用手册.docx`
- 开发文档:`docs/开发文档.md`(架构/API/测试/踩坑)
- 部署文档:`docs/部署文档.md`(一键安装/手工部署/只读 Agent/排障)
- 发布说明:`docs/RELEASE-0.93.md`
- 一键安装包(0.93):`releases/storman-0.93.tar.gz`
  ```bash
  tar -xzf storman-0.93.tar.gz -C /opt && cd /opt/storman-0.93
  ./deploy/install.sh     # root;缺省安装到 /opt/storman,/etc/storman
  ```
  打包:`bash scripts/package.sh 0.93`(需 go/node 构建工具;`REBUILD_WEB=1` 强制重建前端)
- 安装器默认服务名:`storman-manager` / `storman-agent`;**数据库仍为 `zfsmgr`**,
  历史部署的 `zfs-*` 单元与 `/opt/zfs-platform` 路径保持不变。

## 配置与访问

- 配置 `/etc/storman/{manager,agent}.toml`(历史部署 `/etc/zfs-platform/`),敏感项只存引用文件
  (`pg.dsn`/`agent.token`/`data.key`/`admin.secret`,权限 0600,过宽拒绝启动)。
- Manager 默认仅监听 `127.0.0.1:8080`;对外使用 nginx TLS 8443(`deploy/nginx-runstor.conf.example`,
  自签证书示例)。跨网 Agent 通过 `manager_url=https://<manager>:8443` 上报,节点侧需信任该证书。
- 初始管理员口令:`/etc/storman/admin.secret`(首次登录后请立即修改);operator/viewer 由管理员创建。

## 架构

```
manager/  Go 1.24 单二进制(内嵌 web/dist;pgx + pgxpool;迁移随二进制)
  internal/app/  API、RBAC、审计、Agent 对账、RAID 代理(只读门禁)
agent/    Python FastAPI
  zfsmgr_agent/zfs.py     zfs/zpool -j 适配(契约版本校验、白名单属性)
  zfsmgr_agent/lio.py     iSCSI/FC(LIO;rtslib 缺失时自动跳过 Target 同步)
  zfsmgr_agent/raid.py    storcli 只读查询 + 受保护写操作(默认禁用)
  zfsmgr_agent/shares.py  NFS(exportfs)/ SMB(Samba include + testparm 回读)
web/      Vue 3 + Element Plus + Vite(Material You 主题、双语、容量统一 GiB)
e2e/      smoke.sh 41 项全链路冒烟;白名单式清理
deploy/   schema.sql、storman/zfs 单元、install.sh、nginx TLS 示例
```

数据流:前端/API → Manager(元数据、审计、RBAC、12s 对账)→ Agent(本机 zfs/zpool、LIO、storcli,
回读校验)→ 内核。多节点以 `node_id` 隔离;Agent 每次启动重新注册以刷新能力(RAID/storcli/LIO)。

## RAID 能力与只读模式

| 能力 | 接口(Manager) | 说明 |
|---|---|---|
| 控制器/属性 | `GET /api/v1/nodes/{id}/raid/controllers` | 型号、序列号、FW/BIOS、PCI/SAS、驱动、PD/DG/VD 数、BBU、告警 |
| VD 列表 | `GET .../raid/controllers/{cid}/vds` | VD/DG、RAID 级别、状态、容量、访问/缓存/一致性 |
| PD 列表 | `GET .../raid/controllers/{cid}/pds` | EID:Slt、状态、DG、容量、接口/介质/型号/热备 |
| 事件日志 | `GET .../raid/controllers/{cid}/events?limit=N` | 结构化序号/时间/事件,可展开原始记录 |
| 创建 VD | `POST .../raid/vds` | 校验级别/磁盘/名称/条带,确认串 `CREATE <名称或级别>`,回读 VD 数 |
| 删除 VD | `DELETE .../raid/vds/{vd}` | 确认串 `VD<编号>`,删除后回读确认不存在 |
| 修改属性 | `PATCH .../raid/vds/{vd}` | 名称、WT/WB、读策略、Direct/Cached、RW/RO;确认串 `VD<编号>` |

节点能力 `capabilities.raid={available,allow_mutation,storcli}` 决定界面与接口是否可写。写操作在
**Manager 与 Agent 两侧**校验:RAID 默认 `raid_allow_mutation=false`;节点级 `allow_mutation=false`
时所有 ZFS/RAID/共享/LIO 变更在 Agent 侧一律拒绝,适用于生产与验证环境。

## 验证结果

- `e2e/smoke.sh`:**PASS=41 FAIL=0**(RBAC、建池/scrub/销毁、zvol/mkfs/扩容、快照/回滚、CHAP 真机
  iSCSI 读写与 sha1 往返、解映射、审计)。
- Agent pytest:**16 passed**(ZFS 解析 fixture + RAID 只读/校验/事件解析);Manager `go test ./...` 通过。
- 生产节点只读验证:控制器/VD/PD/事件查询成功;Manager RAID 创建/删除返回 **403**,Agent 直连返回
  `READ_ONLY`;**VD 数保持 2、PD 数保持 5,未执行任何 storcli 写命令**。
- ZFS 生产池 `runstor`(DEGRADED,只读查看)与节点 1 的 `vol2` 数据均在回归后核验保留。

## 开发命令

```bash
make sync           # rsync → 192.168.13.38:/opt/zfs-platform(历史路径)
make agent-test     # 远端 pytest
make manager-build  # 远端 go vet + build
make web-build      # 远端 npm build 并内嵌
make deploy         # 配置生成 + systemd 重启(13.38)
make e2e            # 全链路冒烟
```

本地前端构建:`cd web && npm install && npm run build`,再同步到 `manager/internal/app/static/dist`。

## 已知边界

- RAID 写操作已实现但按生产约束**未在验证环境执行**;需在非生产环境完成创建/删除/属性变更的实机验收。
- storcli 事件日志按文本块解析;超大数据量默认仅取最近 N 条(1..500)。
- FC 能力依赖 QLogic HBA + tcm_qla2xxx;无硬件时接口明确置灰。rtslib 缺失的节点(如 2.128)自动跳过
  iSCSI/FC 同步,不影响 ZFS/RAID。
- 目标端“强制断开会话”受 LIO configfs 限制,需在 Initiator 侧登出(会话实时查看不受影响)。
- 数据库标识(`zfsmgr`)、历史部署路径(`/opt/zfs-platform`、`zfs-*` 单元)为兼容保留,产品展示与
  新安装统一为 **storman**。

## e2e 清理范围

`e2e/smoke.sh` 仅清理自身命名空间(主机 `e2e-host`、卷 `tank/vol-a`、池 `e2epool` 及对应
Target/ACL/映射),不会删除其它主机/卷/映射。生产节点不参与 e2e 写操作。

# storman 0.93 发布说明(2026-10-18)

## 重点

1. **产品更名 storman**:Web 标题/品牌/登录页、安装包(`storman-0.93.tar.gz`)、新安装缺省目录
   `/opt/storman`、`/etc/storman`、systemd 单元 `storman-manager`/`storman-agent`。
   数据库名与历史部署路径(`/opt/zfs-platform`、`zfs-*` 单元)为兼容保持不变。
2. **RAID 管理(经 Node Agent / storcli)**:
   - 控制器信息:型号、序列号、FW/BIOS、PCI/SAS、驱动、端口/PD/DG/VD 数、BBU、告警、温度、内存;
   - VD:列表、RAID 0/1/5/6/10/50/60 创建、删除、名称/写策略/读策略/IO 策略/访问策略修改;
   - PD:状态、DG、容量、接口、介质、扇区、型号、热备、SED;
   - 日志:事件总数 + 最近 N 条结构化(序号/时间/事件),可展开原始记录;
   - 保护:RAID 写默认禁用;Manager 侧 403 门禁 + Agent 侧 `READ_ONLY` 双保险,确认串校验,写后回读。
3. **节点级写保护**:`allow_mutation=false` 时 Agent 拒绝全部 ZFS/RAID/共享/LIO 变更;适用于生产/验证节点。
4. **Agent 与 ZFS 集成**:同一 Python Agent 上报 RAID/storcli 与 LIO 能力;无 rtslib 的节点自动跳过
   Target 对账。跨网 Agent 支持 `advertise_addr` 回连与 TLS 证书信任。
5. Web 新增「RAID」页(控制器卡片、VD/PD 表、日志视图、只读提示/禁用按钮);节点详情展示 RAID、
   LIO 与节点写保护能力。中英文与 Material You 亮/暗主题延续。

## 验证

- 13.38 回归:`e2e/smoke.sh` **PASS=41 FAIL=0**;Agent pytest **16 passed**;Manager `go test` 通过。
- 2.128(生产验证节点,只读):
  - 查询控制器/VD/PD/事件均成功;VD=2、PD=5;
  - Manager 创建/删除 RAID 返回 **403 RAID_READ_ONLY**;Agent 直连创建/删除/改属性返回 `READ_ONLY`;
  - ZFS 写操作同样被 Agent 拒绝;全程未执行任何 storcli 写命令,生产池/VD/PD 无变更;
  - UI 实测只读徽标、禁用按钮、控制器信息、PD(5) 与事件表格均正常。
- 13.38 用户数据核验:池 `tank`、`tank/vol2`、`tank/vol3` 与既有映射保留。

## 升级/兼容

- 新安装:解压 `storman-0.93.tar.gz` 后执行 `deploy/install.sh`(缺省 storman 路径)。
- 历史部署:可设置 `PREFIX=/opt/zfs-platform CFG_DIR=/etc/zfs-platform` 并沿用原 `zfs-*` 单元;
  Manager 数据库无需迁移(版本化 schema 自动执行)。
- 跨网 Agent 需将 Manager TLS 证书加入节点信任库,并使用 HTTPS 入口上报。

## 已知边界

- RAID 写操作未在 2.128 执行(生产约束),需在独立测试节点完成实机验收。
- 未实现 BBU/热备/重建等高级 RAID 操作;事件读取依赖 storcli 文本格式。
- FC 需 QLogic HBA + tcm_qla2xxx;无硬件时置灰。

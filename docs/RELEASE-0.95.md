# storman 0.95 发布说明(2026-10-18)

## 新增

1. **节点硬件详情**:节点页「详情」新增实时硬件信息:
   - 操作系统:发行版名称/版本、内核、架构、主机名、运行时间,并按发行版显示官方图标
     (Debian、openEuler、Ubuntu、CentOS、Fedora、RHEL、AlmaLinux、Rocky、openSUSE/SUSE);
   - CPU:型号、逻辑处理器、物理核心、插槽/每插槽核心、当前平均主频;
   - 内存:总量/已用/可用/空闲、使用率、交换分区;
   - RAID 卡:型号、序列号、固件、健康、BBU、温度、内存、PD/DG/VD 数量;
   - 物理磁盘:设备、容量、型号、序列号、WWN、接口/介质、文件系统、挂载点、分区数;
   - HBA:FC 端口(WWPN/WWNN/状态/速率/驱动)与 PCI 存储控制器(FC/SAS/SCSI,含驱动)。
2. Agent 新增 `hardware` 查询,Manager 新增 `GET /api/v1/nodes/{id}/hardware`;
   物理磁盘清单排除 zvol(zd*)、nbd/rbd、loop/ram/rom 等虚拟块设备。
3. 页面底部保留能力探测与原始 capabilities JSON,便于排障。

## 验证

- 节点 1(Debian 13,2 vCPU/1.9G):OS 图标、CPU、内存、磁盘 sda/sdb、Virtio SCSI 控制器正常显示。
- 节点 4(openEuler 24.03,2×Xeon E5-2603 v4/78GiB):
  - OS 图标与 openEuler 24.03 (LTS-SP2) 正常;
  - CPU 12 逻辑/12 物理核心、2 插槽 × 6 核、约 1.5GHz;
  - 内存 78.0 GiB、交换 4.0 GiB;
  - RAID 卡 SAS9364-8i、BBU 缺失、71°C、1024MB、5/2/2;
  - 磁盘 sda/sdb 型号 MR9364-8i、WWN/序列号/分区数正常;
  - HBA:未检测到 FC/SAS HBA(RAID 卡单独展示)。
- Agent pytest **19 passed**;Manager `go test`/`go vet` 通过;全链路 smoke **PASS=41 FAIL=0**。
- 生产节点仅执行查询,未修改 VD/PD/池。

## 兼容

- Agent 升级到 0.4.0;旧 Agent 节点仍可查看现有能力面板,但硬件区提示不可用。
- 新安装包 `storman-0.95.tar.gz`;历史 `zfs-*` 单元/路径与数据库 `zfsmgr` 保持兼容。

# storman 0.94 发布说明(2026-10-18)

## 重点

1. **只读节点口令解锁**:RAID 页新增「输入密码临时解锁」。管理员输入**当前登录账号口令**并选择
   5/15/30/60 分钟后,节点级 ZFS/RAID 写操作临时放开;Manager 重新校验口令 + 限速,Agent 仅保存
   内存态解锁,到期或 Agent 重启自动回锁;支持「立即回锁」。解锁/回锁均审计,口令自动脱敏。
2. **RAID 日志分页**:Agent 解析全部事件后按 `page/page_size` 切片(10~200/页),前端增加总数、
   页码/页大小/跳页控件;不再一次性下发数千条日志。
3. **存储池关联磁盘**:存储池列表展开行新增「关联磁盘」,显示设备名、所属 vdev、状态、设备路径、
   容量及读/写/校验错误,数据复用 `zpool status` 的 vdev 叶节点。
4. **RAID 硬件信息修复**:控制器详情改用 `storcli show all J` 的 `HwCfg/Status/Version` 字段:
   - BBU:`Absent/Msng` 显示「缺失(未配置)」,并读取 `BBU Status`;
   - 温度:`ROC temperature(Degree Celsius)` → `71 °C`;
   - 内存:`On Board Memory Size`(如 1024MB)、FW Cache、内存可纠正/不可纠正 ECC 计数;
   - 健康状态由 `Status.Controller Status` 显示为 `Optimal`。

## 验证

- 13.38 `e2e/smoke.sh`:**PASS=41 FAIL=0**。
- Agent pytest:**17 passed**;Manager `go test ./...` / `go vet` 通过。
- 2.128(生产只读节点):
  - 控制器显示 BBU 缺失、71 °C、1024MB / FW Cache 629MB、ECC 0/0;
  - 事件分页:总数 4376、88 页,第 2 页返回 50 条且序号连续切换;
  - 池 `runstor` 展开可见关联磁盘 `/dev/sdb1` 与 DEGRADED 状态;
  - 错误口令解锁返回 401;正确口令解锁/回锁成功,审计记录口令为 `***`;
  - 解锁窗口内未执行任何 ZFS/RAID 写操作;回锁后写门禁恢复 **403**,**VD=2、PD=5 未变化**。

## 安全与限制

- 临时解锁仅 admin 可发起,需本人口令;解锁为节点级(同时放开 ZFS 与 RAID),TTL 最长 60 分钟;
  Agent 重启/配置重载恢复只读。
- 解锁依赖 Agent 共享令牌下发,生产环境仍应保护 Manager 与 Agent 令牌、限制网络来源。
- RAID 实际创建/删除/属性写入因生产约束仍未在 2.128 执行,需在独立测试节点完成实机验收。
- 文档:部署/开发文档已更新;使用手册 RAID 章节需按本版本补充解锁、分页与硬件信息说明。

# RunStor 存储管理平台 v0.92(安全修订)

日期:2026-10-02 · 基线:v0.91

## 变更
- **凭据全面参数化,代码零硬编码口令/密钥**:
  - 初始管理员口令改为部署期生成,存放 `/etc/zfs-platform/admin.secret`(0600),由 `init_admin_password_file` 引用;未提供时由 Manager 生成并仅记录文件路径;
  - 移除内置 operator/viewer 演示账号(改由管理员在「系统 → 用户」创建);
  - e2e 冒烟凭据改为环境变量 / 0600 文件读取,并在运行时创建 operator/viewer;
  - 安装器(setup_env.sh / install.sh)与文档同步更新,仓库内不再出现任何演示口令。
- 允许管理员修改**自己的口令**(仍禁止修改自身角色与启用状态),支撑密钥轮换流程;
- 部署实测:管理员口令已轮换为 `admin.secret` 值(旧口令 401),全量 e2e 41/41 通过。

## 资产
- `zfs-platform-0.92.tar.gz`:一键部署包(含参数化后的 Manager/Agent/部署脚本/文档)。

# ADR-0001 产品基线落地决策(2026-09-23)

## 背景
依据 `product-dev-baseline` 完成符合性检查(见 docs/产品基线符合性检查.md),本 ADR 记录执行决策与偏离。

## 决策
1. **配置格式迁 TOML**(已完成):Manager/Agent 统一 TOML;敏感项只写引用(`pg.dsn`/`agent.token`/`data.key`,0600);未知键拒绝启动;旧 JSON 自动迁移为 `.bak`。
2. **令牌哈希化**(已完成):DB 仅存 `sha256:<hex>`;存量一次性幂等迁移,旧会话平滑保留。
3. **对外仅 HTTPS**(部分完成):应用收敛回环;入口用 nginx 终止 TLS(8443,自签+HSTS/XCTO/XFO/CSP/gzip)。**Caddy 暂缓**,待确认证书/域名与 nginx:80 存量站点归属后切换。
4. **健康与可观测**(已完成):`/healthz` `/readyz` `/version`(ldflags 注入版本)与最小 `/metrics`;JSON 结构化日志 + `X-Request-Id` 贯穿;panic 边界与访问日志中间件。
5. **优雅退出**(已完成):SIGTERM 停止接新请求 → 20s drain → 后台轮询随 ctx 取消。
6. **迁移版本化**(已完成):`internal/app/migrations/000N_*.sql` + `schema_migrations` 表,只前进;0001 为幂等基线。
7. **i18n 协议**(部分完成):错误信封新增 `key/params`(保留中文 `message`),前端英文模式按 key 渲染;余量文案后续按 key 收口。
8. **登录防爆破**(已完成):用户名+IP 5 次失败锁 5 分钟(429/RATE_LIMITED)。
9. **主题**(已完成):light / dark / **auto(跟随系统)**,localStorage 记忆,首屏防闪烁。

## 偏离与理由
- **Agent 用 Python 而非 Go**:LIO 官方编程接口 rtslib 属 Python 生态;Go 侧自封装 configfs 复杂度与风险不成比例(设计文档 §3.2)。
- **HTTP 压缩**:由入口代理(nginx gzip;Caddy 就绪后 `encode zstd gzip`)承担,应用自身不做压缩中间层。
- **Caddy 暂缓**:环境存在存量 nginx:80 站点,归属与证书待管理员确认;当前以 nginx+TLS 达成"对外仅 HTTPS"目标。

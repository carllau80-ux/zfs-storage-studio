# ZFS 存储管理平台 —— 构建/同步/部署入口(在 192.168.13.38 上开发与测试)
REMOTE := 192.168.13.38
VERSION ?= 0.92
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo nogit)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X zfsmgr/internal/app.Version=$(VERSION) -X zfsmgr/internal/app.Commit=$(COMMIT) -X zfsmgr/internal/app.BuildTime=$(BUILD_TIME)
SSH := ssh -o BatchMode=yes root@$(REMOTE)

.PHONY: sync agent-test manager-build web-build deploy e2e setup status all

# 1) 代码同步到远端
sync:
	rsync -az --delete \
	  --exclude 'manager/bin' --exclude 'node_modules' --exclude 'web/dist' --exclude 'bin/' \
	  --exclude '*.pyc' --exclude '__pycache__' \
	  -e "ssh -o BatchMode=yes" ./ root@$(REMOTE):/opt/zfs-platform/
	@# 仓库不含前端产物;远端 embed 目录不存在时从已构建的 web/dist 自愈
	$(SSH) 'cd /opt/zfs-platform && if [ -f web/dist/index.html ]; then \
	  if [ ! -f manager/internal/app/static/dist/index.html ]; then \
	    rm -rf manager/internal/app/static/dist && cp -r web/dist manager/internal/app/static/dist && echo "[sync] 已恢复 embed 产物"; \
	  fi; \
	fi'
	@echo "[ok] 已同步到 $(REMOTE):/opt/zfs-platform"

# 2) Agent 单元测试(远端)
agent-test: sync
	$(SSH) 'cd /opt/zfs-platform/agent && python3 -m pytest -q 2>&1 | tail -5'

# 3) Manager 构建(goproxy.cn)
manager-build: sync
	$(SSH) 'cd /opt/zfs-platform/manager && export GOPROXY=https://goproxy.cn,direct && go mod tidy && go vet ./... && mkdir -p /opt/zfs-platform/bin && go build -ldflags \"$(LDFLAGS)\" -o /opt/zfs-platform/bin/zfs-manager ./cmd/zfs-manager && echo BUILD-OK'

# 4) 前端构建(远端 node)
web-build: sync
	$(SSH) 'cd /opt/zfs-platform/web && export NODE_OPTIONS=--max-old-space-size=1024 && npm install --no-audit --no-fund 2>&1 | tail -2 && npm run build 2>&1 | tail -6 && rm -rf /opt/zfs-platform/manager/internal/app/static/dist && cp -r dist /opt/zfs-platform/manager/internal/app/static/dist && echo WEB-BUILD-OK'

# 5) 全部构建 + 部署(systemd)
build-all: agent-test manager-build web-build manager-embed

# 重新内嵌前端并重编 Manager(web-build 后执行)
manager-embed:
	$(SSH) 'cd /opt/zfs-platform/manager && export GOPROXY=https://goproxy.cn,direct && go build -o /opt/zfs-platform/bin/zfs-manager ./cmd/zfs-manager && echo REBUILD-OK'

deploy: sync
	$(SSH) 'bash /opt/zfs-platform/deploy/setup_env.sh \
	  && rm -rf /opt/zfs-platform/manager/internal/app/static/dist \
	  && cp -r /opt/zfs-platform/web/dist /opt/zfs-platform/manager/internal/app/static/dist \
	  && cd /opt/zfs-platform/manager && export GOPROXY=https://goproxy.cn,direct GOTOOLCHAIN=local \
	  && go build -o /opt/zfs-platform/bin/zfs-manager ./cmd/zfs-manager \
	  && systemctl daemon-reload && systemctl restart zfs-agent zfs-manager || true; \
	  systemctl enable zfs-agent zfs-manager; sleep 3; \
	  systemctl --no-pager --plain status zfs-agent zfs-manager | head -12'

status:
	$(SSH) 'systemctl status zfs-agent zfs-manager --no-pager -l | head -30; curl -s http://127.0.0.1:8080/api/v1/dashboard -o /dev/null -w "manager http: %{http_code}\n"'

# 6) e2e 冒烟(远端执行,输出 PASS/FAIL)
e2e: deploy
	$(SSH) 'bash /opt/zfs-platform/e2e/smoke.sh'

logs:
	$(SSH) 'journalctl -u zfs-manager -n 40 --no-pager; echo ===; journalctl -u zfs-agent -n 40 --no-pager'

# ---------- 基线工程门禁 ----------
check-cgo:
	$(SSH) 'cd /opt/zfs-platform/manager && CGO_ENABLED=0 GOFLAGS=-mod=mod go build ./... && echo CGO0-BUILD-OK'

vet-all:
	$(SSH) 'cd /opt/zfs-platform/manager && go vet ./... && echo VET-OK'

staticcheck:
	$(SSH) 'command -v staticcheck >/dev/null || GOBIN=/usr/local/bin go install honnef.co/go/tools/cmd/staticcheck@2025.1.1; cd /opt/zfs-platform/manager && staticcheck ./... && echo STATICCHECK-OK'

vulncheck:
	$(SSH) 'command -v govulncheck >/dev/null || GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@latest; cd /opt/zfs-platform/manager && govulncheck -mode=source ./... 2>&1 | tail -5'

agent-race:
	$(SSH) 'cd /opt/zfs-platform/agent && python3 -m pytest -q'

ci: check-cgo vet-all agent-test e2e

# MoeFrp（Go 重写版）

萌！FRP 高级客户端的重写版：客户端是单个 exe，frp 以库的形式内嵌；服务端是一个 Go 二进制加 SQLite。

完整设计见 `PLAN.md`，每个阶段的实现说明见 `docs/phase*.md`。

## 目录

| 目录 | 内容 |
|---|---|
| `shared/` | 两端共用的数据模型、API 结构体、错误码、校验 |
| `server/` | moefrp-server：账号、云端配置同步、分享与订阅、版本信息、旧库导入、网页管理后台（`/admin/`） |
| `client/` | Wails v3 + Vue 3 客户端；`internal/engine` 内嵌 frp v0.71.1 |
| `spike/` | Phase 0 技术验证，保留作参考 |
| `scripts/release.ps1` | 发布构建 |
| `docs/` | 各阶段说明、服务端运维、发布清单、上线切换手册 |

客户端直接依赖 Go 模块 `github.com/fatedier/frp v0.71.1`，克隆后不需要另外下载 frp 源码。要调试 frp 内部时，可以把 frp 克隆到仓库旁边，在 `client/go.mod` 里临时加一行 `replace github.com/fatedier/frp => ../../frp`。`spike/` 是 Phase 0 的验证代码，依赖旁边的 `../frp`，只作参考。

## 状态（2026-10-23）

| 阶段 | 状态 | 说明 |
|---|---|---|
| Phase 0 技术验证 | 完成 | `spike/REPORT.md` |
| Phase 1 shared + 服务端核心 | 完成 | `docs/phase1.md` |
| Phase 2 客户端引擎、本地配置 | 完成 | `docs/phase2.md` |
| Phase 3 账号与云同步 | 完成 | `docs/phase3.md` |
| Phase 4 分享与订阅 | 完成 | `docs/phase4.md` |
| Phase 5 体验完善 | 完成 | `docs/phase5.md` |
| Phase 6 迁移与发布 | 代码和文档完成，**上线需要人工** | `docs/release.md`、`docs/cutover.md` |

需要你来做的事汇总在 `docs/todo-for-owner.md`。

## 服务端 Docker 部署

服务端镜像 `ghcr.io/xmzo/moefrp-server` 同时支持 amd64 和 arm64，由 GitHub Actions 在原生 x86 和 ARM 机器上构建（不用 QEMU）。部署只需要根目录的 `docker-compose.yml` 一个文件，它包含服务端和自动申请 HTTPS 证书的 Caddy：

```bash
curl -O https://raw.githubusercontent.com/XMZO/AnotherMoeFrpClient/main/docker-compose.yml
echo "DOMAIN=frplus.example.com" > .env
docker compose up -d
docker compose logs moefrp | grep 初始化码    # 打开 https://<域名>/admin/ 用它创建管理员
```

邀请码、用户、审计日志和备份都在网页管理后台 `/admin/` 里操作。详见 `docs/docker.md`。

## 常用命令

```powershell
# 测试（分别在 shared、server、client 目录下）
go vet ./...; go test ./...
cd client\frontend; pnpm exec vue-tsc --noEmit; pnpm test

# 构建
cd client; wails3 build PACKAGE_MANAGER=pnpm                                         # bin\MoeFrp.exe
cd client; wails3 build PACKAGE_MANAGER=pnpm EXTRA_TAGS=e2e APP_NAME=MoeFrp-e2e      # 端到端测试版
pwsh .\scripts\release.ps1 -Version 3.0.0                                           # 发布产物到 dist\3.0.0\

# 本机跑服务端
cd server; go run ./cmd/moefrp-server serve --auto-migrate     # 默认 127.0.0.1:8080，见 docs/server-ops.md
```

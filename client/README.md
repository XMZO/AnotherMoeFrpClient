# MoeFrp 客户端（Wails v3 + Vue 3，内嵌 frp）

说明文档：`../docs/phase2.md`（引擎、本地配置）、`../docs/phase3.md`（账号与云同步）。计划：`../PLAN.md` 第 6 节。

```powershell
# 正式构建（产物 bin\MoeFrp.exe，单文件；不认任何测试用的环境变量）
wails3 build PACKAGE_MANAGER=pnpm
wails3 build PACKAGE_MANAGER=pnpm VERSION=3.0.0   # 指定版本号

# 端到端测试构建（产物 bin\MoeFrp-e2e.exe）：只有它认 MOEFRP_HOME / MOEFRP_CDP_PORT / MOEFRP_DIAG /
# MOEFRP_WEBVIEW_BROWSER_PATH / MOEFRP_API_BASE，见 internal/testhooks
wails3 build PACKAGE_MANAGER=pnpm EXTRA_TAGS=e2e APP_NAME=MoeFrp-e2e

# 测试
go vet ./... ; go test ./...          # 需要先构建一次（main 包 embed 了 frontend/dist）
go vet -tags e2e ./...
cd frontend ; pnpm test               # 错误码映射、日志合并、邀请码校验

# 界面端到端（都用 MoeFrp-e2e.exe，CDP 驱动）
.\scripts\e2e\run-e2e.ps1             # Phase 2：本地配置、引擎、外壳
node .\scripts\e2e\run-phase3.mjs     # Phase 3：本机起 moefrp-server，两个实例模拟两台机器
.\scripts\e2e\bench-startup.ps1
.\scripts\e2e\test-no-webview2.ps1
```

目录：

- `main.go`：外壳（托盘、单实例、关闭到托盘、退出时停隧道、崩溃日志），`webview2check.go`：WebView2 预检
- `internal/engine`：内嵌 frp 的引擎（映射、状态机、热更新、日志 sink、TCP 探测）和集成测试
- `internal/store`：`settings.json`、DPAPI 加密的 `cache.bin`、各目录位置
- `internal/apiclient`：访问 moefrp-server 的 HTTP 客户端（错误码、系统代理）
- `internal/credstore`：Windows 凭据管理器里的会话令牌（`MoeFrp/session`）
- `internal/cloudsync`：账号会话和云同步（拉取、推送、冲突、离线）
- `internal/services`：绑定给前端的 Go 服务
- `internal/apperr`：客户端本地错误码
- `internal/testhooks`：只在 `-tags e2e` 构建里生效的测试钩子
- `cmd/localfrps`：本机 frps + echo，给界面测试当对端
- `scripts/e2e/credtool`：端到端测试检查、清理测试凭据的小工具
- `frontend/`：Vue 3 + TS + Pinia，不依赖组件库：通用控件在 `frontend/src/ui/`（弹窗、抽屉、菜单、开关、提示等），配色和主题变量在 `frontend/src/style.css`；中文错误提示集中在 `frontend/src/errors.ts`

# Phase 0 技术验证报告

> 日期 2026-10-18。范围见 `PLAN.md` 第 10 节 Phase 0。所有代码、脚本、产物都在 `gogogo\spike\` 下；`frp\`、`client\`、`server\`、`PLAN.md` 均未改动。

## 结论

**继续用 Wails v3（锁定 `v3.0.0-beta.31`），不改用 Wails v2.17 + energye/systray。**

- 没有发现阻塞性问题。托盘、托盘菜单、单实例激活、关闭到托盘、单文件 exe、frp 进程内嵌、热更新、优雅停止都在真实 exe 上跑通了。
- 发现 1 个 Wails v3 的 bug 和若干需要绕开的点（见"遇到的坑"），都有低成本的绕法，已经在 spike 里验证：
  - 缺少 WebView2 时，进程**静默退出**（退出码 1），没有任何提示；而且 `Options.ErrorHandler` 收不到这个错误。
  - 绕法：在 `app.Run()` 之前自己查注册表，缺失时弹框提示。
- v2.17 能解决的只有"缺 WebView2 时有内置提示"这一点。为此要换掉托盘和单实例的实现，不值得。

## 环境与版本

| 项目 | 版本 |
|---|---|
| 系统 | Windows 11 IoT 企业版 LTSC 2024（Build 26200），AMD 7950X3D，64GB |
| WebView2 Runtime | 155.0.4283.62（Evergreen，已安装） |
| Go | 1.26.1（构建产物 `CGO_ENABLED=0`） |
| **wails3 CLI / Go 模块** | **v3.0.0-beta.31**（`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.31`，Go 代理上的最新 tag，发布于 2026-10-18 03:34 UTC） |
| `@wailsio/runtime`（npm） | 3.0.0-beta.28（原因见坑 2） |
| 前端 | Vue 3.5.43、Vite 8.3.4、TypeScript 5.9.3、vue-tsc 3.3.13、pnpm 10.30.3、Node 24.14.0 |
| frp | 本地 `E:\frp_end\frp`，dev 分支 `26742060`，`version.Full()` = 0.71.1，通过 `go.work` 引用 |
| golib | v0.8.2（与 frp go.mod 一致） |
| golang.design/x/clipboard | v0.11.0 |

## 逐项结果

| # | 验证项 | 结果 | 关键数据 |
|---|---|---|---|
| 1a | Wails v3 vue 模板打出单个 exe | **通过** | `app-empty.exe` 9,848,320 字节（9.4 MiB）；PE 导入表只有 `kernel32.dll`，运行时不需要 WebView2Loader.dll |
| 1b | 托盘图标 + 菜单 | **通过** | 向 Explorer 查询 `Shell_NotifyIconGetRect` 返回图标矩形；左键显示窗口；右键菜单 4 项，菜单项"显示主界面""退出"点击生效 |
| 1c | 单实例（二次启动激活已有窗口） | **通过** | 第二个进程 62–71ms 后退出（退出码 0）；已有窗口从隐藏变为可见，并成为前台窗口 |
| 1d | 关闭窗口最小化到托盘 | **通过** | `WM_CLOSE` 被 `WindowClosing` 钩子拦截，窗口隐藏，进程存活 |
| 2a | 进程内 frps（`server.NewService` + `Run`） | **通过** | 127.0.0.1 随机端口，`Close()` 后监听端口可以被重新绑定 |
| 2b | 6.3.2 流程启动 + tcp 代理真实转发 | **通过** | 远程端口 → frps → frpc → 本地 echo，往返数据一致 |
| 2c | `WithOutput` 截获日志，识别 login success | **通过** | 启动后约 20ms 识别到 `login to server success, get run id [...]`，并解析出 runID |
| 2d | `GetProxyStatus` 读到 running | **通过** | `phase="running" remoteAddr=":<port>"` |
| 2e | `UpdateConfigSource` 增删代理不重连 | **通过** | runID 不变；客户端的 login / try to connect 日志行数不变；frps 侧的 `client login info` 行数不变；被删代理的远程端口被释放 |
| 2f | `GracefulClose` 后 Run 返回、端口释放 | **通过** | `GracefulClose(500ms)` 后 500ms Run 返回 nil；frps 上的远程端口被释放；用新 Service 重启也正常 |
| 2g | token 错误时 Run 返回错误 | **通过** | 5ms 返回 `login to the server failed: token in login doesn't match token from configuration...`，引擎状态为 failed |
| 3a | 引擎接入 Wails：按钮启停、状态、日志 | **通过** | 用 CDP 真实点击按钮：点击后 54ms 状态变为 running，代理 375ms 变为 running；echo 经 frps 转发成功；日志 4 行，全部是纯文本节点；点"停止"后 550ms 回到 idle，远程端口被释放 |
| 3b | 打出单个 exe，记录体积 | **通过** | `app-engine.exe` 19,047,936 字节（18.2 MiB）。frp client 带来约 9.2 MB 增量。PLAN 6.11 预估 20–30MB |
| 3c | 冷启动时间 | **通过**（有保留，见下） | 从进程创建到前端 `onMounted` 回调进 Go：构建后第一次运行 610ms；删除 WebView2 数据目录后首次运行 485ms；热启动 429–442ms；`main()` 在进程创建后 31–33ms 进入 |
| 3d | 隧道运行中从托盘退出 | **通过** | `OnShutdown` 里 `GracefulClose(500ms)` 用时 500ms，进程退出，frps 上的远程端口被释放 |
| 4 | Defender 扫描最终 exe | **未能验证** | `MpCmdRun -Scan -ScanType 3 -File` 失败，`hr=0x800106ba`。原因是本机 `WinDefend` 服务处于停止状态（启动类型 Manual），实际在跑的是 ESET（ekrn）和火绒（HipsDaemon）。启动 Defender 属于修改系统安全设置，没有做。补充一条弱证据：两个 exe 在 ESET 和火绒实时防护下多次生成和运行，都没有被拦截或隔离 |
| 5 | clipboard 在 `CGO_ENABLED=0` 下编译 | **通过** | 编译成功（3.4MB），`clipboard.Init()` 返回 nil；Windows 下依赖图里没有 cgo 包。为了不覆盖剪贴板，没有实际写入图片（加 `-write-image` 参数可以测） |
| 6 | 缺少 WebView2 时的处理 | **已查清**（源码 + 模拟实测；未在干净虚拟机上实测） | Wails v3 静默 `os.Exit(1)`，没有提示，`ErrorHandler` 收不到；只有 NSIS 安装包会装 bootstrapper。自己写的预检（注册表）已验证有效。详见下文 |

## 详细说明

### 1. Wails v3 空应用（`spike/app-empty`）

- 创建命令：`wails3 init -n app-empty -t vue -mod spike/app-empty`。注意模板名是 `vue`（Vue + TS），**没有 `vue-ts`**，见坑 1。
- 代码：`app-empty/main.go`，约 150 行。
  - 托盘：`app.SystemTray.New()` + `SetMenu`。
  - 单实例：`SingleInstanceOptions{UniqueID, OnSecondInstanceLaunch}`，回调里依次调用 `Show`、`Restore`、`Focus`。
  - 关闭到托盘：`RegisterHook(events.Common.WindowClosing)` 里 `Hide()` + `e.Cancel()`，并设置 `Windows.DisableQuitOnLastWindowClosed`。
- 验证方式（全部自动化，见 `scripts/`）：
  - 关闭到托盘、单实例：用 Win32 API 发 `WM_CLOSE`、二次启动 exe，再读窗口可见性、前台窗口，以及应用写出的 `spike-diag.log`。
  - 托盘：
    - 图标是否已出现在通知区：`Shell_NotifyIconGetRect`，结果来自 Explorer。
    - 点击：向托盘窗口发送与 Explorer 相同的回调消息（`WM_USER+1`，lParam 为 `WM_LBUTTONUP` 或 `WM_CONTEXTMENU`）。
    - 菜单：在真实弹出的 `#32768` 菜单窗口上读出菜单项，再用 ↓/↑ + 回车选择。
  - 本来想用桌面截图做一次人工式确认，但任务栏（explorer）的访问授权被拒绝，所以全部改为程序化验证。
- 构建：`wails3 build PACKAGE_MANAGER=pnpm`。
  - 首次约 40 秒（含 `pnpm install`），之后约 6 秒。
  - Taskfile 默认参数：`-tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui"`，`CGO_ENABLED=0`。
- 未改动的模板基线（含模板自带的约 700KB 背景图和字体）为 10,557,440 字节；去掉这些资源后为 9,848,320 字节。

### 2. 最小引擎（`spike/engine`）

- `engine.go`：严格按 PLAN 6.3.2 实现：`buildCommon` → `ConfigSource.ReplaceAll` → `NewAggregator` → `Load` / `FilterClientConfigurers` / `Complete*` → `ValidateAllClientConfig` → `client.NewService` → `go Run(ctx)`。
  - 状态机按 6.3.4 从日志分类得到。
  - `Update` 先校验，通过后才调用 `UpdateConfigSource`。
  - `Stop` = `GracefulClose(grace)` + 等待 Run 返回。
- `logsink.go`：`frplog.Logger = frplog.Logger.WithOptions(golog.WithOutput(sink), golog.WithLevel(...))`。环形缓冲 5000 行，支持订阅。
- `frpstest/`：用 `server.NewService(*v1.ServerConfig)` + `Run(ctx)` 起进程内 frps，另外提供一个 echo 服务。
- `cmd/localfrps`：独立进程的 frps + echo，给 GUI 演示用。这样 `app-engine.exe` 里不会编进 frp 的 server 包，体积数据有代表性。
- `engine_test.go`：8 个测试，全部通过（`go test ./...`，约 20 秒），输出保存在 `engine-test-report.txt`。除了任务要求的各项，还额外测了：
  - 连不上服务器。
  - 正在拨号时 Stop。
  - 远程端口冲突时的 `start error` 原因：`Err="port already used"`。
  - 代理重名。
  - GracefulClose 期间 `GetProxyStatus` 是否阻塞。
  - 结果见坑 5–9。
- 依赖方式：`spike/go.work` 中 `use ../../frp`；`engine/go.mod` 写 `require github.com/fatedier/frp v0.71.1`。`go version -m app-engine.exe` 显示 frp 为 `(devel)`，确认编进去的是本地源码。

### 3. 引擎接入 Wails（`spike/app-engine`）

- 结构：`main.go`（外壳 + 托盘 + 单实例 + 关闭到托盘 + 退出时停引擎）、`tunnel.go`（`TunnelService`：`Start`、`Stop`、`View`、`Logs`）、`webview2check.go`（WebView2 预检）、`frontend/src/App.vue`。
- 事件：
  - `tunnel:state`：状态变化时推送；另外每秒轮询一次代理状态，有变化才推。
  - `tunnel:logs`：每 200ms 批量推送一次。
- 前端：日志用 `{{ line }}` 插值渲染，没有 `v-html`；测试确认日志行里没有任何子元素。
- 托盘菜单的"状态：xxx""启动/停止隧道"两项会跟随引擎状态更新，`SetLabel` 生效。
- 端到端测试：`scripts/test-engine-gui.ps1`。
  - 先起 `localfrps.exe`，再用 `SPIKE_CDP_PORT` 打开 WebView2 远程调试端口，由 `drive-engine-gui.mjs` 通过 CDP 真实点击按钮。
  - 截图：`app-engine-running.png`、`app-engine-stopped.png`。
- 启动时间：`scripts/bench-startup.ps1`，每个应用测 6 次。

| 应用 | 首次（删 WebView2 数据目录） | 热启动 | 主进程工作集 |
|---|---|---|---|
| app-empty | 423ms | 401–448ms | 33MB |
| app-engine | 485ms | 429–442ms | 36MB |

- 构建后第一次运行（最接近真正的冷启动，可能包含安全软件对新文件的扫描）：app-engine 610ms，app-empty 437ms。没有重启机器，所以测不到"开机后文件缓存全冷"的情况。
- 运行时总内存：主进程加 6 个 `msedgewebview2.exe`，合计工作集约 366MB，私有内存约 247MB。

### 4. Defender

- 未能验证。原因见上表：`WinDefend` 服务已停止。
- 复现命令（需要先在"Windows 安全中心"里恢复 Defender，由你决定）：
  ```
  "C:\Program Files\Windows Defender\MpCmdRun.exe" -Scan -ScanType 3 -File E:\frp_end\gogogo\spike\app-engine\bin\app-engine.exe -DisableRemediation
  ```
- 扫描对象的哈希（之后有人补扫时可以对照）：
  - `app-engine.exe`：SHA256 `C5E5D20156EA6886E5E51D320F3D431B3D00F7C79FBA3B249811CC9C690CAB01`
  - `app-empty.exe`：SHA256 `B7BEBE9A1A2860318B94D0CC502210944390DDE06236ADE8807C3E979AE5484A`
- 本机装有 ESET 的命令行扫描器 `C:\Program Files\ESET\ESET Security\ecls.exe`。考虑到 ESET 的 LiveGrid 可能按现有设置提交样本，和"不上传"的约束冲突，没有主动运行。

### 5. golang.design/x/clipboard

- `CGO_ENABLED=0 GOOS=windows go build` 通过；Windows 实现（`clipboard_windows.go`）是纯 syscall，不需要 cgo。返回 `errNoCgo` 的 `clipboard_nocgo.go` 只在 Windows、macOS、Linux、BSD、js 之外的平台且没开 cgo 时才会编译进来。
- v0.11.0 的 API 和旧文档不一样：`Read(ctx, fmt) ([]byte, error)`、`Write(ctx, fmt, buf) (<-chan struct{}, error)`。
- Wails v3 自带的 `app.Clipboard` 只支持文本，图片仍然需要这个库。

### 6. 缺少 WebView2 时的表现

**源码结论**（beta.31）

1. 版本检查 `webviewloader.GetAvailableCoreWebView2BrowserVersionString` 在找不到运行时时返回 `("", nil)`，不报错。
2. 随后 `Chromium.Embed` 调用 `CreateCoreWebView2EnvironmentWithOptions`，失败（`no webview2 found`）。
3. 失败后走 `errorCallback`：默认处理器把错误和堆栈 `log.Printf` 到 stderr，然后 `os.Exit(1)`。
   - GUI 子系统的 exe 没有控制台，用户什么也看不到。
   - 生产构建的默认 logger 也是 `io.Discard`。
4. **bug**：即使设置了 `Options.ErrorHandler` 也收不到这个错误。
   - `EmbedWithError` 出错时会先执行 `defer e.Close()`，`Close` → `ShuttingDown()` 把 `globalErrorCallback` 重置成了默认处理器。
   - 然后 `Embed` 才调用 `errorCallback`。
5. 只有 `wails3 package`（NSIS 安装包）会检查注册表，并静默安装随包附带的 `MicrosoftEdgeWebview2Setup.exe`。单文件分发拿不到这层保护。

**模拟实测**

- 方法：把 `Windows.WebviewBrowserPath` 指向一个空目录（环境变量 `SPIKE_WEBVIEW_BROWSER_PATH`）。这和"系统没装 Evergreen Runtime"走的是同一条 Embed 失败路径。
- 结果：
  - app-empty（Wails 默认）：116ms 后退出，退出码 1，没有窗口也没有提示框。
  - app-engine 只设置 `ErrorHandler`（未加预检）：同样静默退出，`ErrorHandler` 没有被调用。stderr 重定向后能看到 `[WebView2 Error] error calling Webview2Loader: no webview2 found` 和堆栈，印证了上面的 bug。
  - app-engine 加预检后（`webview2check.go`，在 `app.Run` 之前执行）：
    - 正常机器上读到 `pv=155.0.4283.62`，正常启动。
    - 模拟缺失时弹出错误提示框（含下载链接），关闭后以退出码 1 退出。
- 预检逻辑：
  - 查 `HKLM\SOFTWARE\WOW6432Node\…`、`HKLM\SOFTWARE\…`、`HKCU\Software\…` 下的 `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}\pv`，与 Wails NSIS 模板的判断一致。
  - 指定了固定版本目录时，改为检查 `EBWebView\x64\EmbeddedBrowserWebView.dll` 是否存在。

**未做**：干净 Win10 虚拟机实测，本机没有虚拟机。另外，"注册表里有 pv 但运行时已损坏"这种情况，预检会放行，之后仍会静默退出。建议发布前在虚拟机上补测，并把"缺 WebView2 → 提示框"写进发布清单。

## 遇到的坑

1. **`wails3 init -t vue-ts` 会失败**：报 `invalid remote git URL`，因为 `-t` 的值不是内置模板名时会被当作远程地址。Vue + TS 模板的名字就是 `vue`，可以用 `wails3 init -l` 查看模板列表。
2. **npm 运行时版本跟不上 Go 端**：
   - 模板的 `package.json` 写的是 `"@wailsio/runtime": "latest"`，pnpm 解析成了 beta.28。
   - 想钉到 beta.31 时，被本机 pnpm 的 `minimumReleaseAge` 策略拒绝：`ERR_PNPM_NO_MATURE_MATCHING_VERSION`，beta.31 才发布 7 小时，beta.29、30 也还在等待期内。我没有绕过这个供应链策略，最后钉在 beta.28。
   - beta.28 的 JS 运行时配 beta.31 的 Go 端，`Call` 和 `Events` 都正常。
   - 正式项目要把两端钉成同一版本，等版本过了等待期再升级。
3. **`wails3 build` 一定会先执行 `go mod tidy`，而 tidy 不读 `go.work`**：
   - 工作区里的本地模块（spike 的 `spike/engine`；正式项目里是 `moefrp/shared`）必须在 `client/go.mod` 里写 `replace … => ../shared`，否则 tidy 会去网上拉这个模块并失败。
   - frp 这边：tidy 会去 Go 代理拉 `v0.71.1` 这个 tag 来补全 go.sum，而实际构建通过 go.work 用的是本地源码。只要 tag 存在就没问题，但这意味着两边看到的 frp 版本可能不一致。
4. **WebView2 用户数据目录默认是 `%APPDATA%\<exe 文件名>`**：
   - PLAN 6.11 计划的产物名是 `MoeFrp-<版本>-windows-amd64.exe`，每发一个版本就会新建一个 WebView2 配置目录，前端的 localStorage 等数据会丢失，旧目录也会一直堆在那里。
   - 应设置 `Windows.WebviewUserDataPath = %LOCALAPPDATA%\MoeFrp\WebView2`，app-engine 已经这样做。
   - 这也意味着"运行时不释放文件"做不到绝对：WebView2 一定会写这个目录。
5. **代理重名不会报错**：`ConfigSource.ReplaceAll` 以代理名作为 map 的 key，重名时后一个直接覆盖前一个，`ValidateAllClientConfig` 也不检查重名。重名检查只存在于从文件加载配置的 `LoadClientConfig` 里。
   - PLAN 6.3.2 注释里举的例子"代理名重复"实际上不会出现。重名必须由 `shared/validate` 和引擎自己检查。
6. **拨号还没结束时 Stop，Run 要等拨号结束才返回**：
   - 连接被拒绝时约 2.5–2.7 秒（Windows 回环地址的 SYN 重试），黑洞地址约 4.8 秒。
   - PLAN 6.3.8 的"最多等 2 秒"不够用。
   - 此时 Run 返回的错误是 `login to the server failed: <nil>`，引擎需要识别出这是用户主动停止，不能当成登录失败。
7. **GracefulClose(d) 期间 `GetProxyStatus` 会被阻塞整个 d**：`stop()` 持有 `ctlMu`，一直等到优雅关闭结束；实测 `GracefulClose(2s)` 时阻塞了 1.9 秒。界面每秒轮询代理状态时，Stopping 状态下要跳过轮询，不能在 UI 线程上同步调用。
8. **连不上服务器时的错误文本是 `session shutdown`**：`connect to server error: session shutdown`，用户看不懂。界面上的提示应该改用自己的 TCP 探测（`PingNodes`）给出原因，frp 原文只放在日志里。
9. **日志出口是全局的**：
   - 进程内的 frps 和 frpc 写同一个 Logger。
   - 换节点时，如果旧 Service 还在拨号，它的 `connect to server error` 会被误判成新 Service 的状态。
   - 换节点必须等旧 Run 返回后再启动新的 Service，结合坑 6，UI 需要有"停止中"状态。
10. **`go test -race` 在本机无法运行**：链接出的测试 exe 打开时报 `Access is denied`。换 `GOTMPDIR` 也一样，疑似本机安全软件拦截外部链接的二进制。这不影响产物，因为产物用 `CGO_ENABLED=0` 和内部链接。
11. **默认 manifest**：已经是 `asInvoker` 和 PerMonitorV2，但没有 `supportedOS` 兼容性声明，PLAN 6.11 要求的"声明支持 Win10+"需要手动加。
12. **托盘右键菜单依赖较新的修复**：源码注释写明，要处理 `WM_CONTEXTMENU`，否则 Windows 上托盘菜单根本打不开。说明这块在最近的 beta 里才修好，升级 Wails 版本时要回归测试托盘。

## 对 PLAN 的修订建议（未改 PLAN，供决策）

- **4 节**：`client/go.mod` 需要对 `moefrp/shared` 写 `replace`（坑 3）。
- **6.3.2**：注释里的"例如代理名重复"改成其他例子；在 `shared/validate` 和引擎里都做重名检查（坑 5）。
- **6.3.4**：
  - Stopping 状态下不要轮询 `GetProxyStatus`（坑 7）。
  - 连接失败的原因改用自己的 TCP 探测来解释（坑 8）。
  - 识别"拨号中被停止"返回的 `<nil>` 错误（坑 6）。
- **6.3.5 / 6.3.8**：
  - 换节点：等旧 Run 返回后再启动新的 Service（坑 6、9）。
  - 退出：`GracefulClose(500ms)`，然后最多等 2 秒；等不到就直接退出进程，不再继续等。
- **6.11**：
  - 设置 `WebviewUserDataPath`（坑 4）。
  - manifest 加上 `supportedOS`（坑 11）。
  - `@wailsio/runtime` 与 Go 端钉成同一版本（坑 2）。
  - 版本号通过修改 Taskfile 的 `BUILD_FLAGS` 加 `-X main.version=…`。
- **6.11 / 12 节 WebView2**：
  - 在 `app.Run` 前做注册表预检，缺失时弹框提示，并给出下载链接。
  - 不要依赖 `ErrorHandler`（Wails bug）。
  - 可以考虑向 Wails 上游报告这个 bug。
- **6.1 剪贴板**：v0.11.0 的 API 需要 context 参数；不需要 cgo。

## 目录与复现

```
spike/
├─ REPORT.md                 本报告
├─ go.work                   use ../../frp ./app-empty ./app-engine ./clipboard ./engine
├─ app-empty/                Wails v3 空应用（托盘/单实例/关闭到托盘）→ bin/app-empty.exe
├─ app-engine/               Wails v3 + 引擎 → bin/app-engine.exe
├─ engine/                   最小引擎 + 集成测试；frpstest/（进程内 frps）；cmd/localfrps/
├─ clipboard/                CGO_ENABLED=0 编译检查
├─ bin/                      localfrps.exe、clipboard-nocgo.exe
├─ scripts/                  自动化验证脚本（PowerShell + Node/CDP）
├─ engine-test-report.txt    引擎测试的关键输出
├─ app-engine-running.png / app-engine-stopped.png
└─ build-*.log               wails3 build 日志
```

复现（PowerShell）：

```powershell
cd E:\frp_end\gogogo\spike\engine; go test -count=1 -v .          # 引擎集成测试
go build -o ..\bin\localfrps.exe .\cmd\localfrps
cd ..\app-empty;  wails3 build PACKAGE_MANAGER=pnpm
cd ..\app-engine; wails3 build PACKAGE_MANAGER=pnpm
..\scripts\test-shell.ps1 -Exe .\bin\app-engine.exe               # 关闭到托盘 + 单实例
..\scripts\test-tray.ps1  -Exe .\bin\app-engine.exe               # 托盘图标/菜单/退出（需应用在运行）
..\scripts\test-engine-gui.ps1                                    # 界面按钮 → 引擎 → 真实转发
..\scripts\test-quit-running.ps1                                  # 隧道运行中从托盘退出
..\scripts\bench-startup.ps1 -Exe .\bin\app-engine.exe -WebviewDataDir "$env:LOCALAPPDATA\MoeFrpSpike\WebView2"
..\scripts\test-no-webview2.ps1 -Exe .\bin\app-engine.exe         # 模拟缺 WebView2
```

测试过程中在 spike 之外产生的运行时目录已经删除：`%APPDATA%\app-empty.exe`、`%LOCALAPPDATA%\MoeFrpSpike`、`%TEMP%\spike-empty-webview2`。另外，`wails3.exe` 装到了 `%USERPROFILE%\go\bin`，依赖包缓存在 Go 模块缓存和 pnpm store 里。

# Phase 2：客户端引擎 + 不登录也能用

对应 PLAN 第 10 节的 Phase 2。代码在 `gogogo\client\`，没有提交（仓库已 `git init`，等你确认）。`shared\`、`server\`、`spike\`、`PLAN.md` 都没有改。

## 验收结果

| PLAN 的验收标准 | 结果 | 怎么验证的 |
|---|---|---|
| 不连服务器，新建一个本地配置就能启动并成功转发 | **通过** | 在真实的 `bin\MoeFrp.exe` 上用 CDP 操作界面：新建配置 → 添加节点（本机 `localfrps` 进程）→ 添加 tcp 代理 → 点"启动"。点击后 61ms 变为"运行中"，251ms 代理变为 running，经 frps 远程端口 17002 的 echo 往返一致 |
| 运行中修改代理会热更新，连接不断 | **通过** | 先开一条经 17002 的长连接；在界面上添加代理、关掉一个代理的开关、删除一个代理。每次都提示"已热更新（连接不断）"；长连接每次都能继续收发；runId 不变、登录次数仍为 1；关掉的代理的远程端口被释放 |
| 停止后干净退出，退出程序时 frp 也随之停止 | **通过** | 点"停止"：563ms 回到"未运行"，远程端口释放。隧道运行中从托盘菜单"退出"：约 675ms 进程退出，端口释放，诊断日志里有 `engine stopped (quit)`。另外模拟注销（向窗口发 `WM_ENDSESSION`）：501ms 内停完隧道、端口释放 |
| 代理名重复、端口被占用这类错误能在界面上看到明确原因 | **通过** | 重名：抽屉里"名称"下方显示"与已有的名称重复（代理和访客的名称不能相同）"，配置没有被保存。远程端口被别的程序占用：运行状态列显示"启动失败 · 远程端口不可用（可能被节点上的其他程序占用）"；与另一个代理冲突："启动失败 · 远程端口已被占用"；其他代理不受影响。节点端口没人监听：运行栏显示"无法连接节点：连接被拒绝（127.0.0.1:17999）…"（frp 原文是 `session shutdown` / `EOF`），测速结果同样显示原因 |

截图在 `docs\phase2\`：`01-running.png`、`02-duplicate-name.png`、`03-port-errors.png`、`04-node-refused.png`、`05-dark-theme.png`。端到端结果（界面内 30 项检查 + 外壳和存储检查，全部通过）：`docs\phase2\e2e-main.json`、`e2e-relaunch.json`、`e2e-summary.json`。

其余完成标准：

- `client`、`shared`、`server` 三个模块下 `go vet ./...`、`go test ./...` 全部通过；`GOWORK=off` 下 `go vet` 也通过。没有跑 `-race`（本机跑不了，见 spike 坑 10）。
- `wails3 build PACKAGE_MANAGER=pnpm` 产出单个 `bin\MoeFrp.exe`。
- 前端 `vue-tsc` 无错误，`vitest` 6 个用例通过（错误码映射全覆盖、字段错误定位、日志合并去重）。

## frp 版本（PLAN 13 节 #9）

- 原来：`E:\frp_end\frp` 在 `dev` 分支，提交 `26742060`。
- 执行了 `git fetch --tags` 和 `git checkout v0.71.1`，现在是 `v0.71.1` 标签（`d8cf1800`，2026-10-14），处于 detached HEAD，工作区干净，没有改任何文件。
  - `fetch` 顺带把远程跟踪分支 `origin/dev` 更新到了 `93b52799`（只是引用，本地 `dev` 分支仍是 `26742060`）。
  - `dev`（26742060）比 v0.71.1 多了 `useSystemCa` 等 15 个文件的改动，我们没用到。
- 切回原来的分支：`git -C E:\frp_end\frp checkout dev`。
- `go version -m bin\MoeFrp.exe` 显示 `github.com/fatedier/frp v0.71.1 => ..\frp (devel)`，确认编进去的是本地源码；`version.Full()` = 0.71.1。

## 实现了什么

### 引擎 `client\internal\engine`

从 `spike\engine` 起步，按 PLAN 6.3 补全。

- **映射**（`mapping.go`，6.3.3）：8 种代理（tcp udp http https tcpmux stcp xtcp sudp）和 3 种访客（stcp xtcp sudp）。`Prepare` 依次做：`shared/validate` 全量校验（含代理名与访客名合并查重，未启用的也查）→ 找到选中的节点组装 `ClientCommonConfig` → 和 frpc 一样走 ConfigSource → Aggregator → Filter/Complete → `ValidateAllClientConfig`。tcpmux 固定 `httpconnect`；节点 `tls` 为 nil 时跟随 frp 默认（开启）；不开 frpc 管理端口。
- **状态机**（6.3.4）：idle / connecting / running / reconnecting / failed / stopping。处理了 REPORT 的坑 5–9：
  - 坑 5 重名：启动和热更新前都校验，不通过就不提交。
  - 坑 6 拨号中被停止：Run 返回的 `login to the server failed: <nil>` 按主动停止处理，回到 idle，不报失败。
  - 坑 7 Stopping 期间：轮询在独立 goroutine 里，Stopping 时跳过；对外的 `Status()`/`ProxyStatuses()` 只读缓存，测试确认 `GracefulClose(2s)` 期间调用都在 50ms 内返回。
  - 坑 8 连接失败：首次登录失败时用和测速同一套 TCP 探测给出 `node_refused / node_timeout / node_dns / node_unreachable`；能连上但登录失败给 `node_handshake_failed`；token 错误给 `node_token_invalid`。登录成功后断线（reconnecting）也会异步探测给出原因。frp 原文只放日志和悬停提示。
  - 坑 9 换节点：先进入 stopping（带 `pending` 标记，按钮显示"切换中…"），等旧 Run 返回后再新建 Service。
- **日志归属**：每个 Service 的 ctx 里放一个带 `[run-N]` 前缀的 `xlog.Logger`，frp 会沿用它，于是这个 Service 打出的每一行都带前缀。状态分类只认当前 Service 的前缀，所以全局日志出口上的旧 Service、其他 Service（测试里两个引擎同时运行）、进程内 frps 的日志都不会被误判。编号是进程级的。
- **热更新和重启**（6.3.5）：`Update(profile)` 只改代理/访客时调用 `UpdateConfigSource`；节点的连接参数（地址、端口、token、user、协议、TLS、proxyURL）变了就重启；只改备注不重启。校验不通过不提交，运行中的隧道保持原样。
- **日志 sink**（6.3.6）：环形缓冲 5000 行，每行带递增 `seq` 和级别；客户端自己的说明也写进同一个 sink（`[moefrp]` 前缀）。
- **退出**（6.3.8）：`Shutdown(500ms, 2s)`，拨号中等不到就直接返回（测试确认 2.0 秒返回）。
- **集成测试**（第 11 节）：20 个用例，约 34 秒，进程内起 frps。覆盖状态机转换、tcp 真实转发、热更新不重连（长连接不断、runId 不变、frps 侧没有新登录）、校验失败不提交、停止后端口释放、重启新建 Service、token 错误、连接被拒绝、域名解析失败、拨号中停止（拒绝和黑洞两种）、换节点等旧 Run、节点参数变化自动重启、重名拦截、远程端口冲突/被占用、同名代理已在 frps 上（`proxy_name_taken`，同时验证两个引擎的日志不串）、Stopping 期间接口不阻塞、stcp 访客访问（另一个引擎做访问端）、断线重连、退出、测速；映射测试覆盖每种类型单独和组合通过 `ValidateAllClientConfig`。

### 本地存储 `client\internal\store`（6.5）

- `%APPDATA%\MoeFrp\settings.json`：明文，`schemaVersion`、`installId`（首次运行生成）、主题、关闭行为、日志级别、上次启动的配置、每个配置上次选的节点、`autoConnect`（字段先留着）。损坏时改名为 `.bad-<时间>` 并用默认值；缺字段或非法值补默认。
- `%APPDATA%\MoeFrp\cache.bin`：`MFC1` 文件头 + DPAPI（当前用户，带固定 entropy）密文，整体重写、先写临时文件再改名。解不开（换了用户/机器）或损坏时改名备份，从空缓存开始，界面右上角提示一次。
  - 为 Phase 3 的 revision 同步留好了位置：每份配置有 `origin`（local/cloud）、`profile.revision`（云端版本，0 = 未上传）、`baseRevision`、`dirty`、`deleted`（墓碑：上传过的配置删除时保留，等同步删除；没上传过的直接删）、`localUpdatedAt`；文件级有 `sync{account, stateVersion, lastSyncAt}`。
- `%LOCALAPPDATA%\MoeFrp\WebView2`：设为 `WebviewUserDataPath`（坑 4）。
- `%LOCALAPPDATA%\MoeFrp\crash.log`：`debug.SetCrashOutput`，追加写，每次启动记一行。
- `%LOCALAPPDATA%\MoeFrp\startup.log`：每次启动清空，标准库 `log` 和 stderr 都指向它（PLAN 6.11 提到的"至少能留下线索"）。

### Go 服务层 `client\internal\services`（6.4）

| 服务 | 方法 |
|---|---|
| `ProfileService` | `List`、`Get`、`Create`、`Save`、`Delete`、`Duplicate`、`GenerateSecret` |
| `TunnelService` | `Start`、`StartLast`、`Stop`、`State`、`ProxyStatuses`、`PingNodes`、`PingNode`、`Logs`、`ClearLogs`、`ExportLogs(path)`、`ExportLogsDialog` |
| `SettingsService` | `Get`、`Update` |
| `AppService` | `Version`、`Startup`、`Ready`、`OpenURL`、`Quit` |

事件：`tunnel:state`（`Status`）、`tunnel:proxy`（全部启用代理的状态列表）、`tunnel:logs`（200ms 一批）。用 `application.RegisterEvent` 注册，前端拿到的是带类型的事件。

`Save` 会补 ID、给 stcp/xtcp/sudp 代理补 32 字节随机 secretKey，完整校验后写缓存；这份配置正在运行时顺带按 6.3.5 应用，返回 `applied: none|hot|restart` 和 `applyError`（例如删掉了正在用的节点：已保存，但没应用，隧道保持原样）。

错误统一是 `*apierr.Error`（`{code, message, details}`，和服务端格式一样），前端从 Wails `CallError.cause` 里读 `code`。客户端本地的 code 在 `internal\apperr`。

### 前端 `client\frontend`（Vue 3 + TS + Naive UI + Pinia）

- 左侧导航：隧道 / 设置 / 关于。
- 隧道页：左栏配置列表（新建、重命名、复制、删除，运行中的配置有绿点）；右侧编辑区"代理 / 节点 / 访客"三个标签页，编辑一律用抽屉；底部运行栏（节点下拉框显示测速结果、测速、启动/停止、状态徽标、失败或重连原因）；日志面板可收起。
- 代理表格：开关、名称、类型、本地 → 远程、**运行状态**（运行中 + 远程地址 / 失败原因 / 未启用 / 等待连接；悬停显示 frp 原文）、操作。
- 抽屉里的字段错误按 `details.fields` 定位到具体表单项；定位不到的放在抽屉顶部。
- 日志面板：`NVirtualList` 虚拟列表，固定行高，`{{ }}` 插值纯文本渲染，没有 `v-html`；端到端测试确认日志行里没有任何子元素。跟随最新、清空、导出（另存为对话框）。
- 主题：跟随系统 / 浅色 / 深色，默认跟随系统（`useOsTheme`），端到端测试用 CDP 模拟系统切换深浅色验证。
- 错误码 → 中文提示集中在 `frontend\src\errors.ts`（服务端全部 code + 客户端全部 code + 校验 reason），vitest 检查每个 code 都有提示。

### 外壳 `client\main.go`

- 托盘：显示主界面、状态（运行中时带配置名）、启动上次的配置 / 停止隧道 / 正在停止…、退出。
- 单实例：再次双击激活已有窗口（第二个进程约 53ms 退出）。
- 关闭按钮默认最小化到托盘，设置里可改为退出；最小化到托盘时隧道继续转发（已验证）。
- WebView2 预检：复用 `webview2check.go`，模拟缺失时 126ms 弹出提示框，关闭后退出码 1（`scripts\e2e\test-no-webview2.ps1`）。
- `WM_ENDSESSION`（关机/注销）：通过 `WndProcInterceptor` 同步停隧道再返回。
- manifest 加了 Win10/11 的 `supportedOS`；版本号通过 Taskfile 的 `BUILD_FLAGS` 注入 `-X main.version={{.VERSION}}`，默认 `3.0.0-dev`，`wails3 build PACKAGE_MANAGER=pnpm VERSION=3.0.0` 指定。版本信息资源用旧 `version.txt` 的产品名和版权。
- 窗口底色按设置和系统深浅色选择，避免启动时闪白。

## 与 PLAN 不一致或需要你决定的地方

按影响从大到小。带"需要决定"的是我按自己的判断先做了。

1. **需要决定：测试钩子留在正式构建里**。下面几个环境变量都不设置时没有任何效果，但设置了 `MOEFRP_CDP_PORT` 就会打开 WebView2 远程调试端口，本机任何程序都能通过它操作界面、读到节点 token。能设置这个环境变量的程序本来就能以当前用户身份做任何事，所以我认为风险可以接受；如果不想要，可以在 Phase 6 改成只在带 `-tags e2e` 的构建里生效（端到端测试改用那个构建）。
   - `MOEFRP_HOME`：把数据放到指定目录（`Roaming\`、`Local\` 两个子目录），测试用，也可以当"便携模式"。
   - `MOEFRP_DIAG`：诊断日志文件。
   - `MOEFRP_CDP_PORT`：远程调试端口（同时关闭窗口被遮挡时的渲染节流，否则截图和 CSS 过渡会停在旧状态）。
   - `MOEFRP_WEBVIEW_BROWSER_PATH`：指定 WebView2 固定版本目录（模拟缺失）。
2. **需要决定：`@wailsio/runtime` 用的是 3.0.0-beta.30**，Go 端是 beta.31。beta.31 发布于 10-18，按本机 7 天的 `minimumReleaseAge` 要到 10-25 才能装，没有绕过。beta.30 配 beta.31 的 `Call`、`Events`、带类型事件都正常。到期后把 `frontend\package.json` 改成 `3.0.0-beta.31` 再 `pnpm install`。`frontend\.npmrc` 沿用 spike 的 `minimum-release-age=10080`。
3. **日志行多了 `[run-N]` 前缀**（见"日志归属"）。这是 PLAN 里没有的做法，但比"只靠等旧 Run 返回"更可靠：即使将来有别的 Service 同时打日志，状态也不会串。代价是界面日志里能看到这个前缀。
4. **`tunnel:proxy` 每次推送全部启用代理的状态**，而不是只推变化的那一个（PLAN 6.4 写的是"某个代理"）。代理最多 100 个，整表推送更简单，也不会因为错过一条事件而状态不一致。
5. **运行中再点另一个配置/节点的"启动"= 切换**：先停旧的（stopping + pending），等旧 Run 返回再启动新的。PLAN 没规定这种情况，我没有做成报错。运行中在节点下拉框换节点也走这条路。
6. **失败状态下点"停止"（按钮此时显示"启动"）**：`Stop()` 会清掉错误回到 idle；界面上是直接重新启动。
7. **正在运行的配置不能删除**（`profile_in_use`），要先停。
8. **访客没有单独的运行状态**：frp 的 `StatusExporter` 只有代理状态，访客那一栏显示"监听 地址:端口"或隧道整体状态。
9. **新增的错误码**（都在 `internal\apperr`，前端都有中文）：配置类 `profile_not_found` `profile_in_use` `node_not_found` `node_required` `frp_config_invalid`；隧道类 `tunnel_busy` `tunnel_not_running` `tunnel_start_failed`；连接类 `node_token_invalid` `node_refused` `node_timeout` `node_dns` `node_unreachable` `node_handshake_failed` `node_udp_unprobed`；代理状态 `proxy_port_in_use` `proxy_port_not_allowed` `proxy_port_unavailable` `proxy_name_taken` `proxy_domain_conflict` `proxy_type_unsupported` `proxy_start_error`；本地 `storage_failed` `export_failed` `invalid_argument`。
   - 注意：frps 对"端口被别的程序占用"报的是 `port unavailable`，对"和自己的另一个代理冲突"报的是 `port already used`，所以界面上是两种提示。
10. **kcp / quic 节点无法测速**：走 UDP，TCP 探测没有意义，只检查域名能否解析，显示"kcp / quic 走 UDP，无法测速"。节点配置了 `proxyURL` 时探测的是代理服务器。
11. **secretKey 自动生成用 base64url 编码**（32 字节 → 43 个字符），PLAN 只说了 32 字节随机值。
12. **PLAN 6.4 里本阶段没做的方法**：`ProfileService.ResolveConflict`（Phase 3）、`AppService.CheckUpdate`（Phase 5）。多出来的方法见上表（`Duplicate`、`GenerateSecret`、`StartLast`、`PingNode`、`Logs`、`ClearLogs`、`ExportLogsDialog`、`Startup`、`Ready`）。
13. **exe 名是 `MoeFrp.exe`**（Taskfile 的 `APP_NAME`），PLAN 6.11 的 `MoeFrp-<版本>-windows-amd64.exe` 和 `.sha256` 留到 Phase 6 的发布脚本。
14. **图标还是 Wails 模板的默认图标**（`build\appicon.png`，托盘也是）。需要你提供正式图标，放进去后执行 `wails3 task common:generate:icons`。
15. **`go.work` 加了 `./client`，生成了 `go.work.sum`**。在 gogogo 下 `go build`/`wails3 build` 走工作区模式，依赖版本是三个模块合起来的 MVS 结果；`wails3 build` 里的 `go mod tidy` 不读 `go.work`，靠 `client\go.mod` 的两条 `replace`（PLAN 第 4 节）。
16. **干净检出后要先构建一次才能 `go vet ./...` / `go test ./...`**：`main.go` 用 `go:embed all:frontend/dist`，而 `frontend\dist` 在 `.gitignore` 里。只测内部包可以 `go test ./internal/...`。
17. 前端产物 JS 约 876KB（gzip 249KB），Vite 会给一个 chunk 超过 500KB 的警告，对桌面应用没有影响，没有做拆分。

## 怎么构建和运行

```powershell
cd E:\frp_end\gogogo\client
wails3 build PACKAGE_MANAGER=pnpm            # 首次会 pnpm install；之后增量约 12 秒
.\bin\MoeFrp.exe

# 测试
go vet ./... ; go test ./...                  # 引擎集成测试约 34 秒
cd frontend ; pnpm test ; cd ..

# 界面端到端：先构建，再跑（会临时占用本机 17000–17004 端口）
.\scripts\e2e\run-e2e.ps1                     # 结果在 scripts\e2e\out\（截图、main.json、relaunch.json、summary.json）
.\scripts\e2e\bench-startup.ps1               # 启动耗时
.\scripts\e2e\test-no-webview2.ps1            # 模拟缺少 WebView2
```

想不连任何外部服务器手动试用：`go build -o bin\localfrps.exe .\cmd\localfrps`，运行 `bin\localfrps.exe`（frps 在 127.0.0.1:17000，token `e2e-token`；echo 在 17001），然后在界面里添加节点 `127.0.0.1:17000` 和一个本地端口 17001 的 tcp 代理。

端到端测试的数据放在 `%TEMP%\moefrp-e2e-<时间>`，跑完删除；不会动真实的 `%APPDATA%\MoeFrp`。三轮运行分别验证：

1. 主流程：新建配置 → 节点 → 代理 → 启动 → 转发 → 热更新（加、关、删）→ 重名 → 端口被占用 / 冲突 → 停止 → 再启动 → 运行中从托盘退出。
2. 重启后：配置还在（DPAPI 缓存）→ 节点端口改错 → 启动失败原因、测速原因 → 主题跟随系统 → 改回 → 关闭到托盘（隧道仍转发）→ 单实例激活 → 托盘退出。
3. 托盘"启动上次的配置" → 模拟注销（`WM_ENDSESSION`）→ 隧道停止、端口释放。

最后检查数据目录：`settings.json`、`cache.bin`（`MFC1` 头，明文里找不到节点 token 和备注）、`WebView2\`、`crash.log`、`startup.log` 都在预期位置。

## exe 体积和启动时间

- `bin\MoeFrp.exe`：**20,189,696 字节（19.3 MiB）**，SHA256 `BAF474091AEA0CFB3DA17C1D8D39D1BB401D7205237269170BC1972A598A47D9`。比 spike 的 app-engine（19,047,936）多约 1.1MB，主要是完整界面（Naive UI）。PLAN 6.11 预估约 20MB。
- 启动（`bench-startup.ps1`，6 次，从进程创建到 Vue 挂载完成回调 Go）：

  | 次数 | 情况 | main() 进入 | 前端就绪 | 主进程工作集 |
  |---|---|---|---|---|
  | 1 | 全新数据目录（WebView2 用户数据为空） | 26ms | 441ms | 37.9MB |
  | 2–6 | 热启动 | 26–28ms | 418–440ms | 37.9–39.2MB |

  构建后第一次运行（端到端测试的第一轮，同时打开了远程调试端口）是 532ms～1945ms（最后一次 1877ms），波动来自安全软件对新文件的扫描（有一次 `main()` 在进程创建后 1507ms 才进入）。
- 其他耗时：点"启动"到运行中 61ms，到代理 running 251ms；点"停止"到 idle 563ms（含 `GracefulClose(500ms)`）；托盘退出约 675ms。

## 留给后面的

- `-race`：本机跑不了，放到 CI 或别的机器。
- Defender 扫描、干净 Win10 虚拟机上的 WebView2 缺失实测：仍按 PLAN 并入 Phase 6 发布清单。
- 托盘图标、正式图标（见上面第 14 条）。
- Phase 3：登录、凭据管理器、云同步（缓存结构已留好字段）；Phase 5：开机自启、自动连接（`autoConnect` 字段已有）、网络代理、检查更新。

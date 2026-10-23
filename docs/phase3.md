# Phase 3：账号与云同步

对应 PLAN 第 10 节的 Phase 3。代码没有提交。`PLAN.md`、`spike\` 没有改；`shared\`、`server\` 也没有改——服务端现有接口够用，按 `docs\phase1.md` 的实际行为对接，没有发现需要修的 bug。

## 验收结果

| PLAN 的验收标准 | 结果 | 实测 |
|---|---|---|
| 两台机器登录同一个账号：在 A 上修改后，B 在 60 秒内同步到 | **通过** | A 加节点 → 1.7 秒后推送完成 → B 在推送后 **58.0 秒**由 60 秒轮询拉到（`trigger=timer`，B 没有任何操作），界面列表和编辑区都刷新 |
| 两台机器同时修改同一个配置时会弹出冲突提示 | **通过** | A、B 同时各加一个代理。先推送的一方成功，另一方收到 409，弹出"同步冲突"，差异摘要为"代理「fromA」：只在本机 / 代理「fromB」：只在云端"；选"保留本机"后两边最终一致（另一台在下一次轮询拿到） |
| 开启单设备模式时，在 B 上登录会把 A 挤下线，并且 A 看到明确的提示 | **通过** | B 登录后 A 在下一次轮询（58.5 秒）收到 `session_replaced`，弹窗"你的账号已在其他设备登录，本机已退出登录。本机缓存的配置仍然可以启动，重新登录后会继续同步"；A 的凭据被删除；A 正在运行的隧道不受影响，照常转发 |

"两台机器"用两个 `MoeFrp-e2e.exe` 实例模拟：各自的 `MOEFRP_HOME`（独立的 `%APPDATA%`/`%LOCALAPPDATA%` 等价目录），单实例锁和凭据目标名按数据目录区分，两个进程同时运行。服务端是本机的 `moefrp-server serve --auto-migrate`（临时 SQLite、`127.0.0.1:18080`），邀请码和重置码用 `admin` 命令生成。前两条验收时 `single_session=false`，之后重启服务端改为 `true`（同一个库，会话不丢）。

另外验证的（同一次运行里，全部通过）：

| 项目 | 结果 |
|---|---|
| 注册时前端校验邀请码 | 校验位错误显示"校验位不正确，请检查是否输错"、格式错误显示格式说明，都不发请求；小写的正确邀请码通过（自动转大写）；注册成功后切到登录页 |
| 交互登录后云端为空时自动建默认配置 | 建了"我的云端配置"并上传；第二台设备登录不会再建 |
| 本地配置和云端配置并存、界面区分 | 列表分"云端配置 / 本地配置"两组，编辑区标题旁有标签 |
| 离线 | 停掉服务端后，A 编辑 → 状态"离线"、配置标"待同步"；离线状态下启动隧道并真实转发成功；服务端恢复后 15 秒内自动推送 |
| 正在运行的配置被云端修改 | B 改了 A 正在运行的配置 → A 弹出"云端配置已更新，是否应用（热更新，连接不断）" → 点"应用"热更新，新代理可以转发，没有重连（frps 登录次数仍为 1） |
| 退出登录 | 清掉云端配置缓存、本地配置保留、凭据被删除 |
| 改密码 | 旧密码错误显示"当前密码不正确"且**不下线**（`invalid_credentials` 也是 401）；正确后成功 |
| 重启客户端后自动登录 | 正常退出再启动，不用输密码直接登录并同步成功 |
| 退出登录后不会自动登录 | 退出 → 重启 → 未登录，凭据管理器里没有凭据，本地配置还在 |
| 重置密码 | `admin user reset-code` 生成的重置码在"重置密码"页使用成功，新密码能登录 |
| 不勾"保持登录" | 凭据管理器里不留任何东西 |
| `client_outdated` | 服务端 `min_version` 改成 9.0.0 后，点"立即同步"弹出"客户端版本过低"（带"下载新版"），同步状态"版本过低"；本机缓存的配置照常启动成功 |
| 凭据管理器里没有密码 | 凭据只有一条（目标名、用户名=昵称、内容 47 字节且以 `mfs_` 开头），内容、用户名、备注、属性里都找不到密码 |
| 数据目录里没有密码 | 扫描两个实例的全部数据文件（535 个，含 WebView2 目录），UTF-8 和 UTF-16 都找不到三次用过的密码 |
| 清理 | 凭据管理器里的测试凭据（`MoeFrp/session/e2e-*`）已全部删除，临时数据目录已删除 |

结果文件：`docs\phase3\e2e-result.json`；截图：`docs\phase3\01-register-invite-checksum.png` … `09-outdated.png`（`04-conflict.png` 截在弹窗淡入动画中途，略透明；脚本之后加了等待）。整个场景约 4 分 35 秒，大部分时间在等 60 秒轮询。

调试过程中跑了 5 遍：前几遍失败都是脚本或界面刷新的问题（见下面"过程中修掉的问题"），最后一遍 31 项全过。其中有一遍验收 1 是 20 秒由**窗口获得焦点**触发的拉取（`trigger=focus`，桌面上窗口焦点变化），这也是设计行为（PLAN 6.6），脚本里改为只要求"推送后 60 秒内"，并记录触发方式。

完成标准：

- `shared`、`server`、`client` 三个模块 `go vet ./...`、`go test ./...` 全部通过；`client` 另外 `go vet -tags e2e ./...`、`GOWORK=off go vet ./...` 也通过。没有跑 `-race`（本机跑不了，见 spike 坑 10）。
- `wails3 build PACKAGE_MANAGER=pnpm` 产出正式的 `client\bin\MoeFrp.exe`：20,503,040 字节，SHA256 `CDA4B73E4AE85632C5CF2D8CA9FAFB1993731FA78373B1A25BEBC93D9FFA4D47`，`-tags=production`，exe 里找不到任何 `MOEFRP_` 字符串。
- 前端 `vue-tsc` 无错误，`vitest` 11 个用例通过（新增邀请码校验位 / 昵称 / 密码长度，用和 `shared/validate` 相同的测试向量）。
- Phase 2 的端到端脚本 `run-e2e.ps1` 改用 `MoeFrp-e2e.exe` 后重跑一遍，全部通过。

## 怎么跑

```powershell
cd E:\frp_end\gogogo\client
wails3 build PACKAGE_MANAGER=pnpm                                     # 正式版 bin\MoeFrp.exe
wails3 build PACKAGE_MANAGER=pnpm EXTRA_TAGS=e2e APP_NAME=MoeFrp-e2e  # 测试版 bin\MoeFrp-e2e.exe
node .\scripts\e2e\run-phase3.mjs     # 约 5 分钟；结果在 scripts\e2e\out-phase3\；--keep 保留临时数据目录
```

脚本会自己编译服务端、`localfrps` 和 `credtool`（都放在临时目录），占用 `127.0.0.1` 的 18080、17000–17005、9361/9362，开始前检查端口空闲、凭据管理器里没有残留的测试凭据；结束时（包括出错时）关掉所有进程、删除测试凭据和临时目录。不连接任何线上服务器。

想手动试：按 `docs\phase1.md` 的"在本地运行"起服务端（`MOEFRP_CLIENT_MIN_VERSION` 设成 `3.0.0-dev` 或留空，否则开发版会被判为过低），然后用测试版启动客户端：

```powershell
$env:MOEFRP_HOME="$env:TEMP\moefrp-try"; $env:MOEFRP_API_BASE="http://127.0.0.1:8080"; .\bin\MoeFrp-e2e.exe
```

正式版的服务端地址固定为 `https://frplus.loli-con.cn`（`-ldflags "-X main.apiBase=..."` 可改），不要用正式版连本机服务端。

## 实现了什么

### Go 端（`client\internal\`）

**`apiclient`**：访问 moefrp-server 的 HTTP 客户端。

- 请求和响应都是 `shared\api` 的结构体；每个请求都带 `X-Client-Version`（`main.version`）和 `X-Install-Id`（`settings.json` 的 `installId`），登录后带 `Authorization: Bearer`。
- 出错一律返回 `*apierr.Error`，原样保留服务端的 `code`。分类函数：`SessionEnded`（`unauthorized`、`session_expired`、`session_replaced`、`account_disabled`，**不含** `invalid_credentials`）、`IsOutdated`（426）、`IsOffline`、`ConflictCurrent`（取 409 的 `details.current`）。
- 连不上服务端（拒绝、超时、DNS、网关返回的非 JSON 502/503/504）统一是客户端错误码 `server_unreachable`，界面据此显示"离线"。
- 网络代理：`跟随系统`（默认）读当前用户"Internet 选项"里的手动代理（`ProxyEnable`/`ProxyServer`/`ProxyOverride`，支持分协议写法和 `<local>`、通配符例外），没开手动代理时退回 `HTTP(S)_PROXY` 环境变量；回环地址永远直连。`不使用代理`直连。设置里切换后立即生效。

**`credstore`**：`danieljoos/wincred` v1.2.3，普通凭据，目标名 `MoeFrp/session`，用户名字段是昵称，内容只有会话令牌，`Persist = LocalMachine`（不随漫游配置文件到别的电脑）。有真实读写凭据管理器的单元测试（随机目标名，测完删除）。

**`cloudsync`**：账号会话和云同步，`Manager` 一个对象管两件事（会话失效时同步要停，同步请求本身又是会话检查，分开反而要互相通知）。

- 每次登录对应一个后台循环，所有网络请求在这个 goroutine 里串行执行；退出登录、换账号时取消正在进行的请求并等循环退出，旧循环的结果不会写进缓存。
- 拉取：登录后立即、之后每 60 秒、窗口获得焦点时（距上次拉取超过 3 秒）各拉一次 `GET /api/v1/state`，带 `If-None-Match`。每次启动（每个会话）的第一次拉取不带，保证合并规则至少完整跑一遍。
- 推送：本地保存云端配置后 1.5 秒内没有新修改就推送。没上传过的 → `POST /profiles`（带本机生成的 ID）；改过的 → `PUT`（带 `baseRevision`）；删除 → `DELETE`。推送期间用户又改了的话，记下新版本号但保持"待推送"，下一轮再推。
- 合并规则（`merge`）：本机没有 → 加入；本机没改、云端更新 → 用云端的（正在运行时询问是否应用）；本机改了、云端没动 → 等推送；两边都改了 → 冲突，但内容一样时直接采用云端版本号；本机删了、云端又改了 → 冲突；云端没了 → 本机没改且没在运行就删掉，否则冲突；从没上传成功的保留等推送。
- 冲突：`PUT` 返回 409 或合并时发现，都记成 `Conflict`（含差异摘要），推送跳过它，直到用户选择。"保留本机"基于云端当前版本重新 `PUT`（云端已删除则用同一个 ID 重新 `POST`）；"使用云端"覆盖本机，正在运行时直接按 6.3.5 应用（热更新或重启），云端已删除时停止隧道并删除本机这份。
- 离线：网络错误时状态为 `offline`，每 15 秒重试一次（不登出），编辑照常写本机缓存，恢复后按上面的规则推送。
- 正在运行的配置被云端修改：缓存里立即换成云端内容，发 `sync:remote-update`（附带"热更新 / 需要重启"的预判，`Engine.PredictUpdate`），用户点"应用"才调用 `Engine.Update`；点"暂不"则隧道继续用旧内容，下次启动用新内容。
- 会话失效（`SessionEnded`）：立即下线、删除凭据、清掉令牌，发 `auth:expired`（附错误码）。**本机缓存的云端配置保留**，离线照样能启动；同一账号重新登录后继续同步，没推上去的修改不会丢。
- `client_outdated`：登录或同步时收到 426 → 记下 `latestVersion`/`downloadUrl`，同步暂停（每小时再试一次），会话保留，本地和缓存的配置照常启动。
- 退出登录：先尽量把待推送的修改推上去（最多 5 秒），再 `POST /auth/logout`，删除凭据，清掉缓存里的全部云端配置和同步游标；本地配置保留。正在运行的是云端配置时停止隧道。
- 换账号：登录的账号和缓存里云端配置的所属账号不同时，先清掉旧账号的云端配置。
- 交互登录后，云端和本机都没有云端配置时自动建一个空的"我的云端配置"（docs/phase1.md 第 1 条）。自动登录不建，免得用户删光之后每次启动又冒出来。
- 事件：`auth:state`、`auth:expired`、`sync:status`、`sync:changed`、`sync:conflict`、`sync:remote-update`、`sync:error`，都用 `application.RegisterEvent` 注册了类型。
- 单元测试 13 个（`cloudsync_test.go`），对一个按 phase1.md 行为写的假服务端（ETag/304、`baseRevision` 冲突带 `details.current`、单设备挤下线、426、断网）：默认配置、请求头、编辑传播与 ETag、并发编辑冲突与两种解决、内容相同不算冲突、云端删除时的冲突与重新上传、运行中配置被云端修改询问后应用、被挤下线保留缓存删除凭据、改密码旧密码错误不下线、离线编辑恢复后推送、退出登录清云端留本地且不再自动登录、凭据自动登录、版本过低、换账号清缓存。`apiclient` 另有错误分类、ETag、代理规则的测试。

**`store`（改动）**：`cache.bin` 每份配置加了 `edits` 计数；同步游标加了昵称；新增 `PutNew`（指定来源新建）、`All`、`Tx`（锁内修改、有变化才写盘、出错整体回滚）。`settings.json` 加了 `networkProxy`（`system`/`none`，缺省补 `system`）。

**`services`**：

| 服务 | 本阶段新增 / 改动 |
|---|---|
| `AuthService`（新） | `Login(nickname, password, keepLoggedIn)`、`Register`、`Logout`、`LogoutInfo`、`Me`、`Account`、`ChangePassword`、`ResetPassword`、`SyncStatus`、`SyncNow` |
| `ProfileService` | `Create(name, origin)`（`cloud` 需要登录）、`Upload(id)`、`Conflicts`、`ResolveConflict(id, keep)`、`RemoteUpdates`、`ApplyRemote`、`DismissRemote`；`List` 多了 `sync`（local/synced/pending/conflict/error）、`syncError`、`revision`；`Save`/`Delete` 云端配置时触发防抖推送；`Duplicate` 的副本总是本地配置 |
| `SettingsService` | 可以改 `networkProxy` |

`Register`、`ChangePassword`、`ResetPassword` 在 Go 端先按 `shared/validate` 校验一遍（昵称、密码长度、邀请码格式和校验位），再请求服务端。

**`engine`**：加了 `PredictUpdate(profile)`。

**`testhooks`（安全修复，docs/phase2.md 第 1 条）**：`MOEFRP_HOME`、`MOEFRP_DIAG`、`MOEFRP_CDP_PORT`、`MOEFRP_WEBVIEW_BROWSER_PATH` 和新增的 `MOEFRP_API_BASE` 集中到 `internal\testhooks`，只有 `-tags e2e` 的构建（`hooks_e2e.go`）会读；正式构建（`hooks_prod.go`）的 `Get()` 恒为零值，有单元测试保证设置了环境变量也无效。实测正式的 `bin\MoeFrp.exe` 里连这几个环境变量名的字符串都没有（编译器把没用到的常量去掉了），`MoeFrp-e2e.exe` 里有。Phase 2 的三个端到端脚本改用 `MoeFrp-e2e.exe`。

e2e 构建里设置了 `MOEFRP_HOME` 时，单实例锁 ID 变成 `com.moefrp.client.e2e-<目录哈希>`，凭据目标名变成 `MoeFrp/session/e2e-<目录哈希>`，所以两个实例能同时运行、各存各的令牌（模拟两台机器）。正式构建固定为 `com.moefrp.client` 和 `MoeFrp/session`。

**`main.go`**：服务端地址默认 `https://frplus.loli-con.cn`，可用 `-ldflags "-X main.apiBase=..."` 覆盖；创建 `apiclient`、`credstore`、`cloudsync`，注册 `AuthService`；应用启动后在后台 `Restore()`（用凭据自动登录）；窗口 `WindowFocus` 时 `Focus()`；退出时停止同步循环（不删凭据）。

### 前端（`client\frontend\src\`）

- **登录 / 注册 / 重置密码**（`components\AuthModal.vue`）：一个弹窗三个标签页。注册时邀请码边输边检查格式和校验位（`invite.ts`，算法与 `shared/validate` 相同，用同一组测试向量做了 vitest），昵称、密码长度、两次密码一致在提交时检查；不通过不发请求。注册成功切到登录页并填好昵称。登录页有"保持登录"（默认勾选），下方说明只保存令牌不保存密码。
- **账号区**（`components\AccountArea.vue`，左侧导航栏底部）：未登录显示"登录 / 注册"；登录后显示昵称、同步状态指示（已同步 / 同步中 / 离线 / 版本过低 / 同步出错，悬停显示上次同步时间和原因）、待同步数、冲突数；菜单里有"立即同步""修改密码""退出登录"。退出前确认，说明会清掉本机的云端配置缓存，有没推送的修改或正在运行云端配置时会特别提示。
- **修改密码**：旧密码错误显示"当前密码不正确"，不会下线。
- **配置列表**（`ProfileList.vue`）：分"云端配置"和"本地配置"两组，各有图标；云端配置不是"已同步"时显示"待同步 / 冲突 / 同步失败"标记；编辑区标题旁也有"云端配置 / 本地配置"标签。登录后新建时可选存到云端还是只在本机（默认云端）。本地配置的菜单里有"上传到云端"；云端配置的"复制"叫"复制为本地配置"。未登录但缓存里有云端配置时，分组下说明"这些是 xxx 的云端配置缓存，可以照常启动，登录后继续同步"。
- **冲突弹窗**（`ConflictDialog.vue`）：显示配置名、发生了什么（两边都改了 / 云端删了 / 本机删了）、差异摘要（名称、节点、代理、访客逐项列出"只在本机 / 只在云端 / 两边不同（哪些字段）"）、云端版本和修改时间，两个按钮下各写明选了会发生什么；可以"稍后处理"，账号区的"N 个冲突"一直在。
- **提示**（`SyncNotices.vue`）：被挤下线 / 会话失效 / 账号停用时弹窗说明原因（如"你的账号已在其他设备登录，本机已退出登录"），并说明缓存的配置仍可启动，可以直接"重新登录"；版本过低弹窗给出新版本号和"下载新版"；运行中的配置被云端修改时右上角通知，带"应用 / 暂不"；推送失败（例如超出数量上限）用消息条提示。
- **设置**：新增"网络代理：跟随系统 / 不使用代理"。
- `errors.ts` 补了 `server_unreachable`、`bad_response`、`not_logged_in`、`conflict_gone` 的中文和账号相关的字段名；被动下线原因单独一张表 `sessionEndedMessages`。

### 过程中修掉的问题

- 推送成功后列表上的"待同步"标记不消失：推送不改配置内容，不发 `sync:changed`，前端没重新读列表。现在收到 `sync:status` 且状态、待同步数、冲突数有变化时重新读列表。
- 注册页内容比登录页高，`NTabs` 的切换动画按旧高度裁掉了一截，去掉了动画。
- 名字遮蔽：`isolation()` 的命名返回值和常量 `uniqueID` 同名，正式构建会拿到空的单实例 ID。写完自查时发现，已改名。
- 其余几次失败是测试脚本的选择器问题（下拉菜单要点 `.n-dropdown-option-body`、标签页的激活状态有重复元素），不是产品问题。

## 与 PLAN 不一致或需要你决定的地方

按影响从大到小。带"需要决定"的是我按自己的判断先做了，你可以改。

1. **需要决定：被挤下线 / 会话失效时保留本机的云端配置缓存**，只有主动"退出登录"才清掉。理由：PLAN 1.1 第 4 条要求服务器不可用时仍能用缓存启动，被挤下线的那台机器上隧道可能正在跑；同一账号重新登录后继续同步，没推上去的修改也不会丢（会按冲突规则处理）。代价是共用电脑时，被挤下线后缓存还在本机（DPAPI 加密，只有当前 Windows 用户能读）。换另一个账号登录时会先清掉。
2. **需要决定：登录界面是主窗口里的弹窗**，不是单独的系统窗口。PLAN 6.7 写的是"登录窗口"。弹窗少一个 WebView2 实例（省约 100MB 内存），不登录也能直接用主界面（PLAN 13 节 #1）。
3. **需要决定：本地配置不会自动上传**。登录后要在配置的菜单里点"上传到云端"，上传是"转为云端配置"（ID 不变，运行中也不受影响），不是复制一份；想留本地副本可以对云端配置点"复制为本地配置"。登录后新建配置默认存到云端，可以改选"本地配置"。
4. **需要决定：默认配置只在交互登录时建**（云端和本机都没有云端配置时建"我的云端配置"）。自动登录时不建，否则用户删光云端配置后每次启动都会冒出来。两台机器同时首次登录一个空账号，可能各建一个。
5. **需要决定："保持登录"默认勾选**（和旧版"记住密码"默认勾选的习惯一致），下方写明只保存令牌、不保存密码。
6. **"B 在 60 秒内同步到"的口径**：B 每 60 秒拉一次，A 编辑后要等 1.5 秒防抖才推送，所以从 A 点保存算起最坏约 61.5 秒，从 A 推送完成算起 ≤ 60 秒。实测推送后 58.0 秒。窗口获得焦点时会立即拉取，实际使用中通常更快。
7. **冲突没有持久化**：冲突记在内存里，重启后本机修改还是"待推送"，第一次推送收到 409 会再次弹出，结果一样。每次启动（每个会话）的第一次拉取不带 `If-None-Match`，保证合并规则至少完整跑一遍。
8. **云端删除了正在运行的配置也按冲突处理**（选"使用云端"会停止隧道并删除），而不是悄悄删掉或保留。云端删除了没在运行、本机也没改的配置，直接从本机删掉。
9. **"使用云端"时如果这份配置正在运行，直接应用**（热更新或重启），不再额外询问：用户在冲突弹窗里已经明确选了云端。只有"后台拉到云端修改"这种用户没参与的情况才询问是否应用。
10. **退出登录时离线**：本地照样退出（删除凭据、清缓存），但服务端的会话没能吊销，令牌在服务端要等 30 天滑动过期才失效（本机已经不保存它了）。退出前还没推送的修改会尽量推送，最多等 5 秒，推不上去就丢失，确认框里会提示待同步的数量。
11. **`client_outdated` 时会话保留**、同步暂停（每小时再试一次），弹窗提示下载；本机的配置照常启动。登录接口也会返回 426（phase1.md 第 2 条），同样弹窗。
12. **网络代理"跟随系统"只读手动代理**（"Internet 选项"里的地址和例外列表），不支持自动配置脚本（PAC）和"自动检测设置"；没设手动代理时退回 `HTTP_PROXY`/`HTTPS_PROXY` 环境变量。回环地址永远直连。自定义代理按 PLAN 留到 Phase 5。
13. **e2e 构建的单实例锁和凭据目标名**：设置了 `MOEFRP_HOME` 时分别是 `com.moefrp.client.e2e-<8 位哈希>` 和 `MoeFrp/session/e2e-<8 位哈希>`，正式构建固定为 `com.moefrp.client` 和 `MoeFrp/session`。正式构建现在不认任何 `MOEFRP_*` 环境变量，原来说的"顺带当便携模式用"（phase2.md 第 1 条）也就没有了；以后要做便携模式需要单独设计。
14. **服务端地址写死在客户端**：默认 `https://frplus.loli-con.cn`，界面上不能改，只能构建时 `-ldflags "-X main.apiBase=..."`；测试版可以用 `MOEFRP_API_BASE`。PLAN 没提要不要让用户改。
15. **新增客户端错误码**（`internal\apperr`，前端都有中文）：`server_unreachable`（连不上服务端，显示为离线）、`bad_response`、`not_logged_in`、`conflict_gone`。
16. **新增事件** `sync:status`、`sync:remote-update`、`sync:error`、`auth:state`（PLAN 6.4 只列了 `sync:changed`、`sync:conflict`、`auth:expired`）。新增方法：`AuthService.Account`、`LogoutInfo`、`SyncStatus`、`SyncNow`，`ProfileService.Upload`、`Conflicts`、`RemoteUpdates`、`ApplyRemote`、`DismissRemote`；`ProfileService.Create` 多了 `origin` 参数。
17. **推送失败的处理**：网络、会话、版本、限流以外的错误（例如超出 50 份上限、校验失败）记在这份配置上，列表显示"同步失败"（悬停看原因），等它再被修改时才重试；第一次上传就超出上限时自动改回本地配置并提示。
18. **`@wailsio/runtime` 仍是 3.0.0-beta.30**：今天是 10-23，beta.31 要到 10-25 才过本机 pnpm 的 7 天等待期，没有绕过。到期后改 `frontend\package.json` 再 `pnpm install`。
19. **依赖**：`github.com/danieljoos/wincred` v1.2.3（本机模块缓存里已有、当前最新版）。没有新增前端依赖。
20. **服务端和 shared 没有改**。对接时注意到但没改的：服务端版本检查对没带 `X-Client-Version` 的请求不检查（phase1.md 第 2 条），客户端总是带。

## 留给后面的

- Phase 4：分享与订阅。`state` 里的 `subscriptions` 客户端目前忽略。
- Phase 5：自定义网络代理（`settings.networkProxy` 已经是枚举，加 `custom` 即可）、启动时检查更新（`apiclient.ClientConfig` 已经写好）。
- 登录、同步相关的日志现在写到 `%LOCALAPPDATA%\MoeFrp\startup.log`（不含令牌、密码、节点 token），没有进界面的日志面板，免得和 frp 日志混在一起。
- `-race`：本机跑不了，放到 CI 或别的机器。

## 目录（本阶段新增 / 改动）

```
client/
├─ main.go                       接 apiclient / credstore / cloudsync / AuthService，焦点拉取，测试钩子改走 testhooks
├─ internal/apiclient/           新：HTTP 客户端、错误分类、系统代理
├─ internal/credstore/           新：凭据管理器（wincred）
├─ internal/cloudsync/           新：账号会话、拉取 / 合并 / 推送 / 冲突 / 离线（含假服务端的单元测试）
├─ internal/testhooks/           新：只在 -tags e2e 生效的测试钩子
├─ internal/services/auth.go     新：AuthService
├─ internal/services/profile.go  改：云端配置、上传、冲突、远端更新
├─ internal/store/               改：cache.bin 的 Tx / PutNew / edits，settings 的 networkProxy
├─ internal/engine/engine.go     改：PredictUpdate
├─ frontend/src/                 新：AuthModal、AccountArea、ConflictDialog、SyncNotices、stores/account.ts、invite.ts
│                                改：ProfileList、ProfileEditor、Shell、SettingsPage、errors.ts、labels.ts、stores/profiles.ts
└─ scripts/e2e/                  新：run-phase3.mjs、cdp.mjs、credtool/；改：Phase 2 脚本改用 MoeFrp-e2e.exe
docs/phase3.md、docs/phase3/     本文、截图和端到端结果
```

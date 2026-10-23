# MoeFrp 重写计划（Go 版）

> 写于 2026-10-18。基于本机现状：
> - frp 源码 `E:\frp_end\frp`：`dev` 分支，提交 `26742060`，`pkg/util/version/version.go` 为 **0.71.1**
> - Go 1.26.1、Node、pnpm 已安装；Wails CLI 未安装
> - 旧项目：`E:\frp_end\client`（PySide6 客户端）、`E:\frp_end\server`（Flask 服务端）
>
> 本文所有 frp 接口名、字段名、源码位置都已在本地 frp 源码里核对过。路径写法相对于 `E:\frp_end\frp`。
>
> **2026-10-18 更新**：Phase 0 技术验证已完成，结论是继续用 Wails v3（锁定 `v3.0.0-beta.31`），报告见 `spike/REPORT.md`。本文已按验证结果修订，涉及第 1、4、5、6、10–13 节。
>
> **2026-10-23 更新**：Phase 1–5 已完成，Phase 6 的代码和文档已完成（`scripts/release.ps1`、`docs/release.md`、`docs/cutover.md`），上线需要人工操作。各阶段实现与本计划的差异见 `docs/phase*.md`，需要你处理的事项见 `docs/todo-for-owner.md`。

## 目录

- [0. 一页概览](#0-一页概览)
- [1. 目标、原则、非目标](#1-目标原则非目标)
- [2. 旧系统处置清单](#2-旧系统处置清单)
- [3. 总体架构](#3-总体架构)
- [4. 仓库结构](#4-仓库结构)
- [5. shared：两端共用的契约](#5-shared两端共用的契约)
- [6. 客户端](#6-客户端)
- [7. 服务端](#7-服务端)
- [8. 安全模型](#8-安全模型)
- [9. 可选的后续功能](#9-可选的后续功能)
- [10. 里程碑](#10-里程碑)
- [11. 测试策略](#11-测试策略)
- [12. 风险与对策](#12-风险与对策)
- [13. 需要你决定的事](#13-需要你决定的事)
- [附录 A：旧功能对照](#附录-a旧功能对照)
- [附录 B：旧接口 → 新接口](#附录-b旧接口--新接口)
- [附录 C：旧配置字段 → 新字段](#附录-c旧配置字段--新字段)

---

## 0. 一页概览

- **客户端**：Go + Wails v3 + Vue 3。产出单个 exe，frp 以库的形式编译进去。隧道配置在本机组装、本机启动，不再依赖服务器。
- **服务端**：Go 单个二进制 + SQLite。只负责账号、云端配置同步、分享与订阅、版本信息，不在隧道的数据路径上。
- **shared 模块**：两端共用数据模型、API 结构体和错误码。接口对不上的问题（旧版的 `sk` / `secretKey` 不一致、`set_user_role` 调用不存在的接口）在编译时就会暴露。
- **删除**：版本密钥 / proof / DLL 哈希、配置票据、登录延时、硬件绑定密钥、P2P/VLAN 残留、UAC 提权、Go DLL。
- **数据迁移**：旧 `users.db` 一次性导入。密码哈希格式兼容，用户不需要重置密码。

---

## 1. 目标、原则、非目标

### 1.1 目标

1. 旧版**真正在用**的功能全部保留（清单见附录 A）。
2. 修掉代码审查发现的 bug，并且从结构上杜绝同类问题：
   - 用稳定 ID 代替表格行号（旧版删除代理会让 STCP 密钥错位）。
   - 两端统一使用 frp 原生字段名（旧版 `sk` / `secretKey` 不一致）。
   - 用错误码代替字符串匹配（旧版靠 `"会话无效" in str(data)` 判断）。
3. 单文件 exe：运行时不解压程序文件，也不需要管理员权限。唯一会写的是 WebView2 的用户数据目录（缓存、localStorage），这一点绕不开，位置见 6.5。
4. 服务器宕机时，仍能用本地缓存的配置启动隧道。

### 1.2 原则

- **本地优先**：frp 配置在本机内存里组装并启动，服务器只做同步。
- **只有服务端是信任边界**：客户端的一切都能被用户看到和修改，所以客户端里不放任何"秘密"。
- **一种语言、一份契约**：两端都用 Go，请求和响应结构体放在 `shared`。
- **只用 frp 的公开接口，不改协议**：节点都是标准 frps，任何改动协议的事都不做。
- **能删就删**：没有实际防护效果的机制，一律不移植。

### 1.3 非目标（v1 不做）

- P2P 虚拟局域网（nebula / wireguard / wintun）。
- 同时运行多个 frp 实例。frp 有全局状态，详见 6.3.7；后续方案见 9.2。
- 自动更新。v1 只做更新提示。
- Web 管理后台。v1 用服务器上的命令行管理。
- 兼容旧客户端。切换后旧客户端只会收到"请升级"提示，见 7.12。

---

## 2. 旧系统处置清单

| 旧东西 | 处置 | 说明 |
|---|---|---|
| PySide6 客户端 | 重写 | |
| Flask 服务端 | 重写 | 迁移旧数据 |
| `MoeFrpClient.mfc` / `frp_core*.dll` | 删除 | frp 直接编进 exe |
| 版本密钥 + DLL 哈希 + proof + 挑战码 | 删除 | 改为普通的版本号检查，只用来提示升级 |
| 配置票据 `/api/request_config_ticket`、`/api/get_temp_config` | 删除 | 配置在本机组装 |
| `HardenedDelayDialog` 登录延时 | 删除 | 限流放在服务端做 |
| 硬件指纹 + Argon2 主密钥 + keyring 密钥包 | 删除 | 会话令牌存 Windows 凭据管理器，本地缓存用 DPAPI 加密 |
| "记住密码"（本地保存密码） | 改为"保持登录" | 只保存会话令牌，不再保存密码 |
| `X-Machine-GUID` | 改为随机的 `installId` | 不再采集硬件信息 |
| P2P/VLAN 对话框、nebula、`wireguard.dll` | 删除 | |
| 图片面板 | 保留，可关闭 | 图源改由服务端下发 |
| 单设备登录（在别处登录会挤掉当前会话） | 保留，可配置 | |
| 邀请码注册 | 保留 | 格式和校验位算法不变 |
| 管理员重置密码 | 保留 | 改为在服务器上用命令行生成重置码 |
| `server/generate_invite_code.py` | 重写为 `moefrp-server admin ...` | |
| `invite.py`（含明文 root 密码） | 删除 | 改用 SSH 密钥登录服务器执行命令 |
| `me.spec` 里的 `uac_admin=True` | 删除 | manifest 用 `asInvoker` |

---

## 3. 总体架构

```
┌───────────────────────── MoeFrp.exe（单文件） ─────────────────────────┐
│  前端：Vue 3（运行在 WebView2 里）                                       │
│        ▲  调用 Go 方法 / 接收事件                                        │
│        ▼                                                                 │
│  Go 服务层：Auth / Profile / Share / Tunnel / Settings / Image / App     │
│        │                              │                                  │
│        ▼                              ▼                                  │
│  apiclient（HTTP 客户端）        engine（内嵌 frp client 库）            │
└────────┼──────────────────────────────┼──────────────────────────────────┘
         │ HTTPS                        │ frp 协议（TCP / KCP / QUIC / WS）
         ▼                              ▼
   Caddy → moefrp-server          frps 节点（用户自己的，或分享来的）
               │
               ▼
            SQLite
```

服务端不在隧道的数据路径上。服务器宕机只影响登录、同步和分享，已缓存的配置照样能启动。

---

## 4. 仓库结构

```
E:\frp_end\
├─ frp\                        # 上游 frp 源码（独立 git 仓库，不修改）
└─ gogogo\                     # 新项目（建议单独 git init）
   ├─ PLAN.md
   ├─ go.work                  # use ./shared ./server ./client（frp 用 replace 引用，见下）
   ├─ shared\                  # module moefrp/shared
   │  ├─ model\                # Profile / Node / Proxy / Visitor / Share / Subscription
   │  ├─ api\                  # 请求/响应结构体、路由常量
   │  ├─ apierr\               # 错误码
   │  └─ validate\             # 两端共用的校验
   ├─ server\                  # module moefrp/server
   │  ├─ cmd\moefrp-server\
   │  ├─ internal\...
   │  ├─ migrations\*.sql
   │  └─ deploy\               # systemd 单元、Caddyfile、config.example.toml
   ├─ client\                  # module moefrp/client（Wails 应用）
   │  ├─ main.go
   │  ├─ internal\engine\      # 内嵌 frp
   │  ├─ internal\...
   │  ├─ frontend\             # Vue 3 + TypeScript
   │  └─ build\                # 图标、manifest、版本信息
   └─ docs\                    # 运维手册、发布清单
```

**frp 依赖方式**

- **以 `go.mod` 里的 `replace` 为准，`go.work` 只是辅助。** 原因：`wails3 build` 每次都会先执行 `go mod tidy`，而 tidy 不读 `go.work`（Phase 0 坑 3）。
  - `client/go.mod`：
    ```
    require github.com/fatedier/frp v0.71.1
    replace github.com/fatedier/frp => ../../frp
    replace moefrp/shared => ../shared
    ```
  - `server/go.mod`：`replace moefrp/shared => ../shared`。
  - 只写 `go.work` 的话，tidy 会去 Go 代理拉 `v0.71.1` 这个 tag 来补 go.sum，而实际编译用的是本地源码，两边看到的 frp 可能不是同一份代码。用 `replace` 后 tidy 和编译都用本地源码。
  - `go.work`（`use ./shared ./server ./client`）保留，方便编辑器跨模块跳转。
- 本地 frp 目前在 `dev` 分支，是今天的开发提交，不是发布版。而且本地仓库的标签只到 v0.62.1（`git describe` 输出 `v0.62.1-210-g26742060`）。建议先 `git fetch --tags`，再切到 `v0.71.1` 标签或 `master`。正式发布只用正式 tag 构建。
- frp 的 `go.mod` 要求 `go 1.26.0`，源码里也用了 `new(true)` 这种 1.26 才有的写法，所以整个工作区统一用 Go 1.26。
- 不 fork。确实需要改 frp 时再 fork，并用 `replace` 指向 fork，补丁尽量少。

---

## 5. shared：两端共用的契约

### 5.1 数据模型

JSON 字段一律用 camelCase，和 frp 的字段同名。

```go
type Profile struct {
    ID        string    `json:"id"`       // UUIDv7
    Name      string    `json:"name"`
    Nodes     []Node    `json:"nodes"`
    Proxies   []Proxy   `json:"proxies"`
    Visitors  []Visitor `json:"visitors"`
    Revision  int64     `json:"revision"` // 由服务端递增
    UpdatedAt time.Time `json:"updatedAt"`
}

type Node struct {
    ID         string `json:"id"`
    Remark     string `json:"remark"`
    ServerAddr string `json:"serverAddr"`
    ServerPort int    `json:"serverPort"`
    Token      string `json:"token"`
    User       string `json:"user,omitempty"`     // frp 的 user（代理名前缀）
    Protocol   string `json:"protocol,omitempty"` // tcp/kcp/quic/websocket/wss，默认 tcp
    TLS        *bool  `json:"tls,omitempty"`      // nil 表示跟随 frp 默认（开启）
    ProxyURL   string `json:"proxyURL,omitempty"` // 通过代理连接 frps，可选
}

type Proxy struct {
    ID             string   `json:"id"`   // 稳定 ID：界面和同步都用它，不用行号
    Name           string   `json:"name"`
    Type           string   `json:"type"` // tcp udp http https tcpmux stcp xtcp sudp
    Enabled        bool     `json:"enabled"`
    LocalIP        string   `json:"localIP"`
    LocalPort      int      `json:"localPort"`
    RemotePort     int      `json:"remotePort,omitempty"`
    CustomDomains  []string `json:"customDomains,omitempty"`
    SubDomain      string   `json:"subdomain,omitempty"`
    SecretKey      string   `json:"secretKey,omitempty"`
    AllowUsers     []string `json:"allowUsers,omitempty"`
    UseEncryption  bool     `json:"useEncryption,omitempty"`
    UseCompression bool     `json:"useCompression,omitempty"`
}

type Visitor struct {
    ID         string `json:"id"`
    Name       string `json:"name"`
    Type       string `json:"type"` // stcp xtcp sudp
    Enabled    bool   `json:"enabled"`
    ServerName string `json:"serverName"`
    ServerUser string `json:"serverUser,omitempty"`
    SecretKey  string `json:"secretKey"`
    BindAddr   string `json:"bindAddr"` // 默认 127.0.0.1
    BindPort   int    `json:"bindPort"`
    FallbackTo string `json:"fallbackTo,omitempty"` // xtcp 打洞失败时回退到某个 stcp visitor
}
```

运行时，一个配置里的所有代理和访客都通过同一个选中的节点连接。旧数据的字段（`local_ip`、`sk`、逗号分隔的 `custom_domains` 等）在导入时转换，见附录 C。

### 5.2 校验规则（`shared/validate`）

| 项目 | 规则 |
|---|---|
| 代理名、访客名 | 同一配置内唯一（代理和访客合在一起算）；1–50 个字符；只能是可打印字符，不能有空白和 `.`（`.` 会和 frp 的 `user.` 前缀混淆）。**重名只能靠我们自己检查**：frp 的 `ConfigSource.ReplaceAll` 以名字为 key，重名时后一个直接覆盖前一个，`ValidateAllClientConfig` 也不查重名（Phase 0 坑 5） |
| 端口 | 1–65535。旧版限制 1024 以上是 bug，导致 22、80、443 填不进去 |
| tcp / udp | `localPort` 必填；`remotePort` 为 0 时由 frps 分配 |
| http / https / tcpmux | `customDomains` 和 `subdomain` 至少填一个 |
| stcp / xtcp / sudp | `secretKey` 必填，为空时客户端自动生成 32 字节随机值 |
| 数量上限 | 每个配置：节点 ≤ 20、代理 ≤ 100、访客 ≤ 50；每个用户：配置 ≤ 50、分享 ≤ 50、订阅 ≤ 100 |

客户端启动隧道前还会再跑一遍 frp 自带的 `validation.ValidateAllClientConfig`。服务端不依赖 frp。

### 5.3 API 约定

- 路径前缀 `/api/v1`，认证头为 `Authorization: Bearer <token>`。
- 客户端的每个请求都带 `X-Client-Version` 和 `X-Install-Id` 两个头。
- 错误统一使用下面的格式：

```json
{ "error": { "code": "revision_conflict", "message": "配置已在其他设备修改", "details": {} } }
```

- 客户端只看 `code`。中文提示在前端的一个映射表里集中维护。

### 5.4 错误码（初版）

| code | HTTP | 含义 |
|---|---|---|
| `unauthorized` | 401 | 没有令牌或令牌无效 |
| `session_expired` | 401 | 会话过期 |
| `session_replaced` | 401 | 账号在别的设备登录，当前会话被挤下线 |
| `forbidden` | 403 | 没有权限 |
| `not_found` | 404 | 资源不存在 |
| `validation_failed` | 400 | 参数不合法，`details.fields` 列出具体字段 |
| `invalid_credentials` | 401 | 昵称或密码错误 |
| `account_locked` | 429 | 失败次数过多，`details.retryAfter` 给出等待秒数 |
| `account_disabled` | 403 | 账号已被停用 |
| `invite_invalid` / `invite_used` | 400 / 409 | 邀请码无效 / 已被使用 |
| `nickname_taken` | 409 | 昵称已被注册 |
| `reset_code_invalid` | 400 | 重置码无效或已过期 |
| `revision_conflict` | 409 | 版本冲突，`details.current` 是云端当前版本 |
| `share_revoked` / `share_expired` / `share_full` | 410 / 410 / 409 | 分享已撤销 / 已过期 / 订阅人数已满 |
| `already_subscribed` | 409 | 已经订阅过 |
| `limit_exceeded` | 409 | 超出数量上限 |
| `rate_limited` | 429 | 请求过于频繁，带 `retryAfter` |
| `client_outdated` | 426 | 客户端版本低于最低版本，带 `latestVersion`、`downloadUrl` |
| `internal` | 500 | 服务器内部错误 |

---

## 6. 客户端

### 6.1 技术栈

| 用途 | 选型 | 备注 |
|---|---|---|
| 语言 | Go 1.26 | 本机已装 1.26.1 |
| 桌面框架 | **Wails v3，锁定 `v3.0.0-beta.31`** | Phase 0 已验证托盘、托盘菜单、单实例、关闭到托盘、单文件 exe。CLI 安装：`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.31`。升级 Wails 前必须回归测试托盘菜单（见 12 节） |
| Wails 前端运行时 | `@wailsio/runtime`，**版本与 Go 端一致** | 模板默认写 `"latest"`，要改成固定版本。本机 pnpm 设了 `minimumReleaseAge`，新版本要过了等待期才能装；Phase 0 暂用 beta.28 配 beta.31，`Call` 和 `Events` 都正常，过了等待期后改成 beta.31 |
| 前端 | Vue 3 + TypeScript + Vite | 项目模板名是 `vue`（已含 TS），不是 `vue-ts`：`wails3 init -n client -t vue` |
| UI 组件库 | Naive UI | 自带深色主题，能跟随系统切换 |
| 前端状态 | Pinia | |
| 前端包管理 | pnpm | 本机已装 |
| frp | `github.com/fatedier/frp` v0.71.1，作为库引入 | |
| 凭据存储 | `github.com/danieljoos/wincred` | 存放会话令牌 |
| 本地加密 | DPAPI（`golang.org/x/sys/windows` 的 `CryptProtectData`） | 加密缓存里的节点 token |
| 剪贴板图片 | `golang.design/x/clipboard` v0.11.0 | Phase 0 已确认在 Windows 上不需要 cgo。注意 v0.11.0 的接口带 context：`Read(ctx, fmt)`、`Write(ctx, fmt, buf)`。Wails 自带的剪贴板只支持文本 |

### 6.2 进程与并发模型

- 单进程运行，frp 跑在同一进程的 goroutine 里。
- 前端调用 Go 方法本身就是异步的，界面不会再因为网络请求卡住（旧版所有请求都在 UI 线程上同步执行）。
- 后台循环有两个：
  - 引擎状态轮询，每秒一次。
  - 云端同步轮询，每 60 秒一次。会话检查合并在这个请求里，不再单独发。
- 崩溃记录：
  - 启动时调用 `debug.SetCrashOutput`，把致命错误写到 `%LOCALAPPDATA%\MoeFrp\crash.log`。
  - 我们自己启动的 goroutine 都包一层 recover。
  - frp 内部 goroutine 的 panic 拦不住，这是在进程内嵌入 frp 的代价。可选的子进程模式见 9.2。

### 6.3 frp 引擎（`client/internal/engine`）

#### 6.3.1 用到的 frp 公开接口

| 接口 | 位置 | 用途 |
|---|---|---|
| `client.NewService(client.ServiceOptions)` | `client/service.go` | 创建服务 |
| `(*Service).Run(ctx) error` | 同上 | 阻塞运行。`LoginFailExit=true`（默认值）时，首次登录失败会返回错误 |
| `(*Service).UpdateConfigSource(common, proxies, visitors)` | 同上 | 热更新代理和访客（`frpc reload` 用的也是这个） |
| `(*Service).GracefulClose(d)` / `Close()` | 同上 | 停止服务 |
| `(*Service).StatusExporter().GetProxyStatus(name)` | 同上 | 查单个代理的状态：`Phase`、`Err`、`RemoteAddr` |
| `source.NewConfigSource()`、`ReplaceAll`、`source.NewAggregator` | `pkg/config/source/` | `NewService` 必需的配置来源 |
| `config.FilterClientConfigurers`、`CompleteProxyConfigurers`、`CompleteVisitorConfigurers` | `pkg/config/load.go` | 按 `enabled` 过滤，补默认值 |
| `validation.ValidateAllClientConfig` | `pkg/config/v1/validation/client.go` | 启动前校验 |
| `security.NewUnsafeFeatures(nil)` | `pkg/policy/security/` | 校验和创建服务时需要的参数 |
| 全局变量 `log.Logger`，配合 golib 的 `log.WithOutput(io.Writer)` | `pkg/util/log/log.go` | 截获 frp 日志 |
| `version.Full()` | `pkg/util/version/version.go` | 在"关于"页显示 frp 版本 |

整体流程照搬 frpc 自己的写法：`cmd/frpc/sub/root.go` 中的 `runClientWithAggregator` 和 `startServiceWithAggregator`。

#### 6.3.2 启动流程（示意代码）

```go
import (
    frpclient "github.com/fatedier/frp/client"
    "github.com/fatedier/frp/pkg/config"
    "github.com/fatedier/frp/pkg/config/source"
    v1 "github.com/fatedier/frp/pkg/config/v1"
    "github.com/fatedier/frp/pkg/config/v1/validation"
    "github.com/fatedier/frp/pkg/policy/security"
)

func (e *Engine) Start(ctx context.Context, p model.Profile, n model.Node) error {
    common := &v1.ClientCommonConfig{
        ServerAddr: n.ServerAddr,
        ServerPort: n.ServerPort,
        User:       n.User,
    }
    common.Auth.Token = n.Token
    common.Transport.Protocol = n.Protocol
    common.Transport.ProxyURL = n.ProxyURL
    common.Transport.TLS.Enable = n.TLS // nil 时由 Complete() 补成 true
    // 不设置 WebServer.Port，就不会开 frpc 的管理端口
    if err := common.Complete(); err != nil { // 同时会把 LoginFailExit 补成 true
        return err
    }

    // 先用 shared/validate 检查（含重名）。frp 不查重名，重名会被静默覆盖
    if err := validate.Profile(p); err != nil {
        return err
    }
    proxies, visitors := mapProfile(p) // model → []v1.ProxyConfigurer / []v1.VisitorConfigurer

    cs := source.NewConfigSource()
    if err := cs.ReplaceAll(proxies, visitors); err != nil {
        return err
    }
    agg := source.NewAggregator(cs)

    // 和 frpc 一样：先过滤、补全，再校验
    ps, vs, err := agg.Load()
    if err != nil {
        return err
    }
    ps, vs = config.FilterClientConfigurers(common, ps, vs)
    ps = config.CompleteProxyConfigurers(ps)
    vs = config.CompleteVisitorConfigurers(vs)
    unsafe := security.NewUnsafeFeatures(nil)
    if _, err := validation.ValidateAllClientConfig(common, ps, vs, unsafe); err != nil {
        return err // 直接显示在界面上，例如 http 代理没填域名
    }

    svc, err := frpclient.NewService(frpclient.ServiceOptions{
        Common:                 common,
        ConfigSourceAggregator: agg,
        UnsafeFeatures:         unsafe,
    })
    if err != nil {
        return err
    }
    runCtx, cancel := context.WithCancel(ctx)
    e.setRunning(svc, cancel, p, n)
    go func() { e.onExit(svc.Run(runCtx)) }()
    return nil
}
```

#### 6.3.3 模型到 frp 结构体的映射

先用 `v1.NewProxyConfigurerByType(v1.ProxyType(t))` 创建对象，再填字段。

所有代理共用的字段都在 `ProxyBaseConfig` 里：

- `Name`、`Type`
- `Enabled *bool`
- `Transport{UseEncryption, UseCompression}`
- `ProxyBackend{LocalIP, LocalPort}`

| 类型 | frp 结构体 | 需要填的特有字段 |
|---|---|---|
| tcp | `v1.TCPProxyConfig` | `RemotePort` |
| udp | `v1.UDPProxyConfig` | `RemotePort` |
| http | `v1.HTTPProxyConfig` | `DomainConfig{CustomDomains, SubDomain}` |
| https | `v1.HTTPSProxyConfig` | 同上 |
| tcpmux | `v1.TCPMuxProxyConfig` | 同上，另加 `Multiplexer`（`httpconnect`） |
| stcp | `v1.STCPProxyConfig` | `Secretkey`、`AllowUsers`。**注意 Go 字段名拼作 `Secretkey`，JSON 名是 `secretKey`** |
| xtcp | `v1.XTCPProxyConfig` | 同上 |
| sudp | `v1.SUDPProxyConfig` | 同上 |
| 访客 stcp / xtcp / sudp | `v1.STCPVisitorConfig` / `XTCPVisitorConfig` / `SUDPVisitorConfig` | `VisitorBaseConfig{Name, Type, Enabled, SecretKey, ServerName, ServerUser, BindAddr, BindPort}`；xtcp 还有 `FallbackTo` |

#### 6.3.4 状态机

```
            Start
  Idle ───────────▶ Connecting ──login success──▶ Running
   ▲                    │                           │  ▲
   │          首次登录失败 │                    断线 │  │ 重连成功
   │                    ▼                           ▼  │
   └──────────────── Failed                    Reconnecting

  任何状态 ──Stop──▶ Stopping ──Run 返回──▶ Idle
```

- **连接状态从日志分类得到**，因为 frp 没有对外暴露控制连接的状态。判断依据是下面几条日志，它们都出自 `client/service.go` 的 `loopLoginUntilSuccess`：
  - `try to connect to server` → Connecting 或 Reconnecting
  - `login to server success` → Running
  - `connect to server error` → 记录错误原因

  升级 frp 时由集成测试兜底，见第 11 节。
- **代理状态**：每秒对所有启用的代理名调用一次 `GetProxyStatus`，状态有变化时才推送事件。
  - `Phase` 的取值有 `new`、`wait start`、`start error`、`running`、`check failed`、`closed`，定义在 `client/proxy/proxy_wrapper.go`。
  - 处于 `start error` 时，`Err` 里有失败原因，例如远程端口已被占用（`port already used`）。
  - **Stopping 状态下不轮询**：`GracefulClose(d)` 期间 frp 一直持有内部锁，`GetProxyStatus` 会被阻塞整个 d（实测 `GracefulClose(2s)` 阻塞了 1.9 秒）。轮询放在独立的 goroutine 里，绝不在响应前端调用的路径上同步执行（Phase 0 坑 7）。
- **首次登录失败**：`Run` 返回 `login to the server failed: ...`。
  - token 错误时，frp 的原文是 `token in login doesn't match ...`，可以直接映射成"节点 token 错误"。
  - 连不上服务器时，frp 的原文是 `connect to server error: session shutdown`，用户看不懂（Phase 0 坑 8）。这时由引擎自己对节点做一次 TCP 探测（和 `PingNodes` 同一套代码），给出"连接被拒绝 / 超时 / 域名解析失败"这类原因。frp 原文只放进日志。
- **拨号过程中被停止**：`Run` 要等拨号结束才返回（连接被拒绝约 2.5 秒，地址不通约 4.8 秒），返回的错误是 `login to the server failed: <nil>`。引擎要记住"是用户主动停止的"，这时不能当成登录失败显示（Phase 0 坑 6）。
- **登录成功后断线**：frp 会自己按退避策略重连，间隔最长 20 秒。界面显示"重连中"。

#### 6.3.5 什么时候热更新，什么时候重启

| 改动内容 | 处理方式 |
|---|---|
| 代理或访客的增删改、启用开关 | 调用 `UpdateConfigSource`，连接不断 |
| 换节点，或改了地址、端口、token、协议、user | 停掉再重新启动。必须新建 `Service`，因为关闭后的 `Service` 不能复用（`lifecycleMu` / `closed` 标志） |

- 调用 `UpdateConfigSource` 之前同样要先跑一遍校验（含重名检查）。校验不通过就不提交，并提示用户。
- **重启时必须等旧的 `Run` 返回之后再启动新的 `Service`**，界面这段时间显示"停止中"，按钮不可点。原因有两个（Phase 0 坑 6、9）：
  - 旧 Service 可能还在拨号，最多要等约 5 秒才返回。
  - frp 的日志出口是全局的。两个 Service 同时存在时，旧 Service 的 `connect to server error` 会被误判成新 Service 的状态。

#### 6.3.6 日志

- 程序启动时执行一次 `frplog.Logger = frplog.Logger.WithOptions(golog.WithOutput(sink), golog.WithLevel(level))`。
  - 不调用 `InitLogger`，因为它只支持输出到控制台或文件。
  - golib 的版本必须和 frp `go.mod` 里的一致（目前是 v0.8.2）。
- `sink` 是一个环形缓冲，默认保留 5000 行，每 200ms 批量推送一次事件。前端用虚拟列表渲染。
- 前端用 CSS class 按级别着色，**一律按纯文本渲染，不用 `v-html`**。旧版把 frp 日志和用户填的代理名直接拼进 HTML，这个问题就此消失。
- "导出日志"会写到用户选择的文件里。

#### 6.3.7 frp 全局状态带来的限制

- `log.Logger` 是全局变量，所有 Service 共用同一个日志出口。
- 配置了 `DNSServer` 时，`netpkg.SetDefaultDNSAddress` 修改的是全局设置。
- frp `client` 包的 `init()` 会设置全局的 `crypto.DefaultSalt` 和两个 QUIC 环境变量。
- frpc 自带的多配置并行模式，源码注释里写明了 "only designed for testing. It's not guaranteed to be stable"（`cmd/frpc/sub/root.go`）。

**结论：v1 同一时间只运行一个 Service，也就是一个配置加一个节点。**

#### 6.3.8 退出

- 点关闭按钮默认最小化到托盘。
- 点托盘的"退出"时，先 `GracefulClose(500ms)`，再等 `Run` 返回，最多等 2 秒；超时就直接退出进程，不再等。
  - 正常运行中的隧道，Phase 0 实测 500ms 就停完，frps 上的端口也被释放。
  - 超时只会发生在"还在拨号"的时候。这时本来就没有建立隧道，直接退出没有副作用。
- 系统关机或注销时走同样的停止流程。
- 这和 6.3.5 的"换节点必须等旧 Run 返回"不矛盾：退出时进程都要没了，不存在两个 Service 共用日志出口的问题。

### 6.4 Go 服务层（供前端调用的方法）

| 服务 | 方法（示意） |
|---|---|
| `AuthService` | `Register`、`Login(nickname, password, keepLoggedIn)`、`Logout`、`Me`、`ChangePassword`、`ResetPassword(nickname, code, newPassword)` |
| `ProfileService` | `List`、`Get(id)`、`Create(name)`、`Save(profile)`、`Delete(id)`、`ResolveConflict(id, keep)` |
| `TunnelService` | `Start(profileID, nodeID)`、`Stop`、`State`、`ProxyStatuses`、`PingNodes(profileID)`、`ExportLogs(path)` |
| `ShareService` | `Create`、`ListMine`、`Update`、`Republish`、`Revoke`、`Preview(shareID)`、`Subscribe`、`UpdateSubscription`、`Unsubscribe` |
| `SettingsService` | `Get`、`Update` |
| `ImageService` | `Next`、`CopyToClipboard`、`SaveAs` |
| `AppService` | `Version`、`CheckUpdate`、`OpenURL`、`Quit` |

Go 推送给前端的事件：

| 事件 | 内容 |
|---|---|
| `tunnel:state` | 状态机状态、当前配置和节点、错误 |
| `tunnel:proxy` | 某个代理的 `Phase`、`Err`、`RemoteAddr` 变化 |
| `tunnel:logs` | 一批日志行 |
| `sync:changed` | 云端数据有更新，列表需要刷新 |
| `sync:conflict` | 某个配置出现版本冲突 |
| `auth:expired` | 会话失效或被挤下线，附带错误码 |
| `image:changed` | 换了一张新图片 |
| `update:available` | 有新版本 |

`PingNodes` 和旧版一样，用 TCP 连接节点并计时（超时 2 秒），多个节点并发测。

### 6.5 本地存储

| 内容 | 位置 | 保护方式 |
|---|---|---|
| 设置（主题、代理模式、关闭行为等） | `%APPDATA%\MoeFrp\settings.json` | 明文，不含任何机密 |
| `installId` | 同上 | 首次运行时生成的随机 UUID |
| 会话令牌 | Windows 凭据管理器，目标名 `MoeFrp/session` | 由系统保护，只有当前用户能读 |
| 配置缓存（含节点 token） | `%APPDATA%\MoeFrp\cache.bin` | DPAPI（当前用户）。读不出来就重新从云端拉 |
| WebView2 用户数据（浏览器缓存、localStorage） | `%LOCALAPPDATA%\MoeFrp\WebView2\` | 无。**必须显式设置** `Windows.WebviewUserDataPath`：默认值是 `%APPDATA%\<exe 文件名>`，而发布的文件名带版本号，每升级一次就会新建一个目录，前端数据丢失、旧目录越堆越多（Phase 0 坑 4） |
| 图片缓存 | `%LOCALAPPDATA%\MoeFrp\images\` | 无 |
| 崩溃日志 | `%LOCALAPPDATA%\MoeFrp\crash.log` | 无 |

- 不再保存密码，也不再采集硬件信息。插拔 U 盘这类硬件变化不会再导致设置丢失。
- 首次运行时检测旧版残留：注册表 `HKCU\Software\MoeFrpTool`，以及凭据 `FRP_Client_Tool_V3_Keyring`。发现后提示用户一键清理。

### 6.6 云端同步

- **版本号**：每个配置、每个订阅都有 `revision`。每个用户还有一个 `stateVersion`，任何改动都会让它加 1。
- **拉取**：
  - 调用 `GET /api/v1/state`，带上 `If-None-Match: "<stateVersion>"`，没有变化时返回 304。
  - 登录后立即拉一次，之后每 60 秒一次，窗口获得焦点时也拉一次。
  - 这个请求顺带完成会话检查：返回 401 就下线。
- **推送**：编辑先写入本地缓存，停止输入 1.5 秒后调用 `PUT /api/v1/profiles/{id}`，带上 `baseRevision`。
- **冲突**：服务端返回 409 `revision_conflict` 并附上云端当前版本。界面弹窗让用户选择"保留本机"或"使用云端"，可以查看差异。旧版那种后台刷新悄悄覆盖本地编辑的情况不会再出现。
- **离线**：网络不通时界面显示"离线"，仍然可以编辑本地缓存并启动隧道。网络恢复后按上面的规则推送。
- **正在运行的配置被云端修改**：提示"云端配置已更新，是否应用"。应用时按 6.3.5 决定热更新还是重启。

### 6.7 界面

**登录窗口**

- 登录、注册、重置密码三个标签页。
- 注册需要邀请码。前端先检查格式和校验位，算法与服务端相同。

**主界面**（左侧是导航栏）

- **隧道**
  - 左栏是配置列表，包括"我的配置"和"订阅"。用图标区分类型，订阅还会标出"已撤销"。
  - 右侧是编辑区，分"代理""节点""访客"三个标签页。
  - 底部是运行栏：节点下拉框（显示测速结果）、启动/停止按钮、状态徽标。日志面板可以展开。
  - 代理表格的列：开关、名称、类型、本地地址、远程（端口或域名）、**运行状态**（running / 错误原因 / 远程地址）。编辑用抽屉，不用模态弹窗。
- **我的分享**
  - 列表显示类型、订阅人数、过期时间。
  - 每条分享可以撤销、重新发布、复制 ID。
  - 用"创建分享"向导新建分享。
- **订阅**
  - 先输入分享 ID，看到预览（名称、类型、分享者昵称、节点备注；不显示 token）。
  - 再按类型填写参数：模板型选节点并填写自己的代理；P2P 型为每个目标填本地端口。
  - 最后确认订阅。
- **设置**：见 6.8–6.10。
- **关于**：客户端版本、frp 版本、开源许可（frp 是 Apache-2.0），以及检查更新。

**全局**

- 所有操作都是异步的，带加载状态。出错时用通知条显示错误码对应的中文提示。
- 主题有三种：跟随系统、浅色、深色（用 Naive UI 的 `darkTheme` 加 `useOsTheme` 实现）。

### 6.8 托盘、单实例、开机自启

- **托盘菜单**：显示主界面、当前状态、启动/停止上次的配置、退出。
- **关闭按钮的行为**可在设置里选：最小化到托盘（默认）或退出。
- **单实例**：再次双击 exe 时，激活已经打开的窗口。
- **开机自启**：写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，是用户级设置，不需要管理员权限。
- **启动后自动连接上次的配置**：可选，默认关闭。
- **后台内存**：Phase 0 实测，窗口存在时主进程加 6 个 `msedgewebview2.exe`，总工作集约 366MB，私有内存约 247MB，几乎都是 WebView2 占的。主进程本身只有约 36MB。
  - 对一个常驻托盘的程序来说偏大。计划在 Phase 5 试做"最小化到托盘时关闭窗口、释放 WebView2，从托盘打开时重新创建窗口"。frp 引擎跑在 Go 里，不受影响。
  - 需要验证的点：重新创建窗口的耗时（参考 Phase 0 的热启动约 430ms）；前端状态要能从 Go 端完整恢复（当前状态、最近的日志）。
  - 如果体验不好，就保留隐藏窗口的做法，并在设置里提供开关。

### 6.9 图片面板（可关闭）

- 图源列表由 `GET /api/v1/client/config` 下发，包括权重、是否是 API、JSON 路径。改图源不需要发新版客户端。
- 图片由 Go 端下载，走应用的代理设置。前端只拿本地地址来显示，不受 CORS 限制。GIF 直接用 `<img>` 显示。
- 右键菜单：刷新、复制（图片放进剪贴板）、另存为、查看原图（可缩放、拖动）。
- 在设置里关闭后，不再发出任何图片请求。

### 6.10 网络代理设置

- **应用自身的 HTTP 请求**（API 和图片）有三种模式：不使用代理、跟随系统、自定义。自定义支持 `http://` 和 `socks5://`，Go 标准库原生支持，不再出现旧版"缺少 SOCKS 依赖"的问题。
- **frp 连接节点**：每个节点可以单独设置 `proxyURL`，对应 frp 原生的 `transport.proxyURL`。默认不用。

### 6.11 构建与发布

- **构建命令**：`wails3 build PACKAGE_MANAGER=pnpm`（基于 Taskfile）。
  - Taskfile 的默认参数已经是 `-tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui"`，`CGO_ENABLED=0`。
  - 版本号：在 Taskfile 的 `BUILD_FLAGS` 里追加 `-X main.version=<版本>`。
  - 前端产物通过 `go:embed` 嵌入。
  - 每次构建都会先跑 `go mod tidy`，所以本地模块必须用 `replace` 引用（见第 4 节）。
- **产物**：`MoeFrp-<版本>-windows-amd64.exe`，以及对应的 `.sha256` 文件。
  - Phase 0 实测：空应用 9.4MB，加上 frp 客户端后 18.2MB（frp 带来约 9MB）。正式版加上完整界面，预计 20MB 左右。
  - 启动时间：热启动约 430ms，构建后第一次运行约 610ms。
- **manifest**：
  - Wails 默认的 manifest 已经是 `asInvoker`（不需要管理员）和 PerMonitorV2 DPI。
  - 默认 manifest 里**没有** `supportedOS` 兼容性声明，需要手动加上 Win10/11 的 GUID（Phase 0 坑 11）。
- **版本信息资源**：沿用旧 `client/version.txt` 里的产品名、描述和版权信息。
- **不用 UPX，不用 garble 混淆**：两者都会增加被杀毒软件误报的概率。
- **代码签名**：使用 OV 或 EV 证书，命令为 `signtool sign /fd sha256 /tr <时间戳服务器> /td sha256 MoeFrp.exe`。
- **杀毒软件**：
  - 你这台开发机的 Defender 服务是停着的，实际在跑的是 ESET 和火绒，所以 **Defender 扫描要在另一台开着 Defender 的机器或虚拟机上做**。命令：`MpCmdRun.exe -Scan -ScanType 3 -File <exe> -DisableRemediation`。
  - 国内用户很多装的是火绒、360，发布前也用它们扫一遍。Phase 0 的 exe 在 ESET 和火绒的实时防护下多次生成和运行，都没有被拦截，但这只能算弱证据。
  - 首次发布和每个大版本，都向 Microsoft 提交误报申诉。
- **WebView2 缺失处理**（Phase 0 已查清，必须做）：
  - **问题**：Wails v3 的单文件 exe 在找不到 WebView2 时会直接 `os.Exit(1)`，没有窗口也没有提示，用户只会觉得"双击没反应"。
  - **不能指望 `Options.ErrorHandler`**：这是 Wails 的 bug，出错时内部先把错误回调重置成了默认处理器，`ErrorHandler` 根本收不到。
  - **只有 `wails3 package` 生成的 NSIS 安装包**才会检查并安装 WebView2，单文件分发拿不到这层保护。
  - **做法**：在 `app.Run()` 之前自己检查。依次读以下三个位置下 `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` 的 `pv` 值：
    - `HKLM\SOFTWARE\WOW6432Node\`
    - `HKLM\SOFTWARE\`
    - `HKCU\Software\`

    都没有时，用 Win32 `MessageBox` 弹框说明原因并给出下载地址，然后退出。实现可以直接搬 `spike/app-engine/webview2check.go`，判断逻辑和 Wails 的 NSIS 模板一致。
  - **预检拦不住的情况**：注册表里有 `pv`，但运行时已经损坏，这时仍会静默退出。可以考虑在 `main()` 里把 stderr 重定向到 `%LOCALAPPDATA%\MoeFrp\startup.log`，至少能留下线索。
  - **补测**：发布前在干净的 Win10 虚拟机上实测一次（Phase 0 是在本机模拟的），写进发布清单。
  - **上游**：可以把 `ErrorHandler` 收不到错误的 bug 报给 Wails，要不要报由你决定。

### 6.12 更新检查

- 启动时以及之后每 6 小时，调用一次 `GET /api/v1/client/config`。返回内容包括 `latestVersion`、`minVersion`、`downloadUrl`、`sha256`、更新说明和公告。
- 版本低于 `minVersion` 时，服务端对需要登录的接口一律返回 426 `client_outdated`，客户端弹窗引导用户下载新版。
- 版本过低时**本地缓存的配置仍可启动**：升级提示只影响账号和同步功能。

---

## 7. 服务端

### 7.1 技术栈

| 用途 | 选型 |
|---|---|
| 语言 | Go 1.26 |
| HTTP | 标准库 `net/http`。Go 1.22 起 `ServeMux` 直接支持 `"POST /api/v1/..."` 这种带方法的路由，不需要框架 |
| 数据库 | SQLite，驱动用 `modernc.org/sqlite`（纯 Go 实现，可以 `CGO_ENABLED=0` 交叉编译到 Linux） |
| SQL | 手写 SQL + `database/sql`。迁移文件用 `embed` 嵌进二进制，自己维护 `schema_migrations` 表 |
| 密码哈希 | `golang.org/x/crypto/argon2`（argon2id，PHC 字符串格式，和旧哈希兼容） |
| 限流 | `golang.org/x/time/rate` |
| 日志 | `log/slog`，JSON 格式 |
| 配置 | TOML 文件（`github.com/pelletier/go-toml/v2`，frp 也在用），可以用环境变量覆盖 |
| 命令行 | 标准库 `flag` 实现子命令 |

### 7.2 目录结构

```
server/
├─ cmd/moefrp-server/main.go      # 子命令：serve / migrate / import-legacy / admin / backup
├─ internal/
│  ├─ config/        # 读取配置
│  ├─ store/         # SQLite 连接、迁移、各表的查询
│  ├─ auth/          # 密码哈希、会话、邀请码、重置码、登录锁定
│  ├─ profile/       # 配置 CRUD、stateVersion
│  ├─ share/         # 分享、订阅、运行数据组装
│  ├─ httpapi/       # 路由、中间件、处理函数
│  ├─ ratelimit/
│  ├─ admin/         # 管理子命令的实现
│  └─ legacy/        # 导入旧 users.db；旧接口的"请升级"提示
├─ migrations/0001_init.sql
└─ deploy/           # moefrp.service、Caddyfile.example、config.example.toml
```

### 7.3 数据库

每次连接都执行：

```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
PRAGMA synchronous = NORMAL;
```

所有时间字段都用 Unix 秒（INTEGER）。

```sql
CREATE TABLE users (
  id             TEXT PRIMARY KEY,               -- 沿用旧库的 uuid
  nickname       TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash  TEXT NOT NULL,                  -- argon2id PHC 字符串
  role           TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','vip','admin')),
  status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  state_version  INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);

CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash    BLOB NOT NULL UNIQUE,            -- sha256(token)
  install_id    TEXT NOT NULL,
  device_name   TEXT,
  created_at    INTEGER NOT NULL,
  last_seen_at  INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL,
  revoked_at    INTEGER,
  revoke_reason TEXT                             -- logout / replaced / password_changed / admin
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE invite_codes (
  code        TEXT PRIMARY KEY,                  -- FRPT-XXXX-XXXX
  note        TEXT,
  created_at  INTEGER NOT NULL,
  used_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
  used_at     INTEGER,
  revoked_at  INTEGER
);

CREATE TABLE password_reset_codes (
  code_hash   BLOB PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  used_at     INTEGER
);

CREATE TABLE profiles (
  id              TEXT PRIMARY KEY,
  owner_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  data            TEXT NOT NULL,                 -- JSON: {nodes, proxies, visitors}
  schema_version  INTEGER NOT NULL DEFAULT 1,
  revision        INTEGER NOT NULL DEFAULT 1,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_profiles_owner ON profiles(owner_id);

CREATE TABLE shares (
  id                 TEXT PRIMARY KEY,           -- 128 位随机数，base62 编码
  owner_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               TEXT NOT NULL,
  kind               TEXT NOT NULL CHECK (kind IN ('template','full','p2p')),
  payload            TEXT NOT NULL,              -- JSON 快照
  source_profile_id  TEXT REFERENCES profiles(id) ON DELETE SET NULL,
  revision           INTEGER NOT NULL DEFAULT 1,
  expires_at         INTEGER,
  max_subscribers    INTEGER,
  revoked_at         INTEGER,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
CREATE INDEX idx_shares_owner ON shares(owner_id);

CREATE TABLE subscriptions (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  share_id    TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  params      TEXT NOT NULL,                     -- JSON，结构随 kind 不同
  revision    INTEGER NOT NULL DEFAULT 1,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  UNIQUE (user_id, share_id)
);

CREATE TABLE audit_log (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  at        INTEGER NOT NULL,
  actor_id  TEXT,
  action    TEXT NOT NULL,
  target    TEXT,
  ip        TEXT,
  detail    TEXT
);

CREATE TABLE schema_migrations (
  version     INTEGER PRIMARY KEY,
  applied_at  INTEGER NOT NULL
);
```

配置里的节点 token 以明文存在库中，和旧版一样。保护措施是数据库文件权限设为 600，备份文件加密。

### 7.4 认证与会话

**注册**

- 昵称规则：`^[a-zA-Z0-9_]{3,20}$`，唯一性不区分大小写。`all`、`admin` 等是保留词。
- 密码：8–128 位。
- 邀请码：格式 `FRPT-XXXX-XXXX`，沿用原来的校验位算法。
- 占用邀请码时执行 `UPDATE invite_codes SET used_by=?, used_at=? WHERE code=? AND used_by IS NULL AND revoked_at IS NULL`，并检查影响的行数。这一步和创建用户放在同一个事务里，修掉旧版"并发注册能重复使用同一个邀请码"的问题。

**登录**

- 用户不存在时也做一次假的哈希计算，防止通过响应时间判断昵称是否存在。
- 失败锁定：
  - 同一个昵称连续失败 5 次后锁定 15 分钟，再失败则锁定时间翻倍，最长 24 小时。
  - 同一个 IP 每分钟最多尝试 10 次。
- 登录成功后生成令牌：32 字节随机数，`base64url` 编码，加前缀 `mfs_`（方便在泄露扫描中识别）。数据库只存它的 SHA-256。
- 单设备模式是一个配置项，默认开启：新登录会吊销该用户的其他会话，原因记为 `replaced`。被挤下线的客户端会收到 `session_replaced`。
- 如果旧哈希的参数和当前参数不同，登录成功后自动用新参数重新哈希。

**会话**

- 滑动过期：30 天内没有使用才失效。
- `last_seen_at` 最多每 10 分钟写一次库，避免每个请求都写。

**退出与改密码**

- 退出：吊销当前会话。
- 改密码：先校验旧密码，成功后吊销该用户的其他会话。

**重置密码**

1. 管理员在服务器上执行 `moefrp-server admin user reset-code <昵称>`，得到一个一次性重置码，24 小时内有效，库里只存哈希。
2. 用户在客户端输入昵称、重置码和新密码。
3. 重置成功后吊销该用户的所有会话。

**日志**：任何时候都不记录密码、令牌、重置码和节点 token。旧版把完整的会话令牌写进了 `server.log`。

### 7.5 API 一览

| 方法和路径 | 需要登录 | 说明 |
|---|---|---|
| `GET /healthz` | 否 | 健康检查 |
| `GET /api/v1/client/config` | 否 | 最新版本、最低版本、下载地址、公告、图源 |
| `POST /api/v1/auth/register` | 否 | 昵称、密码、邀请码 |
| `POST /api/v1/auth/login` | 否 | 昵称、密码、设备名 → 令牌和用户信息 |
| `POST /api/v1/auth/logout` | 是 | |
| `POST /api/v1/auth/reset-password` | 否 | 昵称、重置码、新密码 |
| `POST /api/v1/auth/change-password` | 是 | 旧密码、新密码 |
| `GET /api/v1/me` | 是 | 用户信息（昵称、角色） |
| `GET /api/v1/state` | 是 | 全部配置、订阅摘要、`stateVersion`；支持 ETag / 304 |
| `POST /api/v1/profiles` | 是 | 新建配置 |
| `GET /api/v1/profiles/{id}` | 是 | |
| `PUT /api/v1/profiles/{id}` | 是 | 必须带 `baseRevision`，冲突时返回 409 |
| `DELETE /api/v1/profiles/{id}` | 是 | |
| `POST /api/v1/shares` | 是 | 从配置创建分享（保存快照） |
| `GET /api/v1/shares` | 是 | 我的分享，含订阅人数 |
| `PATCH /api/v1/shares/{id}` | 是 | 改名称、过期时间、人数上限 |
| `POST /api/v1/shares/{id}/republish` | 是 | 用来源配置的当前内容更新快照 |
| `DELETE /api/v1/shares/{id}` | 是 | 撤销分享（软删除） |
| `GET /api/v1/shares/{id}/preview` | 是 | 订阅前的预览，**不含 token 和 secretKey** |
| `POST /api/v1/subscriptions` | 是 | 分享 ID + 参数 |
| `PUT /api/v1/subscriptions/{id}` | 是 | 修改参数，带 `baseRevision` |
| `DELETE /api/v1/subscriptions/{id}` | 是 | |
| `GET /api/v1/subscriptions/{id}/runtime` | 是 | 运行所需的完整数据（节点 token、代理或访客），只返回给订阅者本人 |
| `/api/*`（不在 v1 下的所有路径） | — | 旧客户端的升级提示，详见 7.12：`POST /api/login/get_challenge` 返回 200 和一个假挑战码，其余路径统一返回 426 和 `{"error": "请下载新版客户端：<URL>"}` |

**中间件**（按执行顺序）：请求 ID → 从可信代理头里取真实 IP → panic 恢复 → 请求体大小限制 → 访问日志 → 客户端版本检查 → 认证 → 限流。

### 7.6 分享模型

分享有三种类型。类型在创建时显式保存；旧版是靠"恰好只有一个 stcp 代理"来猜的。

| 类型 | 快照内容 | 订阅者需要提供 | 运行时如何组装 |
|---|---|---|---|
| `template` | 选中的若干节点 | 选一个节点 + 自己的代理列表 | 节点 + 订阅者的代理 |
| `full` | 节点 + 代理 | 可选：选一个节点 | 原样使用 |
| `p2p` | 一个节点 + 一个或多个 stcp/xtcp/sudp 目标（`serverName`、`secretKey`、`serverUser`） | 每个目标的本地绑定端口 | 节点 + 自动生成的 visitor |

- **快照**：创建时复制一份。之后分享者修改自己的配置不会影响分享，需要时点"重新发布"。订阅者在下一次同步时拿到新内容。
- **撤销**：软删除，记录 `revoked_at`。订阅者会看到"已撤销"，`runtime` 返回 410 `share_revoked`，客户端随即停止对应的隧道并删除缓存。
- **可选限制**：过期时间、订阅人数上限。
- **分享 ID**：128 位随机数，用 base62 编码。不能用带时间前缀的 UUIDv7，否则更容易被猜到。
- **必须讲清楚的限制**：订阅者运行隧道时一定能拿到节点的 token。这是 frp 的设计，token 就是登录节点的凭证，所以分享就等于允许对方使用你的节点。`preview` 不返回 token，`runtime` 只返回给已订阅的人，但订阅者本机上的 token 是保护不了的。要做到按人授权，见 9.1。

### 7.7 输入限制

- 请求体上限 256KB，用 `http.MaxBytesReader` 实现。旧版没有任何上限。
- 所有请求体都反序列化到 shared 里的结构体：启用 `DisallowUnknownFields` 拒绝未知字段，再跑一遍 `shared/validate`。缺字段返回 400，不会再像旧版那样直接 500。
- 数量上限同 5.2。
- 文本字段只校验长度和字符集，**存储时不做 HTML 转义**。转义是显示层的事，Vue 默认就会做。这样也修掉了旧版用 bleach 把 `&` 存成 `&amp;` 的问题。

### 7.8 限流与真实 IP

- 只有配置项 `trusted_proxies` 里列出的地址（例如本机的 Caddy）发来的 `X-Forwarded-For` 才被信任。直连的请求一律使用 TCP 对端地址。
- 各接口的限流规则：

| 接口 | 规则 |
|---|---|
| 登录 | 每个 IP 每分钟 10 次；另有按昵称的失败锁定（见 7.4） |
| 注册 | 每个 IP 每小时 5 次 |
| 重置密码 | 每个 IP 每 10 分钟 5 次 |
| 分享预览 | 每个用户每分钟 30 次 |
| 其他需要登录的接口 | 每个用户每分钟 120 次 |

- 服务端是单进程，限流状态放在内存里即可。旧版那种"gunicorn 开多个 worker 后，挑战码和限流状态在不同 worker 之间不共享"的问题不会再出现。

### 7.9 日志与审计

- 用 `slog` 输出 JSON 到 stdout，由 journald 收集。
- 访问日志记录：方法、路径、状态码、耗时、用户 ID、IP。不记录请求体和 `Authorization` 头。
- `audit_log` 表记录以下操作：注册、登录成功与失败、改密码、重置密码、管理员操作、创建和撤销分享。

### 7.10 管理命令

> 2026-10-23 更新：命令行管理已改为网页管理后台 `/admin/`（`server/internal/webadmin`、`internal/httpapi/admin.go`），下面的子命令已移除。用法见 `docs/server-ops.md`"管理后台"一节。

```
moefrp-server serve           -c config.toml
moefrp-server migrate         -c config.toml
moefrp-server import-legacy   -c config.toml --from users.db [--dry-run]
moefrp-server backup          -c config.toml --to /path/backup.db

moefrp-server admin invite create  [-n 5] [--note "给 xxx"]
moefrp-server admin invite list    [--unused]
moefrp-server admin invite revoke  FRPT-XXXX-XXXX
moefrp-server admin user list
moefrp-server admin user set-role  <昵称> user|vip|admin
moefrp-server admin user disable   <昵称>
moefrp-server admin user enable    <昵称>
moefrp-server admin user reset-code <昵称>   # 打印一次性重置码，24 小时有效
moefrp-server admin user kick      <昵称>    # 吊销该用户的所有会话
```

- 用 SSH 密钥登录服务器后执行，命令直接操作数据库，不需要先用管理员账号登录 API。
- 这套命令取代 `generate_invite_code.py`。旧脚本里调用的 `/api/admin/set_user_role` 在旧服务端上根本不存在。
- 也取代 `invite.py` 那种"用明文 root 密码 SSH 上去跑脚本"的做法。

### 7.11 部署与运维

**构建**

```
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=<版本>" ./cmd/moefrp-server
```

**配置示例**（`deploy/config.example.toml`）

```toml
listen          = "127.0.0.1:8080"
db_path         = "/var/lib/moefrp/moefrp.db"
trusted_proxies = ["127.0.0.1/32", "::1/128"]
single_session  = true
session_ttl     = "720h"

[client]
latest_version = "3.0.0"
min_version    = "3.0.0"
download_url   = "https://frplus.loli-con.cn/download/MoeFrp-3.0.0-windows-amd64.exe"
sha256         = ""
announcement   = ""

[[client.image_sources]]
url    = "https://umi.li/random?dir=lucky&type=img"
weight = 5
is_api = false
```

**systemd**（`deploy/moefrp.service`，要点）

- `User=moefrp`、`StateDirectory=moefrp`
- `ExecStart=/usr/local/bin/moefrp-server serve -c /etc/moefrp/config.toml`
- 加固选项：`NoNewPrivileges=yes`、`ProtectSystem=strict`、`ProtectHome=yes`、`PrivateTmp=yes`
- `Restart=on-failure`

**Caddy**

```
frplus.loli-con.cn {
    reverse_proxy 127.0.0.1:8080
}
```

**备份**：用 systemd timer 每天执行一次 `moefrp-server backup`（内部是 `VACUUM INTO`），保留 14 天，另外再拷一份到别的机器。

**升级流程**：备份 → 替换二进制 → `migrate` → 重启 → 检查 `/healthz`。

### 7.12 旧数据迁移与切换

**导入命令**：`moefrp-server import-legacy --from users.db [--dry-run]`

| 旧表 | 新表 | 转换方式 |
|---|---|---|
| `users` | `users` | `uuid` 转为 `id`；`password_hash` 原样保留。旧哈希是 argon2-cffi 生成的 PHC 字符串，参数 m=65536, t=3, p=2（见旧 `server.py`），Go 的 `argon2.IDKey` 用同样的参数可以直接验证。`role` 原样保留 |
| `personal_configs` | `profiles` | 解析 `config_json`，按附录 C 转换字段，并为每个节点、代理生成稳定 ID |
| `shares` | `shares` | `is_template=1` → `template`；否则，如果只含 stcp/xtcp 代理，并且有订阅者带了 `visitors` 参数 → `p2p`；其余 → `full` |
| `subscriptions` | `subscriptions` | `user_params_json` 按类型转换：`visitors` → p2p 参数；`proxies` 和 `node_remark` → template 参数 |
| `invitation_codes` | `invite_codes` | 原样 |
| `password_reset_tokens` | 不迁移 | |
| 会话 | 不迁移 | 用户需要重新登录 |

导入完成后输出统计：用户数、配置数，以及无法解析的条目。务必先用 `--dry-run` 跑一遍。

**切换步骤**（停机约 10 分钟）

1. 提前发布新客户端和公告。
2. 停止 Flask，备份 `users.db`。
3. 先 `import-legacy --dry-run`，确认无误后正式导入。
4. 启动新服务，Caddy 改为指向新服务。旧路径 `/api/*` 由升级提示统一返回 426。
5. 旧客户端：已登录的会话在下一次会话检查失败后被强制下线，旧客户端会随之关闭；用户重新打开并登录时会看到"请下载新版"。
   - 旧客户端登录分两步：先请求挑战码，再请求 `/api/login`。如果第一步就失败，它只会显示写死的"获取挑战码失败"（旧 `MainWindow.handle_login`）。所以升级提示要让 `get_challenge` 返回 200 和一个假挑战码，让 `/api/login` 返回 426。
   - 旧客户端会把 `/api/login` 响应里的 `error` 字段原样弹出来（见旧 `client/api/base.py`），这样用户就能看到下载地址。
6. 观察一周，没有问题后归档旧库和旧代码。

---

## 8. 安全模型

### 8.1 保护什么，不保护什么

| 资产 | 保护方式 |
|---|---|
| 用户密码 | 只在登录时经 HTTPS 传输；服务端用 argon2id 存储；客户端不保存 |
| 会话令牌 | 客户端存在凭据管理器里；服务端只存哈希；可以吊销 |
| 云端配置（含节点 token） | 只返回给本人；传输走 HTTPS；本机缓存用 DPAPI 加密 |
| 分享 | ID 是 128 位随机数；预览不含机密；运行数据只给订阅者 |
| 邀请码、重置码 | 一次性使用；重置码存哈希，24 小时过期 |

**明确不防的情况**

- 用户查看或修改自己的客户端。客户端运行在用户的机器上，任何"客户端秘密"都能被提取出来，所以根本不放。
- 订阅者拿到节点 token（原因见 7.6）。
- 用户用改过的客户端调用 API。服务端按普通用户的权限处理，这就足够了。

### 8.2 动手重写前必须先做的事（与代码无关）

1. 修改 `invite.py` 里那台服务器的 root 密码，改为 SSH 密钥登录并禁用密码登录，然后删除 `invite.py`。
2. 确认 `server/adwd.py` 里的私钥和 `client/key.txt` 里的密钥对是做什么用的。仍在使用的全部更换。
3. 清理线上 `server.log` 里的会话令牌和一次性 token。旧版把它们写进了日志。
4. 给 `gogogo` 执行 `git init`，`.gitignore` 排除 `*.db`、`*.exe`、`*.dll`、`*.7z`、`config.toml`、`node_modules`、`frontend/dist`。仓库里不放任何密码和密钥。

---

## 9. 可选的后续功能

### 9.1 官方节点 + 按人授权（frps 插件）

在 frp 源码中确认过的事实：

- frps 的 HTTP 插件支持这些操作：`Login`、`NewProxy`、`CloseProxy`、`Ping`、`NewWorkConn`、`NewUserConn`（`pkg/plugin/server/manager.go`）。
- `Login` 请求里带有客户端的 `metas` 和 `client_address`（`pkg/plugin/server/types.go`）。
- 插件的 `Login` 检查发生在 `RegisterControl` 校验 token 之前（`server/service.go`）。

方案：

- 自己的 frps 节点配置 `httpPlugins`，指向 `moefrp-server` 的 `/frps-plugin` 接口。
- 客户端登录节点时，在 `metadatas` 里带上服务端给该用户签发的节点凭证。
- 服务端在 `Login` 时校验这个凭证；在 `NewProxy` 时校验端口范围和代理数量，并且可以按 user / vip 角色给不同的额度。
- 节点的 frps token 可以设成公开值，真正决定准入的是插件。这样分享官方节点时不用再交出真正的凭证，撤销也立即生效。

工作量中等，等 v1 稳定后再做。

### 9.2 frp 子进程模式

- 同一个 exe 加参数启动自己，作为 frp 工作进程（`MoeFrp.exe --frp-worker`），主进程和它之间通过命名管道传 JSON。
- 好处：
  - frp 内部 panic 不会带崩界面。
  - 每个工作进程有独立的全局状态，所以可以安全地**同时运行多个配置**，解决 6.3.7 的限制。
  - 仍然是单个文件。
- 代价：多一层进程间通信。等真的需要多配置并行时再做。

### 9.3 自动更新

下载新版 → 校验 sha256 和签名 → 下次启动时替换 exe。

### 9.4 Web 管理后台

把 7.10 的管理命令做成网页。

---

## 10. 里程碑

规模用 S / M / L 粗略表示。每个阶段都有明确的验收标准，**上一阶段验收通过后再进入下一阶段**。

### Phase 0：技术验证 ✅ 已完成（2026-10-18）

报告：`spike/REPORT.md`。结论是继续用 Wails v3，锁定 `v3.0.0-beta.31`，不需要改用 v2.17。

| 验证项 | 结果 |
|---|---|
| 单个 exe；托盘图标和菜单；单实例；关闭到托盘 | 通过（在真实 exe 上自动化验证） |
| 进程内 frps + 按 6.3.2 流程启动，tcp 代理真实转发 | 通过 |
| `WithOutput` 截获日志，识别 login success | 通过（启动后约 20ms） |
| `GetProxyStatus` 读到 running | 通过 |
| `UpdateConfigSource` 增删代理不重连 | 通过（runID 不变，frps 侧没有新的登录） |
| `GracefulClose` 后 Run 返回、端口释放 | 通过 |
| token 错误时 Run 返回错误 | 通过 |
| 引擎接入界面：按钮启停、状态、日志纯文本渲染 | 通过 |
| exe 体积、启动时间 | 18.2MB；热启动约 430ms |
| clipboard 不需要 cgo | 通过 |
| 缺少 WebView2 | 已查清：会静默退出，需要自己做预检（见 6.11）。本机模拟通过，干净虚拟机未测 |
| Defender 扫描 | **未能验证**：本机 Defender 服务是停着的 |

**留到后面做的**

- Defender 扫描、干净 Win10 虚拟机上的 WebView2 缺失实测：并入 Phase 6 的发布清单。
- `@wailsio/runtime` 改成 beta.31：等本机 pnpm 的等待期过了再改。
- spike 里可以直接复用的代码：`engine/`（引擎和集成测试）、`app-engine/webview2check.go`、`app-engine/main.go` 中托盘、单实例、关闭到托盘的写法，以及 `scripts/` 里的自动化验证脚本。

### Phase 1：shared + 服务端核心（M）

- shared：数据模型、API 结构体、错误码、校验，全部带单元测试。
- 服务端：
  - 配置读取、SQLite 和迁移。
  - 注册、登录、退出、me、改密码、重置密码，以及会话管理、单设备模式、限流和锁定。
  - 配置 CRUD、`state` 接口（含 ETag）。
  - 管理命令：`invite`、`user`。
  - 旧接口的升级提示、slog 日志。
- **验收**：
  - `go test ./...` 全部通过，包括并发抢同一个邀请码、被挤下线、版本冲突等用例。
  - 用旧 Python 服务端生成的 argon2 哈希能登录成功。
  - 能用二进制直接部署到测试服务器。

### Phase 2：客户端引擎 + 不登录也能用（M–L）

- 引擎完整实现：8 种代理类型和 3 种访客类型的映射、校验、状态机、状态轮询、热更新、日志。
- 本地存储：`settings.json` 和 DPAPI 加密的缓存。
- 界面：配置编辑（节点、代理、访客）、运行栏、日志面板、测速。
- **验收**：
  - 不连服务器，新建一个本地配置就能启动并成功转发。
  - 运行中修改代理会热更新，连接不断。
  - 停止后干净退出，退出程序时 frp 也随之停止。
  - 代理名重复、端口被占用这类错误能在界面上看到明确原因。

### Phase 3：账号与云同步（M）

- 客户端实现登录、注册、重置密码、改密码的界面。
- 令牌存入凭据管理器，支持自动登录。
- 处理 `session_replaced` 和 `client_outdated`。
- 配置的双向同步、冲突弹窗、离线模式。
- **验收**：
  - 两台机器登录同一个账号：在 A 上修改后，B 在 60 秒内同步到。
  - 两台机器同时修改同一个配置时会弹出冲突提示。
  - 开启单设备模式时，在 B 上登录会把 A 挤下线，并且 A 看到明确的提示。

### Phase 4：分享与订阅（M）

- 服务端：分享的增删改、重新发布、撤销、过期和人数上限；预览；订阅；`runtime`。
- 客户端：创建分享向导、我的分享、添加订阅（含预览）、P2P 访客设置、运行订阅。
- **验收**：
  - 用两个账号把 template、full、p2p 三种类型各走通一遍。
  - 撤销后，订阅方在下一次同步时停止隧道，并显示"已撤销"。

### Phase 5：体验完善（M）

- 托盘、开机自启、自动连接、主题、网络代理设置。
- 图片面板、更新检查、"关于"页和开源许可。
- 崩溃日志；所有错误码都有对应的中文提示。

### Phase 6：迁移与发布（S–M）

- 用旧库的脱敏副本反复跑 `import-legacy --dry-run`，直到没有无法解析的条目。
- 代码签名，向 Microsoft 提交误报申诉，在干净的虚拟机上把发布清单走一遍。
- 补做 Phase 0 留下的两项：在开着 Defender 的机器上扫描；在没装 WebView2 的干净 Win10 虚拟机上确认会弹出提示框。
- 按 7.12 的步骤切换，并观察一周。

---

## 11. 测试策略

**shared**：校验规则；模型与 JSON 的往返转换。

**engine**

- 映射单元测试：每种代理和访客类型，从模型转成 frp 结构体后，都要能通过 `ValidateAllClientConfig`。
- 集成测试：测试里用 frp 的 `server` 包在本机起一个 frps，让引擎连上去，验证以下几点：
  - 状态机按 Connecting → Running 正常转换。
  - tcp 代理能真实转发。
  - 热更新增删代理时不重连。
  - 停止后端口被释放。
  - token 错误时 `Run` 返回错误。
  - stcp：用 frp 自带的 `client` 包再起一个客户端作为访问端。
  - Phase 0 发现的几种边界情况：
    - 拨号中途停止时，`Run` 返回 `<nil>` 错误，引擎不能报成登录失败。
    - 代理重名必须被拦下来，不能被静默覆盖。
    - 远程端口冲突时，状态里出现 `port already used`。
    - Stopping 期间状态轮询不阻塞对外接口。

  **每次升级 frp 版本，这组测试都必须通过。** 日志字符串变了、接口变了，都会在这里暴露。起点是 `spike/engine/engine_test.go`（8 个测试，跑一遍约 20 秒）。
- `go test -race` 在你这台机器上跑不了：测试用的 exe 一打开就报 `Access is denied`，疑似被安全软件拦截。竞态检测放到另一台机器或 CI 上跑。正式产物用 `CGO_ENABLED=0` 构建，不受影响。

**server**

- 用 `httptest` 覆盖每个接口的正常路径和错误路径。
- 专项用例：并发注册抢同一个邀请码、单设备挤下线、版本冲突。
- 旧 argon2 哈希兼容：用 Python argon2-cffi 生成的哈希做测试数据。
- 旧库导入：用脱敏后的旧库副本测试。

**前端**：用 vitest 测 store 和错误码映射。

**发布前人工检查清单**

- 在干净的 Win10 和 Win11 虚拟机上首次运行。
- Defender 扫描、签名校验。
- 单实例、托盘、开机自启。
- 断网时用缓存的配置启动隧道。

---

## 12. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| Wails v3 还是 beta | 可能遇到框架 bug | Phase 0 已验证所需功能全部可用；锁定 beta.31，不跟着每个 beta 升级。备选 Wails v2.17 + 第三方托盘库，Go 业务代码不受影响 |
| 升级 Wails 后托盘菜单失效 | 托盘右键没反应 | Wails 源码注释表明托盘菜单靠处理 `WM_CONTEXTMENU` 才能打开，是最近的 beta 才修好的。每次升级 Wails 都用 `spike/scripts/test-tray.ps1` 回归测试 |
| 缺少 WebView2 时静默退出，`ErrorHandler` 收不到错误（Wails bug） | 用户双击没反应 | 在 `app.Run` 之前自己检查注册表并弹框（见 6.11） |
| WebView2 内存占用大（总工作集约 366MB） | 常驻托盘显得笨重 | Phase 5 试做隐藏到托盘时释放窗口（见 6.8） |
| 停止时 `Run` 要等拨号结束（最长约 5 秒） | 换节点时卡顿；旧 Service 的日志被误判成新的 | 界面增加"停止中"状态，等旧 Run 返回后再启动新的 Service（见 6.3.5） |
| frp 内部接口随版本变化 | 升级时需要改代码 | 锁定版本，升级前看 changelog，集成测试兜底。例如 `ServiceOptions` 已经从直接传代理列表改成了 `ConfigSourceAggregator` |
| 杀毒软件误报（frp 的代码就在主程序里） | 整个客户端被隔离 | 代码签名 + 提交误报申诉；不加壳、不混淆；最坏情况下用 9.2 的子进程模式缩小影响范围 |
| frp 内部 panic | 整个应用崩溃 | 记录崩溃日志；必要时采用 9.2 的子进程模式 |
| 用户机器缺少 WebView2（老版本 Win10、精简版系统） | 无法显示界面 | Phase 0 确定处理方式，并写进发布说明 |
| frp 全局状态 | 无法多开 | v1 只运行一个 Service；多开走 9.2 |
| 连接状态依赖日志字符串 | frp 升级后可能识别不出状态 | 集成测试覆盖；代理状态用的是正式接口，不受影响 |
| SQLite 写入并发 | 偶发"数据库被锁" | WAL 模式 + `busy_timeout`；单进程写入；用户量大了再考虑别的数据库 |
| 旧数据格式五花八门 | 导入时部分条目失败 | 多跑几次 dry-run，无法解析的条目单独列出来人工处理 |

---

## 13. 需要你决定的事

括号里是我的建议。

1. **不登录能不能使用本地配置？**（能。配置本来就在本地运行，账号只提供同步和分享。）
2. **单设备登录是否保留？**（保留，做成配置项，默认开启，和旧版行为一致。）
3. **新旧版本怎么切换？**（一次性切换，旧客户端统一收到"请升级"提示。新旧两套服务并行会导致两份数据各改各的。）
4. **图片面板是否保留、默认开不开？**（保留，默认开启，图源由服务端下发。）
5. **产品名、域名、版本号**：是否继续用"萌！FRP 高级客户端"、`frplus.loli-con.cn`，新版本号从 3.0.0 开始？
6. ~~能否接受 Wails v3 beta？~~ **已定**：Phase 0 验证通过，用 Wails v3，锁定 `v3.0.0-beta.31`。
7. **代码签名证书的预算**：不签名的话，杀毒软件误报和 SmartScreen 警告都会明显更多。
8. **是否要做官方节点**（9.1）？会影响分享功能的长期设计。
9. **本地 frp 用哪个版本**：切到正式的 `v0.71.1` 标签，还是继续跟 `dev` 分支？（建议用正式 tag。）

---

## 附录 A：旧功能对照

| 旧功能 | 新版 | 备注 |
|---|---|---|
| 注册（邀请码） | 保留 | 修复并发重复使用同一邀请码的问题 |
| 登录 / 记住密码 | 登录 / 保持登录 | 不再保存密码 |
| 忘记密码（管理员给令牌） | 保留 | 改为服务器命令行生成重置码 |
| 修改密码 | **新增** | 旧版没有这个功能 |
| 单设备登录、被挤下线提示 | 保留 | 改用 `session_replaced` 错误码 |
| 角色 user / vip / admin | 保留 | 新增管理命令 `set-role`（旧版的这个接口不存在） |
| 多个云端配置（节点表 + 代理表） | 保留 | 新增访客表 |
| 代理启用/停用开关 | 保留 | 直接映射到 frp 原生的 `enabled` 字段 |
| 节点测速 | 保留 | |
| 后台每 30 秒同步 | 改为 60 秒 + ETag + 版本号 | 不会再覆盖本地编辑 |
| 创建分享（模板 / 完整） | 保留，并新增 p2p 类型 | 类型显式保存 |
| 管理我的分享（撤销、复制 ID） | 保留 | 新增订阅人数、过期时间、重新发布 |
| 订阅分享、P2P 访客设置 | 保留 | P2P 支持多个目标 |
| 启动 / 停止隧道、美化日志 | 保留 | 状态来自 frp 正式接口，不再靠正则 |
| 运行时锁定配置 | 改为热更新 | |
| 网络代理（直连 / 系统 / 自定义） | 保留 | 新增节点级别的 `proxyURL` |
| 跟随系统深浅色 | 保留 | 新增手动选择 |
| 图片面板（刷新、复制、另存、查看原图） | 保留，可关闭 | 图源改由服务端下发 |
| 版本过低提示 | 保留 | 改为 `client_outdated`，不影响本地启动 |
| 托盘 | **新增** | 旧版其实没有托盘（旧代码里没有 `QSystemTrayIcon`） |
| P2P 虚拟局域网 | 删除 | 旧版从未跑通 |

## 附录 B：旧接口 → 新接口

| 旧接口 | 新接口 |
|---|---|
| `POST /api/login/get_challenge` | 删除 |
| `POST /api/login` | `POST /api/v1/auth/login` |
| `POST /api/register` | `POST /api/v1/auth/register` |
| `POST /api/session/check` | 合并进 `GET /api/v1/state`；另有 `GET /api/v1/me` |
| `GET /api/configs` | `GET /api/v1/state` |
| `POST /api/configs`（整体删除再全部插入） | 按单个配置操作：`POST`、`PUT`、`DELETE /api/v1/profiles/{id}` |
| `POST /api/share/create` | `POST /api/v1/shares` |
| `GET /api/share/list` | `GET /api/v1/shares` |
| `POST /api/share/revoke` | `DELETE /api/v1/shares/{id}` |
| `GET /api/share/get_public_info/{id}`（无需登录，返回 token） | `GET /api/v1/shares/{id}/preview`（需要登录，不返回 token） |
| `POST /api/share/subscribe` | `POST /api/v1/subscriptions` |
| `POST /api/share/use`（无需登录，返回 token） | `GET /api/v1/subscriptions/{id}/runtime`（只返回给订阅者） |
| `POST /api/request_config_ticket` | 删除 |
| `GET /api/get_temp_config/{id}` | 删除 |
| `POST /api/initiate_password_reset` | `moefrp-server admin user reset-code` |
| `POST /api/perform_password_reset` | `POST /api/v1/auth/reset-password` |
| `/api/admin/set_user_role`（旧服务端上不存在） | `moefrp-server admin user set-role` |

## 附录 C：旧配置字段 → 新字段

**节点**（旧 `config_json.nodes[]`）

| 旧字段 | 新字段 |
|---|---|
| `remark` | `remark` |
| `server_addr` | `serverAddr` |
| `server_port`（可能是字符串） | `serverPort`（int） |
| `token` | `token` |
| 无 | `id`（导入时生成） |

旧数据里还有单节点的写法（顶层的 `serverAddr`、`serverPort`、`auth.token`），也要兼容，参考旧 `CreateShareDialog.populate_nodes` 的处理。

**代理**（旧 `config_json.proxies[]`）

| 旧字段 | 新字段 |
|---|---|
| `enabled`（缺省为 true） | `enabled` |
| `name` | `name`（旧版被 bleach 转义过的 `&amp;` 要还原成 `&`） |
| `type` | `type` |
| `local_ip` | `localIP` |
| `local_port`（字符串） | `localPort`（int） |
| `remote_port`（字符串，可能为空） | `remotePort`（int，0 表示不指定） |
| `custom_domains`（逗号分隔的字符串） | `customDomains`（数组） |
| `sk` | `secretKey` |
| `stun_server` | 不迁移。frp 的 STUN 服务器是全局设置（`natHoleStunServer`）；有需要的话以后再作为节点级设置加回来 |
| 无 | `id`（导入时生成） |

**P2P 订阅参数**（旧 `user_params.visitors[]`，字段已经是 camelCase）

| 旧字段 | 新字段 |
|---|---|
| `name` | `name` |
| `type` | `type` |
| `serverName` | `serverName` |
| `secretKey` | `secretKey` |
| `bindAddr` | `bindAddr` |
| `bindPort` | `bindPort` |

**模板订阅参数**（旧 `user_params`）：`proxies[]` 按上面的代理规则转换；`node_remark` 按备注找到对应的节点 ID，保存为 `nodeId`。

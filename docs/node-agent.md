# 节点 agent 设计（管理后台管理 frps 节点）

目标：在管理后台里添加、配置、监控、升级、卸载 frps 节点，像 komari 管理探针一样，但卸载要干净，升级要可回滚。

已确定的范围：

| 项 | 决定 |
|---|---|
| 管理对象 | frps 节点（客户端连接的服务器） |
| 谁能管 | 只有管理员 |
| 权限 | 固定操作 + 以 root 执行任意命令 |
| 系统 | Linux systemd，amd64 / arm64 |

参考 komari / komari-zig-agent 的做法，并改掉它的几个问题：令牌明文存库、令牌出现在命令行参数和 URL 里、面板删除节点后 agent 还在机器上跑、没有卸载命令、卸载有残留。

## 0. 使用

1. 部署面板。agent 程序从 GitHub Release 下载（见第 6 节），面板本身不带。节点访问面板的地址和管理员打开后台的地址不一样时（例如后台走内网），在配置里设 `public_url`。
2. 管理后台 → 节点 → 添加节点，复制安装命令，在 VPS 上执行：

   ```bash
   curl -fsSL https://<面板域名>/agent/install.sh | sudo sh -s -- MFN-XXXX-XXXX-XXXX
   ```

3. 节点上线后自动下发默认 frps 配置（端口 7000）。在节点的"frps 配置"页用表单修改；节点没有 token，只接受 MoeFrp 客户端用面板签发的登录票据连接（第 12 节）。新节点只给管理员用，在"设置"里勾选"开放给普通用户"后用户才能看到。

代码：`agent/`（节点程序）、`server/internal/nodes`（面板侧）、`server/internal/httpapi/nodes.go`（接口）、`shared/agentproto`（协议）。CI 冒烟测试 `scripts/ci/agent-smoke.sh` 在 GitHub 的 Linux 机器上真实安装、升级、卸载并检查残留。

## 1. 组成

```
管理后台（浏览器）
   │  /api/v1/admin/nodes/*（cookie + X-MoeFrp-Admin）
   ▼
moefrp-server ──────── WebSocket /api/v1/agent/ws（agent 主动连，Bearer 节点令牌）
   │  提供 /agent/install.sh（写好下载地址和校验和）
   ▼
moefrp-agent（节点上，root，systemd 服务）←── 程序从 GitHub Release 下载（agent-v* 标签）
   └─ 进程内嵌 frps v0.71.1（和客户端内嵌 frpc 一样）
```

- **agent 是单个静态二进制**，内嵌 frps，节点上不需要另装 frp。frps 跑在 agent 进程里，改配置时进程内关掉旧的 `Service` 再起新的，agent 和面板的连接不断（已确认 v0.71.1 的 `Service.Close()` 会释放所有监听端口）。
- **只由 agent 主动连面板**，节点不需要开放额外端口。断线后指数退避重连（1s → 60s，带抖动）。
- **agent 程序发布在 GitHub Release**，和面板分开发版：推送 `agent-v1.2.3` 标签时，`.github/workflows/agent-release.yml` 测试、编译 linux-amd64 / linux-arm64，连同 `SHA256SUMS` 发布到 Release（不标记为 Latest，`releases/latest` 留给客户端）。
- 面板查询仓库里标签以 `agent-v` 开头、版本号最大的正式 Release（草稿和预发布不算），结果缓存 10 分钟；查询失败时继续用上次的结果，1 分钟后重试。校验和取自同一个 Release 的 `SHA256SUMS`，由面板写进安装脚本和升级请求，节点下载后核对。
- 连不上 GitHub 的机器：**每个节点可以设自己的下载代理**（例如 `https://ghproxy.example.com/`），下载地址变成"代理 + GitHub 地址"。添加节点时填写，安装命令变成 `curl -fsSL 'https://面板/agent/install.sh?mirror=<代理>' | sudo sh -s -- <安装码>`，脚本里的下载地址带上代理；之后升级也经过它。节点"设置"里可以改掉或清空（直连）。配置里的 `agent_mirror` 只是添加节点时的默认值。校验和仍由面板从 GitHub 获取，代理改不了内容。
- 完全离线：配置 `agent_dist_dir`，把 `moefrp-agent-linux-amd64`、`moefrp-agent-linux-arm64` 和 `VERSION` 放进去，面板改从 `/agent/download/*` 提供程序（CI 冒烟测试就是这样用的）。

## 2. 添加节点和认证

1. 管理员在"节点"页点"添加节点"，填名称。服务端生成：
   - **节点 ID**；
   - **一次性安装码**（24 小时有效，只能用一次，库里只存哈希）。
2. 页面给出安装命令：

   ```bash
   curl -fsSL https://<面板域名>/agent/install.sh | sudo bash -s -- <安装码>
   ```

   安装码只用于第一次注册，泄露在 shell 历史里也只是一个已失效的码。
3. agent 第一次启动时用安装码换取**长期节点令牌**（256 位随机数），写进 `/etc/moefrp-agent/agent.toml`（0600，root）。服务端只存令牌的 SHA-256。
4. 之后每次连接用请求头 `Authorization: Bearer <令牌>`，不放 URL、不放命令行参数，所以不会出现在 `ps`、反代日志里。比较用常量时间。
5. **令牌绑定机器**：agent 上报机器指纹（`/etc/machine-id` 的哈希；读不到时生成一个随机 ID 存在 `/var/lib/moefrp-agent/machine-id`），面板在第一次上线时记下。令牌被拷到别的机器上用，面板拒绝连接（原来的节点不受影响）。
6. 管理员可以"重置令牌"（生成新的安装码，旧令牌立即失效，同时清掉记下的机器指纹），用于令牌泄露或机器重装、换机器。

## 3. 协议

WebSocket（`github.com/coder/websocket`），JSON 文本帧，双向请求/响应：

```json
{"id": "...", "type": "request|response|event", "method": "...", "data": {...}, "error": {...}}
```

| 方向 | 方法 | 说明 |
|---|---|---|
| agent → 面板 | `hello` | 连接后第一条：agent 版本、系统、架构、内核、主机名、开机时间、机器指纹、公网地址（1.4.1 起网卡上的；1.4.5 起先放探测到的出口地址，`report` 里也带，变了面板就更新）、支持的功能、当前 frps 配置的版本号 |
| agent → 面板 | `report` | 每 3 秒（agent 1.2.0 起；之前是 10 秒）：CPU、内存、磁盘、负载、网络流量；frps 状态（运行 / 出错 + 原因）、客户端数、代理数、连接数、累计流量 |
| 面板 → agent | `store.lease` | 本地加密存储的密钥和租约，见第 4 节 |
| 面板 → agent | `access.sync` | 授权表（票据密钥、能用的用户和限额、已撤销的会话），见第 12 节 |
| 面板 → agent | `frps.apply` | 下发 frps 配置（结构化，见第 4 节），agent 校验 → 进程内重启 frps → 失败自动回滚到旧配置，结果原样返回 |
| 面板 → agent | `frps.restart` / `frps.stop` | |
| 面板 → agent | `frps.proxies` | 当前在线的代理列表（名称、类型、用户、客户端、流量） |
| 面板 → agent | `logs` | 最近 N 行日志（agent 和 frps，内存环形缓冲） |
| 面板 → agent | `exec` | 以 root 执行命令，见第 5 节 |
| 面板 → agent | `upgrade` | 升级到面板上的版本，见第 6 节 |
| 面板 → agent | `uninstall` | 自卸载，见第 7 节 |

面板对每个节点只保留一条连接；新连接顶掉旧连接。

断线判定（agent 1.3.1 起，适应丢包严重的跨境线路）：

- agent 每 15 秒 ping 一次面板，**45 秒**没有回应才断开重连（之前 15 秒，丢包时 TCP 重传一卡就超时，把还活着的连接断掉，重连又因 TLS 握手超时失败）。心跳单独跑，不耽误每 3 秒的上报。重连时 TLS 握手最长等 30 秒、整个建连最长 45 秒。
- 面板 **90 秒**收不到任何消息就断开连接。
- 连接断开后 **60 秒**内重连上，管理后台照样显示在线（标为"重连中"），上线时间接着算；超过 60 秒才显示离线。
- 管理后台显示每个节点最近 24 小时的断线次数（只在内存里统计，面板重启后重新计数），用来看节点稳不稳。

## 4. frps 配置（全部托管）

节点都由面板托管：**frps 不设 `auth.token`**，只认面板签发的一次性登录票据（第 12 节）；**节点上没有明文配置**，frps 配置、授权表、准入状态都加密保存。需要 agent 1.4.0 以上，旧版本的节点上线时提示升级，配置先保存，升级后自动生效。

### 结构化配置

- 管理后台不再编辑 TOML。配置项定义在 `shared/frpsset`：每个字段用 `ui` 标签写一次分组、名称、说明、取值范围，后台按 `GET /api/v1/admin/frps-schema` 自动生成表单（基础 / 传输协议 / HTTP·HTTPS 虚拟主机 / 高级），agent 在内存里按字段拼出 frps 的配置。
- 加一个配置项：在 `Settings` 里加字段和 `ui` 标签，在 `Validate` 里加必要的检查，在 agent 的 `frpsrv.ServerConfig` 里映射。删掉一项同理。
- 面板先校验（取值范围、TCP 端口不能重复、KCP 和 QUIC 端口不能相同、子域名后缀要开 HTTP / HTTPS 端口等），出错时标红对应的输入框，不下发；agent 再用 frp 自己的 `Complete` + `validation.ValidateServerConfig` 检查一遍。
- 面板保存每个节点的**期望配置**（JSON）和版本号；agent 上报**实际生效的版本号**，两者不一致时节点显示"配置未同步"，节点上线时自动重新下发。
- 生效顺序：校验 → 关掉旧 frps → 用新配置启动 → 启动失败（例如端口被占用）就用旧配置重新启动，返回"已回滚"和原因。成功才保存。
- **没有准入插件绝不启动 frps**：节点没有 token，不挂插件谁都能连。授权表还没下发（新节点第一次上线、本地存储被锁）时 frps 不启动，节点显示原因。
- 不支持 `webServer`（frps 自带的 dashboard）：frp 每次打开 dashboard 都会再注册一份内存统计，进程内重启几次后数字会翻倍；状态和代理列表改在面板上看。
- 升级：面板启动时把旧的 TOML 配置转换成结构化配置（认得的项保留，`auth.token` 等丢掉）；agent 1.4.0 在收到新配置之前继续用旧的 `frps.toml` 运行，收到后删除它。原来没开"官方节点"的节点改成只给管理员用。

### 本地加密存储（`agent/internal/vault`）

两层密钥：

- **存储密钥** = `HMAC(面板主密钥, 节点 ID + 机器指纹)`，由面板通过 `store.lease` 下发，加密各个数据文件（AES-256-GCM，`/etc/moefrp-agent/*.enc`）。
- **封存密钥** = `SHA-256(机器指纹 + agent 令牌)`，本机算出，加密"存储密钥 + 租约到期时间 + 时间高水位"，存在 `/etc/moefrp-agent/vault.seal`。

租约（兼顾离线可用和防拷走）：

- 面板每次连上都下发租约（**7 天**），在线节点每 6 小时续一次。节点重启时面板离线，租约没过期就照常解开、启动 frps；过期后保持锁定，等面板连上重新下发。
- 时间一律用 Unix 秒（UTC，和时区无关）。面板同时发来自己的当前时间，agent 按面板时间和本机时间的差换算到期时间，本机时钟快了慢了、时区设错都不影响租约长短。
- 高水位记下见过的最晚本机时间（每小时更新），把系统时间往回调超过 10 分钟会被发现，存储保持锁定。
- 存储密钥变了（轮换主密钥、换了机器）时，能解开的文件用新密钥重新加密；解不开的删掉，等面板重新下发。

能防：配置和授权表被拷走、备份泄露、令牌和数据拷到别的机器（机器指纹不同，面板拒绝连接，本机也解不开）、租约过期后的离线使用。防不了：这台机器上有 root 的人（他能读到机器指纹和令牌，也能读进程内存）。

### 面板主密钥

- 节点票据密钥和存储密钥都由面板主密钥（`settings.node_cred_key`，第一次用时生成）派生，一个节点的密钥泄露不影响其他节点。
- **数据库里只存密文**（`server/internal/nodes/secret.go`）：主密钥用"密钥加密密钥"（KEK）以 AES-256-GCM 加密后保存，KEK 不在数据库里——来自配置 `secret_key` / 环境变量 `MOEFRP_SECRET_KEY`（base64 的 32 字节），或者 `secret.key` 文件（`secret_key_file`，默认在数据库旁边，第一次启动时生成，0600）。这样数据库备份、迁移前的快照、管理后台下载的备份泄露出去，也拿不到主密钥。旧版明文保存的主密钥在第一次读到时自动改成加密保存（密钥不变）。
  - **secret.key 要和数据库一起备份**。丢了的话主密钥解不开，"密钥"弹窗里会说明原因，只能轮换一个新的（所有节点自动换用新密钥，在线用户重连一下）。
  - 想让数据库和 KEK 分开存放：把 `secret_key_file` 指到别的目录，或者用环境变量注入 `MOEFRP_SECRET_KEY`。
- 节点页右上角"密钥"显示主密钥的**指纹**（`HMAC(主密钥, 固定串)` 的前 10 字节，按 base32 分组，例如 `K7QF-2M9X-AB3D-PQ7Z`，不暴露主密钥本身的任何哈希）、**版本**（每轮换一次加一）、上次轮换时间和保存方式。
- **轮换**在同一个弹窗里分步完成：说明 → 需要时输入密码（二次验证）→ 结果页并排显示"之前 / 现在"的指纹。轮换后立即给在线节点下发新的存储密钥和授权表，通过实时通道（`docs/client-live.md`）通知在线客户端丢掉旧票据、用新票据重连（实测不到 1 秒）；离线的节点上线时拿到新密钥。实时通道断开时，客户端在节点拒绝旧票据后也会马上丢掉这个节点预取的全部票据重新领，不用一张张试完。

## 5. 执行命令（root）

- 只有管理员能用，并且**每 15 分钟要重新输入一次密码**（二次验证，和登录共用失败锁定），防止 cookie 被偷后直接拿到所有节点的 root。卸载、删除、重置令牌也要二次验证。
- 命令以 `/bin/sh -c` 执行，root 身份，固定 `PATH`，工作目录 `/root`，超时默认 60 秒、最长 1 小时；输出最多保留 1 MB，超出截断。
- 每条命令写审计日志（操作者、节点、命令全文、退出码），结果保存在 `node_tasks` 表，页面可以回看。可以同时选多个节点批量执行。
- 安装时可以加 `--no-exec` 关掉这个功能（写进 agent 配置，面板改不了），给不想被远程执行命令的机器用。
- 网页终端（交互式 shell）放到第二期：需要把 xterm.js 打包进管理后台。

## 6. 升级

- 面板显示每个节点的 agent 版本，有更新的 Release 时提示"可升级"（都是语义化版本时按版本号比较，节点上的版本更新时不提示降级；开发版、CI 构建这类非正式版本只要不同就提示）。
- 发新版：改完 agent 代码后 `git tag agent-v1.2.3 && git push origin agent-v1.2.3`，等 Release 出来，面板 10 分钟内就会提示升级。
- 流程：
  1. 面板下发 `upgrade`，带上下载地址和 SHA-256。
  2. agent 下载到 `/var/lib/moefrp-agent/moefrp-agent.new`，校验哈希，执行 `moefrp-agent.new version` 预检。
  3. 当前二进制复制成 `moefrp-agent.bak`，新文件原子替换 `/usr/local/bin/moefrp-agent`，写升级状态文件，退出；systemd 自动拉起新版本。
  4. 新版本连上面板并成功上报后，删除 `.bak` 和状态文件。
  5. 如果新版本连续启动 3 次都没能连上面板，自动换回 `.bak` 并上报"升级失败，已回滚"。
- 升级期间 frps 会断开几秒（进程重启）。
- 面板请求升级后立即返回，下载在后台进行；结果在节点重新连上后写进任务历史。

## 7. 卸载（要干净）

agent 只会在机器上创建这些东西，卸载时全部删除：

| 路径 | 内容 |
|---|---|
| `/usr/local/bin/moefrp-agent` | 程序 |
| `/etc/moefrp-agent/` | `agent.toml`（令牌、面板地址）、`vault.seal` 和 `*.enc`（加密的 frps 配置、授权表、准入状态） |
| `/var/lib/moefrp-agent/` | 升级临时文件、`.bak`、状态文件、`machine-id`（读不到 `/etc/machine-id` 时） |
| `/etc/systemd/system/moefrp-agent.service` | systemd 服务 |

三种卸载方式，效果相同：

- **面板上点"卸载"**：面板下发 `uninstall`。agent 用 `systemd-run` 起一个临时单元执行 `moefrp-agent uninstall`（这样停服务时不会把卸载过程本身杀掉），完成后上报，面板删除节点并作废令牌。节点离线时，面板标记"待卸载"，下次上线时执行。
- **节点上执行** `sudo moefrp-agent uninstall`。
- **安装脚本**：`curl -fsSL https://<面板域名>/agent/install.sh | sudo bash -s -- --uninstall`（agent 二进制已经坏了时用）。

卸载步骤：停止并禁用服务 → 删除单元文件 → `systemctl daemon-reload` 和 `reset-failed` → 删除上表的目录和文件 → 最后删除程序自身。systemd 日志（journald）里的历史记录不会删：它是整个系统共用的日志，`journalctl --vacuum-*` 不能只清理一个服务（`-u` 对它无效），会按系统设置自动轮转。

面板上**只删除节点**（不卸载）时会作废令牌。agent 被拒绝后在日志里说明原因，frps 按最后的配置继续运行，之后每小时重试一次（进程退出会被 systemd 立刻拉起，没有意义）。

## 8. 安装脚本

`/agent/install.sh` 由面板提供（内容里写好面板地址），步骤：

1. 检查 root、systemd、架构（`uname -m`：x86_64 → amd64，aarch64 → arm64），其他情况直接报错退出。
2. 按架构选面板写好的下载地址（GitHub Release，或者镜像 / 本地目录）下载，用面板写好的 SHA-256 校验。面板拿不到 agent 版本时，脚本直接报出原因（卸载不受影响）。
3. 执行 `moefrp-agent install --server <面板地址> --code <安装码>`：换取令牌写入 `/etc/moefrp-agent/agent.toml`，程序装到 `/usr/local/bin/moefrp-agent`。
4. 写 systemd 单元（`User=root`、`Restart=always`、`RestartSec=3`、`LimitNOFILE=1048576`），`enable --now`。
5. 已经装过时：保留现有配置，只替换程序（相当于升级）；加 `--force` 才重新注册。

## 9. 数据库

```sql
CREATE TABLE nodes (
  id               TEXT PRIMARY KEY,
  name             TEXT NOT NULL,
  token_hash       BLOB,                -- 长期令牌的 SHA-256，注册前为空
  enroll_hash      BLOB,                -- 安装码的 SHA-256，用过即清空
  enroll_expires   INTEGER,
  settings         TEXT NOT NULL,       -- 期望的 frps 配置（frpsset.Settings 的 JSON；0007 起取代 frps_config）
  config_rev       INTEGER NOT NULL,    -- 期望配置的版本号，每次修改 +1
  machine_id       TEXT NOT NULL,       -- 第一次上线时记下的机器指纹，重置令牌时清空
  applied_rev      INTEGER,             -- agent 上报的已生效版本
  pending_uninstall INTEGER NOT NULL DEFAULT 0,
  info             TEXT,                -- 最近一次 hello 的 JSON（版本、系统、IP 等）
  last_seen        INTEGER,
  created_at       INTEGER NOT NULL
  -- 还有官方节点相关的列（roles、description 等，见 0005、0007 迁移）
);
CREATE TABLE node_tasks (
  id TEXT PRIMARY KEY, node_id TEXT NOT NULL, kind TEXT NOT NULL,  -- exec / upgrade / uninstall
  command TEXT, status TEXT NOT NULL, exit_code INTEGER, output TEXT,
  created_by TEXT, created_at INTEGER NOT NULL, finished_at INTEGER
);
```

实时监控数据（report）只放内存，不写库。

## 10. 管理后台

新增"节点"页：

- 列表：名称、在线状态、IP、agent 版本、frps 状态和端口、客户端数、流量、CPU / 内存、最后在线时间；"添加节点"弹出安装命令。
- 节点详情：
  - **概况**：系统信息、实时指标、frps 状态、连接信息（地址、端口；没有 token）；
  - **frps 配置**：按分组生成的表单，每项带说明；"默认值""还原""保存并应用"，显示同步状态；
  - **代理**：在线代理列表；
  - **日志**：agent 和 frps 日志；
  - **命令**：输入命令执行、看历史；
  - **操作**：重启 frps、升级 agent、重置令牌、卸载、删除。

## 11. 分期

| 期 | 内容 |
|---|---|
| 一（已完成） | agent（内嵌 frps、注册、WebSocket、上报、配置下发与回滚、日志、执行命令、升级与回滚、卸载）；服务端接口和表；安装脚本；管理后台节点页；agent 发布到 GitHub Release；CI 冒烟测试 |
| 二 | 网页终端；批量执行；节点流量历史图表 |
| 三（已完成） | 官方节点：面板下发给用户，按角色和单个用户授权，按角色限额（第 12 节） |
| 四（已完成） | 全部托管：节点没有 token、结构化配置、本地加密存储和租约、机器指纹和设备指纹（第 4、12 节） |

## 12. 官方节点（下发给用户）

所有节点都是托管节点，只接受 MoeFrp 客户端用登录票据连接。用户登录客户端后自动拿到自己能用的节点（下面叫官方节点），在运行栏里直接选，不用填地址和 token；客户端界面上不显示节点的地址和端口。客户端自己的 frpc 功能不受影响，照样可以连别的 frps。

### 管理员操作

1. 节点 → 设置：勾选"开放给普通用户"，可以写一句说明（显示在客户端里）。管理员始终能用所有节点。
2. 节点页 →"限额"：按角色设置最多同时在线的代理数、远程端口范围、允许的代理类型（例如只允许 tcp / udp、禁止 http）。
3. 用户 → 管理 →"节点权限"：对单个用户单独允许或禁止某个节点（不管角色），单独设置限额（包括允许的代理类型），重置节点凭证。

节点**只接受 MoeFrp 客户端用一次性登录票据登录**：官方 frpc、别的客户端、拿抓到的旧票据、拿到别的设备上的票据连，都会被拒绝。

### 工作方式

```
客户端 ──GET /api/v1/official-nodes──▶ 面板：节点 ID、名称、说明、地址和端口（客户端内部用，界面不显示）、user=昵称、限额（不含票据）
客户端 ──POST /api/v1/official-nodes/{id}/tickets（X-Device-Id）──▶ 面板：几张登录票据（一次性，15 分钟有效，绑定设备）
客户端 ──frpc 登录（metadatas：moefrp_cred = 一张新票据，moefrp_device = 设备指纹）──▶ 节点 frps ──插件──▶ agent 本机校验
面板 ──access.sync（节点票据密钥 + 授权用户的昵称、代数、限额 + 已撤销的会话）──▶ agent（加密保存，第 4 节）
```

- **登录票据**（`shared/nodeticket`）：`mft_<声明>.<签名>`，声明里有用户 ID、客户端会话 ID、节点 ID、用户的凭证代数、过期时间、随机数和设备标签，用这个节点的票据密钥签名。节点票据密钥 = `HMAC(面板主密钥, 节点 ID)`（第 4 节）。客户端拿到的只是票据，不知道节点的任何密钥。
  - **一次性**：agent 记住用过的随机数（加密保存，重启后还在），同一张票据不能登录第二次。抓包或从客户端里抠出来的票据要么已经用过，要么十几分钟后过期。
  - **绑定设备**：客户端的设备指纹 = SHA-256(Windows `MachineGuid`) 的前 16 字节（读不到时用安装 ID），每个请求带 `X-Device-Id` 头，面板在登录时记进会话。领票据时设备指纹必须和会话的一致；票据里的设备标签 = `HMAC(节点票据密钥, 设备指纹)` 截断，frpc 登录时还要带上设备指纹，agent 算出标签对得上才放行。登录令牌或票据被拷到别的电脑上用不了。
  - **绑定会话**（每台设备一个会话）：退出登录、被踢下线、改密码 / 重置密码、单设备模式下被挤下线后，面板把这个会话列为已撤销推给节点（0.3 秒内），它的票据不能再登录，用它登录的 frpc 也不再接受新代理和新连接。agent 也会自己记住见过的已撤销会话（90 天）。
  - **重置节点凭证**：代数 +1，用户已有的票据和用它们登录的 frpc 立即失效；客户端会自动领新票据重连。
  - **领取**：每个客户端会话每小时最多领 120 次（每次最多 5 张），超过返回 429。
- **客户端怎么用票据**：客户端把 frp 当库用，frp 每次登录（包括断线重连）前都会调用 `ServiceOptions.ConnectorCreator`，传入的正是随后拼登录消息时读 `Metadatas` 的那份配置。客户端在这个钩子里换上一张新票据，不用改 frp（`client/internal/engine/tickets.go`；这个 frp 内部行为由 `agent/internal/access` 的 `TestWithFrps` 用真的 frps 和 frpc 盯着，升级 frp 时测试失败就要跟着改）。
  - 每个节点在内存里预取 3 张（`client/internal/tickets`）：面板短暂不可用（重启、升级）时还能重连；面板长时间不可用、备用的用完或过期后，新的登录会失败，已经连上的不受影响。票据不落盘，退出登录时清空。
  - 票据在连接建立、登录消息发出后才算用掉，连不上节点反复重试时不消耗。
  - 领不到票据时界面显示"无法领取官方节点的登录票据：原因"；节点拒绝登录（会话已退出、凭证被重置、没有权限）时显示"官方节点拒绝了连接：原因"。
- **同一会话只能有一个 frpc**：同一个会话（设备）后登录的生效。插件给新登录分配 run ID 并记住，之前那个 frpc 之后的新代理、新工作连接、新用户连接都会被拒绝，它留下的代理也不算在线数。这种情况不主动断开它：它的会话还有效，断开后会领新票据重连、又把新的顶掉，两边会来回互踢。frpc 断线重连会带上原来的 run ID，不算新登录。这样把登录会话分给别人用，双方会互相顶掉。
- **授权表**：面板为每个官方节点算出能用它的用户（状态正常，且单独授权允许、或没有单独授权但角色在开放列表里），带上凭证代数和有效限额（角色限额 + 用户覆盖），加上节点票据密钥和已撤销的客户端会话，推给 agent。用户注册、改角色、停用、改限额、改授权、重置凭证，以及会话被撤销后 0.3 秒内推送，另外每分钟兜底检查一次，只在表变化时推送。节点上线时立即推送。
- **frps 插件**：agent 在 127.0.0.1 上起一个 HTTP 插件，官方节点的 frps 配置里自动加上它（Login、NewProxy、NewWorkConn、NewUserConn）：
  - **Login**：票据签名正确、没过期、没用过，设备对得上，用户在表里、代数一致、会话没被撤销，frp 的 user 必须是这个用户的昵称。frps 会把 user 加在代理名前面，不同用户的代理名不会冲突。节点没有 token，准入完全由插件决定。
  - **NewProxy**：同样校验身份（不看过期和是否用过，登录后票据一直留在 metadatas 里），并且必须是这个会话当前生效的 frpc；代理类型、远程端口（tcp / udp 指定了范围时必须写远程端口）、同时在线的代理数（取自 frps 自己的统计，按 user 计）都要在限额内。拒绝原因原样显示在客户端的代理状态里。客户端的 TCP+UDP 代理（tcp 勾了同时转发 UDP）在 frp 里是两个代理（`name` 和 `name-udp`），按两个分别校验、算两个名额。
  - **NewWorkConn / NewUserConn**：同样校验身份，被同一会话的新登录顶掉的 frpc 不再接受新连接。
  - **登录失效时直接断开**：每次收到新的授权表，agent 用新表重新核对每个已登录 frpc 登录时的票据，会话被撤销、用户被停用或撤销授权、凭证被重置、面板换了密钥的，直接关掉这个 frpc（连同它的代理和正在传的连接）。frpc 断开后会自动重连，重连时被拒绝，客户端上显示原因。frps 关闭客户端的方法（`ControlManager.GetByID` + `Control.Close`）在未导出的字段上，agent 用反射取（`agent/internal/frpsrv/clients.go`），由 `TestWithFrps` 盯着：吊销后已建立的连接必须被断开。
- **离线**：agent 保存最后的授权表和准入状态，面板离线时照样执行；客户端在加密缓存里保存最后的官方节点列表（不含票据）。
- **客户端**：官方节点不属于任何配置，不会被同步到云端配置里，也不能被分享出去；官方节点只给云端配置用（和云端配置一样是黑盒，只显示运行状态、不显示原始日志）：本地配置的运行栏和节点页里不列官方节点，订阅来的配置也不能用（节点由分享决定）。退出登录时清空。
- **访问地址**：用户在客户端里看到并复制，用来访问自己映射在这个节点上的 tcp / udp 代理（代理行上的复制按钮直接复制"地址:远程端口"）。在节点"设置"里填：域名手填；IPv4、IPv6 默认自动检测（面板看到的节点连接地址 + agent 上报的公网地址，内网、回环、链路本地、运营商级 NAT 地址不算）。agent 1.4.5 起分别强制走 IPv4、IPv6 去问回显服务（面板的 `GET /api/v1/agent/ip`、Cloudflare trace、env.moe、ipify，同时问、取最先回来的），启动时一次、之后每 30 分钟一次，所以 NAT 出来的云服务器 IPv4、连面板时没用到的那种协议也能拿到，可以改，填 `-` 表示不显示。改了马上推给在线客户端。连接节点用的地址和端口仍然不显示在界面和日志里（日志里会替换成 `[节点名]`）。
- **限额在客户端里**：选中官方节点时，添加 / 编辑代理时不允许的类型划掉、不能选，并写出这个节点对你的限制（类型、远程端口范围、最多同时几个代理）；已有的代理类型不被允许时，代理行显示"节点不允许"。面板改了限额实时生效。
- **云端配置和订阅不显示原始日志**，换成"运行状态"面板：标题栏一行连接状态（状态、节点、延迟、在线时长、重连次数），展开后是访问地址和运行事件（把连接、重连、代理上线 / 被拒绝翻译成人话）。本地配置仍然显示日志。
- **还挡不住的**：设备指纹是客户端自己报的。用户在自己的电脑上拿着自己的登录令牌，自己写程序领票据、自己改 frpc 每次登录换票据、带上同样的设备指纹，仍然能不用 MoeFrp 客户端连官方节点——但只能用他自己的额度，同一会话同时只能有一个 frpc，面板随时可以踢掉这个会话。

### 接口

| 接口 | 说明 |
|---|---|
| `GET /api/v1/official-nodes` | 客户端：当前用户能用的官方节点，支持 ETag |
| `POST /api/v1/official-nodes/{id}/tickets` | 客户端：给当前会话领取登录票据 `{count: 1–5}` → `{tickets: [{ticket, expiresAt}]}`，要带和登录时相同的 `X-Device-Id` |
| `PUT /api/v1/admin/nodes/{id}/official` | `{roles, description}`：`roles` 为 `["user"]` 开放给普通用户，`[]` 只给管理员 |
| `PUT /api/v1/admin/nodes/{id}/settings` | 结构化 frps 配置，字段见 `GET /api/v1/admin/frps-schema`；不合法时返回出错的字段 |
| `GET /api/v1/admin/node-secret`、`POST …/node-secret/rotate` | 主密钥指纹 / 轮换主密钥（要二次验证） |
| `PUT /api/v1/admin/nodes/{id}/access` | 访问地址 `{domain, ipv4, ipv6}`：IP 留空自动检测，`-` 不显示 |
| `GET / PUT /api/v1/admin/quotas` | 三个角色的限额 |
| `GET / PUT /api/v1/admin/users/{nickname}/nodes` | 用户的限额覆盖和单独授权 `{override, grants: {节点 ID: true / false / null}}` |
| `POST /api/v1/admin/users/{nickname}/reset-node-credential` | 重置节点凭证 |

端到端测试：`client/scripts/e2e/run-official.mjs`（面板 + 真实 agent + 真实客户端界面）。

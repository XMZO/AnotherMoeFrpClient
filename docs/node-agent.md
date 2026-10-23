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

1. 部署面板（Docker 镜像里已经带了 agent 程序）。节点访问面板的地址和管理员打开后台的地址不一样时（例如后台走内网），在配置里设 `public_url`。
2. 管理后台 → 节点 → 添加节点，复制安装命令，在 VPS 上执行：

   ```bash
   curl -fsSL https://<面板域名>/agent/install.sh | sudo sh -s -- MFN-XXXX-XXXX-XXXX
   ```

3. 节点上线后自动下发默认 frps 配置（端口 7000、随机 token）。在节点的"frps 配置"页修改，"概况"页有客户端连接信息。

代码：`agent/`（节点程序）、`server/internal/nodes`（面板侧）、`server/internal/httpapi/nodes.go`（接口）、`shared/agentproto`（协议）。CI 冒烟测试 `scripts/ci/agent-smoke.sh` 在 GitHub 的 Linux 机器上真实安装、升级、卸载并检查残留。

## 1. 组成

```
管理后台（浏览器）
   │  /api/v1/admin/nodes/*（cookie + X-MoeFrp-Admin）
   ▼
moefrp-server ──────── WebSocket /api/v1/agent/ws（agent 主动连，Bearer 节点令牌）
   │  提供 /agent/install.sh、/agent/download/<arch>
   ▼
moefrp-agent（节点上，root，systemd 服务）
   └─ 进程内嵌 frps v0.71.1（和客户端内嵌 frpc 一样）
```

- **agent 是单个静态二进制**，内嵌 frps，节点上不需要另装 frp。frps 跑在 agent 进程里，改配置时进程内关掉旧的 `Service` 再起新的，agent 和面板的连接不断（已确认 v0.71.1 的 `Service.Close()` 会释放所有监听端口）。
- **只由 agent 主动连面板**，节点不需要开放额外端口。断线后指数退避重连（1s → 60s，带抖动）。
- **agent 二进制由面板自己提供**：服务端镜像里带上 linux-amd64 / linux-arm64 两个 agent，安装和升级都从面板下载。不依赖 GitHub，国内机器也能装；面板版本和 agent 版本始终一致。

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
5. 管理员可以"重置令牌"（生成新的安装码，旧令牌立即失效），用于令牌泄露或机器重装。

## 3. 协议

WebSocket（`github.com/coder/websocket`），JSON 文本帧，双向请求/响应：

```json
{"id": "...", "type": "request|response|event", "method": "...", "data": {...}, "error": {...}}
```

| 方向 | 方法 | 说明 |
|---|---|---|
| agent → 面板 | `hello` | 连接后第一条：agent 版本、系统、架构、内核、主机名、开机时间、当前 frps 配置的版本号 |
| agent → 面板 | `report` | 每 10 秒：CPU、内存、磁盘、负载、网络流量；frps 状态（运行 / 出错 + 原因）、客户端数、代理数、连接数、累计流量 |
| 面板 → agent | `frps.apply` | 下发 frps 配置（TOML），agent 校验 → 进程内重启 frps → 失败自动回滚到旧配置，结果原样返回 |
| 面板 → agent | `frps.restart` / `frps.stop` | |
| 面板 → agent | `frps.proxies` | 当前在线的代理列表（名称、类型、用户、客户端、流量） |
| 面板 → agent | `logs` | 最近 N 行日志（agent 和 frps，内存环形缓冲） |
| 面板 → agent | `exec` | 以 root 执行命令，见第 5 节 |
| 面板 → agent | `upgrade` | 升级到面板上的版本，见第 6 节 |
| 面板 → agent | `uninstall` | 自卸载，见第 7 节 |

面板对每个节点只保留一条连接；新连接顶掉旧连接。超过 45 秒收不到消息就认为离线。

## 4. frps 配置

- 面板保存每个节点的**期望配置**（frps 原生 TOML 文本）和版本号；agent 上报**实际生效的版本号**，两者不一致时节点显示"配置未同步"，节点上线时自动重新下发。
- 编辑器提供常用项的模板（`bindPort`、`auth.token`、`vhostHTTPPort`、`vhostHTTPSPort`、`subDomainHost`、`allowPorts`、`maxPortsPerClient`、`transport.tls.force`），也可以直接改 TOML。
- agent 用 frp 自己的加载和校验代码（`config.LoadConfigure` 严格模式 + `Complete` + `validation.ValidateServerConfig`）检查，错误原样返回给页面。
- 不支持 `webServer`（frps 自带的 dashboard）：frp 每次打开 dashboard 都会再注册一份内存统计，进程内重启几次后数字会翻倍；状态和代理列表改在面板上看。
- 生效顺序：校验 → 关掉旧 frps → 用新配置启动 → 启动失败（例如端口被占用）就用旧配置重新启动，返回"已回滚"和原因。成功才写入 `/etc/moefrp-agent/frps.toml`。
- 节点卡片上可以一键复制"客户端节点信息"（地址、端口、token），方便加进 MoeFrp 客户端。

## 5. 执行命令（root）

- 只有管理员能用，并且**每 15 分钟要重新输入一次密码**（二次验证，和登录共用失败锁定），防止 cookie 被偷后直接拿到所有节点的 root。卸载、删除、重置令牌也要二次验证。
- 命令以 `/bin/sh -c` 执行，root 身份，固定 `PATH`，工作目录 `/root`，超时默认 60 秒、最长 1 小时；输出最多保留 1 MB，超出截断。
- 每条命令写审计日志（操作者、节点、命令全文、退出码），结果保存在 `node_tasks` 表，页面可以回看。可以同时选多个节点批量执行。
- 安装时可以加 `--no-exec` 关掉这个功能（写进 agent 配置，面板改不了），给不想被远程执行命令的机器用。
- 网页终端（交互式 shell）放到第二期：需要把 xterm.js 打包进管理后台。

## 6. 升级

- 面板显示每个节点的 agent 版本，和面板版本不同时提示"可升级"，支持单个或全部升级。
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
| `/etc/moefrp-agent/` | `agent.toml`（令牌、面板地址）、`frps.toml` |
| `/var/lib/moefrp-agent/` | 升级临时文件、`.bak`、状态文件 |
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
2. 从面板下载 `/agent/download/linux-<arch>` 和对应的 SHA-256，校验。
3. 安装到 `/usr/local/bin/moefrp-agent`，执行 `moefrp-agent enroll <面板地址> <安装码>`：换取令牌，写入 `/etc/moefrp-agent/agent.toml`。
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
  frps_config      TEXT NOT NULL,       -- 期望的 frps TOML
  config_rev       INTEGER NOT NULL,    -- 期望配置的版本号，每次修改 +1
  applied_rev      INTEGER,             -- agent 上报的已生效版本
  pending_uninstall INTEGER NOT NULL DEFAULT 0,
  info             TEXT,                -- 最近一次 hello 的 JSON（版本、系统、IP 等）
  last_seen        INTEGER,
  created_at       INTEGER NOT NULL
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
  - **概况**：系统信息、实时指标、frps 状态、客户端连接信息（一键复制）；
  - **frps 配置**：编辑器 + 应用，显示同步状态；
  - **代理**：在线代理列表；
  - **日志**：agent 和 frps 日志；
  - **命令**：输入命令执行、看历史；
  - **操作**：重启 frps、升级 agent、重置令牌、卸载、删除。

## 11. 分期

| 期 | 内容 |
|---|---|
| 一（已完成） | agent（内嵌 frps、注册、WebSocket、上报、配置下发与回滚、日志、执行命令、升级与回滚、卸载）；服务端接口和表；安装脚本；管理后台节点页；镜像里带 agent；CI 冒烟测试 |
| 二 | 网页终端；批量执行；节点流量历史图表 |
| 三 | 和 PLAN 9.1 结合：节点配置 frps 插件指向面板，按用户授权和配额 |

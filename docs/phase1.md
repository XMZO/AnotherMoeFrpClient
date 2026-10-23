# Phase 1：shared + 服务端核心

对应 PLAN 第 10 节的 Phase 1。只做了 shared 模块和服务端，客户端没有动。代码还没有提交（仓库已 `git init`，等你确认后再提交）。

## 验收结果

| PLAN 的验收标准 | 结果 |
|---|---|
| `go test ./...` 全部通过，包括并发抢同一个邀请码、被挤下线、版本冲突 | 通过。`shared` 和 `server` 两个模块下 `go vet ./...`、`go test ./...` 均无错误；`GOWORK=off` 时同样通过 |
| 用旧 Python 服务端生成的 argon2 哈希能登录成功 | 通过。样本由 argon2-cffi 25.1.0 用旧 `server.py` 的参数对测试密码现场生成，见 `server/internal/auth/testdata/gen_legacy_hashes.py` |
| 能用二进制直接部署到测试服务器 | **只验证了一半**：`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/moefrp-server` 能编出静态链接的 ELF，部署文件已备好；按约束没有连接或部署任何服务器，需要你在测试机上跑一遍 `docs/server-ops.md` 的"首次部署" |

没有跑 `go test -race`（这台机器跑不了，见 `spike/REPORT.md` 坑 10），放到别的机器或 CI 上。

## 实现了什么

**shared**（`module moefrp/shared`，无第三方依赖）

- `model`：Profile / Node / Proxy / Visitor（5.1），代理、访客、协议、角色、分享类型常量，UUIDv7 生成（同一毫秒内单调递增）。
- `api`：全部路由常量（含 Phase 4 的分享与订阅，先占位）、请求头常量、请求/响应结构体、版本号解析与比较（客户端检查更新也能用）。
- `apierr`：5.4 的全部错误码及 HTTP 状态码，统一错误格式 `{"error":{"code","message","details"}}`。
- `validate`：5.2 的规则——代理名和访客名合在一起查重、名称字符集（不能有空白和 `.`）、端口 1–65535、各类型必填项、xtcp 的 `fallbackTo` 引用检查、数量上限；昵称、密码长度、保留词；邀请码格式和校验位（与旧算法逐位一致，用旧 Python 代码算出的向量做了测试）。

**server**（`module moefrp/server`，`replace moefrp/shared => ../shared`）

- `config`：TOML（未知字段报错），`MOEFRP_*` 环境变量覆盖，启动前校验。
- `store`：modernc.org/sqlite；每个连接执行 7.3 的四条 PRAGMA；写事务 `BEGIN IMMEDIATE`、只读事务普通 `BEGIN`；`migrations/0001_init.sql` 就是 7.3 的建表语句，嵌入二进制，按编号逐个在事务里执行。
- `auth`：注册时在同一个事务里原子占用邀请码（检查影响行数）；不存在的用户也做一次假哈希；按昵称失败锁定（5 次 / 15 分钟 / 翻倍 / 最长 24 小时）；令牌 `mfs_` + 32 字节 base64url，库里只存 SHA-256；滑动过期 30 天，`last_seen_at` 最多每 10 分钟写一次；单设备模式（配置项，默认开）；改密码吊销其他会话；管理员生成一次性重置码（只存哈希、24 小时）；旧哈希参数不同时登录后自动重新哈希；argon2 同时最多算 4 个。
- `profile`：配置 CRUD，`revision` + `baseRevision`，冲突返回 409 且 `details.current` 是云端完整配置；每次改动在同一事务里让 `stateVersion` 加 1。
- `httpapi`：7.5 中除分享、订阅以外的全部接口和 `/api/v1/client/config`；中间件顺序为请求 ID → 真实 IP → panic 恢复 → 256KB 请求体上限 → 访问日志 → 客户端版本检查 → 认证 → 限流；`DisallowUnknownFields` + shared/validate；`GET /api/v1/state` 支持 `ETag` / `If-None-Match` / 304；`/api/v1` 下不存在的路径返回 JSON 的 404 / 405。
- `legacy`：旧客户端 `POST /api/login/get_challenge` 返回 200 和假挑战码，其余 `/api/*` 返回 426 `{"error":"请下载新版客户端：<URL>"}`。
- `ratelimit`：7.8 的四条规则（分享预览那条在 Phase 4 加），只信任 `trusted_proxies` 发来的 `X-Forwarded-For`。
- 日志：slog JSON 到 stdout；`audit_log` 记录注册、登录成功与失败、改密码、重置密码、管理员操作。测试里专门扫描了日志和库，确认密码、令牌、重置码、节点 token 都不出现。
- `cmd/moefrp-server`：`serve`、`migrate`、`backup`（`VACUUM INTO`）、`admin invite create|list|revoke`、`admin user list|set-role|disable|enable|reset-code|kick`、`version`。
- `deploy/`：`moefrp.service`（含 PLAN 要求的加固项和 `UMask=0077`）、`Caddyfile.example`、`config.example.toml`（有测试保证它和配置结构一致）。
- 运维文档：`docs/server-ops.md`（部署、配置项、管理命令、备份 timer、升级、排查）。

**独立审查后的修复**：写完后让一个子代理只读审查了一遍，确认并修掉了 5 个问题，每个都补了回归测试：

- 登录时昵称不限长度，每次失败都在内存锁定表里留一条，能把内存撑爆。现在不符合昵称格式（`^[a-zA-Z0-9_]{3,20}$`，旧版注册用的是同一个正则）的直接按密码错误处理（照样做假哈希），不进锁定表；锁定表和限流表各有 10 万条的上限，刷满时不会丢掉正在锁定的记录。
- 锁定是"先检查、后计数"，并发请求能绕过：100 个并发错误登录会真正校验 100 次。现在校验前在锁里原子预占一次尝试，已失败加进行中的最多 5 个，超出的返回 429 `rate_limited`（`retryAfter: 1`）；40 个并发只会真正校验 5 次，锁定时长也不会被并发叠加。
- 登录、改密码与重置密码之间有竞态：校验旧密码后、写库前密码被重置，旧密码仍能换到令牌，或用旧密码把新密码覆盖掉。现在登录的事务里复查密码哈希和账号状态，改密码只在哈希仍是旧值且当前会话未被吊销时才替换。
- 备份文件按 umask 创建后才 chmod，中间有一段时间所有人可读。现在先用 0600 建空文件再 `VACUUM INTO`。
- `PUT /profiles/{id}` 漏传 `nodes` 等数组会被当成清空。现在 `baseRevision`、`name`、`nodes`、`proxies`、`visitors` 都必须出现且不为 `null`，否则 400（新建时数组可以省略，表示空配置）。

审查还提到"客户端自带的配置 ID 如果属于别人，返回 400 而不是创建成功，能借此判断某个 ID 是否存在"。没有改：ID 是 UUIDv7，带 74 位随机数，猜不到，知道存在也读不到内容；要完全无法区分只能把主键改成按用户划分。

**测试**：`server/internal/httpapi` 用 `httptest` 覆盖了每个接口的正常和错误路径，另有 store、config、auth、ratelimit、admin、命令行的单元测试。专项用例：

- 20 个并发请求抢同一个邀请码，恰好 1 个成功；10 个并发请求注册同一个昵称，恰好 1 个成功且失败的不消耗邀请码。
- 单设备模式下第二次登录后，第一个令牌访问 `/me` 和 `/state` 都收到 `session_replaced`；关掉单设备模式时两个都能用。
- 两台设备基于同一版本修改，后提交的收到 409 和云端版本，基于新版本重新提交成功；10 个并发提交同一版本恰好 1 个成功。
- 旧哈希（m=65536,t=3,p=2）直接登录且不重新哈希；argon2-cffi 默认参数（p=4）的哈希登录后被重新哈希成当前参数。
- 失败锁定的完整时间线、滑动过期（用假时钟）、限流和 `Retry-After`、可信代理的各种 `X-Forwarded-For` 组合。

## 与 PLAN 不一致或需要你决定的地方

按影响从大到小排列。带"需要决定"的是我按自己的判断先做了，你可以改。

1. **需要决定：注册后不自动创建默认配置**。旧版注册时会建一个空的"我的云端配置"。新版注册只建账号（返回 201 和用户信息，也不自动登录），由客户端在配置为空时自己建。如果想保留旧行为，在 `auth.Register` 的事务里加一条插入即可。
2. **需要决定：版本检查的范围**。PLAN 6.12 写的是"需要登录的接口返回 426"。实现是 `/api/v1` 下除 `client/config` 外都检查，**包括登录和注册**——过低的客户端拿到令牌也只会处处 426。不带 `X-Client-Version` 头的请求（curl、监控）不检查；带了但解析不了的按过低处理。
3. **需要决定：保留昵称**。PLAN 只写了"`all`、`admin` 等"，现在是 `all admin administrator root system moefrp null undefined anonymous everyone`（不区分大小写），在 `shared/validate/account.go`。
4. **新增两个错误码**：`method_not_allowed`（405）和 `payload_too_large`（413），5.4 的表里没有。
5. **单个配置内的数量超限返回 400 `validation_failed`**（`reason: too_many`），每个用户的配置数超限才返回 409 `limit_exceeded`。前者是请求体本身不合法，后者取决于服务端已有数据。
6. **邀请码占用多加了一个条件 `used_at IS NULL`**。`used_by` 是 `ON DELETE SET NULL`，按 PLAN 原文的条件，用户被删除后邀请码会重新变得可用。
7. **邀请码字符集更严格**。旧服务端的正则允许 I、L、O（按 -1 参与计算），新版直接判为格式错误。旧生成脚本从不产生这三个字符，已有的邀请码不受影响。
8. **注册时先查邀请码、再查昵称**，没有有效邀请码的人不能借注册接口探测昵称是否存在。
9. **会话失效时的错误码**：被挤下线 → `session_replaced`；自己退出 → `unauthorized`；改密码、重置密码、管理员踢下线或停用、过期 → `session_expired`；账号被停用后再请求 → `account_disabled`。
10. **`invalid_credentials` 是 401**（PLAN 5.4 定的）。改密码时旧密码错误也返回它，所以客户端必须按 `code` 判断，不能"凡是 401 就下线"。改密码的旧密码错误同样计入按昵称的失败锁定。
11. **锁定和限流的细节**：都在内存里，重启清零；24 小时没有新的失败就清零重新计数（所以锁满 24 小时之后会从头算）；同一昵称已有 5 个尝试在进行中时返回 429 `rate_limited`；格式不合法的昵称不计入锁定；IPv6 按 /64 限流，按单个地址限流形同虚设。
12. **`/api/v1/state` 多了 `user` 字段**（昵称、角色）。管理员改角色也会让 `stateVersion` 加 1，客户端下次同步就能看到。`subscriptions` 目前总是空数组，`SubscriptionSummary` 的字段是占位，Phase 4 可能调整。
13. **配置 ID 可以由客户端提供**（离线新建的配置用自己的 UUIDv7）。同一用户重复创建同一个 ID 返回 409 `revision_conflict` 和云端版本，方便"请求成功但响应丢了"之后重试；ID 属于别人时返回 400。节点、代理、访客缺 ID 时服务端补上。
14. **PLAN 没规定的长度上限是我定的保守值**：配置名、节点备注、设备名 50 字符，地址和域名 253，token 和 secretKey 256 字节，`customDomains` 和 `allowUsers` 各 20 / 50 项，重置码输入 64。所有代理类型都要求 `localPort`（不支持插件，frp 在没有插件时也要求）。与类型无关的字段（例如 tcp 代理上残留的 `secretKey`）不报错，原样保存。
15. **重置码格式**：PLAN 没定，用的是 `RST-XXXXX-XXXXX-XXXXX`（字符集同邀请码，约 74 位熵），输入时不区分大小写。生成新码会作废该用户之前没用过的码。
16. **登录不校验密码长度**，旧库里可能有短于 8 位的密码；注册、改密码、重置密码校验 8–128 位。
17. **`serve` 遇到未执行的迁移拒绝启动**，必须先 `migrate`（升级时一定会先停下来备份）；开发时可以 `serve --auto-migrate`。管理命令和 `backup` 在库不存在或未迁移时拒绝执行，不会凭空建库。
18. **管理命令的配置路径**：`-c` > `MOEFRP_CONFIG` > `/etc/moefrp/config.toml` > 只用默认值。要以 `moefrp` 用户执行（`sudo -u moefrp ...`），因为数据目录是 0700。
19. **依赖版本**：`golang.org/x/crypto` 用的是 v0.57.0（发布超过 5 周），没用 9 天前刚发布的 v0.58.0；其余是当前最新版。
20. **不支持的旧哈希**：argon2d（Go 的 x/crypto 没实现）和 `v=16` 的哈希无法校验。argon2-cffi 默认就是 argon2id、v=19，旧库应该没有这两种，Phase 6 的 `import-legacy --dry-run` 时再确认。
21. 在 `gogogo` 根目录直接 `go test ./...` 不行（根目录没有 go.mod），要在 `shared`、`server` 目录下分别跑，或者在根目录 `go test moefrp/shared/... moefrp/server/...`。

## 在本地运行

下面用 Git Bash。`*.db`、`*.exe`、`config.toml` 都已在 `.gitignore` 里。

```bash
cd /e/frp_end/gogogo/server
go build -o moefrp-server.exe ./cmd/moefrp-server
```

```bash
export MOEFRP_DB_PATH=./dev.db MOEFRP_CLIENT_LATEST_VERSION=3.0.0 MOEFRP_CLIENT_MIN_VERSION=3.0.0 MOEFRP_CLIENT_DOWNLOAD_URL=https://example.com/MoeFrp.exe
```

```bash
./moefrp-server.exe migrate && ./moefrp-server.exe admin invite create -n 2
```

另开一个终端（同样先 `export` 上面的变量），启动服务，默认监听 `127.0.0.1:8080`：

```bash
./moefrp-server.exe serve
```

### 用 curl 试接口

```bash
B=http://127.0.0.1:8080; H='-H X-Client-Version:3.0.0 -H X-Install-Id:dev-install-1'; INVITE=FRPT-XXXX-XXXX   # 换成上面打印的邀请码
```

```bash
curl -s $B/healthz; curl -s $B/api/v1/client/config
```

```bash
curl -s $H -X POST $B/api/v1/auth/register -d "{\"nickname\":\"alice\",\"password\":\"correct-horse\",\"inviteCode\":\"$INVITE\"}"
```

```bash
TOKEN=$(curl -s $H -X POST $B/api/v1/auth/login -d '{"nickname":"alice","password":"correct-horse","deviceName":"dev"}' | sed -E 's/.*"token":"([^"]+)".*/\1/'); echo $TOKEN
```

```bash
curl -s $H -H "Authorization: Bearer $TOKEN" $B/api/v1/me
```

新建配置（节点、代理缺 ID 时服务端补上），再拿到 state 和 ETag。Windows 上命令行参数里的中文会被 curl 按本地代码页转码，服务端收到的不是 UTF-8，会报 `validation_failed`；要测中文就把 JSON 存成 UTF-8 文件，用 `--data-binary @file.json` 发送：

```bash
curl -s $H -H "Authorization: Bearer $TOKEN" -X POST $B/api/v1/profiles -d '{"name":"home","nodes":[{"remark":"hk","serverAddr":"frp.example.com","serverPort":7000,"token":"t"}],"proxies":[{"name":"ssh","type":"tcp","enabled":true,"localIP":"127.0.0.1","localPort":22,"remotePort":6022}],"visitors":[]}'
```

```bash
curl -s -i $H -H "Authorization: Bearer $TOKEN" $B/api/v1/state
```

带上 ETag 再请求一次，返回 304：

```bash
curl -s -o /dev/null -w '%{http_code}\n' $H -H "Authorization: Bearer $TOKEN" -H 'If-None-Match: "1"' $B/api/v1/state
```

修改配置（`PROFILE` 换成上面返回的 `id`）。`baseRevision` 写 1 成功；再用 1 提交一次就是 409 `revision_conflict`：

```bash
PROFILE=换成配置ID; curl -s $H -H "Authorization: Bearer $TOKEN" -X PUT $B/api/v1/profiles/$PROFILE -d '{"baseRevision":1,"name":"home 2","nodes":[],"proxies":[],"visitors":[]}'
```

单设备模式：再登录一次，旧令牌访问 `/me` 得到 `session_replaced`：

```bash
curl -s $H -X POST $B/api/v1/auth/login -d '{"nickname":"alice","password":"correct-horse"}' >/dev/null; curl -s $H -H "Authorization: Bearer $TOKEN" $B/api/v1/me
```

版本过低（426）和旧客户端的升级提示：

```bash
curl -s -H X-Client-Version:2.9.0 $B/api/v1/me; echo; curl -s -X POST $B/api/login/get_challenge -d '{}'; echo; curl -s -w ' %{http_code}\n' -X POST $B/api/login -d '{}'
```

重置密码：

```bash
CODE=$(./moefrp-server.exe admin user reset-code alice); curl -s -w '%{http_code}\n' $H -X POST $B/api/v1/auth/reset-password -d "{\"nickname\":\"alice\",\"code\":\"$CODE\",\"newPassword\":\"brand-new-pw\"}"
```

## 目录

```
gogogo/
├─ go.work                   use ./shared ./server
├─ .gitignore
├─ shared/                   model / api / apierr / validate
├─ server/
│  ├─ cmd/moefrp-server/     main.go（serve/migrate/backup）、admin.go
│  ├─ internal/              config store auth profile httpapi ratelimit admin legacy
│  ├─ migrations/            0001_init.sql + embed
│  └─ deploy/                moefrp.service、Caddyfile.example、config.example.toml
└─ docs/                     phase1.md（本文）、server-ops.md（运维手册）
```

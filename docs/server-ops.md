# moefrp-server 运维手册

适用于 Phase 1 的服务端（账号、云端配置同步、版本信息）。部署文件在 `server/deploy/`。

## 构建

```bash
cd server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=3.0.0" ./cmd/moefrp-server
```

产物是一个静态链接的 `moefrp-server`，不依赖 glibc。SQLite 用的是纯 Go 驱动 `modernc.org/sqlite`，不需要 cgo。

## 首次部署

```bash
useradd --system --no-create-home --shell /usr/sbin/nologin moefrp
install -m 0755 moefrp-server /usr/local/bin/moefrp-server
install -d -m 0750 -o root -g moefrp /etc/moefrp
install -m 0640 -o root -g moefrp config.example.toml /etc/moefrp/config.toml   # 按需修改
install -d -m 0700 -o moefrp -g moefrp /var/lib/moefrp
sudo -u moefrp moefrp-server migrate -c /etc/moefrp/config.toml
install -m 0644 moefrp.service /etc/systemd/system/moefrp.service
systemctl daemon-reload && systemctl enable --now moefrp
curl -fsS http://127.0.0.1:8080/healthz
journalctl -u moefrp | grep 初始化码     # 第一次启动时打印，用来创建管理员
```

然后按 `Caddyfile.example` 配置 Caddy，`systemctl reload caddy`，打开 `https://<域名>/admin/` 用初始化码创建第一个管理员。

要点：

- `serve` 发现有未执行的迁移时**拒绝启动**，提示先执行 `migrate`。这样升级时一定会先停下来做备份。开发环境可以用 `serve --auto-migrate`。
- 数据库文件新建时权限是 0600；systemd 单元里 `UMask=0077`，WAL、SHM、备份文件同样只有 `moefrp` 能读。
- 配置里的节点 token 是明文存在库里的（PLAN 7.3），所以数据库和备份都要当机密对待。

## 配置

完整示例见 `server/deploy/config.example.toml`。配置文件里出现未知字段时直接报错退出，避免拼错的配置项被静默忽略。

| 配置项 | 默认值 | 环境变量 | 说明 |
|---|---|---|---|
| `listen` | `127.0.0.1:8080` | `MOEFRP_LISTEN` | 只监听本机，由 Caddy 反代 |
| `db_path` | `moefrp.db` | `MOEFRP_DB_PATH` | 服务器上用 `/var/lib/moefrp/moefrp.db` |
| `trusted_proxies` | `["127.0.0.1/32", "::1/128"]` | `MOEFRP_TRUSTED_PROXIES`（逗号分隔） | 只信任这些地址发来的 `X-Forwarded-For` |
| `single_session` | `true` | `MOEFRP_SINGLE_SESSION` | 单设备模式：新登录挤掉该用户的其他会话 |
| `session_ttl` | `720h` | `MOEFRP_SESSION_TTL` | 滑动过期，至少 `1h` |
| `log_level` | `info` | `MOEFRP_LOG_LEVEL` | `debug` / `info` / `warn` / `error` |
| `admin_setup_code` | 空 | `MOEFRP_ADMIN_SETUP_CODE` | 管理后台初始化码，12–128 个字符；空时随机生成并写进日志 |
| `admin_recovery` | `false` | `MOEFRP_ADMIN_RECOVERY` | 已有用户时也开放初始化页面（管理员密码丢失时用，用完关掉） |
| `client.latest_version` | 空 | `MOEFRP_CLIENT_LATEST_VERSION` | |
| `client.min_version` | 空 | `MOEFRP_CLIENT_MIN_VERSION` | 低于它的客户端收到 426；空表示不检查 |
| `client.download_url` | 空 | `MOEFRP_CLIENT_DOWNLOAD_URL` | 同时用于旧客户端的"请下载新版"提示 |
| `client.sha256` | 空 | `MOEFRP_CLIENT_SHA256` | |
| `client.release_notes` | 空 | `MOEFRP_CLIENT_RELEASE_NOTES` | |
| `client.announcement` | 空 | `MOEFRP_CLIENT_ANNOUNCEMENT` | |
| `client.image_sources` | 空 | 无（数组只能写在文件里） | 图片面板的图源 |

配置文件路径：`-c` 优先；没给 `-c` 时依次尝试环境变量 `MOEFRP_CONFIG`、`/etc/moefrp/config.toml` 和 `/data/config.toml`（Docker 镜像用）；都没有就只用默认值和环境变量。

另外两个只用环境变量设置的项：`MOEFRP_PUID`、`MOEFRP_PGID`。设置后，以 root 启动的 `serve`、`migrate`、`backup`、`import-legacy` 会先把数据库所在目录的属主改成这个用户，再切换到它运行（Docker 镜像默认 `65532`，见 `docker.md`）。systemd 部署不要设置，由 `User=` 决定用户。

修改 `client.*` 后需要重启服务才生效。

## 管理后台

邀请码、用户和审计日志都在网页管理后台 `https://<域名>/admin/` 里管理，服务端没有管理用的命令行子命令。

- **首次使用**：库里还没有用户时，`serve` 启动时生成一个初始化码，以 `WARN` 级别写进日志（`journalctl -u moefrp | grep 初始化码`）。在 `/admin/` 页面填初始化码、昵称和密码创建第一个管理员，创建后自动登录。初始化码只能用一次；也可以用 `admin_setup_code` 固定它，这时日志里不打印。
- **登录**：只有 `admin` 角色能登录。会话放在 HttpOnly、`SameSite=Strict` 的 cookie 里（HTTPS 下带 `Secure`），12 小时不操作自动过期。管理后台的会话和桌面客户端的会话分开计算，单设备模式不会让两者互相挤下线。
- **功能**：概览统计；生成（一次最多 200 个，可写备注）、查看和撤销邀请码；用户列表、改角色、停用和启用、踢下线（只注销客户端会话）、生成一次性重置码（24 小时有效，只显示一次）；审计日志；下载数据库备份。
- **保护**：不能修改或停用自己；把管理员降级会立刻注销他的管理后台会话。所有管理接口都要求带 `X-MoeFrp-Admin: 1` 请求头（防 CSRF），页面有严格的 CSP。每个操作都写一条 `audit_log`（`detail.via = "web"`）。
- **管理员密码丢失**：配置里设 `admin_recovery = true` 并重启，日志里会出现新的初始化码，用它再建一个管理员；然后改回 `false` 再重启。也可以先在客户端用重置码改密码（需要另一个管理员生成）。

## 备份

`moefrp-server backup --to <文件>` 内部用 `VACUUM INTO`，服务运行中也可以执行，目标文件已存在时报错。

每天备份、保留 14 天（PLAN 7.11），用 systemd timer：

```ini
# /etc/systemd/system/moefrp-backup.service
[Unit]
Description=MoeFrp database backup

[Service]
Type=oneshot
User=moefrp
UMask=0077
ExecStart=/bin/sh -c 'exec /usr/local/bin/moefrp-server backup -c /etc/moefrp/config.toml --to /var/lib/moefrp/backup/moefrp-$(date +%%Y%%m%%d-%%H%%M%%S).db'
ExecStartPost=/usr/bin/find /var/lib/moefrp/backup -name 'moefrp-*.db' -mtime +14 -delete
```

```ini
# /etc/systemd/system/moefrp-backup.timer
[Unit]
Description=Daily MoeFrp database backup

[Timer]
OnCalendar=*-*-* 04:00:00
RandomizedDelaySec=15m
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
install -d -m 0700 -o moefrp -g moefrp /var/lib/moefrp/backup
systemctl daemon-reload && systemctl enable --now moefrp-backup.timer
```

另外再拷一份到别的机器。库里有节点 token，异地副本请先加密（例如 `age` 或 `gpg`）再传。

恢复：停服务 → 把备份文件复制成 `db_path` → 删除旁边残留的 `-wal`、`-shm` 文件 → `migrate` → 启动。

## 升级

1. `mf backup --to /var/lib/moefrp/backup/before-upgrade.db`
2. 替换 `/usr/local/bin/moefrp-server`
3. `mf migrate`
4. `systemctl restart moefrp`
5. `curl -fsS http://127.0.0.1:8080/healthz`，确认 `version` 是新版本

## 日志

- 服务以 JSON 格式把日志写到 stdout，由 journald 收集：`journalctl -u moefrp -f`。
- 每个请求一条 `"msg":"http"` 访问日志：方法、路径（不含查询串）、状态码、耗时、用户 ID、真实 IP、请求 ID。响应头 `X-Request-Id` 与日志里的 `request_id` 对应，排查用户反馈时可以按它查。
- 任何时候都不记录请求体、`Authorization` 头、密码、令牌、重置码和节点 token。
- 审计记录在库里的 `audit_log` 表：注册、登录成功与失败、改密码、重置密码、管理员操作。登录失败时如果昵称不存在，不记录昵称本身（用户有时会把密码误填进昵称框）。

```bash
sudo -u moefrp sqlite3 /var/lib/moefrp/moefrp.db \
  "SELECT datetime(at,'unixepoch','localtime'), action, target, ip, detail FROM audit_log ORDER BY id DESC LIMIT 20"
```

## 限流与锁定

都在内存里，重启后清零（单进程，PLAN 7.8）。

| 对象 | 规则 |
|---|---|
| 登录 | 每个 IP 每分钟 10 次 |
| 注册 | 每个 IP 每小时 5 次 |
| 重置密码 | 每个 IP 每 10 分钟 5 次 |
| 其他需要登录的接口 | 每个用户每分钟 120 次 |
| 按昵称的失败锁定 | 连续失败 5 次锁 15 分钟，之后每再失败一次时长翻倍，最长 24 小时；登录成功或重置密码后清零；24 小时没有新的失败也清零 |

- IPv6 按 /64 计数。
- 锁定表和每个限流表最多各记 10 万条，刷满时先清理过期的，再随机丢掉不在锁定中的（正在锁定的不会丢）。格式不合法的昵称（`^[a-zA-Z0-9_]{3,20}$` 以外）直接按密码错误处理，不计入锁定。
- 只有 `trusted_proxies` 里的对端发来的 `X-Forwarded-For` 才被采信，从右往左取第一个不可信的地址。如果 Caddy 前面还有 CDN，要把 CDN 回源的网段也加进 `trusted_proxies`，否则所有请求都会算在 CDN 的 IP 上。
- 某个账号被恶意锁定时，可以重启服务清零，或者给用户发重置码（重置成功会清除锁定）。

## 旧客户端

切换后（PLAN 7.12），Caddy 指向新服务即可，不需要额外配置：

- `POST /api/login/get_challenge` 返回 200 和一个假挑战码，让旧客户端走到下一步；
- 其余 `/api/*`（不在 `/api/v1` 下）一律返回 426 和 `{"error": "请下载新版客户端：<download_url>"}`，旧客户端会把这句话弹出来。

## 排查

| 现象 | 原因与处理 |
|---|---|
| 启动时报"数据库有 N 个未执行的迁移" | 先备份，再 `mf migrate` |
| 启动时报配置里有 unknown fields | 配置项拼错了，对照 `config.example.toml` |
| `/healthz` 返回 503 | 数据库打不开，看 `journalctl -u moefrp` |
| 日志里出现 `database is locked` | 有别的进程长时间持有写锁（例如手动开着的 `sqlite3` 事务），关掉它 |
| 所有用户都报 429 | `trusted_proxies` 没包含真正的反代地址，所有请求都算成了同一个 IP |
| 客户端都报 426 `client_outdated` | `client.min_version` 设得比已发布的客户端还高 |

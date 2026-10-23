# 用 Docker 部署服务端

服务端镜像是 `ghcr.io/xmzo/moefrp-server`，同时支持 linux/amd64 和 linux/arm64，由 GitHub Actions 构建（`.github/workflows/server-image.yml`）：

- x86 和 ARM 分别在 GitHub 的原生机器（`ubuntu-24.04`、`ubuntu-24.04-arm`）上编译，不用 QEMU 模拟，最后合并成一个多架构镜像。
- 推送到 `main` 分支时生成 `latest`、`main`、`sha-xxxxxxx` 三个标签；推送 `v3.0.0` 这样的 git tag 时生成 `3.0.0`、`3.0`。
- 基础镜像是 distroless，里面只有程序本身（约 12MB），没有 shell。

## 部署

```bash
mkdir moefrp && cd moefrp
curl -O https://raw.githubusercontent.com/XMZO/AnotherMoeFrpClient/main/docker-compose.yml
docker compose up -d
docker compose ps                                # moefrp 显示 healthy 即正常
curl http://127.0.0.1:8080/healthz
```

服务只监听宿主机的 `127.0.0.1:8080`，域名和 HTTPS 由宿主机上的反向代理负责。

**Caddy**（会自动设置 `X-Forwarded-For` 和 `X-Forwarded-Proto`）：

```caddyfile
frplus.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

**Nginx**：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

两个头都要带：`X-Forwarded-For` 用于按 IP 限流和审计日志，`X-Forwarded-Proto` 决定管理后台的 cookie 是否带 `Secure`。

## 第一个管理员

服务第一次启动、库里还没有用户时，会在日志里打印一个初始化码：

```bash
docker compose logs moefrp | grep 初始化码
```

打开 `https://<你的域名>/admin/`，填初始化码、管理员昵称和密码，创建后自动登录。初始化码只能用一次。这个管理员账号也可以直接登录桌面客户端。

管理后台里可以：生成和撤销邀请码、查看用户、改角色（普通用户 / VIP / 管理员）、停用账号、踢下线、生成密码重置码、看审计日志、下载数据库备份。只有管理员能登录，12 小时不操作自动退出，和桌面客户端的登录互不影响。

**管理员密码丢了**：在 `data/config.toml` 里加一行 `admin_recovery = true`，`docker compose restart`，用日志里新的初始化码再建一个管理员，然后删掉这一行再重启。

## 数据目录

所有数据都在 compose 文件旁边的 `./data` 里，删容器、删镜像都不会丢：

| 文件 | 说明 |
|---|---|
| `moefrp.db`（以及 `-wal`、`-shm`） | 数据库 |
| `config.toml` | 可选的配置文件，见下一节 |
| `moefrp.db.pre-migrate-<时间>.db` | 升级时自动生成：新版本有数据库迁移时，先备份旧库再迁移 |

**搬到另一台机器**：

```bash
docker compose down                              # 先停，保证数据库文件完整
tar czf moefrp.tgz docker-compose.yml data
# 新机器上解压后
docker compose up -d
```

数据库里有密码哈希和节点 token，`data` 目录和打包文件都要当机密保管。

## 配置

compose 文件里只放了必须的设置。客户端版本号、公告、图源等写在 `data/config.toml`（没有这个文件时全部用默认值），改完 `docker compose restart` 生效：

```toml
[client]
latest_version = "3.0.0"
min_version    = "3.0.0"     # 低于它的客户端会被要求升级，留空不检查
download_url   = "https://github.com/XMZO/AnotherMoeFrpClient/releases/latest"
announcement   = ""

# 图片面板的图源，可以有多个
[[client.image_sources]]
url    = "https://umi.li/random?dir=lucky&type=img"
weight = 5
```

全部配置项见 `server-ops.md` 的"配置"一节和 `server/deploy/config.example.toml`。`listen` 和 `db_path` 由镜像固定，不用写。每一项也可以在 compose 的 `environment` 里用 `MOEFRP_*` 环境变量设置，环境变量优先于文件。配置文件里有未知字段时服务拒绝启动，避免拼错被静默忽略。

## 日常操作

| 操作 | 命令 |
|---|---|
| 看日志 | `docker compose logs -f moefrp` |
| 邀请码 / 用户 / 重置密码 / 审计日志 | 管理后台 `https://<你的域名>/admin/` |
| 备份 | 管理后台"备份"页直接下载；或者 `docker compose exec moefrp moefrp-server backup --to /data/backup-$(date +%F).db`，文件出现在 `./data` 里 |
| 升级 | `docker compose pull && docker compose up -d` |

定期备份可以在宿主机的 cron 里执行上面的备份命令，再把文件拷到别的机器。

## 从旧版迁移

```bash
cp users.db data/old-users.db
docker compose exec moefrp moefrp-server import-legacy --from /data/old-users.db --dry-run
# 确认报告没问题后去掉 --dry-run 正式导入（只能导入到还没有用户的新库，所以导入前不要先在网页上创建管理员）
docker compose exec moefrp moefrp-server import-legacy --from /data/old-users.db
```

导入后用旧版的管理员账号登录管理后台。完整的切换步骤见 `cutover.md`。

## 以普通用户运行

compose 里写了 `user: "0:0"`，因为 `./data` 不存在时 Docker 会以 root 身份创建它，普通用户写不进去。想以镜像默认的非 root 用户（uid 65532）运行：

```bash
docker compose down
sudo chown -R 65532:65532 data
# 删掉 docker-compose.yml 里的 user: "0:0"
docker compose up -d
```

## 对外直接监听

不经过反向代理、直接对外提供 HTTP 时，把端口改成 `"8080:8080"`，并且**删掉** `MOEFRP_TRUSTED_PROXIES`。否则任何人都能伪造 `X-Forwarded-For` 绕过按 IP 的限流。客户端和管理后台之间传的是密码和令牌，正式使用请走 HTTPS。

## 自己构建镜像

```bash
docker build -f server/Dockerfile --build-arg VERSION=3.0.0 -t moefrp-server .
```

构建上下文必须是仓库根目录，因为服务端要引用 `shared/`。

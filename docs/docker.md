# 用 Docker 部署服务端

服务端镜像是 `ghcr.io/xmzo/moefrp-server`，同时支持 linux/amd64 和 linux/arm64，由 GitHub Actions 构建（`.github/workflows/server-image.yml`）：

- x86 和 ARM 分别在 GitHub 的原生机器（`ubuntu-24.04`、`ubuntu-24.04-arm`）上编译，不用 QEMU 模拟，最后合并成一个多架构镜像。
- 推送到 `main` 分支时生成 `latest`、`main`、`sha-xxxxxxx` 三个标签；推送 `v3.0.0` 这样的 git tag 时生成 `3.0.0`、`3.0`。
- 基础镜像是 distroless，里面只有程序本身（约 12MB），没有 shell，以非 root 用户（uid 65532）运行。

## 单文件部署

仓库根目录的 `docker-compose.yml` 一个文件就够了，里面已经包含 moefrp-server 和 Caddy（自动申请 HTTPS 证书），两份配置也直接写在这个文件里。

```bash
# 前提：Docker Compose v2.23.1 及以上；域名已解析到这台服务器；80、443 端口已开放
curl -O https://raw.githubusercontent.com/XMZO/AnotherMoeFrpClient/main/docker-compose.yml
# 按需修改文件末尾 moefrp_config 里的 [client] 部分（版本号、下载地址、公告、图源）
echo "DOMAIN=frplus.example.com" > .env
docker compose up -d
docker compose ps                                  # moefrp 显示 healthy 即正常
curl https://frplus.example.com/healthz
```

**第一个管理员**：服务第一次启动、库里还没有用户时，会在日志里打印一个初始化码。

```bash
docker compose logs moefrp | grep 初始化码
```

打开 `https://<你的域名>/admin/`，填初始化码、管理员昵称和密码，创建后自动登录。初始化码只能用一次，之后就只能用管理员账号登录。这个管理员账号也可以直接登录桌面客户端。

想用固定的初始化码（例如自动化部署），在 `moefrp_config` 里写 `admin_setup_code = "..."`（至少 12 个字符），这时日志里不会打印它。

**管理后台**（`/admin/`）里可以：生成和撤销邀请码、查看用户、改角色（普通用户 / VIP / 管理员）、停用账号、踢下线、生成密码重置码、看审计日志、下载数据库备份。只有管理员角色能登录，登录 12 小时不操作会自动退出，和桌面客户端的登录互不影响。

**管理员密码丢了**：在 `moefrp_config` 里加 `admin_recovery = true`，`docker compose up -d --force-recreate moefrp`，用日志里新的初始化码再建一个管理员，然后把这一行删掉再重建一次容器。

## 日常操作

| 操作 | 命令 |
|---|---|
| 看日志 | `docker compose logs -f moefrp` |
| 邀请码 / 用户 / 重置密码 / 审计日志 | 网页管理后台 `https://<你的域名>/admin/` |
| 备份 | 管理后台"备份"页直接下载；或者 `docker compose exec moefrp moefrp-server backup --to /var/lib/moefrp/backup-$(date +%F).db` |
| 把备份拷出来 | `docker compose cp moefrp:/var/lib/moefrp/backup-2026-10-23.db .` |
| 改配置 | 修改 `docker-compose.yml` 里的 `moefrp_config`，然后 `docker compose up -d --force-recreate moefrp` |
| 升级 | `docker compose pull && docker compose up -d` |

- 数据库在命名卷 `moefrp_moefrp-data` 里，路径是容器内的 `/var/lib/moefrp/moefrp.db`。
- **升级时自动备份**：容器以 `serve --auto-migrate` 启动。新版本有数据库迁移时，会先把旧库备份成 `/var/lib/moefrp/moefrp.db.pre-migrate-<时间>.db`，再执行迁移。
- 定期备份可以在宿主机的 cron 里执行上面的备份命令，备份文件再拷到别的机器。

## 从旧版迁移

```bash
docker compose cp users.db moefrp:/var/lib/moefrp/old-users.db
docker compose exec moefrp moefrp-server import-legacy --from /var/lib/moefrp/old-users.db --dry-run
# 确认报告没问题后去掉 --dry-run 正式导入（只能导入到还没有用户的新库）
docker compose exec moefrp moefrp-server import-legacy --from /var/lib/moefrp/old-users.db
```

完整的切换步骤见 `cutover.md`。

## 不用自带的 Caddy

如果服务器上已经有 Nginx、Caddy 这类反向代理：

1. 删掉 `caddy` 服务和 `caddy-*` 卷。
2. 给 `moefrp` 加上 `ports: ["127.0.0.1:8080:8080"]`。
3. 把 `trusted_proxies` 改成反向代理所在的地址。反向代理跑在宿主机上时，填 Docker 网桥的网关地址，例如 `172.29.0.1/32`。
4. 反向代理要设置 `X-Forwarded-For` 头，否则限流会把所有用户当成同一个 IP。

## 自己构建镜像

```bash
docker build -f server/Dockerfile --build-arg VERSION=3.0.0 -t moefrp-server .
```

构建上下文必须是仓库根目录，因为服务端要引用 `shared/`。

# 上线切换手册：旧 Flask 服务 → moefrp-server（PLAN 7.12、Phase 6）

整个过程需要在服务器上操作，**由你执行**。预计停机约 10 分钟。

## 0. 开始之前（与代码无关，PLAN 8.2）

- [ ] 换掉 `invite.py` 里写着的那台服务器的 root 密码，改成 SSH 密钥登录，并禁用密码登录（`PasswordAuthentication no`）。然后删除 `invite.py`。
- [ ] 确认 `server/adwd.py` 里的私钥、`client/key.txt` 里的密钥对是做什么用的。还在用的全部更换。
- [ ] 旧服务的 `server.log` 里有完整的会话令牌和一次性 token，切换后归档或删除。
- [ ] 新客户端已按 `release.md` 构建、签名、测试，下载地址可以访问。
- [ ] 服务器上配好 `[[client.image_sources]]`（不配的话，客户端的图片面板会显示"暂时没有可用的图源"）。

## 1. 提前演练（不停机）

在服务器上把旧库复制一份，用 dry-run 看看导入结果：

```bash
# 新服务的二进制和配置先按 server-ops.md 的"首次部署"装好，但先不启动
sudo cp /root/backf/users.db /tmp/users-copy.db          # 旧库的实际路径以旧服务为准
sudo -u moefrp moefrp-server migrate
sudo -u moefrp moefrp-server import-legacy --from /tmp/users-copy.db --dry-run
```

输出示例：

```
试运行（未写入）：用户 42，配置 57（节点 63、代理 210），邀请码 120（已使用 42）
分享 12/13，订阅 18/20（已导入/旧库总数）
问题 9 条，其中跳过 3 条
  [跳过] users bob：昵称与 "Bob" 只差大小写，新版昵称不区分大小写
  [警告] personal_configs conf-…（我的云端配置）：代理 rdp 没有 secretKey，已随机生成，访问端需要重新配置
  ...
```

- 每一条 `[跳过]` 都要人工判断：数据可以丢，就接受；不能丢，就在旧库副本里改好后再跑一遍。
- `[警告]` 是已经自动修正的问题，看一遍即可。
- 导入的是 dry-run，不会写入。
- 目标库里已经有用户时，导入会拒绝执行，避免把数据混在一起。

转换规则见 PLAN 7.12 和附录 C：

- 密码哈希原样保留，用户**不需要重置密码**。
- 旧分享 ID 原样保留，用户手里存着的旧分享 ID 仍然可以订阅。
- 会话、密码重置令牌、P2P 网络相关的表不迁移，用户需要重新登录。

## 2. 正式切换

```bash
# 1) 停旧服务，备份旧库
sudo systemctl stop <旧 Flask 服务名>          # 或者结束 gunicorn 进程
sudo cp /root/backf/users.db /root/users-$(date +%F).db

# 2) 新库：全新的库，导入旧数据
sudo -u moefrp moefrp-server migrate
sudo -u moefrp moefrp-server import-legacy --from /root/users-$(date +%F).db     # 去掉 --dry-run

# 3) 启动新服务，Caddy 指向它
sudo systemctl enable --now moefrp
curl -s http://127.0.0.1:8080/healthz
sudo cp deploy/Caddyfile.example /etc/caddy/Caddyfile   # 按需修改域名
sudo systemctl reload caddy
curl -s https://frp.example.com/healthz
```

导入后库里已经有用户，所以不会生成初始化码：直接用**旧版的管理员账号**登录 `https://frp.example.com/admin/`，在"用户"页抽查导入结果。旧库里没有管理员时，按 `server-ops.md`"管理后台"一节用 `admin_recovery = true` 建一个。

## 3. 验证

- [ ] `curl -s https://frp.example.com/api/v1/client/config` 返回新版本号、公告和图源。
- [ ] 用新客户端登录一个**导入的老账号**，密码不变；云端配置都在。
- [ ] 订阅一个**旧分享 ID**，能看到预览，并且能运行。
- [ ] 旧客户端登录时弹出"请下载新版客户端：<下载地址>"。新服务对旧路径的处理是：`get_challenge` 返回假挑战码，`/api/login` 返回 426。
- [ ] `journalctl -u moefrp -n 200` 里没有 5xx。
- [ ] `sudo -u moefrp moefrp-server backup --to /root/moefrp-after-cutover.db` 能成功。备份定时器按 `server-ops.md` 配好。

## 4. 回滚（只在切换当天考虑）

新服务出现无法修复的问题时：

```bash
sudo systemctl stop moefrp
# Caddy 改回指向旧服务
sudo systemctl start <旧 Flask 服务名>
```

切换后新客户端上产生的修改不会回到旧库。所以回滚只适合切换后很短的时间内，并且要先通知用户。

## 5. 观察一周后

- 归档旧库（`/root/users-*.db`）和旧代码，删除旧服务的 systemd 单元。
- 旧客户端登录时会一直看到升级提示，这个提示不需要撤掉。

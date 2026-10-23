# 需要你来做的事

代码和文档都已完成（Phase 0–6）。下面这些要么需要你的账号、证书或服务器，要么是需要你拍板的决定，所以没有替你做。

## 安全（尽快，与重写无关）

1. **换掉 root 密码**：`E:\frp_end\invite.py` 里有一台服务器的明文 root 密码。改成 SSH 密钥登录、禁用密码登录，然后删除 `invite.py`。
2. **检查并更换密钥**：`server/adwd.py` 里的私钥、`client/key.txt` 里的密钥对。
3. **清理旧日志**：旧服务的 `server.log` 里有完整的会话令牌。

## 上线

4. **部署测试服务器**：按 `docs/server-ops.md` 的"首次部署"操作。Phase 1 的验收只差这一项。
5. **配置图源**：在服务器的 `config.toml` 里配置 `[[client.image_sources]]`。客户端里没有写死任何图源。
6. **发布客户端**：按 `docs/release.md` 操作，包括：
   - 代码签名，需要购买 OV 或 EV 证书。
   - 在开着 Defender 的机器上扫描，并向 Microsoft 提交误报申诉。
   - 在干净的 Win10 虚拟机上测试缺少 WebView2 时的提示。
7. **上线切换**：按 `docs/cutover.md` 操作，先用 `import-legacy --dry-run` 演练，再正式切换。
8. **图标**：已经换成旧客户端的图标（取自旧版 `windows.ico` 里的 256×256 图）。exe、窗口和托盘都用这个图标。要换别的图，替换 `client/build/appicon.png`，然后执行 `wails3 task common:generate:icons`，再重新构建。

## 需要你拍板（都已按我的建议先做了，可以改）

| 事项 | 现在的做法 | 详见 |
|---|---|---|
| 注册后是否自动建一个空的"我的云端配置" | 不在注册时建，第一次交互登录、云端为空时才建 | phase1.md #1、phase3.md #4 |
| 客户端版本过低时登录、注册是否也拒绝 | 拒绝（426），本地配置仍可启动 | phase1.md #2 |
| 保留昵称列表 | `all admin administrator root system moefrp null undefined anonymous everyone` | phase1.md #3 |
| 被挤下线时是否保留本机的云端配置缓存 | 保留，只有主动退出登录才清掉 | phase3.md #1 |
| "保持登录"是否默认勾选 | 默认勾选 | phase3.md #5 |
| 订阅能否复制成本地配置 | 不能（防止撤销后 token 收不回来） | phase4.md #1 |
| 分享者能否看到订阅者是谁 | 只能看到人数 | phase4.md #9 |
| 隐藏到托盘时是否释放 WebView2 | 默认释放（后台内存从 512MB 降到 51MB） | phase5.md #1 |
| 图片多久换一张 | 60 秒 | phase5.md #3 |
| 是否做官方节点 + frps 插件按人授权 | 没做，留作后续（PLAN 9.1） | PLAN 9.1 |
| 是否向 Wails 上游报告 ErrorHandler 收不到 WebView2 错误的 bug | 没报 | spike/REPORT.md |

## 小事

- `client/frontend/package.json` 里的 `@wailsio/runtime` 现在是 `3.0.0-beta.30`，等 pnpm 的等待期过了，改成 `3.0.0-beta.31`。
- 代码都还没有 commit。`gogogo` 已经执行过 `git init`，`.gitignore` 也配好了，要提交的时候告诉我即可。
- `E:\frp_end\frp` 现在停在 `v0.71.1` 标签上（detached HEAD）。要切回原来的分支，执行 `git -C E:\frp_end\frp checkout dev`。

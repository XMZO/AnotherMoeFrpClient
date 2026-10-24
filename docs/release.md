# 发布清单（PLAN 6.11、Phase 6）

每次发布客户端都按这份清单走一遍。服务端的部署、升级见 `server-ops.md`，第一次上线的切换步骤见 `cutover.md`。

## 1. 构建

```powershell
cd E:\frp_end\gogogo
pwsh .\scripts\release.ps1 -Version 3.0.0
```

脚本会做这几件事：

1. 跑一遍 shared、server、client 的 `go vet`、`go test`，以及前端的 `vue-tsc`、`vitest`。
2. 构建 `dist\3.0.0\MoeFrp-3.0.0-windows-amd64.exe`。
3. 构建 `moefrp-server-3.0.0-linux-amd64`。
4. 复制部署文件，生成 `SHA256SUMS`。

可以加 `-SkipTests` 只做构建。

建议再跑一遍界面端到端测试（需要先构建 e2e 版）：

```powershell
cd client
wails3 build PACKAGE_MANAGER=pnpm EXTRA_TAGS=e2e APP_NAME=MoeFrp-e2e
pwsh .\scripts\e2e\run-e2e.ps1          # 本地配置、引擎、外壳
node .\scripts\e2e\run-phase3.mjs       # 账号与云同步（约 5 分钟）
node .\scripts\e2e\run-phase4.mjs       # 分享与订阅（约 3 分钟）
node .\scripts\e2e\run-phase5.mjs       # 体验完善（约 2 分钟）
```

## 2. 代码签名（需要证书）

- 用 OV 或 EV 代码签名证书签名：

  ```powershell
  signtool sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 /a dist\3.0.0\MoeFrp-3.0.0-windows-amd64.exe
  signtool verify /pa dist\3.0.0\MoeFrp-3.0.0-windows-amd64.exe
  ```

- 签名会改变文件内容，签名后要重新生成 `SHA256SUMS`。
- 不签名也能发布，但 SmartScreen 会提示"未知发布者"，杀毒软件误报的概率也明显更高。

## 3. 杀毒软件

- 开发机上的 Defender 服务是停着的（实际在跑的是 ESET 和火绒），所以 **Defender 扫描要在另一台开着 Defender 的机器或虚拟机上做**：

  ```powershell
  & "C:\Program Files\Windows Defender\MpCmdRun.exe" -Scan -ScanType 3 -File <exe 路径> -DisableRemediation
  ```

- 再用火绒、360 各扫一遍，国内用户用得多。
- 首次发布和每个大版本，都在 https://www.microsoft.com/wdsi/filesubmission 提交误报申诉，类型选"软件开发者"。
- **不要**用 UPX 加壳，也不要用 garble 混淆。这两种做法都会让误报更多。

## 4. 干净虚拟机测试

在**没装过 WebView2** 的 Windows 10 虚拟机上：

1. 双击 exe，应弹出"缺少 WebView2"的提示框，里面有下载链接。不能是"双击没反应"（Wails 的 bug，见 PLAN 6.11）。
2. 安装 WebView2 后再运行，完整走一遍：
   - 注册、登录、新建配置并启动隧道。
   - 订阅一个分享并运行。
   - 托盘的显示和退出。
   - 开机自启：重启后只出现托盘图标，隧道自动连上。
3. 在 Windows 11 上再走一遍。

## 5. 更新服务器上的版本信息

修改服务器的 `/etc/moefrp/config.toml`：

```toml
[client]
latest_version = "3.0.0"
download_url   = "https://frp.example.com/download/MoeFrp-3.0.0-windows-amd64.exe"
sha256         = "<签名后 exe 的 SHA256>"
release_notes  = "本次更新：……"
# min_version 只在必须淘汰旧版本时提高（低于它的客户端账号和同步会停止，本地配置仍可启动）
```

改完执行 `sudo systemctl restart moefrp`。客户端会在启动时，或者最多 6 小时内提示更新。

## 6. 发布后

- 发布的 exe 和 `SHA256SUMS` 放到 `download_url` 对应的位置。
- 观察服务端日志：`journalctl -u moefrp -f`，重点看 `client_outdated` 和 5xx。

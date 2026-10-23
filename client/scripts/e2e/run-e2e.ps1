# PLAN Phase 2 验收的端到端自动化：在真实构建出的端到端测试版 MoeFrp-e2e.exe 上，用 CDP 操作界面。
# 测试钩子（MOEFRP_HOME / MOEFRP_CDP_PORT 等）只在 -tags e2e 的构建里生效，正式的 MoeFrp.exe 不认这些环境变量。
#
#   1. 不连服务器，新建本地配置，启动隧道并真实转发（对端是本机的 localfrps 进程）
#   2. 运行中改代理：热更新，长连接不断，runId 不变
#   3. 停止 → 端口释放；运行中从托盘退出 → 进程退出、端口释放
#   4. 代理重名、远程端口被占用、节点连不上：界面上显示明确原因
#   另外：配置重启后还在（DPAPI 缓存）、关闭到托盘、单实例激活、数据目录位置
#
# 用法（PowerShell）：
#   cd E:\frp_end\gogogo\client
#   wails3 build PACKAGE_MANAGER=pnpm EXTRA_TAGS=e2e APP_NAME=MoeFrp-e2e
#   .\scripts\e2e\run-e2e.ps1
# 数据放在临时目录（MOEFRP_HOME），不会动到真实的 %APPDATA%\MoeFrp。
param(
    [string]$Exe = "$PSScriptRoot\..\..\bin\MoeFrp-e2e.exe",
    [string]$OutDir = "$PSScriptRoot\out",
    [int]$CdpPort = 9350,
    [switch]$KeepData
)
$ErrorActionPreference = "Stop"
. "$PSScriptRoot\win32.ps1"
. "$PSScriptRoot\win32tray.ps1"

$client = (Resolve-Path "$PSScriptRoot\..\..").Path
$Exe = (Resolve-Path $Exe).Path
New-Item -ItemType Directory -Force $OutDir | Out-Null
$OutDir = (Resolve-Path $OutDir).Path
Get-ChildItem $OutDir -File | Remove-Item -Force

$localFrps = Join-Path $client "bin\localfrps.exe"
Push-Location $client
try { go build -o $localFrps ./cmd/localfrps } finally { Pop-Location }

$dataHome = Join-Path $env:TEMP ("moefrp-e2e-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
$diagLog = Join-Path $OutDir "diag.log"
$summary = [ordered]@{ exe = $Exe; exeBytes = (Get-Item $Exe).Length; dataHome = $dataHome }

function DiagHas([string]$pattern) { (Test-Path $diagLog) -and (Select-String -Path $diagLog -Pattern $pattern -Quiet) }
function DiagCount([string]$pattern) { if (Test-Path $diagLog) { @(Select-String -Path $diagLog -Pattern $pattern).Count } else { 0 } }
function PortFree([int]$port) {
    try { $l = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $port); $l.Start(); $l.Stop(); return $true } catch { return $false }
}
function WaitPortFree([int]$port, [int]$ms = 5000) { Wait-Until { PortFree $port } $ms 100 }

function Start-App {
    $env:MOEFRP_HOME = $dataHome
    $env:MOEFRP_DIAG = $diagLog
    $env:MOEFRP_CDP_PORT = "$CdpPort"
    $n = DiagCount "frontend ready"
    $p = Start-Process -FilePath $Exe -PassThru
    Remove-Item Env:\MOEFRP_CDP_PORT, Env:\MOEFRP_DIAG, Env:\MOEFRP_HOME
    if (-not (Wait-Until { (DiagCount "frontend ready") -gt $n } 30000)) { throw "app did not become ready" }
    return $p
}

# 通过托盘菜单"退出"（↑ 选最后一项），返回退出耗时
function Quit-ViaTray($p) {
    $tray = Find-TrayIcon $p.Id
    if (-not $tray) { throw "tray icon not found" }
    $n = DiagCount "tray menu: quit"
    # 向菜单窗口投递按键偶尔会丢（菜单还没拿到焦点），没生效就重新打开菜单再试
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        Send-TrayEvent $tray $WM_CONTEXTMENU
        $script:menuWnd = [IntPtr]::Zero
        $null = Wait-Until { $m = [SpikeTray]::PopupMenusOf([uint32]$p.Id); if ($m.Count -gt 0) { $script:menuWnd = $m[0]; $true } else { $false } } 3000
        if ($script:menuWnd -eq [IntPtr]::Zero) { continue }
        $hMenu = [SpikeTray]::SendMessage($script:menuWnd, $MN_GETHMENU, [IntPtr]::Zero, [IntPtr]::Zero)
        $items = [SpikeTray]::MenuItems($hMenu)
        Start-Sleep -Milliseconds 200
        $sw = [Diagnostics.Stopwatch]::StartNew()
        Send-MenuKey $script:menuWnd $VK_UP
        Start-Sleep -Milliseconds 150
        Send-MenuKey $script:menuWnd $VK_RETURN
        if (Wait-Until { (DiagCount "tray menu: quit") -gt $n } 2000) {
            $exited = $p.WaitForExit(8000)
            return [ordered]@{ menuItems = $items; attempts = $attempt; quitClicked = $true; exited = $exited; exitMs = $sw.ElapsedMilliseconds }
        }
        # 关掉还开着的菜单
        if ([SpikeTray]::PopupMenusOf([uint32]$p.Id).Count -gt 0) { Send-MenuKey $script:menuWnd 0x1B; Start-Sleep -Milliseconds 300 }
    }
    throw "quit via tray menu failed after 3 attempts"
}

foreach ($port in 17000..17004) { if (-not (PortFree $port)) { throw "port $port is in use, cannot run e2e" } }
Get-Process MoeFrp-e2e, localfrps -ErrorAction SilentlyContinue | Stop-Process -Force

$frpsOut = Join-Path $OutDir "localfrps.log"
$frps = Start-Process -FilePath $localFrps -ArgumentList "-port", "17000", "-token", "e2e-token", "-echo", "127.0.0.1:17001" `
    -PassThru -WindowStyle Hidden -RedirectStandardOutput $frpsOut -RedirectStandardError "$frpsOut.err"
if (-not (Wait-Until { -not (PortFree 17000) } 10000)) { throw "localfrps did not start" }

try {
    # ---- 第一次运行：主流程 ----
    $app = Start-App
    $summary.firstRunReadyMs = [int](Select-String -Path $diagLog -Pattern "frontend ready: (\d+) ms").Matches[-1].Groups[1].Value
    node "$PSScriptRoot\drive-e2e.mjs" $CdpPort $OutDir main > (Join-Path $OutDir "main.json")
    $summary.mainScenarioOk = ($LASTEXITCODE -eq 0)

    # 隧道运行中从托盘退出：GracefulClose(500ms) 后进程退出，frps 上的端口被释放（PLAN 6.3.8）
    $q = Quit-ViaTray $app
    $q.remotePortFreeAfterQuit = WaitPortFree 17002
    $q.engineStoppedLogged = DiagHas "engine stopped \(quit\)"
    $summary.quitWhileRunning = $q

    # ---- 第二次运行：配置持久化、连接失败原因、外壳 ----
    $app = Start-App
    node "$PSScriptRoot\drive-e2e.mjs" $CdpPort $OutDir relaunch > (Join-Path $OutDir "relaunch.json")
    $summary.relaunchScenarioOk = ($LASTEXITCODE -eq 0)

    $shell = [ordered]@{}
    $hwnd = [IntPtr]::Zero
    $null = Wait-Until { $script:hwnd = Get-AppWindow $app.Id "*FRP*"; $script:hwnd -ne [IntPtr]::Zero } 5000
    $shell.windowFound = ($hwnd -ne [IntPtr]::Zero)
    # 关闭按钮 → 最小化到托盘（WM_CLOSE 与点 X 等价）
    [void][SpikeWin32]::PostMessage($hwnd, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)
    $null = Wait-Until { -not [SpikeWin32]::IsWindowVisible($hwnd) } 3000
    Start-Sleep -Milliseconds 300
    $app.Refresh()
    $shell.hiddenToTray = (-not [SpikeWin32]::IsWindowVisible($hwnd)) -and (DiagHas "WindowClosing intercepted")
    $shell.aliveAfterClose = -not $app.HasExited
    $shell.tunnelStillForwarding = $false
    try {
        $c = [Net.Sockets.TcpClient]::new("127.0.0.1", 17002); $s = $c.GetStream()
        $buf = [Text.Encoding]::ASCII.GetBytes("tray`n"); $s.Write($buf, 0, $buf.Length); $s.ReadTimeout = 3000
        $rb = New-Object byte[] 16; $n = $s.Read($rb, 0, 16); $c.Close()
        $shell.tunnelStillForwarding = ([Text.Encoding]::ASCII.GetString($rb, 0, $n).Trim() -eq "tray")
    } catch {}
    # 单实例：第二个进程立即退出，已有窗口被激活
    $env:MOEFRP_HOME = $dataHome
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $p2 = Start-Process -FilePath $Exe -PassThru
    Remove-Item Env:\MOEFRP_HOME
    $shell.secondExited = $p2.WaitForExit(15000)
    $shell.secondLifetimeMs = $sw.ElapsedMilliseconds
    $null = Wait-Until { [SpikeWin32]::IsWindowVisible($hwnd) } 5000
    $shell.secondInstanceActivatedWindow = [SpikeWin32]::IsWindowVisible($hwnd) -and (DiagHas "second instance launched")
    $shell.processCount = @(Get-Process -Name MoeFrp-e2e -ErrorAction SilentlyContinue).Count
    $summary.shell = $shell

    $q2 = Quit-ViaTray $app
    $q2.remotePortFreeAfterQuit = WaitPortFree 17002
    $summary.quitSecondRun = $q2

    # ---- 第三次运行：托盘"启动上次的配置"，然后模拟注销（WM_ENDSESSION）----
    $app = Start-App
    $third = [ordered]@{}
    $tray = Find-TrayIcon $app.Id
    $nRunning = DiagCount "tunnel state: running profile=`"e2e`""
    for ($attempt = 1; $attempt -le 3 -and -not (DiagHas "tray menu: toggle"); $attempt++) {
        Send-TrayEvent $tray $WM_CONTEXTMENU
        $script:menuWnd = [IntPtr]::Zero
        $null = Wait-Until { $m = [SpikeTray]::PopupMenusOf([uint32]$app.Id); if ($m.Count -gt 0) { $script:menuWnd = $m[0]; $true } else { $false } } 3000
        Start-Sleep -Milliseconds 200
        # ↑ 到"退出"，再 ↑ 跳过分隔线到"启动上次的配置"
        Send-MenuKey $script:menuWnd $VK_UP; Start-Sleep -Milliseconds 100
        Send-MenuKey $script:menuWnd $VK_UP; Start-Sleep -Milliseconds 100
        Send-MenuKey $script:menuWnd $VK_RETURN
        if (-not (Wait-Until { DiagHas "tray menu: toggle" } 2000)) {
            if ([SpikeTray]::PopupMenusOf([uint32]$app.Id).Count -gt 0) { Send-MenuKey $script:menuWnd 0x1B; Start-Sleep -Milliseconds 300 }
        }
    }
    $third.trayStartLast = (Wait-Until { (DiagCount "tunnel state: running profile=`"e2e`"") -gt $nRunning } 10000) -and (Wait-Until { -not (PortFree 17002) } 5000)
    $hwnd3 = [IntPtr]::Zero
    $null = Wait-Until { $script:hwnd3 = Get-AppWindow $app.Id "*FRP*"; $script:hwnd3 -ne [IntPtr]::Zero } 5000
    $sw = [Diagnostics.Stopwatch]::StartNew()
    [void][SpikeWin32]::SendMessage($hwnd3, 0x0016, [IntPtr]1, [IntPtr]::Zero)   # WM_ENDSESSION(TRUE)
    $third.endSessionHandledMs = $sw.ElapsedMilliseconds
    $third.engineStoppedOnEndSession = Wait-Until { DiagHas "engine stopped \(end session\)" } 5000
    $third.remotePortFreeAfterEndSession = WaitPortFree 17002
    $third.exitedAfterEndSession = $app.WaitForExit(5000)
    $summary.endSession = $third

    # ---- 本地存储（PLAN 6.5） ----
    $roaming = Join-Path $dataHome "Roaming"; $local = Join-Path $dataHome "Local"
    $cache = Join-Path $roaming "cache.bin"
    $bytes = [IO.File]::ReadAllBytes($cache)
    $raw = [Text.Encoding]::UTF8.GetString($bytes)
    $settings = Get-Content (Join-Path $roaming "settings.json") -Raw | ConvertFrom-Json
    $summary.storage = [ordered]@{
        settingsJson     = Test-Path (Join-Path $roaming "settings.json")
        installId        = $settings.installId
        cacheMagic       = [Text.Encoding]::ASCII.GetString($bytes, 0, 4)
        cacheHasPlainToken = $raw.Contains("e2e-token") -or $raw.Contains("local-frps")
        webView2Dir      = Test-Path (Join-Path $local "WebView2")
        crashLog         = Test-Path (Join-Path $local "crash.log")
        startupLog       = Test-Path (Join-Path $local "startup.log")
    }
}
finally {
    Get-Process MoeFrp-e2e -ErrorAction SilentlyContinue | Stop-Process -Force
    $frps | Stop-Process -Force -ErrorAction SilentlyContinue
    if (-not $KeepData) { Start-Sleep -Milliseconds 500; Remove-Item $dataHome -Recurse -Force -ErrorAction SilentlyContinue }
}

$summary | ConvertTo-Json -Depth 5 | Tee-Object -FilePath (Join-Path $OutDir "summary.json")
Write-Host "---- main.json checks ----"
(Get-Content (Join-Path $OutDir "main.json") -Raw | ConvertFrom-Json).checks | ConvertTo-Json -Depth 4
Write-Host "---- relaunch.json checks ----"
(Get-Content (Join-Path $OutDir "relaunch.json") -Raw | ConvertFrom-Json).checks | ConvertTo-Json -Depth 4

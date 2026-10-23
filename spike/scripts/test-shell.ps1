# 自动验证 Wails 应用外壳：启动耗时、关闭到托盘、单实例激活。
# 托盘图标/菜单需要人工（或 computer-use）点击，脚本最后会让应用保持运行，并打印 PID。
param(
    [Parameter(Mandatory)] [string]$Exe,
    [string]$TitleLike = "MoeFrp Spike*",
    [switch]$FreshWebviewData,
    [switch]$Quit
)
$ErrorActionPreference = "Stop"
. "$PSScriptRoot\win32.ps1"

$Exe = (Resolve-Path $Exe).Path
$name = [IO.Path]::GetFileNameWithoutExtension($Exe)
$diagLog = Join-Path (Split-Path $Exe) "spike-diag.log"

Get-Process -Name $name -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 300
Remove-Item $diagLog -ErrorAction SilentlyContinue
if ($FreshWebviewData) {
    # Wails v3 默认 WebView2 用户数据目录是 %APPDATA%\<exe 名>
    Remove-Item (Join-Path $env:APPDATA "$name.exe") -Recurse -Force -ErrorAction SilentlyContinue
}

function DiagHas([string]$pattern) { (Test-Path $diagLog) -and (Select-String -Path $diagLog -Pattern $pattern -Quiet) }

$result = [ordered]@{}
$p = Start-Process -FilePath $Exe -PassThru
$ok = Wait-Until { DiagHas "frontend ready" } 30000
$result.startOk = $ok
$line = (Select-String -Path $diagLog -Pattern "frontend ready: (\d+) ms").Matches | Select-Object -First 1
$result.readyMs = if ($line) { [int]$line.Groups[1].Value } else { $null }

$hwnd = [IntPtr]::Zero
$null = Wait-Until { $script:hwnd = Get-AppWindow $p.Id $TitleLike; $script:hwnd -ne [IntPtr]::Zero } 5000
$result.windowFound = ($hwnd -ne [IntPtr]::Zero)
$result.windowClass = [SpikeWin32]::Class($hwnd)
$result.visibleAtStart = [SpikeWin32]::IsWindowVisible($hwnd)

# 1) 关闭到托盘：发 WM_CLOSE（与点 X 等价，点 X 走 SC_CLOSE→WM_CLOSE）
[void][SpikeWin32]::PostMessage($hwnd, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)
$null = Wait-Until { -not [SpikeWin32]::IsWindowVisible($hwnd) } 3000
Start-Sleep -Milliseconds 500
$p.Refresh()
$result.closeIntercepted = DiagHas "WindowClosing intercepted"
$result.hiddenAfterClose = -not [SpikeWin32]::IsWindowVisible($hwnd)
$result.aliveAfterClose = -not $p.HasExited

# 2) 单实例：再启动一次，第二个进程应立即退出，并把已有窗口显示出来
$sw = [Diagnostics.Stopwatch]::StartNew()
$p2 = Start-Process -FilePath $Exe -ArgumentList "--from-second" -PassThru
$p2.WaitForExit(15000) | Out-Null
$result.secondExited = $p2.HasExited
$result.secondExitCode = if ($p2.HasExited) { $p2.ExitCode } else { $null }
$result.secondLifetimeMs = $sw.ElapsedMilliseconds
$null = Wait-Until { [SpikeWin32]::IsWindowVisible($hwnd) } 5000
$result.secondNotified = DiagHas "second instance launched"
$result.visibleAfterSecond = [SpikeWin32]::IsWindowVisible($hwnd)
Start-Sleep -Milliseconds 300
$result.foregroundAfterSecond = ([SpikeWin32]::GetForegroundWindow() -eq $hwnd)
$result.processCount = @(Get-Process -Name $name -ErrorAction SilentlyContinue).Count
$result.firstPid = $p.Id

if ($Quit) {
    $p.Kill(); $p.WaitForExit(5000) | Out-Null
}

$result | ConvertTo-Json
Write-Host "---- diag ----"
Get-Content $diagLog

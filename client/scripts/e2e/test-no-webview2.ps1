# 模拟"缺少 WebView2"：把 WebviewBrowserPath 指向一个空目录（Wails 找不到 WebView2 client DLL，
# 走的是和系统没装 Evergreen Runtime 时同一条 Embed 失败路径）。观察进程退出码、有无窗口/提示框。
param([string]$Exe = "$PSScriptRoot\..\..\bin\MoeFrp-e2e.exe", [int]$WaitMs = 8000)
$ErrorActionPreference = "Stop"
. "$PSScriptRoot\win32.ps1"
$Exe = (Resolve-Path $Exe).Path
$name = [IO.Path]::GetFileNameWithoutExtension($Exe)
$dataHome = Join-Path $env:TEMP ("moefrp-nowv2-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
$diagLog = Join-Path $dataHome "diag.log"
New-Item -ItemType Directory -Force $dataHome | Out-Null
Get-Process -Name $name -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 300
Remove-Item $diagLog -ErrorAction SilentlyContinue

$empty = Join-Path $dataHome "empty-webview2"
New-Item -ItemType Directory -Force $empty | Out-Null
$env:MOEFRP_WEBVIEW_BROWSER_PATH = $empty
$env:MOEFRP_HOME = $dataHome
$env:MOEFRP_DIAG = $diagLog
$p = Start-Process -FilePath $Exe -PassThru
Remove-Item Env:\MOEFRP_WEBVIEW_BROWSER_PATH, Env:\MOEFRP_HOME, Env:\MOEFRP_DIAG

$r = [ordered]@{}
$dialog = $null
$sw = [Diagnostics.Stopwatch]::StartNew()
while ($sw.ElapsedMilliseconds -lt $WaitMs -and -not $p.HasExited) {
    foreach ($h in [SpikeWin32]::WindowsOf([uint32]$p.Id)) {
        if ([SpikeWin32]::Class($h) -eq "#32770" -and [SpikeWin32]::IsWindowVisible($h)) { $dialog = $h }
    }
    if ($dialog) { break }
    Start-Sleep -Milliseconds 100
    $p.Refresh()
}
$r.exitedOnItsOwn = $p.HasExited
$r.exitCode = if ($p.HasExited) { $p.ExitCode } else { $null }
$r.msUntilExitOrDialog = $sw.ElapsedMilliseconds
$r.dialogShown = [bool]$dialog
if ($dialog) {
    $r.dialogTitle = [SpikeWin32]::Title($dialog)
    # 读提示框正文（Static 控件）
    $texts = @()
    $child = [IntPtr]::Zero
    Add-Type -Namespace MoeFrpNs -Name U -MemberDefinition '[DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern System.IntPtr FindWindowEx(System.IntPtr p, System.IntPtr a, string c, string t);' -ErrorAction SilentlyContinue
    while (($child = [MoeFrpNs.U]::FindWindowEx($dialog, $child, "Static", $null)) -ne [IntPtr]::Zero) { $t = [SpikeWin32]::Title($child); if ($t) { $texts += $t } }
    $r.dialogText = $texts -join " | "
    [void][SpikeWin32]::PostMessage($dialog, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)  # 关掉提示框
    $p.WaitForExit(5000) | Out-Null
    $r.exitCodeAfterDialog = if ($p.HasExited) { $p.ExitCode } else { "still running" }
}
$visibleWindows = 0
if (-not $p.HasExited) { $p.Kill() }
$r | ConvertTo-Json
Write-Host "---- diag ----"
if (Test-Path $diagLog) { Get-Content $diagLog }
Remove-Item $dataHome -Recurse -Force -ErrorAction SilentlyContinue

# 验证托盘：图标是否已注册到通知区（向 Explorer 查询 Shell_NotifyIconGetRect）、左键显示窗口、右键菜单内容与菜单项点击。
# 前提：被测应用已在运行（例如 test-shell.ps1 之后）。最后通过托盘菜单"退出"结束进程。
param(
    [Parameter(Mandatory)] [string]$Exe,
    [string]$TitleLike = "MoeFrp Spike*",
    [switch]$KeepRunning
)
$ErrorActionPreference = "Stop"
. "$PSScriptRoot\win32.ps1"
. "$PSScriptRoot\win32tray.ps1"

$Exe = (Resolve-Path $Exe).Path
$name = [IO.Path]::GetFileNameWithoutExtension($Exe)
$diagLog = Join-Path (Split-Path $Exe) "spike-diag.log"
function DiagCount([string]$pattern) { if (Test-Path $diagLog) { @(Select-String -Path $diagLog -Pattern $pattern).Count } else { 0 } }

$p = Get-Process -Name $name | Select-Object -First 1
$hwnd = Get-AppWindow $p.Id $TitleLike
$r = [ordered]@{ pid = $p.Id }

$tray = Find-TrayIcon $p.Id
$r.trayIconRegistered = [bool]$tray
$r.trayIconRect = if ($tray) { $tray.Rect } else { $null }
$r.trayUid = if ($tray) { $tray.Uid } else { $null }
if (-not $tray) { $r | ConvertTo-Json; exit 1 }

# 左键单击托盘图标（与 Explorer 回调同一条消息）
[void][SpikeWin32]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
$null = Wait-Until { -not [SpikeWin32]::IsWindowVisible($hwnd) } 3000
$r.hiddenBeforeClick = -not [SpikeWin32]::IsWindowVisible($hwnd)
$n0 = DiagCount "tray left click"
Send-TrayEvent $tray $WM_LBUTTONUP
$null = Wait-Until { [SpikeWin32]::IsWindowVisible($hwnd) } 3000
$r.leftClickHandled = (DiagCount "tray left click") -gt $n0
$r.visibleAfterLeftClick = [SpikeWin32]::IsWindowVisible($hwnd)

# 右键：打开菜单，读出菜单项，键盘 ↓ + 回车 选第一项"显示主界面"
[void][SpikeWin32]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
$null = Wait-Until { -not [SpikeWin32]::IsWindowVisible($hwnd) } 3000
Send-TrayEvent $tray $WM_CONTEXTMENU
$menuWnd = [IntPtr]::Zero
$null = Wait-Until { $m = [SpikeTray]::PopupMenusOf([uint32]$p.Id); if ($m.Count -gt 0) { $script:menuWnd = $m[0]; $true } else { $false } } 3000
$r.menuOpened = ($menuWnd -ne [IntPtr]::Zero)
if ($r.menuOpened) {
    $hMenu = [SpikeTray]::SendMessage($menuWnd, $MN_GETHMENU, [IntPtr]::Zero, [IntPtr]::Zero)
    $r.menuItems = [SpikeTray]::MenuItems($hMenu)
    $n1 = DiagCount "tray menu: show"
    Send-MenuKey $menuWnd $VK_DOWN
    Start-Sleep -Milliseconds 150
    Send-MenuKey $menuWnd $VK_RETURN
    $null = Wait-Until { [SpikeWin32]::IsWindowVisible($hwnd) } 3000
    $r.menuShowClicked = (DiagCount "tray menu: show") -gt $n1
    $r.visibleAfterMenuShow = [SpikeWin32]::IsWindowVisible($hwnd)
}

if (-not $KeepRunning) {
    # 再开一次菜单，↑ 选最后一项"退出"
    Send-TrayEvent $tray $WM_CONTEXTMENU
    $menuWnd = [IntPtr]::Zero
    $null = Wait-Until { $m = [SpikeTray]::PopupMenusOf([uint32]$p.Id); if ($m.Count -gt 0) { $script:menuWnd = $m[0]; $true } else { $false } } 3000
    if ($menuWnd -ne [IntPtr]::Zero) {
        Send-MenuKey $menuWnd $VK_UP
        Start-Sleep -Milliseconds 150
        Send-MenuKey $menuWnd $VK_RETURN
    }
    $r.exitedViaMenu = $p.WaitForExit(8000)
    $r.quitLogged = (DiagCount "tray menu: quit") -gt 0
    $r.shutdownLogged = (DiagCount "OnShutdown") -gt 0
    $r.trayIconGoneAfterExit = -not [bool](Find-TrayIcon $p.Id)
}

$r | ConvertTo-Json
Write-Host "---- diag tail ----"
Get-Content $diagLog | Select-Object -Last 12

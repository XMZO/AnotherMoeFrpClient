# 端到端验证 app-engine：起 localfrps → 启动 app-engine（打开 CDP 端口）→ node 脚本点按钮验证 → 托盘"退出"。
param(
    [string]$Exe = "$PSScriptRoot\..\app-engine\bin\app-engine.exe",
    [string]$LocalFrps = "$PSScriptRoot\..\bin\localfrps.exe",
    [int]$CdpPort = 9333
)
$ErrorActionPreference = "Stop"
. "$PSScriptRoot\win32.ps1"

$Exe = (Resolve-Path $Exe).Path
$diagLog = Join-Path (Split-Path $Exe) "spike-diag.log"
Get-Process app-engine, localfrps -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 300
Remove-Item $diagLog -ErrorAction SilentlyContinue

$frps = Start-Process -FilePath (Resolve-Path $LocalFrps).Path -PassThru -WindowStyle Hidden
Start-Sleep -Milliseconds 500

$env:SPIKE_CDP_PORT = "$CdpPort"
$app = Start-Process -FilePath $Exe -PassThru
Remove-Item Env:\SPIKE_CDP_PORT
$null = Wait-Until { (Test-Path $diagLog) -and (Select-String -Path $diagLog -Pattern "frontend ready" -Quiet) } 30000

$shotDir = (Resolve-Path "$PSScriptRoot\..").Path
node "$PSScriptRoot\drive-engine-gui.mjs" $CdpPort 17002 $shotDir

# 再启动一次隧道（走托盘菜单的"启动隧道"项不方便，用前端按钮已验证过），这里验证"隧道运行中直接从托盘退出"
Write-Host "---- quit while running ----"
& "$PSScriptRoot\test-tray.ps1" -Exe $Exe -TitleLike "MoeFrp Spike*"

$frps | Stop-Process -Force -ErrorAction SilentlyContinue
Write-Host "---- diag ----"
Get-Content $diagLog

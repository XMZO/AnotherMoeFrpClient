# 隧道运行中从托盘"退出"：OnShutdown 里 GracefulClose，进程退出，frps 上的远程端口被释放。
param(
    [string]$Exe = "$PSScriptRoot\..\app-engine\bin\app-engine.exe",
    [string]$LocalFrps = "$PSScriptRoot\..\bin\localfrps.exe",
    [int]$CdpPort = 9334
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
node "$PSScriptRoot\drive-engine-gui.mjs" $CdpPort 17002 $env:TEMP leave-running
& "$PSScriptRoot\test-tray.ps1" -Exe $Exe -TitleLike "MoeFrp Spike*"
$free = $false
for ($i = 0; $i -lt 50 -and -not $free; $i++) {
    try { $l = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 17002); $l.Start(); $l.Stop(); $free = $true } catch { Start-Sleep -Milliseconds 100 }
}
Write-Host "remotePort17002FreeAfterQuit=$free"
$frps | Stop-Process -Force -ErrorAction SilentlyContinue

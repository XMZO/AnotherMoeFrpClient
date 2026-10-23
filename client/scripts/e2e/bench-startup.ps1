# 测启动耗时：进程创建 → main() / 前端挂载完成（Vue 挂载后调用 Go 的 AppService.Ready）。
# 第 1 次运行使用全新的数据目录（WebView2 用户数据为空，模拟首次运行），之后是热启动。
# 数据放在临时目录（MOEFRP_HOME），不动真实的 %APPDATA%\MoeFrp。
param(
    [string]$Exe = "$PSScriptRoot\..\..\bin\MoeFrp-e2e.exe",
    [int]$Runs = 6
)
$ErrorActionPreference = "Stop"
. "$PSScriptRoot\win32.ps1"
$Exe = (Resolve-Path $Exe).Path
$dataHome = Join-Path $env:TEMP ("moefrp-bench-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
$diagLog = Join-Path $dataHome "diag.log"
New-Item -ItemType Directory -Force $dataHome | Out-Null

$rows = @()
try {
    for ($i = 1; $i -le $Runs; $i++) {
        Get-Process -Name MoeFrp -ErrorAction SilentlyContinue | Stop-Process -Force
        Start-Sleep -Milliseconds 800
        Remove-Item $diagLog -ErrorAction SilentlyContinue
        $env:MOEFRP_HOME = $dataHome
        $env:MOEFRP_DIAG = $diagLog
        $sw = [Diagnostics.Stopwatch]::StartNew()
        $p = Start-Process -FilePath $Exe -PassThru
        Remove-Item Env:\MOEFRP_HOME, Env:\MOEFRP_DIAG
        $ok = Wait-Until { (Test-Path $diagLog) -and (Select-String -Path $diagLog -Pattern "frontend ready" -Quiet) } 30000
        if (-not $ok) { throw "run $i did not become ready" }
        $wall = $sw.ElapsedMilliseconds
        $ready = [int](Select-String -Path $diagLog -Pattern "frontend ready: (\d+) ms").Matches[0].Groups[1].Value
        $main = [int](Select-String -Path $diagLog -Pattern "main\(\) entered: (\d+) ms").Matches[0].Groups[1].Value
        Start-Sleep -Milliseconds 500
        $p.Refresh()
        $rows += [pscustomobject]@{
            run = $i; kind = $(if ($i -eq 1) { "first (empty data dir)" } else { "warm" })
            mainMs = $main; frontendReadyMs = $ready; launcherObservedMs = $wall
            workingSetMB = [math]::Round($p.WorkingSet64 / 1MB, 1)
        }
    }
}
finally {
    Get-Process -Name MoeFrp -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Milliseconds 500
    Remove-Item $dataHome -Recurse -Force -ErrorAction SilentlyContinue
}
$rows | Format-Table -AutoSize | Out-String -Width 200

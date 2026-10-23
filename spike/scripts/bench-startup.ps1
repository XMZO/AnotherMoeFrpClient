# 测启动耗时：进程创建 → main() / 前端挂载完成（Vue onMounted 调 Go 的 Ready）。
# 第 1 次运行前删除 WebView2 用户数据目录（模拟首次运行），之后是热启动。
param(
    [Parameter(Mandatory)] [string]$Exe,
    [Parameter(Mandatory)] [string]$WebviewDataDir,
    [int]$Runs = 6
)
$ErrorActionPreference = "Stop"
. "$PSScriptRoot\win32.ps1"
$Exe = (Resolve-Path $Exe).Path
$name = [IO.Path]::GetFileNameWithoutExtension($Exe)
$diagLog = Join-Path (Split-Path $Exe) "spike-diag.log"

$rows = @()
for ($i = 1; $i -le $Runs; $i++) {
    Get-Process -Name $name -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Milliseconds 800
    Remove-Item $diagLog -ErrorAction SilentlyContinue
    if ($i -eq 1) { Remove-Item $WebviewDataDir -Recurse -Force -ErrorAction SilentlyContinue }
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $p = Start-Process -FilePath $Exe -PassThru
    $ok = Wait-Until { (Test-Path $diagLog) -and (Select-String -Path $diagLog -Pattern "frontend ready" -Quiet) } 30000
    $wall = $sw.ElapsedMilliseconds
    $ready = [int](Select-String -Path $diagLog -Pattern "frontend ready: (\d+) ms").Matches[0].Groups[1].Value
    $main = [int](Select-String -Path $diagLog -Pattern "main\(\) entered: (\d+) ms").Matches[0].Groups[1].Value
    $p.Refresh()
    $rows += [pscustomobject]@{
        run = $i; kind = $(if ($i -eq 1) { "first (fresh WebView2 data)" } else { "warm" })
        mainMs = $main; frontendReadyMs = $ready; launcherObservedMs = $wall
        workingSetMB = [math]::Round($p.WorkingSet64 / 1MB, 1)
    }
}
Get-Process -Name $name -ErrorAction SilentlyContinue | Stop-Process -Force
# 统计 WebView2 子进程（msedgewebview2.exe）不在这里做，只记主进程工作集
$rows | Format-Table -AutoSize | Out-String -Width 200

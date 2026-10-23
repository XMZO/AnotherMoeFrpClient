# 发布构建（PLAN 6.11、7.11、Phase 6）：跑测试 → 构建客户端 exe 和服务端 Linux 二进制 → 生成 SHA256SUMS。
#
#   pwsh .\scripts\release.ps1 -Version 3.0.0
#   pwsh .\scripts\release.ps1 -Version 3.0.1 -SkipTests     # 只构建
#
# 产物在 dist\<版本>\：
#   MoeFrp-<版本>-windows-amd64.exe     客户端（单文件，未签名）
#   moefrp-server-<版本>-linux-amd64    服务端（静态链接）
#   deploy\                              systemd 单元、Caddyfile、config.example.toml
#   SHA256SUMS
#
# 签名、Defender 扫描、干净虚拟机测试、上线切换都需要人工完成，见 docs\release.md 和 docs\cutover.md。
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [switch]$SkipTests
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($Version -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') {
    throw "版本号格式应为 X.Y.Z 或 X.Y.Z-tag，收到：$Version"
}
$root = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $root "dist\$Version"
$client = Join-Path $root 'client'
$server = Join-Path $root 'server'
$shared = Join-Path $root 'shared'
$env:PATH = "$env:PATH;$(go env GOPATH)\bin"

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Run([string]$dir, [string]$exe, [string[]]$argv) {
    Push-Location $dir
    try {
        & $exe @argv
        if ($LASTEXITCODE -ne 0) { throw "$exe $($argv -join ' ') 失败（退出码 $LASTEXITCODE，目录 $dir）" }
    } finally { Pop-Location }
}

if (Test-Path $dist) { Remove-Item -Recurse -Force $dist }
New-Item -ItemType Directory -Force $dist | Out-Null

if (-not $SkipTests) {
    Step 'shared：go vet / go test'
    Run $shared go @('vet', './...')
    Run $shared go @('test', '-count=1', './...')
    Step 'server：go vet / go test'
    Run $server go @('vet', './...')
    Run $server go @('test', '-count=1', './...')
    Step 'client：go vet / go test（internal）'
    Run $client go @('vet', './internal/...')
    Run $client go @('test', '-count=1', './internal/...')
    Step 'client 前端：vue-tsc / vitest'
    Run (Join-Path $client 'frontend') pnpm @('exec', 'vue-tsc', '--noEmit')
    Run (Join-Path $client 'frontend') pnpm @('test')
}

Step "客户端：wails3 build VERSION=$Version"
Run $client wails3 @('build', 'PACKAGE_MANAGER=pnpm', "VERSION=$Version")
$exeName = "MoeFrp-$Version-windows-amd64.exe"
Copy-Item (Join-Path $client 'bin\MoeFrp.exe') (Join-Path $dist $exeName)

Step "服务端：linux/amd64，CGO_ENABLED=0"
$serverName = "moefrp-server-$Version-linux-amd64"
$old = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
try {
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    Run $server go @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$Version", '-o', (Join-Path $dist $serverName), './cmd/moefrp-server')
} finally {
    foreach ($k in $old.Keys) { if ($null -eq $old[$k]) { Remove-Item "Env:$k" -ErrorAction SilentlyContinue } else { Set-Item "Env:$k" $old[$k] } }
}

Step '部署文件'
Copy-Item -Recurse (Join-Path $server 'deploy') (Join-Path $dist 'deploy')

Step 'SHA256SUMS'
$sums = Get-ChildItem $dist -File | Sort-Object Name | ForEach-Object {
    '{0}  {1}' -f (Get-FileHash -Algorithm SHA256 $_.FullName).Hash.ToLower(), $_.Name
}
$sums | Set-Content -Encoding ascii (Join-Path $dist 'SHA256SUMS')
$sums | ForEach-Object { Write-Host "    $_" }

$exeHash = (Get-FileHash -Algorithm SHA256 (Join-Path $dist $exeName)).Hash.ToLower()
Write-Host ''
Write-Host "完成：$dist" -ForegroundColor Green
Write-Host "客户端 $exeName（$([math]::Round((Get-Item (Join-Path $dist $exeName)).Length / 1MB, 1)) MB）"
Write-Host ''
Write-Host '接下来（人工，见 docs\release.md）：'
Write-Host "  1. 代码签名：signtool sign /fd sha256 /tr <时间戳服务器> /td sha256 $exeName（签名后重新生成 SHA256SUMS）"
Write-Host '  2. 在开着 Defender 的机器上扫描；用火绒、360 各扫一遍'
Write-Host '  3. 干净 Win10 虚拟机：缺 WebView2 时应弹出提示框；装好后完整走一遍'
Write-Host "  4. 服务器 config.toml：latest_version = `"$Version`"，download_url 指向发布地址，sha256 = `"$exeHash`"（签名后用签名后的哈希）"

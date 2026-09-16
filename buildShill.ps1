<#
.SYNOPSIS
    Git AI Tool 一键打包脚本（Wails v2 / Windows）

.DESCRIPTION
    自动完成「检查环境 → 准备 wails CLI → 前端构建 → Go 编译 → 校验产物」全流程，
    产物落在 build\bin\ 下。
    -clean 会清空 build\bin，脚本默认会把 build\bin\config.json（用户配置）备份并在打包后恢复。

.PARAMETER Run
    打包完成后立即启动 exe

.PARAMETER SkipFrontend
    跳过前端 vite build，直接复用已有的 frontend\dist（前端没改时更快）

.PARAMETER NoClean
    保留 build\bin 里的旧文件（默认先清空）

.PARAMETER FreshConfig
    不恢复打包前的 config.json（即重置为全新无配置状态）

.PARAMETER Test
    打包前先跑 go test ./...，失败即中止

.PARAMETER Devtools
    构建带 devtools 的版本（可用 F12 打开开发者工具，仅排障用）

.PARAMETER Installer
    额外生成 NSIS 安装包（build\bin\*-installer.exe）

.PARAMETER Upx
    若本机装了 UPX，则压缩最终二进制（未装则自动跳过并提示）

.PARAMETER TrimPath
    移除二进制里的文件系统路径（体积略小、路径不泄露）

.PARAMETER Arch
    目标 CPU 架构：amd64（默认）/ arm64

.PARAMETER WebView2
    WebView2 运行时策略：download（默认）/ embed / browser / error

.PARAMETER ReinstallCLI
    强制重新 go install wails CLI

.PARAMETER DryRun
    只打印真正的编译命令，不执行

.EXAMPLE
    .\buildShill.ps1
    完整打包并校验

.EXAMPLE
    .\buildShill.ps1 -SkipFrontend -Run
    复用已有前端产物直接编译并运行

.EXAMPLE
    .\buildShill.ps1 -Test -Installer
    跑完单测后打包并额外生成安装包
#>
[CmdletBinding()]
param(
    [switch]$Run,
    [switch]$SkipFrontend,
    [switch]$NoClean,
    [switch]$FreshConfig,
    [switch]$Test,
    [switch]$Devtools,
    [switch]$Installer,
    [switch]$Upx,
    [switch]$TrimPath,
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    [ValidateSet('download', 'embed', 'browser', 'error')]
    [string]$WebView2 = 'download',
    [switch]$ReinstallCLI,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

# ---------- 小工具 ----------
function Write-Step($n, $total, $text) {
    Write-Host ''
    Write-Host "==> [$n/$total] $text" -ForegroundColor Cyan
}
function Write-Ok($text) { Write-Host "    OK  $text" -ForegroundColor Green }
function Write-Info($text) { Write-Host "    ..  $text" -ForegroundColor DarkGray }
function Write-Warn2($text) { Write-Host "    !!  $text" -ForegroundColor Yellow }
function Write-Err2($text) { Write-Host "    XX  $text" -ForegroundColor Red }
function Fail($text) { Write-Err2 $text; exit 1 }

$sw = [System.Diagnostics.Stopwatch]::StartNew()
Write-Host ''
Write-Host '========================================' -ForegroundColor Magenta
Write-Host '  Git AI Tool 打包脚本' -ForegroundColor Magenta
Write-Host '========================================' -ForegroundColor Magenta

# ---------- [1] 定位项目根目录 ----------
$totalSteps = 7
Write-Step 1 $totalSteps '定位项目根目录'

if ($PSScriptRoot) {
    $root = $PSScriptRoot
}
elseif ($MyInvocation.MyCommand.Path) {
    $root = Split-Path -Parent $MyInvocation.MyCommand.Path
}
else {
    $root = (Get-Location).Path
}
# 允许脚本被放在项目根目录，也允许从别处调用
if (-not (Test-Path (Join-Path $root 'wails.json'))) {
    $fallback = Join-Path $root 'git-ai-tool'
    if (Test-Path (Join-Path $fallback 'wails.json')) { $root = $fallback }
}

$wailsJsonPath = Join-Path $root 'wails.json'
$goModPath = Join-Path $root 'go.mod'
if (-not (Test-Path $wailsJsonPath)) { Fail "找不到 wails.json，$root 看起来不是 Wails 项目根目录" }
if (-not (Test-Path $goModPath)) { Fail "找不到 go.mod，$root 看起来不是 Wails 项目根目录" }
Write-Ok "项目根目录: $root"

# ---------- [2] 读取项目配置 ----------
Write-Step 2 $totalSteps '读取项目配置'

$wailsConf = Get-Content -LiteralPath $wailsJsonPath -Raw -Encoding UTF8 | ConvertFrom-Json
$outName = if ($wailsConf.outputfilename) { $wailsConf.outputfilename } else { $wailsConf.name }

$modText = Get-Content -LiteralPath $goModPath -Raw
$m = [regex]::Match($modText, 'github\.com/wailsapp/wails/v2\s+v([0-9][0-9A-Za-z\.\-]*)')
if (-not $m.Success) { Fail 'go.mod 里没找到 wails/v2 依赖，无法确定要安装哪个版本的 CLI' }
$wailsVer = $m.Groups[1].Value

# 从 app.go 解析应用版本号（const appVersion = "x.y.z"）
$appVersion = 'unknown'
$appGoPath = Join-Path $root 'app.go'
if (Test-Path $appGoPath) {
    $vm = Select-String -LiteralPath $appGoPath -Pattern 'appVersion\s*=\s*"([^"]+)"' | Select-Object -First 1
    if ($vm) { $appVersion = $vm.Matches[0].Groups[1].Value }
}

$binDir = Join-Path $root 'build\bin'
$exePath = Join-Path $binDir "$outName.exe"
Write-Ok "输出文件名: $outName.exe  (v$appVersion)"
Write-Ok "目标平台: windows/$Arch    Wails CLI: v$wailsVer"

# ---------- [3] 检查工具链 ----------
Write-Step 3 $totalSteps '检查工具链'

$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) { Fail '找不到 go，请先安装 Go 并加入 PATH' }
$goVersion = (& go version 2>&1 | Out-String).Trim()
Write-Ok "go: $goVersion"

$npmCmd = Get-Command npm -ErrorAction SilentlyContinue
if (-not $npmCmd) { Fail '找不到 npm，请先安装 Node.js 并加入 PATH' }
if (-not $SkipFrontend) {
    $nodeVersion = (& node -v 2>&1 | Out-String).Trim()
    Write-Ok "node: $nodeVersion"
}

# -SkipFrontend 依赖已构建好的 frontend/dist（go:embed 需要它）
if ($SkipFrontend -and -not (Test-Path (Join-Path $root 'frontend\dist\index.html'))) {
    Write-Warn2 'frontend\dist 不存在，-SkipFrontend 无法生效，自动改为完整前端构建'
    $SkipFrontend = $false
}

if (-not (Test-Path (Join-Path $root 'frontend\node_modules')) -and -not $SkipFrontend) {
    Write-Warn2 'frontend\node_modules 不存在，wails 会自动执行 npm install（可能较慢）'
}

# UPX 预检查：没装就别把 -upx 传给 wails，避免构建中途失败
if ($Upx -and -not (Get-Command upx -ErrorAction SilentlyContinue)) {
    Write-Warn2 '未检测到 upx，-Upx 已被忽略'
    $Upx = $false
}

# 解析 wails CLI：PATH -> GOPATH\bin -> 自动安装
function Resolve-Wails {
    $c = Get-Command wails -ErrorAction SilentlyContinue
    if ($c) { return $c.Source }
    $candidates = @()
    $gopath = (& go env GOPATH 2>&1 | Out-String).Trim()
    if ($gopath) { $candidates += (Join-Path $gopath 'bin\wails.exe') }
    if ($env:USERPROFILE) { $candidates += (Join-Path $env:USERPROFILE 'go\bin\wails.exe') }
    foreach ($p in $candidates) { if (Test-Path -LiteralPath $p) { return $p } }
    return $null
}

$wailsExe = if ($ReinstallCLI) { $null } else { Resolve-Wails }
if (-not $wailsExe) {
    Write-Info "未找到 wails CLI，正在安装 github.com/wailsapp/wails/v2/cmd/wails@v$wailsVer ..."
    Push-Location $root
    try {
        & go install "github.com/wailsapp/wails/v2/cmd/wails@v$wailsVer"
        if ($LASTEXITCODE -ne 0) { Fail "wails CLI 安装失败（exit=$LASTEXITCODE），请检查网络 / GOPROXY" }
    }
    finally { Pop-Location }
    $wailsExe = Resolve-Wails
    if (-not $wailsExe) { Fail 'wails CLI 安装后仍未找到，请确认 %GOPATH%\bin 存在 wails.exe' }
}
$wailsVerOut = ((& $wailsExe version 2>&1 | Out-String) -split "`r?`n" |
    Where-Object { $_ -match '\S' } | Select-Object -First 1)
Write-Ok "wails: $wailsExe  ($($wailsVerOut.Trim()))"

# 关掉可能占着 exe 的旧实例，避免编译产物被锁
$running = @(Get-Process -Name $outName -ErrorAction SilentlyContinue)
if ($running.Count -gt 0) {
    Write-Info "发现 $($running.Count) 个正在运行的 $outName，先结束它们（否则 exe 会被占用）"
    $running | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
}

# config.json 是用户配置（exe 同级绿色版模式），-clean 会清空 build\bin，先备份
$configFile = Join-Path $binDir 'config.json'
$configBackup = $null
if (-not $NoClean -and -not $FreshConfig -and (Test-Path -LiteralPath $configFile)) {
    $configBackup = Get-Content -LiteralPath $configFile -Raw -Encoding UTF8
    Write-Info '已备份 build\bin\config.json（打包后自动恢复；不想恢复请加 -FreshConfig）'
}

# ---------- [4] 运行单元测试（可选） ----------
Write-Step 4 $totalSteps '运行单元测试（-Test 时）'
if ($Test) {
    Push-Location $root
    try {
        & go test ./...
        $testCode = $LASTEXITCODE
    }
    finally { Pop-Location }
    if ($testCode -ne 0) { Fail "go test 失败（exit=$testCode），已中止打包" }
    Write-Ok '全部测试通过'
}
else {
    Write-Info '已跳过（加 -Test 启用打包前单测）'
}

# ---------- [5] 组装并执行构建 ----------
Write-Step 5 $totalSteps '组装构建命令'

$buildArgs = @('build', '-platform', "windows/$Arch", '-webview2', $WebView2)
if (-not $NoClean) { $buildArgs += '-clean' }
if ($SkipFrontend) { $buildArgs += '-s' }
if ($Devtools) { $buildArgs += '-devtools' }
if ($Installer) { $buildArgs += '-nsis' }
if ($Upx) { $buildArgs += '-upx' }
if ($TrimPath) { $buildArgs += '-trimpath' }
if ($DryRun) { $buildArgs += '-dryrun' }
$buildArgs += '-v'
$buildArgs += '1'

Write-Info "wails $($buildArgs -join ' ')"

Write-Step 6 $totalSteps '执行构建（前端 vite build + Go 编译，约 1 分钟）'

Push-Location $root
try {
    & $wailsExe @buildArgs
    $code = $LASTEXITCODE
}
finally { Pop-Location }

if ($code -ne 0) { Fail "构建失败（exit=$code）" }
Write-Ok '构建命令执行成功'

# ---------- [7] 校验产物 ----------
Write-Step 7 $totalSteps '校验产物'

if ($DryRun) {
    Write-Warn2 'DryRun 模式：未真正生成 exe'
}
else {
    if (-not (Test-Path -LiteralPath $exePath)) { Fail "构建结束但没找到产物: $exePath" }
    $f = Get-Item -LiteralPath $exePath
    $mb = [Math]::Round($f.Length / 1MB, 2)
    $sha = (Get-FileHash -LiteralPath $exePath -Algorithm SHA256).Hash.ToLower()
    Write-Ok "产物: $($f.FullName)"
    Write-Ok "版本: v$appVersion    大小: $mb MB    时间: $($f.LastWriteTime)"
    Write-Ok "SHA256: $sha"

    # 生成 .sha256 校验文件（标准格式，可用 certutil -hashfile 或 sha256sum -c 验证）
    $shaFile = "$exePath.sha256"
    Set-Content -LiteralPath $shaFile -Value "$sha  $outName.exe" -Encoding ASCII
    Write-Ok "校验文件: $shaFile"

    $extra = @(Get-ChildItem -LiteralPath $binDir -Filter '*.exe' -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -ne $f.FullName })
    foreach ($e in $extra) {
        Write-Ok ("附带产物: " + $e.Name + '  (' + [Math]::Round($e.Length / 1MB, 2) + ' MB)')
    }

    # 恢复打包前备份的用户配置
    # 注意：必须无 BOM 写入（PS5.1 的 Set-Content -Encoding UTF8 会带 BOM，Go 的 JSON 解析会报 invalid character 'ï'）
    if ($null -ne $configBackup) {
        $clean = $configBackup -replace "^\uFEFF", ''
        [System.IO.File]::WriteAllText($configFile, $clean, [System.Text.UTF8Encoding]::new($false))
        Write-Ok '已恢复 config.json（用户配置随包保留）'
    }
    elseif ($FreshConfig -and -not $NoClean) {
        Write-Info '按 -FreshConfig 要求，未恢复旧 config.json（应用将以全新状态启动）'
    }
}

Write-Host ''
Write-Host ('完成，用时 ' + [Math]::Round($sw.Elapsed.TotalSeconds, 1) + ' 秒') -ForegroundColor Green
Write-Host ''
Write-Host '提示：' -ForegroundColor DarkGray
Write-Host ('  · 配置文件位置：' + (Split-Path $exePath) + '\config.json（exe 同级，随 exe 一起拷贝即可）') -ForegroundColor DarkGray
Write-Host '  · main.go 用 go:embed 把 frontend/dist 编进二进制，改了前端必须重新打包' -ForegroundColor DarkGray
Write-Host '  · 分发给别人时，若目标机器没有 WebView2 运行时，用 -WebView2 embed' -ForegroundColor DarkGray
Write-Host ''

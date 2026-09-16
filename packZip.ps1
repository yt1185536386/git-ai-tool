<#
.SYNOPSIS
    Git AI Tool 绿色版一键打 zip（Wails v2 / Windows）

.DESCRIPTION
    把 build\bin 下的 exe 打成一个可以直接发给别人的 zip，产物落在 build\dist\ 下。
    zip 里默认只有 exe + 一份使用说明，**不含 config.json**（里面有 API Key 和工作区路径）。
    确需带上自己的配置（比如换机器迁移）时加 -WithConfig。

.PARAMETER Build
    打 zip 之前先调用 buildShill.ps1 重新打包 exe

.PARAMETER SkipFrontend
    配合 -Build 使用：复用已有的 frontend\dist，跳过 vite build

.PARAMETER WithConfig
    把 build\bin\config.json 一起打进 zip。**含明文 API Key，外发前务必确认**

.PARAMETER Output
    指定 zip 输出路径（默认 build\dist\<包名>.zip）

.PARAMETER Arch
    包名里标注的目标架构：amd64（默认）/ arm64

.PARAMETER KeepStaging
    保留临时暂存目录，方便检查 zip 里到底装了什么

.PARAMETER DryRun
    只打印将要做什么，不实际建目录 / 复制 / 压缩

.EXAMPLE
    .\packZip.ps1
    复用已有 exe，直接打一个纯绿色 zip

.EXAMPLE
    .\packZip.ps1 -Build
    先完整打包 exe，再压成 zip

.EXAMPLE
    .\packZip.ps1 -WithConfig -Output D:\tmp\mytool.zip
    连自己的 config.json 一起打，并指定输出位置
#>
[CmdletBinding()]
param(
    [switch]$Build,
    [switch]$SkipFrontend,
    [switch]$WithConfig,
    [string]$Output = '',
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    [switch]$KeepStaging,
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
function Fail($text) { Write-Host "    XX  $text" -ForegroundColor Red; exit 1 }

$sw = [System.Diagnostics.Stopwatch]::StartNew()
$totalSteps = 6

Write-Host ''
Write-Host '========================================' -ForegroundColor Magenta
Write-Host '  Git AI Tool 绿色版打 zip' -ForegroundColor Magenta
Write-Host '========================================' -ForegroundColor Magenta

# ---------- [1] 定位项目根目录 ----------
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
if (-not (Test-Path (Join-Path $root 'wails.json'))) {
    $fallback = Join-Path $root 'git-ai-tool'
    if (Test-Path (Join-Path $fallback 'wails.json')) { $root = $fallback }
}
if (-not (Test-Path (Join-Path $root 'wails.json'))) {
    Fail "找不到 wails.json，$root 看起来不是项目根目录"
}
Write-Ok "项目根目录: $root"

# ---------- [2] 读取项目配置 ----------
Write-Step 2 $totalSteps '读取项目配置'

$wailsConf = Get-Content -LiteralPath (Join-Path $root 'wails.json') -Raw -Encoding UTF8 | ConvertFrom-Json
$outName = if ($wailsConf.outputfilename) { $wailsConf.outputfilename } else { $wailsConf.name }

# 从 app.go 解析版本号（const appVersion = "x.y.z"）
$appVersion = 'unknown'
$appGo = Join-Path $root 'app.go'
if (Test-Path $appGo) {
    $vm = Select-String -LiteralPath $appGo -Pattern 'appVersion\s*=\s*"([^"]+)"' | Select-Object -First 1
    if ($vm) { $appVersion = $vm.Matches[0].Groups[1].Value }
}

$pkgName = "$outName-v$appVersion-win-$Arch"
$binDir = Join-Path $root 'build\bin'
$srcExe = Join-Path $binDir "$outName.exe"
$srcCfg = Join-Path $binDir 'config.json'
$distDir = Join-Path $root 'build\dist'
$stageParent = Join-Path $root 'build\.zip-staging'
$stageDir = Join-Path $stageParent $pkgName
$zipPath = if ($Output) { $Output } else { Join-Path $distDir "$pkgName.zip" }

Write-Ok "包名: $pkgName"
Write-Ok "输出: $zipPath"
if ($WithConfig) { Write-Warn2 '已开启 -WithConfig：zip 里会带 config.json（含明文 API Key，外发前请三思）' }

# ---------- [3] 准备 exe ----------
Write-Step 3 $totalSteps '准备 exe'

if ($Build) {
    $buildScript = Join-Path $root 'buildShill.ps1'
    if (-not (Test-Path $buildScript)) { Fail "找不到 buildShill.ps1，无法重新打包 exe" }
    if ($DryRun) {
        Write-Info "将执行: buildShill.ps1$(if ($SkipFrontend) { ' -SkipFrontend' })"
    }
    else {
        Write-Info '调用 buildShill.ps1 重新打包 exe ...'
        $buildArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $buildScript)
        if ($SkipFrontend) { $buildArgs += '-SkipFrontend' }
        & powershell @buildArgs
        if ($LASTEXITCODE -ne 0) { Fail "buildShill.ps1 执行失败（exit=$LASTEXITCODE）" }
    }
}

if (-not (Test-Path -LiteralPath $srcExe)) {
    Fail "找不到 $srcExe，请先运行 .\buildShill.ps1，或直接加 -Build 让本脚本代劳"
}
$exeFile = Get-Item -LiteralPath $srcExe
Write-Ok "exe: $($exeFile.Name)  ($([Math]::Round($exeFile.Length / 1MB, 2)) MB, $($exeFile.LastWriteTime))"

# ---------- [4] 组装暂存目录 ----------
Write-Step 4 $totalSteps '组装 zip 内容'

if ($DryRun) {
    Write-Info "将创建暂存目录: $stageDir"
    Write-Info "  - $($exeFile.Name)"
    if ($WithConfig -and (Test-Path -LiteralPath $srcCfg)) { Write-Info '  - config.json' }
    Write-Info '  - 使用说明.txt'
}
else {
    if (Test-Path -LiteralPath $stageDir) { Remove-Item -LiteralPath $stageDir -Recurse -Force }
    New-Item -ItemType Directory -Path $stageDir -Force | Out-Null

    Copy-Item -LiteralPath $srcExe -Destination $stageDir -Force
    Write-Ok "已放入 $($exeFile.Name)"

    if ($WithConfig) {
        if (Test-Path -LiteralPath $srcCfg) {
            Copy-Item -LiteralPath $srcCfg -Destination $stageDir -Force
            Write-Ok '已放入 config.json'
        }
        else {
            Write-Warn2 "$srcCfg 不存在，跳过（应用首次启动会以全新状态运行）"
        }
    }
    else {
        Write-Info '未放入 config.json（需要请加 -WithConfig）'
    }

    # 先在 here-string 外面把分支算好：PowerShell 5.1 对双引号 here-string 里嵌套
    # $(if ...) + 内层引号解析不稳，宁可多两行变量
    if ($WithConfig) {
        $cfgLine = '  本包已附带 config.json，双击就能直接用。'
    }
    else {
        $cfgLine = @'
  本包未附带 config.json，首次使用请到「系统配置」页填写：
    1) 服务商 / API Key / Base URL / 模型
    2) 至少一个工作区目录（要扫描的项目根目录）
  保存后会在 exe 同级目录生成 config.json。
'@
    }

    $readme = @"
Git AI Tool v$appVersion
========================================

【怎么跑】
  双击 $outName.exe 即可，绿色版免安装。
$cfgLine

【注意】
  · 请把本目录放在「可写」的位置，不要放 Program Files —— 那里写不了 config.json。
  · config.json 是纯本地配置，和 exe 放一起即可整体迁移；里面存有 API Key，别外发。
  · 若启动提示缺少 WebView2 运行时，装一次 Microsoft Edge WebView2 Runtime 即可
    （Win10 / Win11 一般自带）。

【从源码重新打包】
  .\packZip.ps1 -Build    # 先编译 exe 再压成 zip（改了前端用这个）
  .\buildShill.ps1        # 只编译 exe，产物在 build\bin
"@
    # 必须带 BOM 写：记事本按 ANSI 打开带 BOM 的 UTF-8 才不乱码
    $readmePath = Join-Path $stageDir '使用说明.txt'
    [System.IO.File]::WriteAllText($readmePath, $readme, [System.Text.UTF8Encoding]::new($true))
    Write-Ok '已生成 使用说明.txt'

    $staged = @(Get-ChildItem -LiteralPath $stageDir -Force)
    Write-Ok "暂存目录: $($staged.Count) 个文件"
}

# ---------- [5] 压缩 ----------
Write-Step 5 $totalSteps '压缩成 zip'

if ($DryRun) {
    Write-Info "将压缩 $stageDir -> $zipPath"
}
else {
    $zipDir = Split-Path -Parent $zipPath
    if ($zipDir -and -not (Test-Path -LiteralPath $zipDir)) {
        New-Item -ItemType Directory -Path $zipDir -Force | Out-Null
    }
    if (Test-Path -LiteralPath $zipPath) { Remove-Item -LiteralPath $zipPath -Force }

    # 传目录本身（不是 目录\*），zip 里才会有顶层文件夹。
    # -LiteralPath 在很老的 Windows PowerShell 上还没有，退回到 -Path（本项目路径不含通配符，等价）
    try {
        Compress-Archive -LiteralPath $stageDir -DestinationPath $zipPath -CompressionLevel Optimal -Force
    }
    catch {
        Compress-Archive -Path $stageDir -DestinationPath $zipPath -CompressionLevel Optimal -Force
    }
    if (-not (Test-Path -LiteralPath $zipPath)) { Fail '压缩后没有生成 zip，请检查 Compress-Archive 的输出' }
    Write-Ok '压缩完成'

    if (-not $KeepStaging) {
        Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
        # 暂存根目录空了就一起清掉
        if ((Test-Path -LiteralPath $stageParent) -and -not (Get-ChildItem -LiteralPath $stageParent -Force)) {
            Remove-Item -LiteralPath $stageParent -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    else {
        Write-Info "按要求保留暂存目录: $stageDir"
    }
}

# ---------- [6] 校验产物 ----------
Write-Step 6 $totalSteps '校验产物'

if ($DryRun) {
    Write-Warn2 'DryRun 模式：未真正生成 zip'
}
else {
    $zipFile = Get-Item -LiteralPath $zipPath
    $mb = [Math]::Round($zipFile.Length / 1MB, 2)
    $sha = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLower()

    # 列出 zip 里的条目，确认打包内容没跑偏
    Add-Type -AssemblyName System.IO.Compression.FileSystem -ErrorAction SilentlyContinue
    $entries = @()
    try {
        $archive = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
        try { $entries = @($archive.Entries | ForEach-Object { $_.FullName }) }
        finally { $archive.Dispose() }
    }
    catch {
        Write-Warn2 '未能读取 zip 条目列表（不影响产物）'
    }

    Write-Ok "产物: $($zipFile.FullName)"
    Write-Ok "大小: $mb MB    时间: $($zipFile.LastWriteTime)"
    Write-Ok "SHA256: $sha"
    foreach ($e in $entries) { Write-Ok "  含: $e" }

    # 顺手生成校验文件，方便对方核对
    Set-Content -LiteralPath "$zipPath.sha256" -Value "$sha  $([System.IO.Path]::GetFileName($zipPath))" -Encoding ASCII
    Write-Ok "校验文件: $zipPath.sha256"

    if ($WithConfig) {
        Write-Warn2 '提醒：本 zip 内含 config.json（明文 API Key），仅限自己使用 / 内部可信分发'
    }
}

Write-Host ''
Write-Host ('完成，用时 ' + [Math]::Round($sw.Elapsed.TotalSeconds, 1) + ' 秒') -ForegroundColor Green
Write-Host ''
Write-Host '提示：' -ForegroundColor DarkGray
Write-Host '  · 解压后目录里是 exe + 使用说明，双击即可运行（无需安装）' -ForegroundColor DarkGray
Write-Host '  · 默认不带 config.json，收包人自己到「系统配置」页填一次即可' -ForegroundColor DarkGray
Write-Host '  · 改了前端记得先 .\packZip.ps1 -Build，否则 zip 里还是旧界面' -ForegroundColor DarkGray
Write-Host ''

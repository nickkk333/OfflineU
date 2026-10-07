<#
.SYNOPSIS
把 OfflineU 编译成一个可以直接双击运行的 Windows exe（便携版）。

.DESCRIPTION
产物是一个单文件 exe：Vue 前端已经内嵌（//go:embed all:web/dist），不需要 Node、
Python 或任何运行时依赖。

便携版行为（由 -ldflags -X ...Portable=1 打开，只影响这个脚本产出的 exe）：
  * 课程文件夹 = exe 所在的文件夹。把 offlineu.exe 拷进课程目录双击即可，
    浏览范围也被限制在这个文件夹里，不会跑到磁盘其它地方。
  * 进度和课程列表写到 exe 同级的 data\ 目录，整包可以随时拷走。
  * 仍然可以用命令行参数覆盖：offlineu.exe "D:\Courses\Python"
    （或设 AUTO_LOAD_COURSE 环境变量），那时就以该路径为准。

版本和 docker/run.ps1 一样自动推导，不需要手动指定：
  * HEAD 正好打着 tag（v1.2.3） -> offlineu-1.2.3.exe，二进制 Version=1.2.3
  * 其它（日常开发）            -> offlineu-latest.exe，二进制 Version=latest

注意：转封装（MKV / MPEG-TS -> 浏览器原生 MP4）依赖 ffmpeg。exe 首次播放会自动
下载静态 ffmpeg，也可以自己装好放进 PATH 或设 OFFLINEU_FFMPEG。

.EXAMPLE
.\build-windows.ps1                 # 编译前端 + 生成 dist\offlineu-<version>.exe
.\build-windows.ps1 -SkipFrontend   # web\dist 已是最新时跳过前端编译
.\build-windows.ps1 -Run            # 编译完立刻启动，验证一下
#>
[CmdletBinding()]
param(
    # 产物目录，默认仓库根目录下的 dist
    [string]$OutDir,
    # 跳过前端编译（web\dist 已经是最新的时用）
    [switch]$SkipFrontend,
    # 不内嵌 ffmpeg（exe 回到 ~8 MB，首次播放 MKV 时再联网下载）
    [switch]$SkipFFmpeg,
    # 只内嵌 ffmpeg，不内嵌 ffprobe（省 ~79 MB，探测改用 ffmpeg -i）
    [switch]$NoFFprobe,
    # 编译完成后立即启动 exe
    [switch]$Run
)

$ErrorActionPreference = "Continue"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path

# ---------------------------------------------------------------------------
# 1) 版本：打 tag 才有版本号，开发用 latest
# ---------------------------------------------------------------------------
$raw = cmd /c "git -C `"$root`" describe --tags --exact-match HEAD 2>nul"
if ($LASTEXITCODE -eq 0 -and $raw) {
    $tag     = ([string]($raw | Select-Object -First 1)).Trim()
    $version = $tag -replace '^v', ''
    Write-Host "==> 版本 $version（来自 git tag $tag）"
} else {
    $version = "latest"
    Write-Host "==> 版本 latest（HEAD 没有 tag，开发构建）"
}

# ---------------------------------------------------------------------------
# 2) 前端：Go 会把 web/dist 编进二进制，所以必须先构建好
# ---------------------------------------------------------------------------
$web = Join-Path $root "web"
$dist = Join-Path $web "dist"
if (-not $SkipFrontend) {
    if (-not (Test-Path (Join-Path $web "node_modules"))) {
        Write-Host "==> npm ci"
        cmd /c "cd /d `"$web`" && npm ci --no-audit --no-fund 2>&1"
        if ($LASTEXITCODE -ne 0) { throw "npm ci 失败（退出码 $LASTEXITCODE）" }
    }
    Write-Host "==> npm run build"
    cmd /c "cd /d `"$web`" && npm run build 2>&1"
    if ($LASTEXITCODE -ne 0) { throw "npm run build 失败（退出码 $LASTEXITCODE）" }
}
if (-not (Test-Path (Join-Path $dist "index.html"))) {
    throw "web\dist 里没有 index.html：前端没有构建成功（去掉 -SkipFrontend 再试）"
}

# ---------------------------------------------------------------------------
# 3) ffmpeg / ffprobe：内嵌进 exe，这样首次播放 MKV、MPEG-TS 时不用联网下载
#    （本机没有 ffmpeg 时，transcoder 会先把内嵌的这份解包到缓存目录再用）。
#    下载只做一次，之后复用 internal\offlineu\ffmpegwin 下的副本。
#    许可：eugeneware/ffmpeg-static 提供的是 GPL 构建，随 exe 一起分发需遵守 GPL。
# ---------------------------------------------------------------------------
$ffmpegDir = Join-Path $root "internal\offlineu\ffmpegwin"
$buildTag  = ""
if (-not $SkipFFmpeg) {
    New-Item -ItemType Directory -Force -Path $ffmpegDir | Out-Null
    $assets = [ordered]@{ "ffmpeg-win32-x64" = "ffmpeg.exe" }
    if (-not $NoFFprobe) { $assets["ffprobe-win32-x64"] = "ffprobe.exe" }
    $ProgressPreference = "SilentlyContinue"
    foreach ($asset in $assets.Keys) {
        $dest = Join-Path $ffmpegDir $assets[$asset]
        if (Test-Path -LiteralPath $dest) {
            Write-Host "==> 复用已下载的 $dest"
            continue
        }
        $url = "https://github.com/eugeneware/ffmpeg-static/releases/latest/download/$asset"
        Write-Host "==> 下载 $asset -> $dest"
        try {
            Invoke-WebRequest -Uri $url -OutFile $dest -UseBasicParsing -TimeoutSec 900
        } catch {
            throw "下载 $asset 失败：$($_.Exception.Message)（可加 -SkipFFmpeg 跳过内嵌，或挂好代理重试）"
        }
    }
    $buildTag = "-tags bundleffmpeg"
}

# ---------------------------------------------------------------------------
# 4) 编译 exe（静态二进制，无需 CGO）
# ---------------------------------------------------------------------------
if (-not $OutDir) { $OutDir = Join-Path $root "dist" }
if (-not (Test-Path -LiteralPath $OutDir)) {
    New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
}
$out = Join-Path (Resolve-Path -LiteralPath $OutDir).ProviderPath "offlineu-$version.exe"

$env:CGO_ENABLED = "0"
$env:GOOS        = "windows"
$env:GOARCH      = "amd64"
$ldflags = "-s -w " +
           "-X github.com/nickkk333/offlineu/internal/offlineu.Version=$version " +
           "-X github.com/nickkk333/offlineu/internal/offlineu.Portable=1"

Write-Host "==> go build $buildTag -> $out"
cmd /c "cd /d `"$root`" && go build $buildTag -trimpath -ldflags=`"$ldflags`" -o `"$out`" . 2>&1"
if ($LASTEXITCODE -ne 0) { throw "go build 失败（退出码 $LASTEXITCODE）" }

Write-Host ""
Write-Host "完成: $out"
Write-Host "  用法：把这个 exe 拷进课程文件夹，双击即可（课程文件夹 = exe 所在目录，"
Write-Host "        进度写在同级的 data\ 下）。浏览器打开 http://127.0.0.1:5000"
Write-Host "  指定课程：.\offlineu-$version.exe `"D:\Courses\Python`""
Write-Host "  局域网访问：加 --host 0.0.0.0（首次运行会弹防火墙提示，允许专用网络）"

if ($Run) {
    Write-Host ""
    Write-Host "==> 启动中（Ctrl-C 结束）"
    & $out
}

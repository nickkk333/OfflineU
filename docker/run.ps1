<#
.SYNOPSIS
本地编译 OfflineU 的 Docker 镜像，并用 host 网络启动容器（开发用）。

.DESCRIPTION
版本全自动推导，不需要手动指定：
  * HEAD 正好打着 tag（例如 v1.2.3） -> 镜像 offlineu:1.2.3，VERSION=1.2.3
  * 其它任何情况（日常开发）          -> 镜像 offlineu:latest，VERSION=latest

也就是说：只有打 tag 时才有版本号，开发永远是 latest。发布用的多架构镜像仍由
.github/workflows/docker-build.yml 构建，这个脚本只负责本地开发这一条路径。

host 网络（--network host）让容器直接用宿主机网络，端口就是 OFFLINEU_PORT 本身，
不再需要 -p 映射，DLNA/投屏依赖的 SSDP 组播也能到达局域网。

注意：Windows / macOS 的 Docker Desktop 上 host 网络指的是它内部的那台 Linux 虚拟机，
不是桌面本身；想在本机浏览器直接打开，就从 WSL 里跑这个脚本（Linux 上 host 网络就是
真宿主机），或者加 -Bridge 走端口映射。

.EXAMPLE
.\docker\run.ps1                       # 编译 + host 网络后台启动，端口 5000
.\docker\run.ps1 -Foreground           # 前台运行，Ctrl-C 停止
.\docker\run.ps1 -Bridge               # 改用 -p 端口映射（Docker Desktop 上能直连）
.\docker\run.ps1 -Port 8080            # 换个端口
.\docker\run.ps1 -NoBuild -Logs        # 跳过编译，直接跟踪已有容器日志
.\docker\run.ps1 -Courses D:\Courses -Data D:\offlineu-data
#>
[CmdletBinding()]
param(
    # 课程目录，映射到容器 /courses（只读），默认仓库根目录下的 ./courses
    [string]$Courses,
    # 进度 / 课程列表目录，映射到 /app/data，默认仓库根目录下的 ./data
    [string]$Data,
    # 监听端口（host 网络下就是宿主机端口）
    [int]$Port = 5000,
    # 使用已有镜像，跳过 docker build
    [switch]$NoBuild,
    # 前台运行（Ctrl-C 即停止并删除容器），默认后台 -d
    [switch]$Foreground,
    # 改用 bridge + 端口映射（Windows / macOS 的 Docker Desktop 上 host 网络指的是它
    # 内部那台 Linux 虚拟机，桌面浏览器连不上；此时加这个参数，代价是投屏搜不到设备）
    [switch]$Bridge,
    # 启动后跟踪日志
    [switch]$Logs
)

# docker 把进度输出到 stderr；走 cmd /c 中转，避免 PowerShell 把它当成终止性错误，
# 退出码统一看 $LASTEXITCODE。
$ErrorActionPreference = "Continue"

$root       = Split-Path -Parent $PSScriptRoot        # 仓库根目录（Dockerfile 在这里）
$dockerfile = Join-Path $root "Dockerfile"
$name       = "offlineu-dev"

function Resolve-MountDir([string]$path) {
    if (-not (Test-Path -LiteralPath $path)) {
        New-Item -ItemType Directory -Force -Path $path | Out-Null
    }
    return (Resolve-Path -LiteralPath $path).ProviderPath
}

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
$image = "offlineu:$version"

# ---------------------------------------------------------------------------
# 2) 两个映射目录：不存在就创建，并转成绝对路径（Docker 只接受绝对路径）
# ---------------------------------------------------------------------------
if (-not $Courses) { $Courses = Join-Path $root "courses" }
if (-not $Data)    { $Data    = Join-Path $root "data" }
$coursesDir = Resolve-MountDir $Courses
$dataDir    = Resolve-MountDir $Data

# ---------------------------------------------------------------------------
# 3) 编译镜像
# ---------------------------------------------------------------------------
if (-not $NoBuild) {
    Write-Host "==> docker build -t $image --build-arg VERSION=$version"
    cmd /c "docker build --build-arg VERSION=$version -t $image -f `"$dockerfile`" `"$root`" 2>&1"
    if ($LASTEXITCODE -ne 0) { throw "docker build 失败（退出码 $LASTEXITCODE）" }
}

# ---------------------------------------------------------------------------
# 4) 启动容器（默认 host 网络）。重复执行会直接替换旧容器，可以一直复用这条命令。
# ---------------------------------------------------------------------------
cmd /c "docker rm -f $name >nul 2>&1"

if ($Foreground) { $mode = "--rm -it" } else { $mode = "-d" }
if ($Bridge) { $network = "-p ${Port}:${Port}" } else { $network = "--network host" }
$cmdLine = "docker run $mode --name $name $network " +
           "-e OFFLINEU_HOST=0.0.0.0 -e OFFLINEU_PORT=$Port " +
           "-e OFFLINEU_ROOTS=/courses -e OFFLINEU_PROGRESS_DIR=/app/data " +
           "-v `"$coursesDir`:/courses:ro`" -v `"$dataDir`:/app/data`" $image"

Write-Host "==> $cmdLine"
cmd /c "$cmdLine 2>&1"
if ($LASTEXITCODE -ne 0) { throw "docker run 失败（退出码 $LASTEXITCODE）" }

Write-Host ""
$modeName = if ($Bridge) { "bridge 端口映射" } else { "host 网络" }
Write-Host "OfflineU $version 已启动（$modeName）：http://localhost:$Port"
Write-Host "  课程目录: $coursesDir -> /courses"
Write-Host "  数据目录: $dataDir -> /app/data"
if (-not $Foreground) {
    Write-Host "  日志: docker logs -f $name      停止: docker rm -f $name"
    if ($Logs) { cmd /c "docker logs -f $name 2>&1" }
}

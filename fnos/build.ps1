# Build the OfflineU fnOS package (.fpk) locally with the official fnpack tool.
#
# amd64 / x64 only. fnOS's manifest calls the 64-bit Intel/AMD architecture
# `platform = x86` (its only values are x86 / arm / all), so the file keeps the
# `_x86.fpk` suffix fnOS expects - it IS the amd64/x64 build: the image inside
# is built with `--platform linux/amd64` (64-bit) and no arm package is produced.
# Do NOT hand-roll a tar.gz - fnOS rejects that structure with
# "应用包不符合系统要求"; the package must be produced by fnpack.
#
# Fully OFFLINE install: this script also builds the Docker image locally, exports
# it to app/images/offlineu-amd64.tar, and that tar is packed into the fpk. At
# install time cmd/main (native app) does `docker load` on it, so the NAS never
# reaches out to any registry.
#
# Requirements:
#   - Docker Desktop running in Linux-container mode (builds the amd64 image on
#     Windows via --platform linux/amd64). Needs internet to pull base images at
#     BUILD time only - the resulting fpk is offline for the NAS.
#   - The first run also fetches fnpack from fnnas.com; cached as fnpack.exe.
#
# Version - same rule as build-windows.ps1 and docker/run.ps1, never typed by hand:
#   HEAD tagged v1.2.3 -> version 1.2.3 (manifest + fpk name + image VERSION arg)
#   anything else      -> version latest (development build)
# The image inside the package is additionally tagged offlineu:<version>, so the
# tar also carries the same tag docker/run.ps1 produces; cmd/main keeps loading
# offlineu:local, which is what the bundled docker-compose.yaml and the fnOS
# lifecycle scripts expect.
#
param(
    # 复用本地已有的 offlineu:local 镜像，跳过 docker build（导出仍会执行）
    [switch]$SkipBuild
)

# "Continue" (not "Stop") so docker's progress output on stderr is not flagged as
# a terminating NativeCommandError; we check $LASTEXITCODE explicitly instead.
$ErrorActionPreference = "Continue"

$root       = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot   = Split-Path -Parent $root          # OfflineU repo root (Dockerfile lives here)
$app        = Join-Path $root "offlineu"
$imgDir     = Join-Path (Join-Path $app "app") "images"
$imgTar     = Join-Path $imgDir "offlineu-amd64.tar"
$fnpack     = Join-Path $root "fnpack.exe"

# 0) 版本：打 tag 才有版本号，开发用 latest
$raw = cmd /c "git -C `"$repoRoot`" describe --tags --exact-match HEAD 2>nul"
if ($LASTEXITCODE -eq 0 -and $raw) {
    $tag     = ([string]($raw | Select-Object -First 1)).Trim()
    $version = $tag -replace '^v', ''
    Write-Host "==> 版本 $version（来自 git tag $tag）"
} else {
    $version = "latest"
    Write-Host "==> 版本 latest（HEAD 没有 tag，开发构建）"
}
$out = Join-Path $root "offlineu_${version}_x86.fpk"

# 0b) manifest 的 version 跟随同一个版本号（和 CI 打 tag 时做的事一致）。
$manifestPath = Join-Path $app "manifest"
$manifestText = [System.IO.File]::ReadAllText($manifestPath)
$manifestText = [regex]::Replace($manifestText, '(?m)^version\s*=\s*\S+', "version = $version")
[System.IO.File]::WriteAllText($manifestPath, $manifestText)

# 1) Ensure fnpack (Windows amd64) is available.
$fnpackCmd = $null
if (Get-Command fnpack -ErrorAction SilentlyContinue) {
    $fnpackCmd = "fnpack"            # fnpack on PATH (no dot-source warning)
} elseif (Test-Path $fnpack) {
    $fnpackCmd = $fnpack            # cached next to this script (invoke via full path)
}
if (-not $fnpackCmd) {
    foreach ($v in @("1.2.3", "1.2.1", "1.2.0")) {
        $url = "https://static2.fnnas.com/fnpack/fnpack-$v-windows-amd64"
        Write-Host "==> Downloading fnpack $v ..."
        try {
            Invoke-WebRequest -Uri $url -OutFile $fnpack -UseBasicParsing
            Write-Host "    saved $fnpack"
            $fnpackCmd = $fnpack
            break
        } catch {
            Write-Host "    failed: $($_.Exception.Message)"
        }
    }
}
if (-not $fnpackCmd) {
    throw "fnpack unavailable. Download it from https://developer.fnnas.com/docs/cli/fnpack/ and place fnpack.exe next to this script."
}

# 2) Build the Docker image (amd64) and export it into the package tree.
# docker streams progress to stderr; run it via cmd /c so PowerShell does not
# surface that as a NativeCommandError.
$dockerfile = Join-Path $repoRoot "Dockerfile"
if ($SkipBuild) {
    Write-Host "==> Skipping docker build (reusing the local offlineu:local image)"
} else {
    Write-Host "==> Building docker image offlineu:local (linux/amd64), VERSION=$version ..."
    cmd /c "docker build --platform linux/amd64 --build-arg VERSION=$version -t offlineu:local -f `"$dockerfile`" `"$repoRoot`" 2>&1"
    if ($LASTEXITCODE -ne 0) { throw "docker build failed" }
    # 同一个镜像再打上 offlineu:<version>，和 docker/run.ps1 的命名保持一致。
    cmd /c "docker tag offlineu:local offlineu:$version 2>&1"
    if ($LASTEXITCODE -ne 0) { throw "docker tag failed" }
}
Write-Host "==> Saving image to $imgTar ..."
New-Item -ItemType Directory -Force -Path $imgDir | Out-Null
cmd /c "docker save offlineu:local offlineu:$version -o `"$imgTar`" 2>&1"
if ($LASTEXITCODE -ne 0) { throw "docker save failed" }

# 3) Normalize text files to LF so the bash scripts run under fnOS (Linux).
#    git's autocrlf would otherwise inject CRLF, breaking the `#!/bin/bash` shebang.
Write-Host "==> Normalizing line endings to LF ..."
Get-ChildItem -Path $app -Recurse -File | Where-Object {
    $_.Extension -notin @('.png', '.tar')
} | ForEach-Object {
    $content = [System.IO.File]::ReadAllText($_.FullName)
    if ($content -match "`r") {
        $lf = $content -replace "`r`n", "`n" -replace "`r", "`n"
        [System.IO.File]::WriteAllText($_.FullName, $lf)
    }
}

# 4) Pack with fnpack. The manifest already pins platform=x86 (see offlineu/manifest).
Write-Host "==> Building fpk ..."
Push-Location $app
try {
    & $fnpackCmd build
    $built = Get-ChildItem -Filter *.fpk | Select-Object -First 1
    if (-not $built) { throw "fnpack did not produce an .fpk" }
    Move-Item -Force $built.FullName $out
} finally {
    Pop-Location
}

Write-Host ""
Write-Host "Done: $out"
Write-Host "  内置镜像: offlineu:local (+ offlineu:$version)，安装时由 cmd/main 离线 docker load"
Write-Host "  安装：飞牛OS 应用中心 → 手动安装 → 上传上面的 fpk"

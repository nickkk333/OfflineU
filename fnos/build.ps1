# Build the OfflineU fnOS package (.fpk) locally with the official fnpack tool.
#
# amd64 / x86 only. Do NOT hand-roll a tar.gz - fnOS rejects that structure with
# "应用包不符合系统要求"; the package must be produced by fnpack.
#
# Fully OFFLINE install: this script also builds the Docker image locally, exports
# it to app/docker/offlineu-image.tar, and that tar is packed into the fpk. At
# install time cmd/main (native app) does `docker load` on it, so the NAS never
# reaches out to any registry.
#
# Requirements:
#   - Docker Desktop running in Linux-container mode (builds the amd64 image on
#     Windows via --platform linux/amd64). Needs internet to pull base images at
#     BUILD time only - the resulting fpk is offline for the NAS.
#   - The first run also fetches fnpack from fnnas.com; cached as fnpack.exe.
# "Continue" (not "Stop") so docker's progress output on stderr is not flagged as
# a terminating NativeCommandError; we check $LASTEXITCODE explicitly instead.
$ErrorActionPreference = "Continue"

$root       = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot   = Split-Path -Parent $root          # OfflineU repo root (Dockerfile lives here)
$app        = Join-Path $root "offlineu"
$imgTar     = Join-Path (Join-Path (Join-Path $app "app") "docker") "offlineu-image.tar"
$out        = Join-Path $root "offlineu_1.0.0_x86.fpk"
$fnpack     = Join-Path $root "fnpack.exe"

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
Write-Host "==> Building docker image offlineu:local (linux/amd64) ..."
docker build --platform linux/amd64 -t offlineu:local -f (Join-Path $repoRoot "Dockerfile") $repoRoot
if ($LASTEXITCODE -ne 0) { throw "docker build failed" }
Write-Host "==> Saving image to $imgTar ..."
docker save offlineu:local -o $imgTar
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

Write-Host "Done: $out"

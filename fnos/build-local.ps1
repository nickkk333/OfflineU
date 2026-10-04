# Local, CI-free build of the OfflineU fnOS package (.fpk).
#
# What it does, end to end, without touching GitHub / any registry:
#   1. docker build the OfflineU image locally (linux/amd64) and tag it offlineu:local
#   2. docker save that image to offlineu-1.0.0-image.tar for manual import into fnOS
#   3. package fnos/offlineu/ into offlineu-1.0.0.fpk
#
# After this, on the fnOS NAS:
#   - import offlineu-1.0.0-image.tar (镜像 -> 导入) so the tag offlineu:local exists
#   - install offlineu-1.0.0.fpk (compose uses pull_policy: never, so no pull happens)
$ErrorActionPreference = "Stop"

$fnosDir  = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $fnosDir
$app      = Join-Path $fnosDir "offlineu"
$fpk      = Join-Path $fnosDir "offlineu-1.0.0.fpk"
$imgTar   = Join-Path $fnosDir "offlineu-1.0.0-image.tar"
$imgTag   = "offlineu:local"

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "docker not found in PATH; please install Docker Desktop and start it."
}

# 1) Build the image locally (linux/amd64 to match an x86 fnOS box).
Write-Host "==> Building image $imgTag from $repoRoot ..."
Push-Location $repoRoot
try {
    docker build --platform linux/amd64 -t $imgTag -f Dockerfile .
} finally {
    Pop-Location
}

# 2) Export the image so it can be imported into fnOS without a registry.
Write-Host "==> Saving image to $imgTar ..."
docker save $imgTag -o $imgTar
$size = [math]::Round((Get-Item $imgTar).Length / 1MB, 1)
Write-Host "    saved ($size MB). Import this into fnOS before installing the fpk."

# 3) Package the fpk (fnpack if available, else a plain tar.gz with the right name).
Write-Host "==> Packaging $fpk ..."
if (Get-Command fnpack -ErrorAction SilentlyContinue) {
    Push-Location $app
    fnpack build
    Pop-Location
} else {
    Push-Location $app
    tar -czf $fpk manifest ICON.PNG ICON_256.PNG app cmd config wizard
    Pop-Location
}

Write-Host "Done."
Write-Host "  image tar : $imgTar"
Write-Host "  package   : $fpk"

# Build the OfflineU fnOS package (.fpk) from the offlineu/ app tree.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$app  = Join-Path $root "offlineu"
$out  = Join-Path $root "offlineu-1.0.0.fpk"

if (Get-Command fnpack -ErrorAction SilentlyContinue) {
    Write-Host "fnpack found, using it to build a validated package..."
    Push-Location $app
    fnpack build
    Pop-Location
} else {
    Write-Host "fnpack not found; building tar.gz manually (manifest + app tree)..."
    Push-Location $app
    tar -czf $out manifest ICON.PNG ICON_256.PNG app cmd config wizard
    Pop-Location
}
Write-Host "Built: $out"

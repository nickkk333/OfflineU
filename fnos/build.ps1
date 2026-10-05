# Build the OfflineU fnOS package (.fpk) locally with the official fnpack tool.
#
# amd64 / x86 only. Do NOT hand-roll a tar.gz - fnOS rejects that structure with
# "应用包不符合系统要求"; the package must be produced by fnpack. The package
# carries the compose + metadata; its compose pulls ghcr.io/nickkk333/offlineu:main
# (the image is built and pushed by the GitHub Actions workflow).
#
# The first run needs network to fetch fnpack from fnnas.com; after that it is
# cached next to this script as fnpack.exe.
$ErrorActionPreference = "Stop"

$root   = Split-Path -Parent $MyInvocation.MyCommand.Path
$app    = Join-Path $root "offlineu"
$out    = Join-Path $root "offlineu_1.0.0_x86.fpk"
$fnpack = Join-Path $root "fnpack.exe"

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

# 2) Build. The manifest already pins platform=x86 (see offlineu/manifest).
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

param([string]$Glibc = '2.31')
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path $PSScriptRoot -Parent
$toolsRoot = Join-Path (Split-Path $repoRoot -Parent) '.tools'
$goExe = Join-Path $toolsRoot 'go/bin/go.exe'
$zigExe = Join-Path $toolsRoot 'zig-x86_64-windows-0.14.1/zig.exe'
if (!(Test-Path $goExe) -or !(Test-Path $zigExe)) { throw 'Portable Go and Zig toolchains are required; see DEVELOPMENT_CN.md.' }
$saved = @{}
foreach ($name in 'GOOS','GOARCH','CGO_ENABLED','CC','CXX','ZIG_GLOBAL_CACHE_DIR') { $saved[$name] = [Environment]::GetEnvironmentVariable($name) }
try {
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '1'
    $env:CC = '"' + $zigExe + '" cc -target x86_64-linux-gnu.' + $Glibc
    $env:CXX = '"' + $zigExe + '" c++ -target x86_64-linux-gnu.' + $Glibc
    $env:ZIG_GLOBAL_CACHE_DIR = Join-Path $toolsRoot 'zig-cache'
    $outDir = Join-Path $repoRoot 'dist/linux/amd64'
    New-Item -ItemType Directory -Force $outDir | Out-Null
    $version = (Get-Content (Join-Path $repoRoot 'VERSION') -Raw).Trim()
    & $goExe -C (Join-Path $repoRoot 'plugin') build -buildmode=c-shared -trimpath -ldflags "-s -w -X main.version=$version-dev" -o (Join-Path $outDir 'usage-report.so') .
    if ($LASTEXITCODE -ne 0) { throw 'Linux plugin build failed.' }
    $digest = (Get-FileHash (Join-Path $outDir 'usage-report.so') -Algorithm SHA256).Hash.ToLowerInvariant()
    "$digest  usage-report.so" | Set-Content (Join-Path $outDir 'checksums.txt') -Encoding ascii
    Write-Host "Linux amd64 plugin: $outDir/usage-report.so (glibc >= $Glibc)"
} finally {
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
}

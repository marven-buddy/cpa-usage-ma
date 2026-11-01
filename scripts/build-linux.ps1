param(
    [ValidatePattern('^\d+\.\d+$')][string]$Glibc = '2.31',
    [switch]$Release
)
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
    $buildVersion = if ($Release) { $version } else { "$version-dev" }
    & $goExe -C (Join-Path $repoRoot 'plugin') build -buildmode=c-shared -trimpath -ldflags "-s -w -X main.version=$buildVersion" -o (Join-Path $outDir 'cpa-usage-ma.so') .
    if ($LASTEXITCODE -ne 0) { throw 'Linux plugin build failed.' }
    $digest = (Get-FileHash (Join-Path $outDir 'cpa-usage-ma.so') -Algorithm SHA256).Hash.ToLowerInvariant()
    "$digest  cpa-usage-ma.so" | Set-Content (Join-Path $outDir 'checksums.txt') -Encoding ascii
    Write-Host "Linux amd64 plugin: $outDir/cpa-usage-ma.so (glibc >= $Glibc)"
    if ($Release) {
        $releaseDir = Join-Path $repoRoot "dist/release/$version/linux-amd64"
        $packageDir = Join-Path $releaseDir 'package'
        New-Item -ItemType Directory -Force $packageDir | Out-Null
        $library = Join-Path $packageDir "cpa-usage-ma-v$version.so"
        Copy-Item -LiteralPath (Join-Path $outDir 'cpa-usage-ma.so') -Destination $library -Force
        Copy-Item -LiteralPath (Join-Path $repoRoot 'LICENSE'),(Join-Path $repoRoot 'THIRD_PARTY_NOTICES.md') -Destination $packageDir -Force
        $zipName = "cpa-usage-ma_${version}_linux_amd64.zip"
        $zipPath = Join-Path $releaseDir $zipName
        Compress-Archive -LiteralPath $library,(Join-Path $packageDir 'LICENSE'),(Join-Path $packageDir 'THIRD_PARTY_NOTICES.md') -DestinationPath $zipPath -Force
        $zipDigest = (Get-FileHash $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
        "$zipDigest  $zipName" | Set-Content (Join-Path $releaseDir 'checksums.txt') -Encoding ascii
        Write-Host "Release candidate: $zipPath"
    }
} finally {
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
}

# Build candidate archives with the existing project-local Windows SDK.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [switch]$NativeOnly,
    [switch]$AllowDirty
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$previousGoos = [Environment]::GetEnvironmentVariable('GOOS', 'Process')
$previousGoarch = [Environment]::GetEnvironmentVariable('GOARCH', 'Process')
Push-Location -LiteralPath $projectRoot
try {
    if ($Version -notmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$') {
        throw 'Use a vMAJOR.MINOR.PATCH[-prerelease] version.'
    }
    $dirty = @(git status --porcelain=v1)
    if ($LASTEXITCODE -ne 0) { throw 'Unable to inspect the source tree.' }
    if ($dirty.Count -gt 0 -and -not $AllowDirty) { throw 'Commit source changes first, or use -AllowDirty for a local preview.' }
    if ($AllowDirty -and $Version -notmatch '-') { throw '-AllowDirty requires a prerelease version.' }
    $commit = (git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Unable to read the source commit.' }
    if ($dirty.Count -gt 0) { $commit += '-dirty'; Write-Warning 'Building a dirty local preview, not a publication-approved release.' }
    $commitDate = (git show -s --format=%cI HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Unable to read the source date.' }
    $buildDate = ([DateTimeOffset]::Parse($commitDate)).UtcDateTime.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $releaseRoot = Join-Path $projectRoot ('dist/' + $Version)
    if (Test-Path -LiteralPath $releaseRoot) { throw 'The version output directory already exists; use a new preview version instead of replacing assets.' }
    New-Item -ItemType Directory -Path $releaseRoot | Out-Null
    [Environment]::SetEnvironmentVariable('GOOS', $null, 'Process')
    [Environment]::SetEnvironmentVariable('GOARCH', $null, 'Process')
    $goLauncher = Join-Path $PSScriptRoot 'go.ps1'
    $nativeTarget = (& $goLauncher env GOOS GOARCH)
    if ($LASTEXITCODE -ne 0 -or $nativeTarget.Count -ne 2) { throw 'Unable to inspect the native SDK target.' }
    $packTool = Join-Path $projectRoot '.cache/go/bin/releasepack.exe'
    & $goLauncher build -trimpath -o $packTool ./cmd/releasepack
    if ($LASTEXITCODE -ne 0) { throw 'Unable to build the packaging tool.' }
    $targets = @('windows/amd64', 'windows/arm64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64')
    if ($NativeOnly) { $targets = @(($nativeTarget[0].Trim() + '/' + $nativeTarget[1].Trim())) }
    $module = 'github.com/ObsidianLoneCrane/iptest/internal/buildinfo'
    $metadata = '-s -w -X ' + $module + '.Version=iptest-version:' + $Version + ' -X ' + $module + '.Commit=' + $commit + ' -X ' + $module + '.Date=' + $buildDate
    foreach ($target in $targets) {
        $parts = $target.Split('/')
        [Environment]::SetEnvironmentVariable('GOOS', $parts[0], 'Process')
        [Environment]::SetEnvironmentVariable('GOARCH', $parts[1], 'Process')
        $buildDir = Join-Path $releaseRoot ($parts[0] + '-' + $parts[1])
        New-Item -ItemType Directory -Path $buildDir | Out-Null
        $binaryName = 'iptest'
        if ($parts[0] -eq 'windows') { $binaryName += '.exe' }
        $binaryPath = Join-Path $buildDir $binaryName
        & $goLauncher build -trimpath -ldflags $metadata -o $binaryPath ./cmd/iptest
        if ($LASTEXITCODE -ne 0) { throw ('Application build failed: ' + $target) }
        & $packTool -binary $binaryPath -version $Version -os $parts[0] -arch $parts[1] -out $releaseRoot
        if ($LASTEXITCODE -ne 0) { throw ('Packaging failed: ' + $target) }
    }
    $manifestArgs = @('-checksums-only', '-version', $Version, '-out', $releaseRoot)
    if (-not $NativeOnly) { $manifestArgs += '-require-all' }
    & $packTool @manifestArgs
    if ($LASTEXITCODE -ne 0) { throw 'Checksum generation failed.' }
    Write-Output ('Candidate archives and SHA256SUMS: ' + $releaseRoot)
    Write-Output 'Cross-built archives still require native runtime and downloaded-package acceptance.'
}
finally {
    [Environment]::SetEnvironmentVariable('GOOS', $previousGoos, 'Process')
    [Environment]::SetEnvironmentVariable('GOARCH', $previousGoarch, 'Process')
    Pop-Location
}

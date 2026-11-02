# Exercise an extracted native user package without invoking Go or public APIs.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Archive,
    [Parameter(Mandatory = $true)][string]$Version
)
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$archivePath = (Resolve-Path -LiteralPath $Archive).Path
$manifestPath = Join-Path ([IO.Path]::GetDirectoryName($archivePath)) 'SHA256SUMS'
$expectedDigest = @()
foreach ($line in Get-Content -LiteralPath $manifestPath) {
    if ($line -match '^([0-9a-f]{64})  (.+)$' -and $Matches[2] -ceq [IO.Path]::GetFileName($archivePath)) { $expectedDigest += $Matches[1] }
}
$actualDigest = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($expectedDigest.Count -ne 1 -or $expectedDigest[0] -cne $actualDigest) { throw 'Package checksum is missing, ambiguous, or incorrect.' }
$smokeRoot = Join-Path $projectRoot ('.cache/go/package-smoke-' + [Guid]::NewGuid().ToString('N'))
$extractRoot = Join-Path $smokeRoot ('path with spaces-' + [char]0x4E2D + [char]0x6587)
New-Item -ItemType Directory -Path $extractRoot -Force | Out-Null
if ($archivePath.EndsWith('.zip')) {
    Expand-Archive -LiteralPath $archivePath -DestinationPath $extractRoot
}
elseif ($archivePath.EndsWith('.tar.gz')) {
    & tar -xzf $archivePath -C $extractRoot
    if ($LASTEXITCODE -ne 0) { throw 'Package extraction failed.' }
}
else { throw 'Expected a ZIP or tar.gz package.' }
$windowsHost = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$binaryName = 'iptest'
if ($windowsHost) { $binaryName += '.exe' }
$binaryPath = Join-Path $extractRoot $binaryName
foreach ($required in @($binaryName, 'README.md', 'LICENSE')) {
    if (-not (Test-Path -LiteralPath (Join-Path $extractRoot $required) -PathType Leaf)) { throw ('Package is missing ' + $required) }
}
function Invoke-PackageProbe {
    param([string]$Arguments)
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $binaryPath
    $start.Arguments = $Arguments
    $start.WorkingDirectory = $extractRoot
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.StandardOutputEncoding = New-Object Text.UTF8Encoding($false)
    $start.StandardErrorEncoding = New-Object Text.UTF8Encoding($false)
    # Exclude Go SDK locations from the child's PATH while preserving OS tools.
    $separator = [IO.Path]::PathSeparator
    $filteredPath = @($env:PATH.Split($separator) | Where-Object {
        $_ -notmatch '(?i)([\\/]go[\\/]bin|[\\/]go[\\/][0-9]|[\\/]\.tools[\\/]go|[\\/]hostedtoolcache[\\/]go)'
    }) -join $separator
    $start.EnvironmentVariables['PATH'] = $filteredPath
    $start.EnvironmentVariables.Remove('GOROOT')
    $start.EnvironmentVariables.Remove('GOPATH')
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    try {
        [void]$process.Start()
        $stdoutRead = $process.StandardOutput.ReadToEndAsync()
        $stderrRead = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(15000)) { $process.Kill(); $process.WaitForExit(); throw 'Package probe timed out.' }
        return @{ Code = $process.ExitCode; Out = $stdoutRead.GetAwaiter().GetResult(); Err = $stderrRead.GetAwaiter().GetResult() }
    }
    finally { $process.Dispose() }
}
$versionProbe = Invoke-PackageProbe '--version'
if ($versionProbe.Code -ne 0 -or -not $versionProbe.Out.StartsWith('iptest ' + $Version + ' ') -or $versionProbe.Err.Length -ne 0) { throw 'Packaged version metadata is incorrect.' }
$helpProbe = Invoke-PackageProbe '--help'
if ($helpProbe.Code -ne 0 -or -not $helpProbe.Out.Contains('--format FORMAT') -or $helpProbe.Err.Length -ne 0) { throw 'Packaged help failed.' }
$defaultProbe = Invoke-PackageProbe '--proxy http://127.0.0.1:1 --timeout 500ms --no-speedtest --no-traffic'
if ($defaultProbe.Code -ne 1 -or $defaultProbe.Out.Contains('IPv6') -or -not $defaultProbe.Out.Contains('Unknown (detection failed)')) { throw 'Packaged default must inspect only IPv4 and retain the failed proxy.' }
foreach ($format in @('report', 'plain')) {
    foreach ($family in @('ipv4', 'ipv6', 'both', 'auto')) {
        $result = Invoke-PackageProbe ('--proxy http://127.0.0.1:1 --timeout 500ms --no-speedtest --no-traffic --format ' + $format + ' --family ' + $family)
        if ($result.Code -ne 1 -or $result.Out.Contains('IP source data') -or -not $result.Out.Contains('explicit proxy')) { throw 'Packaged failure behavior did not retain the selected proxy.' }
        $expectedFailures = 1
        if ($family -eq 'both' -or $family -eq 'auto') { $expectedFailures = 2 }
        if ([regex]::Matches($result.Out, 'Unknown \(detection failed\)').Count -ne $expectedFailures) { throw 'A failed selected proxy unexpectedly produced or omitted egress results.' }
        if ($family -eq 'ipv4' -and $result.Out.Contains('IPv6')) { throw 'IPv4-only output unexpectedly included IPv6.' }
        if ($format -eq 'report' -and -not $result.Out.Contains('iptest / Network inspection')) { throw 'Packaged report rendering failed.' }
        if ($format -eq 'plain' -and -not $result.Out.Contains('Network path: explicit proxy')) { throw 'Packaged plain rendering failed.' }
    }
}
Write-Output 'Extracted native package passed version/help, all family modes, both formats, and failed-proxy probes; no public API requests were made.'
Write-Output 'Browser-download trust, desktop proxy integration, counters, and live-network acceptance remain separate checks.'

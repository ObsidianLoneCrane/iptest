# Download the pinned official archive without installing Go globally.
[CmdletBinding()]
param()

if ($MyInvocation.InvocationName -eq '.') {
    throw 'Run this script normally; do not dot-source it.'
}
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
. (Join-Path $PSScriptRoot 'go-common.ps1')

$layout = Get-GoLayout
if (Test-Path -LiteralPath $layout.Executable -PathType Leaf) {
    Write-Host "Using the existing project SDK: Go $($layout.Version) ($($layout.Platform))."
}
else {
    if (Test-Path -LiteralPath $layout.SDKParent) {
        throw 'An incomplete SDK directory already exists. Preview clean-go.ps1 before removing it and retrying.'
    }
    $checksums = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'go-checksums.json') -Raw | ConvertFrom-Json
    $checksumEntry = $checksums.PSObject.Properties[$layout.ArchiveName]
    if (-not $checksumEntry -or $checksumEntry.Value -notmatch '^[a-f0-9]{64}$') {
        throw 'No reviewed SHA256 checksum exists for the pinned Go version and platform.'
    }
    $expectedHash = $checksumEntry.Value
    $downloadDirectory = Assert-GoProjectPath $layout.ProjectRoot (Join-Path $layout.ToolsRoot 'downloads')
    New-Item -ItemType Directory -Path $downloadDirectory -Force | Out-Null
    $archivePath = Assert-GoProjectPath $layout.ProjectRoot (Join-Path $downloadDirectory $layout.ArchiveName)
    if (-not (Test-Path -LiteralPath $archivePath -PathType Leaf)) {
        $partialPath = Assert-GoProjectPath $layout.ProjectRoot ($archivePath + '.part')
        $downloadURL = 'https://go.dev/dl/' + $layout.ArchiveName
        Write-Host "Downloading $downloadURL"
        $previousTLS = [Net.ServicePointManager]::SecurityProtocol
        try {
            [Net.ServicePointManager]::SecurityProtocol = $previousTLS -bor [Net.SecurityProtocolType]::Tls12
            Invoke-WebRequest -Uri $downloadURL -OutFile $partialPath -UseBasicParsing -TimeoutSec 300
        }
        finally {
            [Net.ServicePointManager]::SecurityProtocol = $previousTLS
        }
        if ((Get-FileHash -LiteralPath $partialPath -Algorithm SHA256).Hash -ne $expectedHash) {
            throw 'Go archive checksum mismatch. The archive will not be extracted or executed.'
        }
        $null = Assert-GoProjectPath $layout.ProjectRoot $partialPath
        $null = Assert-GoProjectPath $layout.ProjectRoot $archivePath
        if (Test-Path -LiteralPath $archivePath) {
            throw 'The archive destination appeared during download. Inspect it before retrying.'
        }
        Move-Item -LiteralPath $partialPath -Destination $archivePath
    }
    if ((Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash -ne $expectedHash) {
        throw 'Cached Go archive checksum mismatch. The archive will not be extracted or executed.'
    }

    $stagingPath = Assert-GoProjectPath $layout.ProjectRoot (Join-Path $layout.ToolsRoot ('staging\' + [Guid]::NewGuid().ToString('N')))
    New-Item -ItemType Directory -Path $stagingPath -Force | Out-Null
    Write-Host 'SHA256 verified. Extracting the project-local SDK.'
    Expand-Archive -LiteralPath $archivePath -DestinationPath $stagingPath
    if (-not (Test-Path -LiteralPath (Join-Path $stagingPath 'go\bin\go.exe') -PathType Leaf)) {
        throw 'The downloaded archive does not contain the expected Go executable.'
    }
    $sdkContainer = Assert-GoProjectPath $layout.ProjectRoot ([IO.Path]::GetDirectoryName($layout.SDKParent))
    New-Item -ItemType Directory -Path $sdkContainer -Force | Out-Null
    # Validate resolved absolute paths immediately before moving the extracted directory.
    $null = Assert-GoProjectPath $layout.ProjectRoot $stagingPath
    $null = Assert-GoProjectPath $layout.ProjectRoot $layout.SDKParent
    if (Test-Path -LiteralPath $layout.SDKParent) {
        throw 'The SDK destination appeared during extraction. Inspect it before retrying.'
    }
    Move-Item -LiteralPath $stagingPath -Destination $layout.SDKParent
}

& (Join-Path $PSScriptRoot 'go.ps1') version
if ($LASTEXITCODE -ne 0) { throw 'Project-local Go verification failed.' }
Write-Host 'Ready. Use .\scripts\go.ps1 for Go commands; no persistent environment settings were changed.'

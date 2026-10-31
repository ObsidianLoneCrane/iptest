# Forward Go arguments while keeping development state inside this repository.
# Deliberately use $args so native Go flags are not consumed by PowerShell parameters.
if ($MyInvocation.InvocationName -eq '.') {
    throw 'Run this script normally; do not dot-source it.'
}
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
. (Join-Path $PSScriptRoot 'go-common.ps1')

$previousEnvironment = @{}
$changedEnvironment = @()
$locationChanged = $false
$goExitCode = 1
try {
    $layout = Get-GoLayout
    if (-not (Test-Path -LiteralPath $layout.Executable -PathType Leaf)) {
        throw 'Project Go SDK is missing. Run .\scripts\setup-go.ps1 first.'
    }
    $installedVersion = Get-Content -LiteralPath (Join-Path $layout.SDKRoot 'VERSION') -TotalCount 1
    if ($installedVersion -ne ('go' + $layout.Version)) {
        throw 'The installed SDK does not match .go-version. Run setup-go.ps1 again.'
    }

    $goEnvironment = Get-GoProcessEnvironment $layout
    foreach ($name in $goEnvironment.Keys) {
        $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, $goEnvironment[$name], 'Process')
        $changedEnvironment += $name
    }
    Push-Location -LiteralPath $layout.ProjectRoot
    $locationChanged = $true

    # GOTELEMETRY is read-only. Configure it through Go with APPDATA redirected above.
    & $layout.Executable telemetry off | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Unable to disable telemetry in the project-local configuration.' }

    & $layout.Executable @args
    $goExitCode = $LASTEXITCODE
}
catch {
    Write-Error -Message $_.Exception.Message -ErrorAction Continue
    $goExitCode = 1
}
finally {
    try {
        if ($locationChanged) { Pop-Location }
    }
    finally {
        foreach ($name in $changedEnvironment) {
            if ($null -eq $previousEnvironment[$name]) {
                Remove-Item -LiteralPath ('Env:\' + $name) -ErrorAction SilentlyContinue
            }
            else {
                [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], 'Process')
            }
        }
    }
}
exit $goExitCode

# Shared helpers for the Windows project-local Go toolchain.
Set-StrictMode -Version Latest

function Assert-GoProjectPath {
    param([string]$ProjectRoot, [string]$Path)

    $rootPath = [System.IO.Path]::GetFullPath($ProjectRoot).TrimEnd('\', '/')
    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $prefix = $rootPath + [System.IO.Path]::DirectorySeparatorChar
    if (-not $fullPath.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Go development files must stay inside the project: $fullPath"
    }

    # Inspect ancestors before accessing descendants so a junction cannot redirect writes.
    $pathChain = @()
    $candidate = $fullPath
    while ($candidate) {
        $pathChain = @($candidate) + $pathChain
        $parent = [System.IO.Path]::GetDirectoryName($candidate)
        if ($parent -eq $candidate) { break }
        $candidate = $parent
    }
    foreach ($candidate in $pathChain) {
        try {
            $item = Get-Item -LiteralPath $candidate -Force -ErrorAction Stop
        }
        catch [System.Management.Automation.ItemNotFoundException] {
            continue
        }
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "Refusing a linked Go development path: $candidate"
        }
    }
    return $fullPath
}

function Get-GoLayout {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        throw 'The temporary Go environment scripts currently support Windows only.'
    }
    $projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
    $version = (Get-Content -LiteralPath (Join-Path $projectRoot '.go-version') -Raw).Trim()
    if ($version -notmatch '^\d+\.\d+\.\d+$') {
        throw 'Invalid pinned version in .go-version.'
    }
    $architecture = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $architecture = $env:PROCESSOR_ARCHITEW6432 }
    switch ($architecture.ToUpperInvariant()) {
        'AMD64' { $goArchitecture = 'amd64' }
        'ARM64' { $goArchitecture = 'arm64' }
        default { throw "Unsupported Windows architecture: $architecture" }
    }
    $platform = 'windows-' + $goArchitecture
    $toolsRoot = Assert-GoProjectPath $projectRoot (Join-Path $projectRoot '.tools\go')
    $cacheRoot = Assert-GoProjectPath $projectRoot (Join-Path $projectRoot '.cache\go')
    $sdkParent = Assert-GoProjectPath $projectRoot (Join-Path $toolsRoot "sdk\$version\$platform")
    $sdkRoot = Assert-GoProjectPath $projectRoot (Join-Path $sdkParent 'go')
    $goExecutable = Assert-GoProjectPath $projectRoot (Join-Path $sdkRoot 'bin\go.exe')
    return [pscustomobject]@{
        ProjectRoot = $projectRoot
        Version = $version
        Platform = $platform
        ToolsRoot = $toolsRoot
        CacheRoot = $cacheRoot
        SDKParent = $sdkParent
        SDKRoot = $sdkRoot
        Executable = $goExecutable
        ArchiveName = "go$version.$platform.zip"
    }
}

function Get-GoProcessEnvironment {
    param($Layout)

    $cacheRoot = $Layout.CacheRoot
    $environment = @{
        GOROOT = $Layout.SDKRoot
        GOPATH = (Join-Path $cacheRoot 'gopath')
        GOBIN = (Join-Path $cacheRoot 'bin')
        GOCACHE = (Join-Path $cacheRoot 'build')
        GOMODCACHE = (Join-Path $cacheRoot 'modules')
        GOTMPDIR = (Join-Path $cacheRoot 'tmp')
        GOENV = 'off'
        GOTOOLCHAIN = 'local'
        GOWORK = 'off'
        GO111MODULE = 'on'
        GOFLAGS = ''
        GOCACHEPROG = ''
        CGO_ENABLED = '0'
        APPDATA = (Join-Path $cacheRoot 'user-config')
        LOCALAPPDATA = (Join-Path $cacheRoot 'user-local')
        TEMP = (Join-Path $cacheRoot 'tmp')
        TMP = (Join-Path $cacheRoot 'tmp')
        PATH = (Join-Path $Layout.SDKRoot 'bin') + [System.IO.Path]::PathSeparator + $env:PATH
    }
    foreach ($name in @('GOPATH', 'GOBIN', 'GOCACHE', 'GOMODCACHE', 'GOTMPDIR', 'APPDATA', 'LOCALAPPDATA')) {
        $directory = Assert-GoProjectPath $Layout.ProjectRoot $environment[$name]
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    return $environment
}

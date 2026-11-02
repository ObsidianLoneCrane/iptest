<#
.SYNOPSIS
Removes this project's temporary Go SDK and caches.
.DESCRIPTION
Deletes only .tools/go and .cache/go below the project root. Source files,
build outputs, other tool directories, and global Go installations are kept.
All targets are validated before any deletion. Reparse points and symbolic
links in a target, its ancestors, or its contents cause cleanup to stop.
.EXAMPLE
./scripts/clean-go.ps1 -WhatIf
.EXAMPLE
./scripts/clean-go.ps1
.EXAMPLE
./scripts/clean-go.ps1 -CacheOnly
#>
[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'Medium')]
param(
    [switch]$CacheOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-GoCleanupFullPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $pathRoot = [System.IO.Path]::GetPathRoot($fullPath)
    if ($fullPath.Length -gt $pathRoot.Length) {
        $fullPath = $fullPath.TrimEnd([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
    }
    return $fullPath
}

function Get-GoCleanupItem {
    param([Parameter(Mandatory = $true)][string]$LiteralPath)

    try {
        return Get-Item -LiteralPath $LiteralPath -Force -ErrorAction Stop
    }
    catch [System.Management.Automation.ItemNotFoundException] {
        return $null
    }
}

function Assert-GoCleanupNotLinked {
    param([Parameter(Mandatory = $true)]$Item)

    $isReparsePoint = ($Item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0
    $linkTypeProperty = $Item.PSObject.Properties['LinkType']
    $hasLinkType = ($null -ne $linkTypeProperty) -and -not [string]::IsNullOrEmpty([string]$linkTypeProperty.Value)
    if ($isReparsePoint -or $hasLinkType) {
        throw "Refusing cleanup through a symbolic link, junction, or reparse point: $($Item.FullName)"
    }
}

function Assert-GoCleanupAncestors {
    param([Parameter(Mandatory = $true)][string]$LiteralPath)

    # Inspect from the filesystem root down so a linked parent is rejected
    # before any of its children are accessed.
    $ancestors = [System.Collections.Generic.List[string]]::new()
    $currentPath = Get-GoCleanupFullPath -Path $LiteralPath
    while (-not [string]::IsNullOrEmpty($currentPath)) {
        $ancestors.Add($currentPath)
        $parentPath = [System.IO.Path]::GetDirectoryName($currentPath)
        if ($parentPath -eq $currentPath) {
            break
        }
        $currentPath = $parentPath
    }

    for ($index = $ancestors.Count - 1; $index -ge 0; $index--) {
        $ancestorItem = Get-GoCleanupItem -LiteralPath $ancestors[$index]
        if ($null -ne $ancestorItem) {
            Assert-GoCleanupNotLinked -Item $ancestorItem
            if (-not $ancestorItem.PSIsContainer) {
                throw "Refusing cleanup because a path component is not a directory: $($ancestorItem.FullName)"
            }
        }
    }
}

function Assert-GoCleanupContents {
    param([Parameter(Mandatory = $true)][string]$LiteralPath)

    $targetItem = Get-GoCleanupItem -LiteralPath $LiteralPath
    if ($null -eq $targetItem) {
        return
    }

    $pending = [System.Collections.Generic.Stack[string]]::new()
    $pending.Push($targetItem.FullName)
    while ($pending.Count -gt 0) {
        $directoryPath = $pending.Pop()
        foreach ($childItem in Get-ChildItem -LiteralPath $directoryPath -Force -ErrorAction Stop) {
            Assert-GoCleanupNotLinked -Item $childItem
            if ($childItem.PSIsContainer) {
                $pending.Push($childItem.FullName)
            }
        }
    }
}

function Assert-GoCleanupTarget {
    param(
        [Parameter(Mandatory = $true)][string]$ProjectRoot,
        [Parameter(Mandatory = $true)][string]$TargetPath
    )

    $normalizedRoot = Get-GoCleanupFullPath -Path $ProjectRoot
    $normalizedTarget = Get-GoCleanupFullPath -Path $TargetPath
    $pathComparison = [System.StringComparison]::Ordinal
    if ([System.IO.Path]::DirectorySeparatorChar -eq '\') {
        $pathComparison = [System.StringComparison]::OrdinalIgnoreCase
    }

    if ([string]::Equals($normalizedRoot, $normalizedTarget, $pathComparison)) {
        throw 'Refusing to delete the project root.'
    }

    $rootPrefix = $normalizedRoot
    if (-not $rootPrefix.EndsWith([string][System.IO.Path]::DirectorySeparatorChar)) {
        $rootPrefix += [System.IO.Path]::DirectorySeparatorChar
    }
    if (-not $normalizedTarget.StartsWith($rootPrefix, $pathComparison)) {
        throw "Refusing a cleanup target outside the project: $normalizedTarget"
    }

    $allowedTargets = @(
        (Get-GoCleanupFullPath -Path (Join-Path $normalizedRoot '.tools/go'))
        (Get-GoCleanupFullPath -Path (Join-Path $normalizedRoot '.cache/go'))
    )
    $isAllowed = $false
    foreach ($allowedTarget in $allowedTargets) {
        if ([string]::Equals($normalizedTarget, $allowedTarget, $pathComparison)) {
            $isAllowed = $true
            break
        }
    }
    if (-not $isAllowed) {
        throw "Refusing an unexpected cleanup target: $normalizedTarget"
    }

    Assert-GoCleanupAncestors -LiteralPath $normalizedTarget
    Assert-GoCleanupContents -LiteralPath $normalizedTarget
}

$goCleanupProjectRoot = Get-GoCleanupFullPath -Path (Join-Path $PSScriptRoot '..')
$goCleanupTargets = @()
if (-not $CacheOnly) {
    $goCleanupTargets += Get-GoCleanupFullPath -Path (Join-Path $goCleanupProjectRoot '.tools/go')
}
$goCleanupTargets += Get-GoCleanupFullPath -Path (Join-Path $goCleanupProjectRoot '.cache/go')

# Validate the complete plan before changing either target.
foreach ($goCleanupTarget in $goCleanupTargets) {
    Assert-GoCleanupTarget -ProjectRoot $goCleanupProjectRoot -TargetPath $goCleanupTarget
}

foreach ($goCleanupTarget in $goCleanupTargets) {
    if ($PSCmdlet.ShouldProcess($goCleanupTarget, 'Remove project-local Go SDK or cache')) {
        # Recheck immediately before deletion in case the filesystem changed
        # while confirmation was pending.
        Assert-GoCleanupTarget -ProjectRoot $goCleanupProjectRoot -TargetPath $goCleanupTarget
        $goCleanupExistingItem = Get-GoCleanupItem -LiteralPath $goCleanupTarget
        if ($null -ne $goCleanupExistingItem) {
            Remove-Item -LiteralPath $goCleanupTarget -Recurse -Force -ErrorAction Stop
            Write-Output "Removed: $goCleanupTarget"
        }
        else {
            Write-Verbose "Nothing to remove: $goCleanupTarget"
        }
    }
}

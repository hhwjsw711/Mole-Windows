# Mole - Clean Command
# Deep cleanup for Windows with dry-run support and whitelist

#Requires -Version 5.1
[CmdletBinding()]
param(
    [Alias('dry-run')]
    [switch]$DryRun,
    
    [Alias('s')]
    [switch]$System,
    
    [Alias('game-media')]
    [switch]$GameMedia,
    
    [Alias('d')]
    [switch]$DebugMode,
    
    [Alias('w')]
    [switch]$Whitelist,
    
    [Alias('j')]
    [switch]$Json,
    
    [Alias('h')]
    [switch]$ShowHelp
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Script location
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$libDir = Join-Path (Split-Path -Parent $scriptDir) "lib"

# Import core modules
. "$libDir\core\base.ps1"
. "$libDir\core\log.ps1"
. "$libDir\core\ui.ps1"
. "$libDir\core\file_ops.ps1"
. "$libDir\core\history.ps1"

# Import cleanup modules
. "$libDir\clean\user.ps1"
. "$libDir\clean\caches.ps1"
. "$libDir\clean\dev.ps1"
. "$libDir\clean\apps.ps1"
. "$libDir\clean\system.ps1"

# ============================================================================
# Configuration
# ============================================================================

$script:ExportListFile = "$env:USERPROFILE\.config\mole\clean-list.txt"

# ============================================================================
# Help
# ============================================================================

function Show-CleanHelp {
    $esc = [char]27
    Write-Host ""
    Write-Host "$esc[1;35mmo clean$esc[0m - Deep cleanup for Windows"
    Write-Host ""
    Write-Host "$esc[33mUsage:$esc[0m mo clean [options]"
    Write-Host ""
    Write-Host "$esc[33mOptions:$esc[0m"
    Write-Host "  --dry-run      Preview changes without deleting (recommended first run)"
    Write-Host "  --json         Output cleanup scan results as JSON (dry-run only)"
    Write-Host "  --system       Include system-level cleanup (requires admin)"
    Write-Host "  --game-media   Clean old game replays, screenshots, recordings (>90d)"
    Write-Host "  --whitelist    Manage protected paths"
    Write-Host "  --debug        Enable debug logging"
    Write-Host "  --help         Show this help message"
    Write-Host ""
    Write-Host "$esc[33mExamples:$esc[0m"
    Write-Host "  mo clean --dry-run     # Preview what would be cleaned"
    Write-Host "  mo clean               # Run standard cleanup"
    Write-Host "  mo clean --game-media  # Include old game media cleanup"
    Write-Host "  mo clean --system      # Include system cleanup (as admin)"
    Write-Host ""
}

# ============================================================================
# Whitelist Management
# ============================================================================

function Edit-Whitelist {
    $whitelistPath = $script:Config.WhitelistFile
    $whitelistDir = Split-Path -Parent $whitelistPath

    # Ensure directory exists
    if (-not (Test-Path $whitelistDir)) {
        New-Item -ItemType Directory -Path $whitelistDir -Force | Out-Null
    }

    # Create default whitelist if doesn't exist
    if (-not (Test-Path $whitelistPath)) {
        $defaultContent = @"
# Mole Whitelist - Paths listed here will never be cleaned
# Use full paths or patterns with wildcards (*)
#
# Examples:
# C:\Users\YourName\Documents\ImportantProject
# C:\Users\*\AppData\Local\MyApp
# $env:LOCALAPPDATA\CriticalApp
#
# Add your protected paths below:

"@
        Set-Content -Path $whitelistPath -Value $defaultContent
    }

    # Open in default editor
    Write-Info "Opening whitelist file: $whitelistPath"
    Start-Process notepad.exe -ArgumentList $whitelistPath -Wait

    Write-Success "Whitelist saved"
}

# ============================================================================
# Cleanup Summary
# ============================================================================

function Show-CleanupSummary {
    param(
        [hashtable]$Stats,
        [bool]$IsDryRun
    )

    $esc = [char]27

    Write-Host ""
    Write-Host "$esc[1;35m" -NoNewline
    if ($IsDryRun) {
        Write-Host "Dry run complete - no changes made" -NoNewline
    }
    else {
        Write-Host "Cleanup complete" -NoNewline
    }
    Write-Host "$esc[0m"
    Write-Host ""

    if ($Stats.TotalSizeKB -gt 0) {
        $sizeGB = [Math]::Round($Stats.TotalSizeKB / 1024 / 1024, 2)

        if ($IsDryRun) {
            Write-Host "  Potential space: $esc[32m${sizeGB}GB$esc[0m"
            Write-Host "  Items found: $($Stats.FilesCleaned)"
            Write-Host "  Categories: $($Stats.TotalItems)"
            Write-Host ""
            Write-Host "  Detailed list: $esc[90m$($script:ExportListFile)$esc[0m"
            Write-Host "  Run without --dry-run to apply cleanup"
        }
        else {
            Write-Host "  Space freed: $esc[32m${sizeGB}GB$esc[0m"
            Write-Host "  Items cleaned: $($Stats.FilesCleaned)"
            Write-Host "  Categories: $($Stats.TotalItems)"
            Write-Host ""
            Write-Host "  Free space now: $(Get-FreeSpace)"
        }
    }
    else {
        if ($IsDryRun) {
            Write-Host "  No significant reclaimable space detected."
        }
        else {
            Write-Host "  System was already clean; no additional space freed."
        }
        Write-Host "  Free space now: $(Get-FreeSpace)"
    }

    Write-Host ""
}

# ============================================================================
# Main Cleanup Flow
# ============================================================================

function Start-Cleanup {
    param(
        [bool]$IsDryRun,
        [bool]$IncludeSystem,
        [bool]$IncludeGameMedia
    )

    $esc = [char]27

    $startedAt = (Get-Date).ToString("yyyy-MM-ddTHH:mm:sszzz")

    # Clear screen
    Clear-Host
    Write-Host ""
    Write-Host "$esc[1;35mClean Your Windows$esc[0m"
    Write-Host ""

    # Show mode
    if ($IsDryRun) {
        Write-Host "$esc[33mDry Run Mode$esc[0m - Preview only, no deletions"
        Write-Host ""

        # Prepare export file
        $exportDir = Split-Path -Parent $script:ExportListFile
        if (-not (Test-Path $exportDir)) {
            New-Item -ItemType Directory -Path $exportDir -Force | Out-Null
        }

        $header = @"
# Mole Cleanup Preview - $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')
#
# How to protect files:
# 1. Copy any path below to $($script:Config.WhitelistFile)
# 2. Run: mo clean --whitelist
#

"@
        Set-Content -Path $script:ExportListFile -Value $header
    }
    else {
        Write-Host "$esc[90m$($script:Icons.Solid) Use --dry-run to preview, --whitelist to manage protected paths$esc[0m"
        Write-Host ""
    }

    # System cleanup confirmation
    if ($IncludeSystem -and -not $IsDryRun) {
        if (-not (Test-IsAdmin)) {
            Write-MoleWarning "System cleanup requires administrator privileges"
            Write-Host "  Run PowerShell as Administrator for full cleanup"
            Write-Host ""
            $IncludeSystem = $false
        }
        else {
            Write-Host "$esc[32m$($script:Icons.Success)$esc[0m Running with Administrator privileges"
            Write-Host ""
        }
    }

    # Show system info
    $winVer = Get-WindowsVersion
    Write-Host "$esc[34m$($script:Icons.Admin)$esc[0m $($winVer.Name) | Free space: $(Get-FreeSpace)"
    Write-Host ""

    # Reset stats
    Reset-CleanupStats
    Set-DryRunMode -Enabled $IsDryRun

    # Run cleanup modules
    try {
        # User essentials (temp, logs, etc.)
        Invoke-UserCleanup -TempDaysOld 7 -LogDaysOld 7

        # Browser caches
        Clear-BrowserCaches

        # GPU shader caches (NVIDIA, AMD, Intel, DirectX)
        Clear-GPUShaderCaches

        # Application caches
        Clear-AppCaches

        # Developer tools
        Invoke-DevToolsCleanup

        # Applications cleanup (with optional game media)
        if ($IncludeGameMedia) {
            Invoke-AppCleanup -IncludeGameMedia -GameMediaDaysOld 90
        }
        else {
            Invoke-AppCleanup
        }

        # System cleanup (if requested and admin)
        if ($IncludeSystem -and (Test-IsAdmin)) {
            Invoke-SystemCleanup
        }
    }
    catch {
        Write-MoleError "Cleanup error: $_"
    }

    # Get final stats
    $stats = Get-CleanupStats

    if (-not $IsDryRun) {
        Write-SessionRecord -Command clean `
            -StartedAt $startedAt `
            -EndedAt (Get-Date).ToString("yyyy-MM-ddTHH:mm:sszzz") `
            -Items $stats.FilesCleaned `
            -Size $stats.TotalSizeHuman `
            -OperationCount $stats.FilesCleaned `
            -Removed $stats.FilesCleaned
    }

    # Show summary
    Show-CleanupSummary -Stats $stats -IsDryRun $IsDryRun
}

# ============================================================================
# JSON Scan Mode
# ============================================================================

function Start-CleanupJson {
    param(
        [bool]$IncludeSystem,
        [bool]$IncludeGameMedia
    )

    $origProgPref = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'

    try {
        Set-DryRunMode -Enabled $true
        Set-JsonMode -Enabled $true
        Reset-CleanupStats

        $categories = @()

        $catDefs = @(
            @{
                Name  = "User temp files"
                Func  = { Invoke-UserCleanup -TempDaysOld 7 -LogDaysOld 7 }
                Paths = @("$env:TEMP", "$env:WINDIR\Temp")
            }
            @{
                Name  = "Browser cache"
                Func  = { Clear-BrowserCaches }
                Paths = @(
                    "$env:LOCALAPPDATA\Google\Chrome\User Data\Default\Cache",
                    "$env:LOCALAPPDATA\Microsoft\Edge\User Data\Default\Cache"
                )
            }
            @{
                Name  = "GPU shader caches"
                Func  = { Clear-GPUShaderCaches }
                Paths = @(
                    "$env:LOCALAPPDATA\NVIDIA\DXCache",
                    "$env:LOCALAPPDATA\D3DSCache"
                )
            }
            @{
                Name  = "Application caches"
                Func  = { Clear-AppCaches }
                Paths = @(
                    "$env:LOCALAPPDATA\Spotify\Data",
                    "$env:APPDATA\discord\Cache"
                )
            }
            @{
                Name  = "Developer tools"
                Func  = { Invoke-DevToolsCleanup }
                Paths = @(
                    "$env:LOCALAPPDATA\npm-cache",
                    "$env:LOCALAPPDATA\pip\Cache",
                    "$env:USERPROFILE\.cargo\registry\cache"
                )
            }
            @{
                Name  = "Applications"
                Func  = { Invoke-AppCleanup -IncludeGameMedia:$IncludeGameMedia -GameMediaDaysOld 90 }
                Paths = @(
                    "$env:LOCALAPPDATA\Microsoft\Office\16.0\OfficeFileCache",
                    "$env:LOCALAPPDATA\Microsoft\OneDrive\logs"
                )
            }
        )

        foreach ($def in $catDefs) {
            $prevSize = $script:TotalSizeCleaned
            $prevItems = $script:FilesCleaned

            $null = & $def.Func 6>&1 2>&1 3>&1 4>&1 5>&1 | Out-Null

            $catSizeKB = $script:TotalSizeCleaned - $prevSize
            $catItems = $script:FilesCleaned - $prevItems

            $expandedPaths = foreach ($p in $def.Paths) {
                $ExecutionContext.InvokeCommand.ExpandString($p)
            }
            $filteredPaths = @($expandedPaths | Select-Object -First 5)

            $catSizeBytes = $catSizeKB * 1024
            $catSizeHuman = if ($catSizeKB -gt 0) { Format-ByteSize -Bytes $catSizeBytes } else { "0B" }

            $categories += [PSCustomObject]@{
                name        = $def.Name
                size_bytes  = $catSizeBytes
                size_human  = $catSizeHuman
                item_count  = $catItems
                paths       = $filteredPaths
            }
        }

        if ($IncludeSystem -and (Test-IsAdmin)) {
            $prevSize = $script:TotalSizeCleaned
            $prevItems = $script:FilesCleaned

            $null = Invoke-SystemCleanup 2>$null 3>$null 4>$null 5>$null

            $sysSizeKB = $script:TotalSizeCleaned - $prevSize
            $sysItems = $script:FilesCleaned - $prevItems
            $sysSizeBytes = $sysSizeKB * 1024
            $sysSizeHuman = if ($sysSizeKB -gt 0) { Format-ByteSize -Bytes $sysSizeBytes } else { "0B" }

            $sysPaths = @(
                "$env:WINDIR\Temp",
                "$env:WINDIR\Logs\CBS",
                "$env:WINDIR\SoftwareDistribution\Download"
            ) | ForEach-Object {
                $ExecutionContext.InvokeCommand.ExpandString($_)
            }

            $categories += [PSCustomObject]@{
                name        = "System cleanup"
                size_bytes  = $sysSizeBytes
                size_human  = $sysSizeHuman
                item_count  = $sysItems
                paths       = [string[]]$sysPaths
            }
        }

        $totalSizeBytes = $script:TotalSizeCleaned * 1024
        $totalSizeHuman = if ($totalSizeBytes -gt 0) { Format-ByteSize -Bytes $totalSizeBytes } else { "0B" }

        $output = [PSCustomObject]@{
            command          = "clean"
            dry_run          = $true
            total_size_bytes = $totalSizeBytes
            total_size_human = $totalSizeHuman
            total_items      = $script:FilesCleaned
            categories       = $categories
        }

        $json = $output | ConvertTo-Json -Depth 4

        if ($categories.Count -eq 1) {
            $json = $json -replace '"categories":  \{','"categories":  [{'
            $json = $json -replace '"paths":  \[',']' + "`r`n" + '  }],' + "`r`n" + '  "paths":  ['
        }

        Write-Output $json
    }
    finally {
        $ProgressPreference = $origProgPref
        Set-JsonMode -Enabled $false
    }
}

# ============================================================================
# Main Entry Point
# ============================================================================

function Main {
    # Enable debug if requested
    if ($DebugMode) {
        $env:MOLE_DEBUG = "1"
        $DebugPreference = "Continue"
    }

    # Show help
    if ($ShowHelp) {
        Show-CleanHelp
        return
    }

    # Manage whitelist
    if ($Whitelist) {
        Edit-Whitelist
        return
    }

    # JSON scan mode (dry-run + structured output)
    if ($Json) {
        Start-CleanupJson -IncludeSystem $System -IncludeGameMedia $GameMedia
        return
    }

    # Set dry-run mode
    if ($DryRun) {
        $env:MOLE_DRY_RUN = "1"
    }
    else {
        $env:MOLE_DRY_RUN = "0"
    }

    # Run cleanup
    try {
        Start-Cleanup -IsDryRun $DryRun -IncludeSystem $System -IncludeGameMedia $GameMedia
    }
    finally {
        # Cleanup temp files
        Clear-TempFiles
    }
}

# Run main
Main

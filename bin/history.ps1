# Mole - History Command
# View cleanup session history

#Requires -Version 5.1
[CmdletBinding()]
param(
    [Alias('j')]
    [switch]$Json,
    
    [Alias('l')]
    [ValidateRange(1, 1000)]
    [int]$Limit = 0,
    
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
. "$libDir\core\history.ps1"

$script:DeletionLogFile = "$env:LOCALAPPDATA\Mole\logs\deletions.log"

# ============================================================================
# Help
# ============================================================================

function Show-HistoryHelp {
    $esc = [char]27
    Write-Host ""
    Write-Host "$esc[1;35mmo history$esc[0m - View cleanup session history"
    Write-Host ""
    Write-Host "$esc[33mUsage:$esc[0m mo history [options]"
    Write-Host ""
    Write-Host "$esc[33mOptions:$esc[0m"
    Write-Host "  --json        Output history as JSON"
    Write-Host "  --limit N     Limit to last N sessions (default: all)"
    Write-Host "  --help        Show this help message"
    Write-Host ""
}

# ============================================================================
# JSON Output
# ============================================================================

function Get-HistoryJson {
    param([int]$Limit)

    $sessions = @(Read-SessionHistory -Limit $Limit)

    $deletionLogPath = $script:DeletionLogFile
    if (-not (Test-Path $deletionLogPath)) {
        $deletionLogPath = ""
    }
    $deletionLogPath = $deletionLogPath -replace '\\', '\\'

    $sessionsJson = "["
    if ($sessions.Count -gt 0) {
        $parts = @()
        foreach ($s in $sessions) {
            $parts += ($s | ConvertTo-Json -Depth 4 -Compress)
        }
        $sessionsJson += ($parts -join ",")
    }
    $sessionsJson += "]"

    $output = @"
{
  "sessions": $sessionsJson,
  "logs": {
    "deletions": "$deletionLogPath"
  }
}
"@
    Write-Output $output
}

# ============================================================================
# Main Entry Point
# ============================================================================

function Main {
    if ($ShowHelp) {
        Show-HistoryHelp
        return
    }

    if ($Json) {
        Get-HistoryJson -Limit $Limit
        return
    }

    Show-HistoryHelp
}

# Run main
Main

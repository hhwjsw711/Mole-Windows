# Mole - Session History Module
# Records each command execution for later retrieval

#Requires -Version 5.1
Set-StrictMode -Version Latest

# Prevent multiple sourcing
if ((Get-Variable -Name 'MOLE_HISTORY_LOADED' -Scope Script -ErrorAction SilentlyContinue) -and $script:MOLE_HISTORY_LOADED) { return }
$script:MOLE_HISTORY_LOADED = $true

$script:HistoryDir = "$env:LOCALAPPDATA\Mole\history"
$script:SessionsFile = Join-Path $script:HistoryDir "sessions.json"

function Get-SessionsFilePath {
    return $script:SessionsFile
}

function Initialize-History {
    if (-not (Test-Path $script:HistoryDir)) {
        New-Item -ItemType Directory -Path $script:HistoryDir -Force | Out-Null
    }
}

function Write-SessionRecord {
    param(
        [Parameter(Mandatory)]
        [string]$Command,
        [Parameter(Mandatory)]
        [string]$StartedAt,
        [string]$EndedAt = "",
        [int]$Items = 0,
        [string]$Size = "",
        [int]$OperationCount = 0,
        [int]$Removed = 0,
        [int]$Trashed = 0,
        [int]$Skipped = 0,
        [int]$Failed = 0
    )

    Initialize-History

    $session = [PSCustomObject]@{
        command        = $Command
        started_at     = $StartedAt
        ended_at       = $EndedAt
        items          = $Items
        size           = $Size
        operation_count = $OperationCount
        actions        = [PSCustomObject]@{
            removed = $Removed
            trashed = $Trashed
            skipped = $Skipped
            failed  = $Failed
        }
    }

    $sessions = @()
    if (Test-Path $script:SessionsFile) {
        try {
            $sessions = Get-Content $script:SessionsFile -Raw | ConvertFrom-Json
            if ($null -ne $sessions) {
                $sessions = @($sessions)
            }
        }
        catch {
            $sessions = @()
        }
    }

    $sessions = @($session) + @($sessions)
    $sessions | ConvertTo-Json -Depth 4 | Set-Content $script:SessionsFile -Encoding UTF8 -Force
}

function Read-SessionHistory {
    param(
        [int]$Limit = 0
    )

    Initialize-History

    if (-not (Test-Path $script:SessionsFile)) {
        return @()
    }

    try {
        $sessions = Get-Content $script:SessionsFile -Raw | ConvertFrom-Json
        if ($null -eq $sessions) {
            return @()
        }
        $sessions = @($sessions)
    }
    catch {
        return @()
    }

    if ($Limit -gt 0 -and $sessions.Count -gt $Limit) {
        $sessions = $sessions[0..($Limit - 1)]
    }

    return $sessions
}

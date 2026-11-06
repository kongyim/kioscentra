[CmdletBinding()]
param([string]$DataRoot = "$env:ProgramData\Kiosentra", [int]$TimeoutSeconds = 120)
. "$PSScriptRoot\Common.ps1"
Assert-Administrator
$DataRoot = Assert-LocalPath $DataRoot
$config = Join-Path $DataRoot 'config\service.json'
$record = Get-Content -LiteralPath (Join-Path $DataRoot 'config\installation.json') -Raw | ConvertFrom-Json
$deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
$snapshot = $null
while ([DateTime]::UtcNow -lt $deadline) {
    $snapshot = Get-AgentStatus $record.executable $config
    if ($snapshot) {
        if ($snapshot.deviceId -and ([DateTime]$snapshot.credentialExpiresAt) -gt [DateTime]::UtcNow) { break }
    }
    Start-Sleep -Seconds 1
}
if (-not $snapshot -or -not $snapshot.deviceId -or ([DateTime]$snapshot.credentialExpiresAt) -le [DateTime]::UtcNow) {
    throw 'Enrollment not confirmed. Bootstrap token retained in the protected service configuration; retry when central is available.'
}
$settings = Get-Content -LiteralPath $config -Raw | ConvertFrom-Json
$settings.PSObject.Properties.Remove('KIOSENTRA_ENROLLMENT_TOKEN')
$temporary = Join-Path (Split-Path $config) ([Guid]::NewGuid().ToString() + '.json')
try {
    Write-JSON $temporary $settings
    [IO.File]::Replace($temporary, $config, $null)
} finally {
    if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary }
}
# Restart clears the enrollment token from the old service process's memory.
Restart-Service Kiosentra
Write-Output 'Enrollment confirmed; bootstrap token removed. Service restarted with persisted DPAPI credentials.'

[CmdletBinding()]
param([string]$DataRoot = "$env:ProgramData\Kiosentra", [switch]$ExerciseRestart)
. "$PSScriptRoot\Common.ps1"
Assert-Administrator
$DataRoot = Assert-LocalPath $DataRoot
$record = Get-Content -LiteralPath (Join-Path $DataRoot 'config\installation.json') -Raw | ConvertFrom-Json
if ($record.schema -ne 1 -or $record.dataRoot -ne $DataRoot) { throw 'Installation record mismatch.' }
$service = Get-CimInstance Win32_Service -Filter "Name='Kiosentra'"
if (-not $service -or $service.StartName -ne 'NT SERVICE\Kiosentra' -or $service.StartMode -ne 'Auto') { throw 'Service account or startup mismatch.' }
$config = Join-Path $DataRoot 'config\service.json'
$expectedPath = '"' + $record.executable + '" --mode=service --config="' + $config + '"'
if ($service.PathName -ne $expectedPath) { throw 'Service command mismatch.' }
foreach ($exe in @($record.executable, $record.recoveryExecutable)) {
    $null = Assert-LocalPath $exe
    if ((Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash -ne $record.sha256) { throw 'Installed binary changed.' }
    if (-not $record.development) {
        $signature = Get-AuthenticodeSignature -LiteralPath $exe
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $record.signerThumbprint) { throw 'Installed signature mismatch.' }
    }
    $acl = Get-Acl -LiteralPath $exe
    foreach ($entry in $acl.Access) {
        $sid = $entry.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
        $write = [Security.AccessControl.FileSystemRights]::Write -bor [Security.AccessControl.FileSystemRights]::Delete -bor [Security.AccessControl.FileSystemRights]::ChangePermissions -bor [Security.AccessControl.FileSystemRights]::TakeOwnership
        if ($entry.AccessControlType -eq 'Allow' -and ($entry.FileSystemRights -band $write) -and $sid -notin @('S-1-5-18','S-1-5-32-544')) { throw 'Non-administrator can modify installed executors.' }
    }
}
foreach ($directory in @((Join-Path $DataRoot 'config'), (Join-Path $DataRoot 'state'))) {
    $acl = Get-Acl -LiteralPath $directory
    if (-not $acl.AreAccessRulesProtected) { throw 'Private directory inherits access.' }
    foreach ($entry in $acl.Access) {
        $sid = $entry.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
        if ($entry.AccessControlType -eq 'Allow' -and $sid -notin @('S-1-5-18','S-1-5-32-544',$record.serviceSid)) { throw 'Unexpected private directory reader.' }
    }
}
if ($record.simulationRecovery) {
    $task = Get-ScheduledTask -TaskName KiosentraRecovery
    if ($task.Actions[0].Execute -ne $record.recoveryExecutable -or $task.Principal.UserId -notin @('SYSTEM','S-1-5-18') -or $task.State -eq 'Disabled') { throw 'Recovery task mismatch.' }
}
function Read-Status {
    $status = Get-AgentStatus $record.executable $config
    if (-not $status) { throw 'Service IPC status failed.' }
    return $status
}
$before = Read-Status
if (-not $before.deviceId) { throw 'Enrollment has not completed.' }
if ($ExerciseRestart) {
    Restart-Service Kiosentra
    Start-Sleep -Seconds 3
    $after = Read-Status
    if ($before.deviceId -ne $after.deviceId) { throw 'Device identity changed after service restart.' }
}
Write-Output 'PASS: service configuration, binary integrity, ACL boundaries, IPC and enrollment. Reboot, crash and interactive UI checks remain separate.'

[CmdletBinding(SupportsShouldProcess)]
param([string]$DataRoot = "$env:ProgramData\Kiosentra")
. "$PSScriptRoot\Common.ps1"
Assert-Administrator
$DataRoot = Assert-LocalPath $DataRoot
$record = Get-Content -LiteralPath (Join-Path $DataRoot 'config\installation.json') -Raw | ConvertFrom-Json
if ($record.schema -ne 1 -or $record.dataRoot -ne $DataRoot) { throw 'Installation record mismatch.' }
$service = Get-CimInstance Win32_Service -Filter "Name='Kiosentra'" -ErrorAction SilentlyContinue
if ($service -and -not $service.PathName.StartsWith('"' + $record.executable + '" ', [StringComparison]::OrdinalIgnoreCase)) { throw 'Service executable differs from the installation record.' }
if ($PSCmdlet.ShouldProcess('Kiosentra', 'Stop and unregister service and recovery task; preserve binaries and state')) {
    if ($record.simulationRecovery) {
        $task = Get-ScheduledTask -TaskName KiosentraRecovery -ErrorAction SilentlyContinue
        if ($task) {
            if ($task.Actions[0].Execute -ne $record.recoveryExecutable) { throw 'Recovery task differs from the installation record.' }
            Stop-ScheduledTask -TaskName KiosentraRecovery
            Unregister-ScheduledTask -TaskName KiosentraRecovery -Confirm:$false
        }
    }
    if ($service) {
        Invoke-SC @('config', 'Kiosentra', 'start=', 'disabled')
        Stop-Service Kiosentra
        Invoke-SC @('delete', 'Kiosentra')
    }
    Write-Output 'Service and recovery trigger removed. Protected binaries, configuration and device state retained for explicit retirement/backup.'
}

# Run only on a disposable, elevated Windows test host with no existing Kiosentra installation.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$PackageDirectory,
    [Parameter(Mandatory)][string]$MockExecutable,
    [Parameter(Mandatory)][string]$KeygenExecutable,
    [Parameter(Mandatory)][string]$OutputDirectory
)
. "$PSScriptRoot\Common.ps1"
Assert-Administrator
if (Get-Service Kiosentra -ErrorAction SilentlyContinue) { throw 'Use a host without an existing Kiosentra service.' }
if (Get-ScheduledTask -TaskName KiosentraRecovery -ErrorAction SilentlyContinue) { throw 'Use a host without an existing recovery task.' }
$OutputDirectory = Assert-LocalPath $OutputDirectory
if (Test-Path -LiteralPath $OutputDirectory) { throw 'Use a fresh output directory.' }
$MockExecutable = Assert-LocalPath $MockExecutable
$KeygenExecutable = Assert-LocalPath $KeygenExecutable
New-Item -ItemType Directory -Path $OutputDirectory | Out-Null
Set-DirectoryAccess $OutputDirectory
$tampered = Join-Path $OutputDirectory 'tampered-package'
Copy-Item -LiteralPath $PackageDirectory -Destination $tampered -Recurse
[IO.File]::AppendAllText((Join-Path $tampered 'kiosentra-agent.exe'), 'tamper-test')
$rejected = $false
try { Assert-Package $tampered '' -Development | Out-Null } catch { $rejected = $true }
if (-not $rejected) { throw 'Modified executable passed package verification.' }
$rejected = $false
try { Assert-Package $PackageDirectory ('0' * 40) | Out-Null } catch { $rejected = $true }
if (-not $rejected) { throw 'Development package passed production signature validation.' }
$listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
$listener.Start(); $port = $listener.LocalEndpoint.Port; $listener.Stop()
$origin = "http://127.0.0.1:$port"
$token = New-RandomToken
$admin = New-RandomToken
$keys = (& $KeygenExecutable | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0) { throw 'Signing-key fixture generation failed.' }
$oldEnvironment = @{}
$mockEnvironment = @{
    KIOSENTRA_MOCK_ENROLLMENT_TOKEN = $token
    KIOSENTRA_MOCK_ADMIN_TOKEN = $admin
    KIOSENTRA_MOCK_SIMULATION_PRIVATE_KEY = $keys.privateKey
}
$mock = $null
$data = Join-Path $OutputDirectory 'data'
$install = Join-Path $OutputDirectory 'install'
try {
    foreach ($key in $mockEnvironment.Keys) {
        $oldEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, $mockEnvironment[$key], 'Process')
    }
    try {
        $mock = Start-Process -FilePath $MockExecutable -ArgumentList "-listen=127.0.0.1:$port" -PassThru -RedirectStandardOutput (Join-Path $OutputDirectory 'mock.stdout.log') -RedirectStandardError (Join-Path $OutputDirectory 'mock.stderr.log')
    } finally {
        foreach ($key in $oldEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key, $oldEnvironment[$key], 'Process') }
    }
    $profileDir = Join-Path $OutputDirectory 'technician-issuance'
    & "$PSScriptRoot\New-TechnicianProfile.ps1" -TechnicianIds 'acceptance-reader' -OutputDirectory $profileDir
    & "$PSScriptRoot\Install-Agent.ps1" -PackageDirectory $PackageDirectory -ServerURL $origin -EnrollmentToken (ConvertTo-SecureString $token -AsPlainText -Force) -InstallRoot $install -DataRoot $data -Development -SimulationPublicKey $keys.publicKey -TechniciansFile (Join-Path $profileDir 'technicians.json')
    & "$PSScriptRoot\Complete-Provisioning.ps1" -DataRoot $data
    & "$PSScriptRoot\Test-Installation.ps1" -DataRoot $data -ExerciseRestart
    $config = Join-Path $data 'config\service.json'
    if ((Get-Content $config -Raw) -match 'KIOSENTRA_ENROLLMENT_TOKEN') { throw 'Bootstrap token remains after enrollment.' }
    $record = Get-Content (Join-Path $data 'config\installation.json') -Raw | ConvertFrom-Json
    $before = Get-AgentStatus $record.executable $config
    if (-not $before -or -not $before.namedTechnicians -or -not $before.maintenanceEnabled) { throw 'Named technician provisioning missing.' }
    $service = Get-CimInstance Win32_Service -Filter "Name='Kiosentra'"
    $oldPid = $service.ProcessId
    if (-not $oldPid) { throw 'Service not running before crash test.' }
    Stop-Process -Id $oldPid -Force
    $deadline = [DateTime]::UtcNow.AddSeconds(90)
    $restored = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        Start-Sleep -Seconds 1
        $service = Get-CimInstance Win32_Service -Filter "Name='Kiosentra'"
        if ($service.State -eq 'Running' -and $service.ProcessId -ne $oldPid) {
            $after = Get-AgentStatus $record.executable $config
            if ($after) {
                if ($after.deviceId -eq $before.deviceId) { $restored = $true; break }
            }
        }
    }
    if (-not $restored) { throw 'SCM crash recovery or DPAPI identity persistence failed.' }
    Stop-Service Kiosentra
    $previousRun = (Get-ScheduledTaskInfo -TaskName KiosentraRecovery).LastRunTime
    Start-ScheduledTask -TaskName KiosentraRecovery
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    $taskPassed = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        Start-Sleep -Seconds 1
        $info = Get-ScheduledTaskInfo -TaskName KiosentraRecovery
        $task = Get-ScheduledTask -TaskName KiosentraRecovery
        if ($info.LastRunTime -gt $previousRun -and $task.State -ne 'Running') {
            if ($info.LastTaskResult -ne 0) { throw "Recovery task exit code: $($info.LastTaskResult)" }
            $taskPassed = $true; break
        }
    }
    if (-not $taskPassed) { throw 'Recovery task did not finish.' }
    Write-Output 'PASS: development package installation, protected provisioning, service-account DPAPI persistence, crash restart, and independent task dispatch. This does not test reboot or interactive UI.'
} finally {
    try {
        if (Test-Path -LiteralPath (Join-Path $data 'config\installation.json')) {
            & "$PSScriptRoot\Uninstall-Agent.ps1" -DataRoot $data -Confirm:$false
        }
    } finally {
        if ($mock -and -not $mock.HasExited) { Stop-Process -Id $mock.Id -Force }
    }
}

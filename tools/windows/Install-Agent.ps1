[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$PackageDirectory,
    [Parameter(Mandatory)][uri]$ServerURL,
    [Parameter(Mandatory)][Security.SecureString]$EnrollmentToken,
    [string]$SignerThumbprint,
    [string]$UISid,
    [string]$TechniciansFile,
    [string]$InstallRoot = "$env:ProgramFiles\Kiosentra",
    [string]$DataRoot = "$env:ProgramData\Kiosentra",
    [string]$SimulationPublicKey,
    [switch]$Development
)
. "$PSScriptRoot\Common.ps1"
Assert-Administrator
$InstallRoot = Assert-LocalPath $InstallRoot
$DataRoot = Assert-LocalPath $DataRoot
$PackageDirectory = Assert-LocalPath $PackageDirectory
$manifest = Assert-Package $PackageDirectory $SignerThumbprint -Development:$Development
if ($ServerURL.UserInfo -or $ServerURL.Query -or $ServerURL.Fragment -or $ServerURL.AbsolutePath -ne '/') { throw 'Server URL must be an origin.' }
if ($ServerURL.Scheme -ne 'https') {
    $ip = $null
    if (-not $Development -or $ServerURL.Scheme -ne 'http' -or -not [Net.IPAddress]::TryParse($ServerURL.Host, [ref]$ip) -or -not [Net.IPAddress]::IsLoopback($ip)) { throw 'HTTPS is required outside explicit loopback development.' }
}
if ($SimulationPublicKey -and (-not $Development -or [Convert]::FromBase64String($SimulationPublicKey).Length -ne 32)) { throw 'Simulation requires -Development and a 32-byte Ed25519 public key.' }
if (Get-Service -Name Kiosentra -ErrorAction SilentlyContinue) { throw 'Service already exists. This installer does not replace live installations.' }
if (Get-ScheduledTask -TaskName KiosentraRecovery -ErrorAction SilentlyContinue) { throw 'A recovery task already exists; reconcile the existing installation first.' }
if ((Test-Path -LiteralPath $InstallRoot) -or (Test-Path -LiteralPath $DataRoot)) { throw 'Use fresh install and data directories; existing state is never overwritten.' }
if ($UISid) { $null = [Security.Principal.SecurityIdentifier]::new($UISid) }
if ($TechniciansFile) {
    $TechniciansFile = Assert-LocalPath $TechniciansFile
    $null = Get-Content -LiteralPath $TechniciansFile -Raw | ConvertFrom-Json
}
$createdService = $false
$createdTask = $false
try {
    foreach ($directory in @($InstallRoot, $DataRoot)) {
        New-Item -ItemType Directory -Path $directory | Out-Null
        Set-DirectoryAccess $directory
    }
    $versionDir = Join-Path $InstallRoot "versions\$($manifest.version)"
    $recoveryDir = Join-Path $InstallRoot 'recovery'
    $configDir = Join-Path $DataRoot 'config'
    $stateDir = Join-Path $DataRoot 'state'
    foreach ($directory in @($versionDir, $recoveryDir, $configDir, $stateDir)) { New-Item -ItemType Directory -Path $directory -Force | Out-Null }
    $exe = Join-Path $versionDir 'kiosentra-agent.exe'
    $recovery = Join-Path $recoveryDir 'kiosentra-recovery.exe'
    Copy-Item -LiteralPath (Join-Path $PackageDirectory 'kiosentra-agent.exe') -Destination $exe
    Copy-Item -LiteralPath $exe -Destination $recovery
    foreach ($copy in @($exe, $recovery)) {
        if ((Get-FileHash -LiteralPath $copy -Algorithm SHA256).Hash -ne $manifest.executableSHA256) { throw 'Installed executable hash mismatch.' }
    }
    $serviceConfig = Join-Path $configDir 'service.json'
    $imagePath = '"' + $exe + '" --mode=service --config="' + $serviceConfig + '"'
    Invoke-SC @('create', 'Kiosentra', 'binPath=', $imagePath, 'start=', 'demand', 'obj=', 'NT SERVICE\Kiosentra', 'DisplayName=', 'Kiosentra Agent')
    $createdService = $true
    Invoke-SC @('sidtype', 'Kiosentra', 'unrestricted')
    $serviceSid = ([Security.Principal.NTAccount]::new('NT SERVICE\Kiosentra')).Translate([Security.Principal.SecurityIdentifier]).Value
    $readers = @{}; $readers[$serviceSid] = $true
    if ($UISid) { $readers[$UISid] = $true }
    Set-DirectoryAccess $InstallRoot $readers
    Set-DirectoryAccess $DataRoot $readers
    Set-DirectoryAccess $configDir @{$serviceSid = $true}
    Set-DirectoryAccess $stateDir @{} @{$serviceSid = $true}
    $ipcKey = New-RandomToken
    $settings = @{
        KIOSENTRA_SERVER_URL = $ServerURL.GetLeftPart([UriPartial]::Authority)
        KIOSENTRA_STATE_DIR = $stateDir
        KIOSENTRA_DEVELOPMENT = ([bool]$Development).ToString().ToLowerInvariant()
        KIOSENTRA_IPC_KEY = $ipcKey
        KIOSENTRA_RECOVERY_EXECUTABLE = $recovery
    }
    if ($UISid) { $settings.KIOSENTRA_UI_SID = $UISid }
    if ($TechniciansFile) {
        $destination = Join-Path $configDir 'technicians.json'
        # Recreate under protected parent instead of preserving source ACLs.
        [IO.File]::WriteAllText($destination, [IO.File]::ReadAllText($TechniciansFile), [Text.UTF8Encoding]::new($false))
        $settings.KIOSENTRA_TECHNICIANS_FILE = $destination
    }
    if ($SimulationPublicKey) {
        $settings.KIOSENTRA_SIMULATED_UPDATES = 'true'
        $settings.KIOSENTRA_SIMULATION_PUBLIC_KEY = $SimulationPublicKey
    }
    $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($EnrollmentToken)
    try { $settings.KIOSENTRA_ENROLLMENT_TOKEN = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr) }
    Write-JSON $serviceConfig $settings
    $settings.Remove('KIOSENTRA_ENROLLMENT_TOKEN')
    & $exe --mode=validate-config --config=$serviceConfig | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Agent rejected the provisioned configuration.' }
    if ($UISid) {
        $clientDir = Join-Path $DataRoot 'console'
        New-Item -ItemType Directory -Path $clientDir | Out-Null
        Set-DirectoryAccess $clientDir @{$UISid = $true}
        Write-JSON (Join-Path $clientDir 'client.json') @{
            KIOSENTRA_STATE_DIR = $stateDir; KIOSENTRA_IPC_KEY = $ipcKey
            KIOSENTRA_DEVELOPMENT = ([bool]$Development).ToString().ToLowerInvariant()
        }
    }
    if ($SimulationPublicKey) {
        $recoveryConfig = Join-Path $configDir 'recovery.json'
        Write-JSON $recoveryConfig @{
            KIOSENTRA_STATE_DIR = $stateDir; KIOSENTRA_DEVELOPMENT = 'true'
            KIOSENTRA_SIMULATED_UPDATES = 'true'; KIOSENTRA_SIMULATION_PUBLIC_KEY = $SimulationPublicKey
        }
        $action = New-ScheduledTaskAction -Execute $recovery -Argument ('--mode=updater --config="' + $recoveryConfig + '"')
        $trigger = New-ScheduledTaskTrigger -AtStartup
        $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
        $taskSettings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Minutes 2) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew -StartWhenAvailable
        Register-ScheduledTask -TaskName 'KiosentraRecovery' -Action $action -Trigger $trigger -Principal $principal -Settings $taskSettings | Out-Null
        $createdTask = $true
    }
    Invoke-SC @('failure', 'Kiosentra', 'reset=', '86400', 'actions=', 'restart/5000/restart/30000/restart/60000')
    Invoke-SC @('failureflag', 'Kiosentra', '1')
    Invoke-SC @('config', 'Kiosentra', 'start=', 'delayed-auto')
    Write-JSON (Join-Path $configDir 'installation.json') @{
        schema = 1; version = $manifest.version; executable = $exe; recoveryExecutable = $recovery
        serviceSid = $serviceSid; uiSid = $UISid; dataRoot = $DataRoot; installRoot = $InstallRoot
        development = [bool]$Development; signerThumbprint = $SignerThumbprint
        sha256 = $manifest.executableSHA256; simulationRecovery = [bool]$SimulationPublicKey
    }
    Start-Service Kiosentra
    Write-Output 'Installed. Complete enrollment with Complete-Provisioning.ps1 to remove the bootstrap token.'
} catch {
    if ($createdTask) { Disable-ScheduledTask -TaskName KiosentraRecovery | Out-Null }
    if ($createdService) {
        Stop-Service Kiosentra -ErrorAction SilentlyContinue
        Invoke-SC @('config', 'Kiosentra', 'start=', 'disabled')
    }
    # Preserve protected files for diagnosis; no recursive deletion or rollback of existing state.
    throw
}

[CmdletBinding()]
param(
    [string]$Executable,
    [string]$ConfigFile,
    [switch]$Remove
)
$ErrorActionPreference = 'Stop'
$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
if ($Remove) {
    Remove-ItemProperty -Path $runKey -Name 'KiosentraConsole' -ErrorAction SilentlyContinue
    Write-Output 'Kiosentra console login entry removed.'
    return
}
if (-not $Executable) { throw 'Supply -Executable with the desktop agent executable path.' }
$resolved = (Resolve-Path -LiteralPath $Executable).Path
if (-not (Test-Path -LiteralPath $resolved -PathType Leaf) -or $resolved.Contains('"')) {
    throw 'Expected a local executable file path without quote characters.'
}
if ([IO.Path]::GetExtension($resolved) -ne '.exe' -or $resolved.StartsWith('\\')) {
    throw 'Expected a local .exe file.'
}
$arguments = ' --mode=ui'
if ($ConfigFile) {
    $config = (Resolve-Path -LiteralPath $ConfigFile).Path
    if ($config.Contains('"') -or $config.StartsWith('\\')) { throw 'Expected a local configuration path without quotes.' }
    $arguments += ' --config="' + $config + '"'
}
New-Item -Path $runKey -Force | Out-Null
Set-ItemProperty -Path $runKey -Name 'KiosentraConsole' -Value ('"' + $resolved + '"' + $arguments)
Write-Output 'Kiosentra console will start hidden at the next login for this user. The service is unchanged.'

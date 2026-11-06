[CmdletBinding()]
param(
    [string]$Executable,
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
New-Item -Path $runKey -Force | Out-Null
Set-ItemProperty -Path $runKey -Name 'KiosentraConsole' -Value ('"' + $resolved + '" --mode=ui')
Write-Output 'Kiosentra console will start hidden at this user’s next login. The service is unchanged.'

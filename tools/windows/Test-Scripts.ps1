# Cross-platform parser check. Windows runtime acceptance is Test-Provisioning.ps1.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$failed = $false
Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.ps1' | ForEach-Object {
    $tokens = $null; $errors = $null
    [Management.Automation.Language.Parser]::ParseFile($_.FullName, [ref]$tokens, [ref]$errors) | Out-Null
    if ($errors) { Write-Output $_.Name; $errors | Write-Output; $failed = $true }
}
if ($failed) { throw 'PowerShell syntax validation failed.' }
Write-Output 'PASS: PowerShell parser accepted all Windows scripts.'

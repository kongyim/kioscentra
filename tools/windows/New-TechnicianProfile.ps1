[CmdletBinding()]
param(
    [Parameter(Mandatory)][string[]]$TechnicianIds,
    [ValidateSet('refresh_inventory','stop_service','cancel_deployment')][string[]]$Scopes = @('refresh_inventory'),
    [Parameter(Mandatory)][string]$OutputDirectory
)
. "$PSScriptRoot\Common.ps1"
Assert-Administrator
$OutputDirectory = Assert-LocalPath $OutputDirectory
if (Test-Path -LiteralPath $OutputDirectory) { throw 'Use a fresh directory for issued technician tokens.' }
if ($TechnicianIds.Count -eq 0 -or $TechnicianIds.Count -gt 100 -or $Scopes.Count -eq 0) { throw 'Supply 1–100 technician IDs and at least one scope.' }
$profiles = @{}; $issued = @{}
foreach ($id in $TechnicianIds) {
    if ($id -cnotmatch '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$' -or $profiles.ContainsKey($id)) { throw 'Invalid or duplicate technician ID.' }
    $secret = New-RandomToken
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $hash = $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($secret)) } finally { $sha.Dispose() }
    $profiles[$id] = @{sha256 = ([BitConverter]::ToString($hash)).Replace('-', '').ToLowerInvariant(); scopes = $Scopes}
    $issued[$id] = $secret
}
New-Item -ItemType Directory -Path $OutputDirectory | Out-Null
Set-DirectoryAccess $OutputDirectory
Write-JSON (Join-Path $OutputDirectory 'technicians.json') $profiles
Write-JSON (Join-Path $OutputDirectory 'issued-tokens.json') $issued
Write-Output 'Profiles and issued tokens saved in the protected output directory. Deliver each token privately; never distribute the whole token file to technicians.'

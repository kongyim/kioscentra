[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Executable,
    [Parameter(Mandatory)][ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$')][string]$Version,
    [Parameter(Mandatory)][string]$OutputDirectory,
    [string]$CertificateThumbprint,
    [string]$TimestampServer = 'https://timestamp.digicert.com',
    [switch]$Development
)
. "$PSScriptRoot\Common.ps1"
$Executable = Assert-LocalPath $Executable
$OutputDirectory = Assert-LocalPath $OutputDirectory
if (Test-Path -LiteralPath $OutputDirectory) { throw 'Output directory must not already exist.' }
$certificate = $null
if (-not $Development) {
    if ($CertificateThumbprint -notmatch '^[a-fA-F0-9]{40}$') { throw 'Supply the code-signing certificate thumbprint or explicit -Development.' }
    $certificate = Get-Item -LiteralPath "Cert:\CurrentUser\My\$CertificateThumbprint"
    if (-not $certificate.HasPrivateKey) { throw 'Code-signing private key unavailable.' }
    if (([uri]$TimestampServer).Scheme -ne 'https') { throw 'Use an HTTPS timestamp server.' }
}
New-Item -ItemType Directory -Path $OutputDirectory | Out-Null
Copy-Item -LiteralPath $Executable -Destination (Join-Path $OutputDirectory 'kiosentra-agent.exe')
Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.ps1' | Copy-Item -Destination $OutputDirectory
if ($certificate) {
    Get-ChildItem -LiteralPath $OutputDirectory -File | ForEach-Object {
        $result = Set-AuthenticodeSignature -FilePath $_.FullName -Certificate $certificate -HashAlgorithm SHA256 -TimestampServer $TimestampServer
        if ($result.Status -ne 'Valid') { throw "Signing failed: $($_.Name)" }
    }
}
Write-JSON (Join-Path $OutputDirectory 'manifest.json') @{
    schema = 1; version = $Version; development = [bool]$Development
    executableSHA256 = (Get-FileHash -LiteralPath (Join-Path $OutputDirectory 'kiosentra-agent.exe') -Algorithm SHA256).Hash
}
$catalog = Join-Path $OutputDirectory 'package.cat'
New-FileCatalog -Path $OutputDirectory -CatalogFilePath $catalog -CatalogVersion 2.0 | Out-Null
if ($certificate) {
    $result = Set-AuthenticodeSignature -FilePath $catalog -Certificate $certificate -HashAlgorithm SHA256 -TimestampServer $TimestampServer
    if ($result.Status -ne 'Valid') { throw 'Catalog signing failed.' }
}
Assert-Package $OutputDirectory $CertificateThumbprint -Development:$Development | Out-Null
Write-Output "Package prepared: $OutputDirectory"

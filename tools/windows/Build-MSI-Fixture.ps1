[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$OutputDirectory)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not [IO.Path]::IsPathRooted($OutputDirectory)) { throw 'Output directory must be absolute.' }
if (Test-Path -LiteralPath $OutputDirectory) { throw 'Use a fresh output directory.' }
$wix = Get-Command wix -ErrorAction Stop
$productCode = '{' + [guid]::NewGuid().ToString().ToUpperInvariant() + '}'
$directory = New-Item -ItemType Directory -Path $OutputDirectory
$package = Join-Path $directory.FullName 'kiosentra-test-fixture.msi'
$source = Join-Path $PSScriptRoot 'msi-fixture\fixture.wxs'
& $wix.Source build $source -arch x64 -d "ProductCode=$productCode" -o $package
if ($LASTEXITCODE -ne 0) { throw 'WiX fixture build failed; retained output for diagnosis.' }
$metadata = [ordered]@{
    productCode = $productCode
    productVersion = '1.0.0'
    package = $package
    sizeBytes = (Get-Item -LiteralPath $package).Length
    sha256 = (Get-FileHash -LiteralPath $package -Algorithm SHA256).Hash.ToLowerInvariant()
    installed = $false
}
$metadata | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $directory.FullName 'fixture.json') -Encoding UTF8
$metadata | ConvertTo-Json

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Assert-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Run in an elevated Windows PowerShell session.'
    }
}

function Assert-LocalPath([string]$Path) {
    if (-not [IO.Path]::IsPathRooted($Path) -or $Path.StartsWith('\\') -or $Path.Contains('"') -or $Path.Contains("`n") -or $Path.Contains("`r")) {
        throw 'An absolute local path without quotes or newlines is required.'
    }
    $full = [IO.Path]::GetFullPath($Path)
    $item = $full
    while ($item) {
        if (Test-Path -LiteralPath $item) {
            if ((Get-Item -LiteralPath $item -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw "Reparse points are not allowed: $item"
            }
        }
        $parent = Split-Path -Parent $item
        if ($parent -eq $item) { break }
        $item = $parent
    }
    return $full
}

function Invoke-SC([string[]]$Arguments) {
    & "$env:SystemRoot\System32\sc.exe" @Arguments | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "SC failed ($LASTEXITCODE): $($Arguments[0])" }
}

function Set-DirectoryAccess([string]$Path, [hashtable]$Readers = @{}, [hashtable]$Writers = @{}) {
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
    $rights = @{'S-1-5-18' = 'FullControl'; 'S-1-5-32-544' = 'FullControl'}
    foreach ($sid in $Readers.Keys) {
        if (-not $rights.ContainsKey($sid)) { $rights[$sid] = 'ReadAndExecute' }
    }
    foreach ($sid in $Writers.Keys) {
        if ($sid -notin @('S-1-5-18', 'S-1-5-32-544')) { $rights[$sid] = 'Modify' }
    }
    foreach ($sid in $rights.Keys) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new($sid), $rights[$sid],
            'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Write-JSON([string]$Path, $Value) {
    # Caller creates and locks the parent before writing secrets; no world-readable staging file.
    $text = ConvertTo-Json -InputObject $Value -Depth 10
    [IO.File]::WriteAllText($Path, $text, [Text.UTF8Encoding]::new($false))
}

function New-RandomToken {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return [Convert]::ToBase64String($bytes)
}

function Get-AgentStatus([string]$Executable, [string]$ConfigFile) {
    $Executable = Assert-LocalPath $Executable
    $ConfigFile = Assert-LocalPath $ConfigFile
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $Executable
    $info.Arguments = '--mode=status --config="' + $ConfigFile + '"'
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $info
    try {
        $null = $process.Start()
        $output = $process.StandardOutput.ReadToEndAsync()
        $errors = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(10000)) { $process.Kill(); $process.WaitForExit(); return $null }
        if ($process.ExitCode -ne 0) { return $null }
        return ($output.GetAwaiter().GetResult() | ConvertFrom-Json)
    } finally { $process.Dispose() }
}

function Assert-Package([string]$Directory, [string]$SignerThumbprint, [switch]$Development) {
    $Directory = Assert-LocalPath $Directory
    $catalog = Join-Path $Directory 'package.cat'
    if (-not $Development) {
        if ($SignerThumbprint -notmatch '^[a-fA-F0-9]{40}$') { throw 'Pin the approved signing certificate thumbprint.' }
        $signature = Get-AuthenticodeSignature -LiteralPath $catalog
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $SignerThumbprint) {
            throw 'Package catalog is unsigned, untrusted or signed by a different publisher.'
        }
    }
    if ((Test-FileCatalog -Path $Directory -CatalogFilePath $catalog -FilesToSkip 'package.cat') -ne 'Valid') {
        throw 'Package contents do not match the catalog.'
    }
    $manifest = Get-Content -LiteralPath (Join-Path $Directory 'manifest.json') -Raw | ConvertFrom-Json
    if ($manifest.schema -ne 1 -or $manifest.version -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$') { throw 'Invalid package manifest.' }
    if (-not $Development -and $manifest.development) { throw 'Development package requires explicit -Development.' }
    return $manifest
}

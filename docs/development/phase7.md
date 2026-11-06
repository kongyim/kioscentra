# Phase 7 — production-hardening tools and acceptance gates

Status (6 November 2026): tooling implemented and locally validated on macOS. The user-approved
scope for this milestone is tools plus local validation because no Windows test environment or
code-signing certificate is available. **Production signing and Windows native acceptance remain
open. This is not a declaration that the endpoint manager is production-ready.**

## Delivered scope

| Area | Implementation | Acceptance boundary |
| --- | --- | --- |
| Protected provisioning | Explicit JSON configuration; isolated service, console and recovery settings; bounded parsing; allowlisted settings | Go tests and real-binary smoke passed; Windows ACL runtime pending |
| Technician identity | Named, independently generated tokens with allowed action scopes; single-use grants; actor in audit messages | Go and UI tests passed; local identities, not central SSO |
| Packaging/signing | Windows SHA-256 catalog, signed executable/scripts/catalog, pinned signer verification; explicit unsigned development mode | PowerShell parsed; actual catalog/signing runtime pending |
| Installation | Fresh version directory, virtual service account, private state/config ACLs, delayed auto-start and SCM crash recovery | Windows provisioning test supplied; not run here |
| Bootstrap cleanup | Confirm enrollment over IPC, atomically remove bootstrap token, restart to clear old process memory | Script supplied; Windows runtime pending |
| Independent recovery | Protected separate executable, secret-free recovery settings and startup task for opted-in update simulations | Separate executable exercised locally; Windows task/reboot pending |
| Uninstall | Validate ownership, stop/unregister service and task, retain binaries/config/state | Windows runtime pending |
| Verification | Config validation/status CLI; PowerShell parser; installation checks; disposable-host provisioning/crash test; CI wiring | Local checks recorded in [validation.md](validation.md#phase-7-validation) |

Real MSI/EXE/ZIP deployment, real binary switching, a central identity authority, automatic credential
rotation and a production backend are not enabled by this tooling. The recovery task operates only
the existing simulation journal. Its installation does not create a real updater or a repair path
for a corrupt production executable.

## Protected configuration

The existing environment-only development workflow still works. Use an absolute `--config` path
(or `KIOSENTRA_CONFIG_FILE`) to select a JSON object of string settings:

```json
{
  "KIOSENTRA_SERVER_URL": "https://central.example",
  "KIOSENTRA_STATE_DIR": "C:\\ProgramData\\Kiosentra\\state",
  "KIOSENTRA_IPC_KEY": "<base64 encoding of 32 random bytes>",
  "KIOSENTRA_TECHNICIANS_FILE": "C:\\ProgramData\\Kiosentra\\config\\technicians.json",
  "KIOSENTRA_RECOVERY_EXECUTABLE": "C:\\Program Files\\Kiosentra\\recovery\\kiosentra-recovery.exe"
}
```

File mode is authoritative: omitted settings do not inherit environment values. This prevents
an inherited development flag, enrollment token or server URL from silently altering an installed
service. Files must be regular, at most 64 KiB, and use known setting names. Unix development files
must have owner-only permissions (for example `chmod 600`). On Windows, provisioning establishes
ACLs; `Test-Installation.ps1` inspects them. Do not give untrusted users write access to settings,
their parent paths, installation records or executors.

The installer makes these separate files:

- `config/service.json`: service settings, IPC key and temporary bootstrap token. Administrators
  and SYSTEM control it; the service can read it. The service cannot modify its own executable/config.
- `config/technicians.json`: token hashes and allowed scopes; no plaintext technician tokens.
- `console/client.json`: state-directory identifier and IPC key only. The configured UI SID can read
  this file but receives no service database access, enrollment token or technician token hashes.
- `config/recovery.json`: simulation opt-in, public trust key and state path only; created only when
  development update recovery is explicitly requested. No service/IPC/enrollment credentials.
- `config/installation.json`: version, paths, service SID, expected executable hash and signer.

`--mode=validate-config --config=<path>` validates configuration without opening a service database
or binding IPC. `--mode=status --config=<path>` requests the existing read-only IPC projection and
returns JSON including enrolled device ID, expiry and whether named maintenance is configured. It
never returns bearer tokens, private identity keys, technician hashes or full update job metadata.

## Package and install on Windows

Use Windows PowerShell 5.1+ or PowerShell 7 on a dedicated Windows machine. Build the desktop
variant if UI/tray is required; otherwise use the headless binary. The GUI build still supports
service/updater modes. All administrative scripts require elevation except user login registration.

```powershell
go build -o bin\kiosentra-agent.exe ./apps/agent/cmd/kiosentra-agent

# Release packaging requires an approved code-signing certificate with its private key
# in Cert:\CurrentUser\My and a valid timestamp response.
.\tools\windows\Build-Package.ps1 `
  -Executable "$pwd\bin\kiosentra-agent.exe" -Version '0.1.0' `
  -OutputDirectory 'C:\KiosentraPackages\0.1.0' `
  -CertificateThumbprint '<approved certificate thumbprint>'

# Disposable development package only; never treat it as a signed release.
.\tools\windows\Build-Package.ps1 `
  -Executable "$pwd\bin\kiosentra-agent.exe" -Version 'dev-test' `
  -OutputDirectory 'C:\KiosentraPackages\dev-test' -Development
```

The package is a directory containing the executable, PowerShell tools, manifest and SHA-256
catalog. Keep the directory intact. Production installation checks the catalog's trusted signature,
the explicitly pinned certificate thumbprint and catalog contents before copying files. Development
mode still checks catalog contents. Script execution policy is not a trust boundary: establish
trust in the installer/Common scripts from your reviewed checkout or verify their Authenticode
signatures before executing them. Do not run an untrusted installer merely to ask it to verify itself.

Generate independent random technician tokens in an admin-only directory:

```powershell
.\tools\windows\New-TechnicianProfile.ps1 -TechnicianIds 'tech-alice','tech-bob' `
  -Scopes refresh_inventory -OutputDirectory 'C:\KiosentraSecrets\initial-issuance'
```

Deliver each technician only their own token through an approved private channel. `issued-tokens.json`
is an administrative issuance artifact; do not copy it into a package, repository or UI directory.
`technicians.json` contains hashes and scopes. Use separately issued profiles or edit the reviewed
hash/scopes file for distinct roles. Only `refresh_inventory`, `stop_service` and `cancel_deployment`
are valid. Tokens must be random secrets, not human passwords. SHA-256 here is not a password KDF.

```powershell
$bootstrap = Read-Host 'Short-lived enrollment token' -AsSecureString
.\tools\windows\Install-Agent.ps1 `
  -PackageDirectory 'C:\KiosentraPackages\0.1.0' `
  -SignerThumbprint '<approved certificate thumbprint>' `
  -ServerURL 'https://central.example' -EnrollmentToken $bootstrap `
  -TechniciansFile 'C:\KiosentraSecrets\initial-issuance\technicians.json' `
  -UISid '<dedicated local console user/group SID>'
.\tools\windows\Complete-Provisioning.ps1
.\tools\windows\Test-Installation.ps1 -ExerciseRestart
```

The default service account is `NT SERVICE\Kiosentra`, not LocalSystem. State belongs to that account
and is DPAPI-protected. Do not reuse another account's state. The installer refuses an existing service,
recovery task or destination directory; it never updates a running executable. On an installation error,
it disables a newly created service/task and retains protected files for diagnosis. It is not a transactional
MSI installer and does not silently erase partial installation state.

`Complete-Provisioning.ps1` waits for a valid enrolled identity over authenticated IPC before removing
the bootstrap token. If central is unavailable, it fails with the token retained under the private ACL
so the operator can retry. Never use command-line arguments or unprotected environment files to carry
tokens. The script accepts a `SecureString`; the service config necessarily holds the bootstrap token
as protected plaintext until enrollment finishes. Long-term device credentials remain DPAPI-encrypted.

For the optional UI, run the following **as the intended console user**, using a desktop build:

```powershell
.\tools\windows\register-ui-login.ps1 `
  -Executable 'C:\Program Files\Kiosentra\versions\0.1.0\kiosentra-agent.exe' `
  -ConfigFile 'C:\ProgramData\Kiosentra\console\client.json'
```

Maintenance uses technician ID plus individual token. Token hashes/scopes are loaded at service startup;
remove a profile or replace its hash and restart to revoke that identity and all in-memory grants. Do not
restore a revoked profile from an old backup. Shared `KIOSENTRA_MAINTENANCE_SHA256` is development-only.
Local administrator/SYSTEM access remains trusted. Per-request auditing records the known actor and scope,
never the token; local bounded logs are not tamper-evident or centrally retained. Auth throttling is global
to the service, so repeated failed attempts can temporarily deny maintenance to other technicians.

## Independent recovery and uninstall

The service has delayed automatic start and SCM failure restart delays of 5, 30 and 60 seconds. An intentional
successful stop is distinct from a service failure. Native Windows acceptance must confirm both behaviors.

The installer always places a separate, read-only-to-service copy at `recovery/kiosentra-recovery.exe`.
For simulation acceptance, pass `-Development -SimulationPublicKey <base64-public-key>` to install a
`KiosentraRecovery` startup task under SYSTEM. It runs the separate updater with its secret-free config,
has a two-minute execution limit and ignores overlapping task instances; the helper also uses its journal
lock. No task is created for non-simulation installations. Both executors remain unchanged during simulations.
Keep the protected copy available; real updater replacement and fallback handoff are still separate work.

```powershell
.\tools\windows\Uninstall-Agent.ps1
```

Uninstall checks the recorded service/task paths, unregisters them and retains all state/config/binaries.
Remove per-user login entries with `register-ui-login.ps1 -Remove` in each affected user's session.
Retirement, server-side credential revocation, WAL-aware backups and explicit protected-data deletion are
operator responsibilities; this command does not remove other users' profile settings or silently erase keys.

## Repeatable verification and remaining gates

Local automated checks:

```sh
sh scripts/check.sh
python3 scripts/phase6-smoke.py --protected-settings
pwsh -NoProfile -File tools/windows/Test-Scripts.ps1
# Also run frontend tests/build and desktop vet/build as in Phase 4.
```

On a disposable elevated Windows host without a Kiosentra installation:

```powershell
go build -o bin\mock-server.exe ./tools/mock-server/cmd/mock-server
go build -o bin\simulation-keygen.exe ./tools/mock-server/cmd/simulation-keygen
.\tools\windows\Test-Provisioning.ps1 `
  -PackageDirectory 'C:\KiosentraPackages\dev-test' `
  -MockExecutable "$pwd\bin\mock-server.exe" `
  -KeygenExecutable "$pwd\bin\simulation-keygen.exe" `
  -OutputDirectory 'C:\KiosentraAcceptance\run-001'
```

This test rejects modified packages and unsigned production admission, installs against its own loopback mock,
confirms enrollment/token cleanup, inspects ACLs, tests service restart and DPAPI identity persistence, kills
the **test service** to exercise SCM recovery, dispatches the independent scheduled task while the service is
stopped, then unregisters its installation. It preserves protected evidence and is wired into Windows CI.
It does not reboot the machine and does not exercise a real installer/update. CI has not been run here.

Before production acceptance, complete and record:

- Approved certificate, publisher/timestamp trust and signed-package install; reject a wrong signer and
  any modified executable/script/manifest. Re-sign and rebuild catalogs for every release.
- Windows native provisioning test, negative ACL checks as an ordinary user, no-login service operation,
  wrong-account DPAPI rejection, and intentional stop versus crash recovery.
- Actual reboot during each simulated checkpoint with the separate helper and startup task; service/central
  unavailable recovery, stale helper fencing, damaged candidate and failed recovery diagnostics.
- Interactive desktop checklist: WebView2, tray/hotkey, login, Session 0 isolation and authorized stop.
- Independent security review and operational decisions for central identity/credential lifecycle,
  certificate rotation, updater replacement, long-term state retention and audit export.

Implementation references: [Microsoft SC configuration](https://learn.microsoft.com/en-us/windows/win32/services/configuring-a-service-using-sc),
[SHA-256 file catalogs](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.security/new-filecatalog),
and [scheduled-task principals](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtaskprincipal).

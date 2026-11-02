# Windows Service setup and validation

The SCM adapter is implemented; installer packaging and unattended provisioning are Phase 7.
Build from the root in PowerShell with Go installed:

```powershell
go build -o bin\kiosentra-agent.exe ./apps/agent/cmd/kiosentra-agent
.\bin\kiosentra-agent.exe --version
```

For interactive development, set the same variables as the README using `$env:NAME = 'value'`,
point at the local mock and use `--mode=console`. Windows uses DPAPI under your current account.

## Manual SCM test (elevated PowerShell, dedicated test machine)

Copy the binary to `C:\Program Files\Kiosentra\kiosentra-agent.exe`. Create a **separate** state
directory for the LocalSystem service; do not reuse identity encrypted under your login account.
Restrict its ACL before enrollment:

```powershell
New-Item -ItemType Directory -Force 'C:\ProgramData\Kiosentra'
icacls 'C:\ProgramData\Kiosentra' /inheritance:r
icacls 'C:\ProgramData\Kiosentra' /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'
sc.exe create Kiosentra binPath= '"C:\Program Files\Kiosentra\kiosentra-agent.exe" --mode=service' start= auto
```

The default account here is LocalSystem for initial testing. Production must select the least
privileged service account consistent with approved operations and grant that account access.
Provision service environment explicitly; SCM does not inherit your interactive shell variables:

```powershell
# $enrollmentToken must come from your authorized provisioning workflow.
New-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\Kiosentra' `
  -Name Environment -PropertyType MultiString -Value @(
    'KIOSENTRA_SERVER_URL=http://127.0.0.1:8080',
    'KIOSENTRA_DEVELOPMENT=true',
    'KIOSENTRA_STATE_DIR=C:\ProgramData\Kiosentra',
    "KIOSENTRA_ENROLLMENT_TOKEN=$enrollmentToken"
  ) -Force
sc.exe start Kiosentra
sc.exe query Kiosentra
Get-Content 'C:\ProgramData\Kiosentra\agent.log' -Tail 30
sc.exe stop Kiosentra
```

The bootstrap token is temporarily present in this privileged service configuration; remove
its Environment entry after successful enrollment and restart the service. Device credentials
remain DPAPI-protected in SQLite. A production provisioner needs short-lived tokens and secure
secret injection, rather than retaining reusable enrollment secrets in registry configuration.
Replace loopback HTTP and development mode with verified HTTPS for a production server.
No Windows shell/startup remediation is performed by the agent.

Unregister after the test using `sc.exe delete Kiosentra` while stopped. This intentionally
leaves state for inspection; remove it only under your device retirement/re-enrollment procedure.

## Required native validation

Run `go test ./apps/agent/... ./packages/contracts/... ./tools/mock-server/...` on Windows.
Run the service with no logged-in kiosk user; verify reporting, stop/start and state recovery.
Terminate its process during an outage, restart it and verify queued IDs replay without duplicate
reports. Verify DPAPI decryption under the same account and failure under a different account.
Verify the state ACL, second-instance rejection, log rotation, graceful stop and reboot recovery.
SCM Session 0 must never create a tray icon or interactive window. Cross-compilation alone does
not validate any of these native behaviors. Self-update/replacement is not available.

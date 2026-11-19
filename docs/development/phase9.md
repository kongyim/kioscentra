# Phase 9 — real MSI deployment

**Status: 9A executor core and fixture sources implemented; Phase 9 as a whole is
not complete.** The user selected MSI test fixtures and deferred native acceptance
until a Windows environment is available. There is no automatic real installation
from the central dashboard or Agent service yet.

## 9A: explicit Windows test executor

`kiosentra-install-msi` is a standalone, elevated operator command. It uses Phase 8
HTTPS staging, rechecks the staged bytes, reads MSI product metadata, persists execution
intent before launch, uses fixed quiet/no-reboot arguments and checks the installed
machine product version after a successful exit. It rejects a different installed
version of the same product rather than implementing an untested upgrade strategy.

The signed authorization must include this top-level field in addition to Phase 8 data:

```json
"msi": {
  "productCode": "{ACTUAL-PRODUCT-GUID-FROM-FIXTURE}",
  "productVersion": "1.0.0"
}
```

The version must match `release.version`. Changing product code or version invalidates
the signature. Only self-contained embedded MSI cabinets are admitted by this adapter.
The MSI can contain installation logic; approve the actual package before signing.

The journal is a separate SQLite database with full synchronous writes and a process
lock. Reuse **one protected journal directory for all jobs on a machine**, including
after reboot. The directory and its parents must be protected from ordinary-user writes;
Windows ACL enforcement must be validated during native acceptance.

| Result | Behavior |
| --- | --- |
| `succeeded` | Product version confirmed, or already present; repeating the same recorded job does not install again |
| `failed` | Preflight/staging failure; installer was not launched |
| `installing` | Intent persisted before launch; an interrupted process may have changed the machine |
| `needs_operator` | Unknown/nonzero installer result, failed post-validation or recovered `installing`; blocks subsequent jobs |
| `reboot_required` | Exit 3010; blocks subsequent jobs pending post-reboot acceptance |

Killing the `msiexec` client does not prove its service transaction stopped. Timeout,
cancellation or process loss therefore requires operator reconciliation, not automatic
retry or a claimed rollback. The current CLI deliberately has no command to erase this
fence. Do not delete or switch journals to bypass it on a machine with uncertain effects.
The recovery/approval workflow is part of 9B; use disposable VM snapshots for fixture runs.

## Fixture build and native runbook

The fixture source in `tools/windows/msi-fixture` installs one text file in
`ProgramFiles64Folder\KiosentraMSITestFixture`. It has no service or custom action.
On Windows with a WiX toolset supporting the v4 schema and `wix build`:

```powershell
.\tools\windows\Build-MSI-Fixture.ps1 -OutputDirectory 'C:\KiosentraAcceptance\fixture-001'
```

The script creates a new ProductCode and writes `fixture.json` with product version,
SHA-256 and size. It builds but does not install. A package build/native test was not
performed on the macOS development host. Keep one fixture build installed at a time;
restore the disposable VM snapshot before testing a new independently generated product.

Host the MSI on a trusted HTTPS origin, provision the host's TLS trust, construct a
device/job-bound authorization using the generated metadata and sign it with the separate
release key using `kiosentra-sign-artifact`. Do not disable TLS verification for the test.
Provision separate private staging/journal directories with Administrators/SYSTEM-only
write access. From an elevated test console:

```powershell
.\bin\kiosentra-install-msi.exe --execute `
  --authorization='C:\KiosentraAcceptance\signed.json' `
  --device-id='<enrolled UUID>' --key-id='release-test-1' `
  --public-key='<base64 Ed25519 public key>' `
  --origins='https://trusted-test-artifact-host' `
  --staging-directory='C:\KiosentraAcceptance\private-staging' `
  --journal-directory='C:\KiosentraAcceptance\private-journal'
```

`--execute` is mandatory. Non-Windows and non-elevated execution is rejected. The default
overall timeout is 15 minutes (maximum one hour); signature expiry shortens it. Only
`succeeded` produces exit code zero. Inspect durable state without running the installer:

```powershell
.\bin\kiosentra-install-msi.exe --status --job-id='<job UUID>' `
  --journal-directory='C:\KiosentraAcceptance\private-journal'
```

## 9B: required before central dispatch

- A separately provisioned privileged broker: the existing `NT SERVICE\Kiosentra`
  account is not an elevated MSI operator. The broker must independently verify signed
  authorization, trust, device identity, expiry, package bytes, replay and allowed actions.
- Central signed-authorization admission, immutable device/job binding, lease renewal,
  cancellation at safe boundaries and persistent result forwarding to admin history.
- UI controls that distinguish real execution, simulation, uncertain outcomes and
  requested versus confirmed cancellation; explicit operator recovery with attribution.
- Native executable/file-handle ACL checks and installer containment; post-reboot
  reconciliation for 3010, interrupted installations and recovery approvals.
- Package-specific restoration and application health checks before advertising rollback
  or successful kiosk operation. The current post-check verifies MSI registration only.

## Evidence and remaining tests

Host tests use a deterministic adapter with inert bytes, not a claimed executable MSI.
They cover journal reopen, no repeated execution, crash after launch, blocked subsequent
work, different authorization under the same job ID, package mismatch, existing versions,
changed bytes, exit 3010/1603/1641, interruption and failed post-validation.
Windows builds/vet check the native adapter compiles. The real WiX fixture exists to
exercise that adapter later; native execution, fixture build and reboot tests remain open.

Native implementation follows Microsoft's [read-only MSI database API](https://learn.microsoft.com/en-us/windows/win32/api/msiquery/nf-msiquery-msiopendatabasew)
and [machine-context product information API](https://learn.microsoft.com/en-us/windows/win32/api/msi/nf-msi-msigetproductinfoexw).
Fixture build uses the [WiX CLI](https://docs.firegiant.com/wix/tools/wixexe/)
and [embedded cabinet configuration](https://docs.firegiant.com/wix/schema/wxs/mediatemplate/).

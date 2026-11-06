# Phase 4 — local desktop console

Phase 4 adds a Wails v3.1.0 desktop build of the same executable, with a bundled
React/TypeScript/Vite/MUI frontend. The background service remains independent.
The ordinary Go build remains headless for servers and CI; build with `-tags desktop`
to include `--mode=ui`. Node is a build dependency, never a distributed runtime.

## Build

Use Go as documented in the root README and Node 24.11 or newer supported Node 24/26.
From the repository root:

```sh
npm --prefix apps/agent/frontend ci --ignore-scripts
npm --prefix apps/agent/frontend run build
npm --prefix apps/agent/frontend test
go build -tags desktop -o bin/kiosentra-agent ./apps/agent/cmd/kiosentra-agent
```

For a Windows amd64 executable (including cross-builds):

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags desktop -ldflags="-H=windowsgui" -o bin/kiosentra-agent.exe ./apps/agent/cmd/kiosentra-agent
```

On Windows PowerShell set those environment variables separately, or build natively with
`go build -tags desktop -ldflags="-H=windowsgui" -o bin/kiosentra-agent.exe ./apps/agent/cmd/kiosentra-agent`.
The Windows GUI-subsystem flag prevents a console window at login. The Windows UI requires WebView2. Build frontend assets before compiling the desktop tag;
`dist` is generated and intentionally not checked in. Headless builds require no frontend.

## Provision local access

Set `KIOSENTRA_IPC_KEY` to the same base64-encoded **32 random bytes** in the service and
UI process environments. Generate a fresh key with `openssl rand -base64 32`. Treat it
as a local viewing credential; never use the central enrollment token or development
storage-encryption key. Do not place it in frontend code, source control or browser storage.
If omitted, the service continues with local IPC disabled. Invalid IPC configuration or a
listener failure is logged and does not stop collection or central synchronization.

Windows named pipes permit LocalSystem, administrators and the service account; add the
intended kiosk user's SID using `KIOSENTRA_UI_SID` **in the service environment**. Find that
SID in the kiosk user's session with `whoami /user`. Reader rights explicitly exclude pipe
instance creation; remote clients are rejected by go-winio. The pipe name is derived from
`KIOSENTRA_STATE_DIR`, which must resolve to the same path for both processes.

Non-Windows console testing uses a mode-0600 Unix socket inside the mode-0700 state directory,
requires `KIOSENTRA_DEVELOPMENT=true`, and supports the same local user. It is a development
harness, not production macOS/Linux service support.

The UI never opens the service database, identity or protected credentials. It requests an
explicit safe storage projection and the last 100 in-memory service log messages; arbitrary
kv keys, log paths, outbox bodies and installer payloads are not exposed. Log attribute values
stay in the underlying service logs.

## Start the console

Start the service/console as in the existing local guide, with IPC provisioned. In another
terminal with matching state directory and IPC key:

```sh
# macOS/Linux development only; use the same environment as the console service.
export KIOSENTRA_DEVELOPMENT=true
# Optional for the first run; omit for hidden-at-login behavior.
export KIOSENTRA_UI_SHOW=true
./bin/kiosentra-agent --mode=ui
```

The window starts hidden by default. Tray activation shows/focuses it; closing hides it.
The independent global shortcut defaults to `Ctrl+Alt+Shift+F12`; override it with
`KIOSENTRA_UI_HOTKEY`. A shortcut registration error is recorded, and the console is shown
so it remains accessible. The native error is available through the local UI bridge even while the service is disconnected. Exiting the
console from its tray menu leaves the service running. Service reconnect attempts occur
every three seconds after a completed request; request deadlines are five seconds. A failed
connection preserves the last received data with an explicit stale-data warning.

To start hidden at Windows user login, run in the intended kiosk user's session after
provisioning that user's persistent environment:

```powershell
.\tools\windows\register-ui-login.ps1 -Executable 'C:\Program Files\Kiosentra\kiosentra-agent.exe'
# Reversible removal:
.\tools\windows\register-ui-login.ps1 -Remove
```

This sets only the current user's Run entry. It is not an installer and does not configure
SCM, machine environment variables, ACLs or Session 0. The service never launches the UI.

## Pages and maintenance

Overview, Device, Applications, Deployments, Configuration, Logs, Maintenance and About
consume service data. Missing readings are marked unavailable/unknown; partial inventory
and simulation-only deployment behavior are explicit. Configuration is read-only. Theme
preference is the only browser-local stored value; dark is the default.

Maintenance is disabled until the service receives `KIOSENTRA_MAINTENANCE_SHA256`: the
64-character hex SHA-256 of a separate, high-entropy technician secret. Generate that secret
with a cryptographic random generator (for example 32 random bytes encoded as base64), not
a human password. Compute the digest of the exact UTF-8 text without a trailing newline.
Only provision the digest to the service; keep the original with the technician. There is
no default credential, central identity integration or password-recovery flow in this phase.

The service checks the secret with constant-time digest comparison and rate-limits attempts
to one every five seconds. Successful authentication grants one explicitly selected action:

- `refresh_inventory`: schedules a new inventory during the next service cycle.
- `cancel_deployment` (Phase 5): cancels the active simulation at the next service loop boundary.
- `stop_service`: requests graceful shutdown of this service core; the UI requires a separate
  confirmation. It does not shut down Windows, and an administrator must restart the service.

Random grants expire after two minutes, are single-use and are lost on service restart.
They remain only in process/UI memory. Scope mismatches, expired grants and arbitrary actions
are rejected at the service. The acknowledgement means the request was authenticated and
received; the service logs action queue failures and completion. This is a local shared-secret
technician mechanism, not individual technician identity or tamper-evident audit.

## IPC boundaries

The local protocol uses length-limited AES-256-GCM authenticated/encrypted frames with random
nonces. Request and response directions have separate associated data, and each response is
bound to the request nonce. Requests expire after 30 seconds; a bounded nonce cache rejects
replays within that window. There are at most eight concurrent connections, five-second
connection deadlines, 16 KiB requests and 4 MiB responses. Cancellation closes listeners and
connections before storage closes. No unauthenticated HTTP endpoint is introduced.

All holders of the IPC key and OS access are trusted local viewers. Rotating it requires
restarting the service and UI. Operating-system administrators remain trusted. Production
credential provisioning, named local technician identity and installer/signing tooling are now
provided by [Phase 7](phase7.md). Production signing, native acceptance and independent security review
remain open. Shared-secret maintenance is now development-only; production mode requires named profiles.

## Validation and remaining native checks

Automated checks cover scoped/expiring/single-use authorization, rate limiting, frame tampering,
wrong-key rejection, direction binding, replay expiry, frame size limits, idle cancellation,
response-before-stop ordering, service reconnection and safe storage projection. Frontend tests
cover unavailable telemetry, disconnected maintenance and theme persistence. All prior agent
regressions remain applicable. CI builds desktop assets and the desktop executable on Windows
and macOS, in addition to the headless matrix.

Windows acceptance still requires a real interactive session: verify WebView2, tray behavior,
close-to-hide, shortcut conflicts, operation without Explorer, reader SID ACLs, Session 0
separation, login startup, restart/reconnect and authorized shutdown. Cross-compilation does
not establish those behaviors. Existing SCM/DPAPI and Phase 3 native checklists still apply.

Implementation references: [Wails global shortcuts](https://docs-preview.wails.io/features/keyboard/global-shortcuts/),
[Wails system tray](https://v3.wails.io/features/menus/systray/), and
[Windows pipe rights](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights).

# Kiosentra

Windows-first kiosk agent foundation. **Phases 1–6 implemented (deployment and update phases are simulation-only)**: a runnable Go agent,
versioned API contract, durable local state, and a development central-server mock.
A desktop-tag build adds the local Wails console. The production server, admin dashboard,
software installation and self-update are **not implemented**.

## Layout

- `apps/agent`: single executable, console and Windows SCM modes; internal service core.
- `packages/contracts`: OpenAPI 3.1 and shared Go wire types, without backend dependencies.
- `tools/mock-server`: isolated, in-memory development server and integration-test handler.
- `apps/agent/frontend`: embedded React/TypeScript/MUI local console (desktop build).
- `apps/server`, `apps/admin`, `packages/ui`: future-phase boundaries.
- `docs`: architecture, operating instructions, API semantics, security and future designs.

A third small Go module for contracts avoids making the mock depend on agent internals.
The existing checkout directory may be named `kioscentra`; the product and module paths
are consistently **Kiosentra / kiosentra**. No existing implementation was present.

## Requirements and verification

Use Go **1.26.10 or newer supported Go 1.26/1.27 patch release**. Dependencies are pinned
in `go.mod` and `go.sum`; the SQLite driver is pure Go, so Windows cross-builds need no C compiler.

```sh
go test ./apps/agent/... ./packages/contracts/... ./tools/mock-server/...
go test -race ./apps/agent/... ./packages/contracts/... ./tools/mock-server/...
go vet ./apps/agent/... ./packages/contracts/... ./tools/mock-server/...
go build -o bin/kiosentra-agent ./apps/agent/cmd/kiosentra-agent
go build -o bin/mock-server ./tools/mock-server/cmd/mock-server
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/kiosentra-agent.exe ./apps/agent/cmd/kiosentra-agent
```

Run those commands from the repository root. `go test ./...` at the workspace root is
not the correct multi-module command. Race tests need a supported host and C toolchain.

## Local development

These copy-and-paste commands are for macOS/Linux development. Run each terminal from the
repository root. They use matching, hardcoded **local-development-only** credentials.
Stop any previously running mock server and agent before switching to this quick start.

### Terminal 1 — start the mock server

```sh
# Local development examples. Recommended: change these for your own environment.
# If you change the enrollment token, use the same value in terminal 2.
export KIOSENTRA_MOCK_ENROLLMENT_TOKEN='kiosentra-local-enrollment-change-me'
# Keep the admin token different from the enrollment token; also update terminal 3.
export KIOSENTRA_MOCK_ADMIN_TOKEN='kiosentra-local-admin-change-me'

go run ./tools/mock-server/cmd/mock-server
```

Leave this server running while starting the agent. The mock binds to loopback only.

### Terminal 2 — start the agent with fresh development state

```sh
export KIOSENTRA_SERVER_URL=http://127.0.0.1:8080
export KIOSENTRA_DEVELOPMENT=true
# Must match KIOSENTRA_MOCK_ENROLLMENT_TOKEN in terminal 1.
# Recommended: change both copies together for your own environment.
export KIOSENTRA_ENROLLMENT_TOKEN='kiosentra-local-enrollment-change-me'

# Public example key for local development only: base64 of exactly 32 bytes.
# Recommended: replace it with a key generated ONCE using openssl rand -base64 32.
# Keep that replacement key unchanged whenever you reuse its state directory.
export KIOSENTRA_DEVELOPMENT_KEY='AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8='

# A fresh temporary directory avoids credentials from previous mock-server sessions.
# This preserves any existing .local-state directory and does not read saved key files.
export KIOSENTRA_STATE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/kiosentra-dev.XXXXXX")"
printf 'Development state directory: %s\n' "$KIOSENTRA_STATE_DIR"

go run ./apps/agent/cmd/kiosentra-agent --mode=console
```

You should see `agent started` followed by `device enrolled`. The agent sends real metrics
and heartbeats every 30 seconds. Successful reports are not printed; inspect the data using
terminal 3, or build and provision the [local desktop console](docs/development/phase4.md).

### Terminal 3 — inspect received reports

```sh
# Must match KIOSENTRA_MOCK_ADMIN_TOKEN in terminal 1.
# Recommended: update this value if you changed the admin token above.
export KIOSENTRA_MOCK_ADMIN_TOKEN='kiosentra-local-admin-change-me'

curl --fail --silent --show-error \
  -H "Authorization: Bearer $KIOSENTRA_MOCK_ADMIN_TOKEN" \
  http://127.0.0.1:8080/dev/state | python3 -m json.tool
```

### Restarting and preserving state

- **Restart only the agent:** press Ctrl+C in terminal 2 and rerun just
  `go run ./apps/agent/cmd/kiosentra-agent --mode=console`. This keeps the same identity,
  encryption key and state directory in that terminal.
- **Restart the mock server:** stop the agent too, restart terminal 1, then repeat the
  entire terminal 2 block. The mock forgets registrations on restart, so fresh state is needed
  even when its hardcoded tokens stay unchanged.
- **Repeat the entire terminal 2 block:** this always creates a fresh development identity.
  Previous state is left intact; temporary directories may later be cleared by the OS.
- **Resume in a new terminal without restarting the mock:** use the same encryption key and
  replace the `mktemp` line with `export KIOSENTRA_STATE_DIR='/previously/printed/path'`.

An `invalid_device_credential` error means the saved device credential is not accepted by the
current mock; use fresh state by repeating terminal 2. An `invalid_enrollment_token` error means
the enrollment-token values do not match. A `cipher: message authentication failed` error means
a state directory was reused with a different encryption key; restore its original key or start
with fresh development state. No manual deletion of `.local-state` is needed for this quick start.

Windows uses account-scoped DPAPI instead of `KIOSENTRA_DEVELOPMENT_KEY`; see the Windows
Service guide below. Non-Windows console mode is a development harness, not a supported
production macOS agent.

See [local development](docs/development/local.md), [mock API examples](docs/api/mock-server.md),
and [Windows Service setup](docs/deployment/windows-service.md).

## Implemented behavior

- Console lifecycle, signal cancellation, JSON structured logs, Windows SCM adapter.
- Strict configuration validation; verified HTTPS, explicit loopback-only development HTTP.
- Device-generated Ed25519 identity and enrollment, protected credentials, origin binding.
- Timed CPU/memory/disk/uptime collection with per-source local diagnostics.
- Kiosk process/RSS/CPU observation and true/false/unknown heartbeat state.
- Hardware/network inventory, Windows 32/64-bit uninstall and Java/.NET discovery.
- Read-only shell/startup comparison and custom executable version metadata.
- SQLite WAL state, encrypted identity/credentials, bounded durable telemetry queue, process lock.
- Sequential sync with capped exponential backoff and jitter; persistent request IDs for replay.
- Read-only policy caching; job polling with pagination, durable decisions and duplicate protection.
- Opt-in signed deployment simulations with durable history, lease renewal, recovery and cancellation.
- Independent simulated-update helper with a separate journal, bounded health checks and offline rollback.
- Typed clients and mock routes for inventory, credential rotation, leases, status and updates.
- Authenticated development job creation and redacted report inspection.
- Optional desktop build with nine service-backed pages, dark/light themes, tray and global hotkey.
- ACL-restricted local IPC, encrypted frames and scoped, single-use maintenance authorization.

The agent automatically reports inventory and read-only policy observations. It does not yet
automatically rotate credentials or install releases. By default every job is rejected;
explicit development opt-in permits only signed simulations. See the [Phase 5 guide](docs/development/phase5.md).

See [known limitations](docs/development/limitations.md) and [validation record](docs/development/validation.md).
See [Phase 3 behavior and Windows validation](docs/development/phase3.md).
The [Phase 4 desktop guide](docs/development/phase4.md) covers building the UI, IPC credentials,
login startup and scoped maintenance authorization. Native Windows acceptance is still required.
See the [Phase 6 update recovery guide](docs/development/phase6.md) for the independent helper.
[Phase 7 hardening tools](docs/development/phase7.md) now cover protected configuration, named
technician authorization, Windows package/signing and service provisioning, separate simulation
recovery and acceptance scripts. Local validation is complete; production signing and native Windows
acceptance remain open. Real software deployment and binary replacement remain unavailable.

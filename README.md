# Kiosentra

Windows-first kiosk agent foundation. **Phases 1 and 2 only**: a runnable Go agent,
versioned API contract, durable local state, and a development central-server mock.
The production server, admin dashboard, Wails UI, software installation and self-update
are **not implemented**.

## Layout

- `apps/agent`: single executable, console and Windows SCM modes; internal service core.
- `packages/contracts`: OpenAPI 3.1 and shared Go wire types, without backend dependencies.
- `tools/mock-server`: isolated, in-memory development server and integration-test handler.
- `apps/server`, `apps/admin`, `packages/ui`, agent frontend: future-phase boundaries.
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

In terminal 1, generate two distinct development tokens and keep the enrollment token
available for terminal 2. Do not commit or log their values.

```sh
export KIOSENTRA_MOCK_ENROLLMENT_TOKEN="$(openssl rand -hex 24)"
export KIOSENTRA_MOCK_ADMIN_TOKEN="$(openssl rand -hex 24)"
go run ./tools/mock-server/cmd/mock-server
```

In terminal 2:

```sh
export KIOSENTRA_SERVER_URL=http://127.0.0.1:8080
export KIOSENTRA_DEVELOPMENT=true
export KIOSENTRA_STATE_DIR="$PWD/.local-state"
export KIOSENTRA_ENROLLMENT_TOKEN='<same enrollment token as terminal 1>'
# Non-Windows development only: keep this value outside the repository and reuse it on restart.
export KIOSENTRA_DEVELOPMENT_KEY="$(openssl rand -base64 32)"
go run ./apps/agent/cmd/kiosentra-agent --mode=console
```

Windows uses account-scoped DPAPI instead of `KIOSENTRA_DEVELOPMENT_KEY`. Non-Windows
console mode is a development harness, not a supported production macOS agent.
Stop with Ctrl+C. Restart with the same state directory and encryption key/account;
enrollment is reused and pending telemetry drains after server connectivity returns.
The mock loses all devices on restart: use a **new** state directory to re-enroll against
a restarted mock. Existing production credentials are never silently reset.

See [local development](docs/development/local.md), [mock API examples](docs/api/mock-server.md),
and [Windows Service setup](docs/deployment/windows-service.md).

## Implemented behavior

- Console lifecycle, signal cancellation, JSON structured logs, Windows SCM adapter.
- Strict configuration validation; verified HTTPS, explicit loopback-only development HTTP.
- Device-generated Ed25519 identity and enrollment, protected credentials, origin binding.
- Real basic CPU/memory/disk/uptime readings using gopsutil; heartbeat and metrics reporting.
- SQLite WAL state, encrypted identity/credentials, bounded durable telemetry queue, process lock.
- Sequential sync with capped exponential backoff and jitter; persistent request IDs for replay.
- Read-only policy caching; job polling with pagination, durable rejection and duplicate protection.
- Typed clients and mock routes for inventory, credential rotation, leases, status and updates.
- Authenticated development job creation and redacted report inspection.

The remaining contract routes are available for protocol testing; the agent does not yet
collect software inventory, automatically rotate credentials, evaluate policy compliance,
execute jobs or install releases. It rejects **every** polled job in this milestone.

See [known limitations](docs/development/limitations.md) and [validation record](docs/development/validation.md).
Next: **Phase 3 — monitoring interfaces, Windows inventory and read-only configuration detection**.

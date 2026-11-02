# Validation record

Validated on 2 November 2026 on macOS arm64 using Go 1.26.10. Go was not on PATH;
a temporary toolchain was downloaded under `/tmp/kiosentra-toolchain`, verified against
the SHA-256 in the official Go release metadata, and used with temporary build/module caches.
No system-wide Go installation was changed. Install Go normally to use the README commands,
or use the temporary toolchain while it remains available.

## Passed

- `gofmt` on all Go sources and `go work edit -fmt`.
- Unit/integration tests across all three modules.
- Race-enabled tests across all three modules.
- `go vet` across all three modules.
- Native agent and mock-server builds.
- Windows amd64 agent cross-build with CGO disabled.
- Windows DPAPI test-package cross-compilation (not execution).
- Real-binary smoke test: enrollment, real CPU/memory/disk telemetry, simulated deployment
  rejection, authenticated mock inspection and graceful SIGTERM shutdown.
- `--version` output from the native agent.

The automated tests exercise strict request decoding, OpenAPI/model field agreement,
transport configuration boundaries, certificate rejection, redirect refusal, cancellation,
SQLite durability, ciphertext tampering, process locking, bounded queues, offline restart,
public-key enrollment retry, response-loss replay, durable job decisions, device isolation,
lease expiry, policy caching/ETags, credential rotation replay and origin-bound credentials.

The first integration attempt was blocked by the execution sandbox's prohibition on local
listeners. Tests were rerun with permission to bind ephemeral loopback ports and passed;
no tests were skipped or weakened to work around the restriction.

## Not established by this environment

Native Windows SCM lifecycle, Session 0 behavior, service-account DPAPI access, directory ACLs,
reboots and service recovery. A Windows-only DPAPI round-trip/tamper test is included and
configured for Windows CI, but that CI was not run here. Follow the native validation checklist
in `docs/deployment/windows-service.md`. UI, deployment and updater tests belong to future phases.

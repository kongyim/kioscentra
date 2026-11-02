# Known limitations and next phase

- This is a Phase 1/2 foundation, not production endpoint-management software.
- Native Windows SCM/DPAPI behavior requires the documented Windows validation. Host-side tests and
  cross-builds cannot establish Session 0, ACL, account-change, reboot or recovery correctness.
- No UI, tray, hotkey, IPC, maintenance authentication, inventory collector, watchdog or remediation.
- No installation, artifact download/signature verification, rollback or self-update execution.
- CPU/memory/disk/uptime are real Phase 2 measurements; kiosk state is unknown (`null`), process metrics
  are empty and software inventory is not automatically reported. OS inventory is Phase 3.
- Credential rotation exists as a typed API and mock route, but automatic scheduling and crash-safe
  agent credential replacement are deferred. The mock issues 24-hour credentials. Expiry stops sync
  until explicit operator recovery; offline collection remains bounded.
- The mock is in-memory, loopback-only and uses development authentication. Restarting it invalidates
  enrolled devices; use a fresh development state directory. Do not use it as a production server.
- SQLite schema version 1 has no upgrade migrations. State backups require WAL-aware procedures.
- New telemetry is dropped once the bounded queue fills, with a structured warning. Permanent server
  rejection of an event retains it and can block FIFO delivery; dead-letter tooling is deferred.
- Job replay IDs are retained, capped at 10,000, and fail closed at capacity. No automatic retention
  or operator pruning tool is present. Mock receipts/jobs also have explicit finite limits.
- Retry cadence is bounded by the configured collection interval; no Retry-After scheduling yet.
- A mock job status route enforces acceptance and terminal-state bounds, not the full future installer
  transition graph. Agent-side execution remains disabled regardless of metadata.
- Service logs are bounded local files; centralized/tamper-evident audit, robust installer provisioning,
  code signing and security review are future production-hardening work.

Next is Phase 3: timed collectors with per-source diagnostics, process/kiosk health, Windows uninstall
registry inventory in both 32/64-bit views, Java/.NET/custom executable version discovery, and read-only
shell/startup/policy comparison. Do not use Win32_Product. Keep macOS production operations explicitly
unsupported until designed and implemented. Follow with Wails UI (Phase 4), simulated deployment
engine (Phase 5), independent simulated update recovery (Phase 6) and production hardening (Phase 7).

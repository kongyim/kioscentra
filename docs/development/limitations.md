# Known limitations and next phase

- This is a Phase 1–6 foundation (deployment and update simulation only), not production endpoint-management software.
- Native Windows SCM/DPAPI behavior requires the documented Windows validation. Host-side tests and
  cross-builds cannot establish Session 0, ACL, account-change, reboot or recovery correctness.
- Desktop UI and authenticated local IPC require provisioning; Windows tray/hotkey/login/ACL
  behavior still needs native acceptance. No automatic process restart or remediation.
- No installation, artifact download/signature verification, rollback or self-update execution.
- Monitoring, inventory and read-only Windows policy observation are implemented; see
  [Phase 3 scope](phase3.md) for machine-scope discovery, partial-data and deadline limitations.
- Credential rotation exists as a typed API and mock route, but automatic scheduling and crash-safe
  agent credential replacement are deferred. The mock issues 24-hour credentials. Expiry stops sync
  until explicit operator recovery; offline collection remains bounded.
- The mock is in-memory, loopback-only and uses development authentication. Restarting it invalidates
  enrolled devices; use a fresh development state directory. Do not use it as a production server.
- SQLite schema 3 adds update handoffs, preserving schema-1/2 state through additive migration.
  The separate update journal uses schema 1. Older agents cannot open schema 3; backups
  require WAL-aware procedures.
- New telemetry is dropped once the bounded queue fills, with a structured warning. Permanent server
  rejection of an event retains it and can block FIFO delivery; dead-letter tooling is deferred.
- Simulated deployments require development mode and a separately provisioned signing key.
  No real download, installer, extraction or rollback behavior is exercised by simulation fixtures.
- Job replay IDs are retained, capped at 10,000, and fail closed at capacity. No automatic retention
  or operator pruning tool is present. Mock receipts/jobs also have explicit finite limits.
- Retry cadence is bounded by the configured collection interval; no Retry-After scheduling yet.
- Mock job statuses enforce the simulation transition graph. Real installer execution is unavailable.
- Service logs are bounded local files; centralized/tamper-evident audit, robust installer provisioning,
  code signing and security review are future production-hardening work.

Phase 3 is implemented with automated host tests and Windows cross-compilation. Native Windows
runtime validation remains required. macOS production operations remain unsupported.
Phase 4 adds the [local desktop console](phase4.md); individual technician identity and production
credential provisioning remain future work. [Phase 5](phase5.md) implements signed development simulations. [Phase 6](phase6.md) adds an independent simulated recovery helper. Next is production hardening
(Phase 7); real updater effects and native Windows acceptance remain outstanding.

The update helper can survive service exit and recover offline, but has no installed OS boot trigger
or separate fallback executor. Reboot recovery requires restarting the service/helper. A failed
simulated rollback blocks new update admission; central reporting waits for the service to return.

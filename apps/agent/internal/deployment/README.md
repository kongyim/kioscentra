# Simulated deployments — Phase 5

`Engine` admits only explicitly enabled, signed development simulation jobs. `Simulator`
is a pure state-step adapter: it has no download, process-launch or installation capability.
SQLite stores admission, checkpoints and stable report/renewal IDs; the serial agent loop
renews leases and advances at most one step per cycle. The local console exposes history
and scoped technician cancellation.

See [Phase 5 operation and boundaries](../../../../docs/development/phase5.md).

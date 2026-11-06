# Deployment engine design — simulation implemented, real installation deferred

The v1 contract defines pending, accepted, downloading, verifying, installing, validating,
succeeded, failed, rolling_back, rolled_back and cancelled states. Phase 2 records a rejection
and never transitions into execution by default. Phase 5 adds an opt-in signed simulation
engine; see [implemented scope](../development/phase5.md). The mock is a protocol simulator, not an installer.

A future real-installation engine must validate device-scoped authorization, package-type allowlists, release
signature/trust key and replay/lease state before accepting work. Persist transitions before
external side effects. Download to a bounded staging directory; constrain HTTPS origins, sizes,
timeouts and redirect behavior. Resume only with matching content validators; verify complete
SHA-256 and signed release metadata before any installation. A URL alone is never authorization.

Start with simulated installers behind a focused interface. Then add approved MSI/EXE/ZIP/kiosk
adapters with controlled arguments, deadlines, progress and safe extraction rules. A lease renewal
worker must stop execution at a documented safe point on expiry; it must not create duplicate runs.
On startup reconcile in-flight states, use installer-specific idempotency and verify observed version
before choosing resume, failure or rollback. Record deployment history and terminal reports durably.
Rollback support is package-specific and must not be advertised where restoration is impossible.

Test cancellation, interrupted download, invalid signature/hash, duplicate jobs, lease expiry,
timeout, failed validation, rollback and reboot at every persisted state before enabling real installs.

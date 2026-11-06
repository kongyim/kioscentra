# Agent process model

`kiosentra-agent --mode=console` runs the core in the foreground and handles interrupt/termination.
`kiosentra-agent.exe --mode=service` must run under Windows SCM as service `Kiosentra`.
`--mode=ui` starts a separate local console in a `desktop` build; it does not open the service
database or start a second service. A headless build returns a desktop-build instruction.
`--version` reports product, build version, commit and API version without opening storage.

The SCM adapter reports start/running/stop-pending states, accepts stop/shutdown and
cancels the core. Network requests and collectors receive cancellation. Shutdown waits
up to 25 seconds. An initialization failure returns a nonzero service-specific status;
SCM recovery policy can restart it. SCM Running currently indicates the process loop
has started, not successful enrollment or first telemetry collection.

The UI is a separate interactive-session process of the same distribution. It is never launched
into Session 0 by the service. ACL-restricted Windows named pipes (Unix sockets in development)
carry authenticated/encrypted snapshots and allowlisted maintenance requests. Ordinary viewing
is separate from expiring, single-use maintenance authorization. A UI crash or restart cannot
stop the service; only an authorized stop request can. See [Phase 4](../development/phase4.md).

Service logs rotate at 5 MiB with one retained previous file. Console logs go to stderr.
Critical job decisions and unsent telemetry are independently committed to SQLite.

Phase 6 adds `--mode=updater` for explicit development simulation. The service launches a detached
instance with a separate process lock and journal, passing no service credentials. It survives
service exit, updates only simulated version references, and persists reports for later delivery.
No actual SCM operation or executable replacement occurs. A reboot still requires an external
restart of service/helper; see [Phase 6](../development/phase6.md).

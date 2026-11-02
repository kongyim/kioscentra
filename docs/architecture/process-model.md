# Agent process model

`kiosentra-agent --mode=console` runs the core in the foreground and handles interrupt/termination.
`kiosentra-agent.exe --mode=service` must run under Windows SCM as service `Kiosentra`.
`--mode=ui` returns an explicit Phase 4 unsupported error; it does not start a second service.
`--version` reports product, build version, commit and API version without opening storage.

The SCM adapter reports start/running/stop-pending states, accepts stop/shutdown and
cancels the core. Network requests and collectors receive cancellation. Shutdown waits
up to 25 seconds. An initialization failure returns a nonzero service-specific status;
SCM recovery policy can restart it. SCM Running currently indicates the process loop
has started, not successful enrollment or first telemetry collection.

A future UI is a separate interactive-session process of the same distribution. It will
never be launched into Session 0 by the service. An authenticated, ACL-restricted local
transport must expose only explicit methods; ordinary viewing is separate from maintenance
authorization. A UI crash or restart cannot stop the service. IPC is not exposed in Phase 2.

Service logs rotate at 5 MiB with one retained previous file. Console logs go to stderr.
Critical job decisions and unsent telemetry are independently committed to SQLite.

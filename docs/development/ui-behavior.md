# Local UI behavior — Phase 4

The desktop-tag build embeds a React/TypeScript/Vite/MUI console in Wails v3. It runs in a
separate interactive process, starts hidden, supports tray activation and a configurable
Ctrl+Alt+Shift+F12 global shortcut, and hides on close. Dark/light theme preference is local.
The service continues when the UI exits. Service reconnects are automatic and stale data is marked.

All nine pages consume service-owned data. Viewing grants no maintenance privileges.
Technician authentication issues an expiring, single-use authorization scoped to inventory
refresh, service stop or (Phase 5) cancellation of the active simulation. Shutdown has a separate confirmation; configuration, installation
and arbitrary command execution are not exposed. Missing data remains explicitly unavailable.

See [build, provisioning, IPC boundaries and native acceptance](phase4.md).

Phase 5 adds simulation transition history and explicit simulation labels to Deployments.
See [Phase 5](phase5.md) for opt-in configuration and failure scenarios.

Phase 6 adds an Updates page with simulated version references, recovery history and pending
central reports. The real running agent version is unchanged.

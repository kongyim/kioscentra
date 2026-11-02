# Local UI specification — Phase 4, not implemented

Wails v3 will embed a React/TypeScript/Vite/MUI console in `--mode=ui`; no Electron or distributed
Node runtime. Start at user login, initially hidden. Tray activation shows/focuses the window;
closing hides it. Default global shortcut: Ctrl+Alt+Shift+F12, configurable. Register it independently
of Explorer/tray availability and surface conflicts or unsupported sessions without stopping service.
Dark theme is default; offer light theme. Reconnect authenticated local IPC after service restarts.

Dashboard, device information, applications, deployments, configuration, logs, maintenance and about
pages must consume service-owned data. Clearly mark unavailable fields; do not fabricate installation
or uptime state. Opening the window grants no maintenance privileges. Technician authentication must
produce scoped, expiring authorization for an explicit allowlist of service-side actions. No direct
privileged React operations. Authorized shutdown is separate from closing the window. The service
survives UI crashes. ACL-restricted Windows named pipes are the planned IPC transport, not an exposed
unauthenticated local HTTP API. Hotkey, tray, IPC and maintenance authentication remain unimplemented.

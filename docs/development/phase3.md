# Phase 3 — read-only monitoring and discovery

The production entry point wires `monitoring.Basic`, `watchdog.Processes`,
`inventory.System` and Windows policy observation into the existing serial agent loop.
No new dependencies, wire fields or SQLite migrations are required.

## Collection and reporting

- Each cycle collects CPU, memory, the state-directory volume's usage and uptime.
  Each basic source has a two-second deadline inside the configured collection budget.
  A failed source does not discard other local readings. Because v1 metrics cannot encode
  unavailable scalar readings, incomplete basic metrics are kept locally rather than
  sent as misleading zeros. A degraded heartbeat is still queued using the parent context.
- After a policy is cached, processes are matched by their absolute executable path,
  resolving filesystem aliases and comparing Windows paths case-insensitively. Basenames
  never match. All matching PIDs (up to 1,000) report RSS bytes and gopsutil lifetime-average
  process CPU percentage; this may exceed 100% across multiple cores.
- `kioskRunning` is true when a matching process is found, false only after a complete scan
  finds none, and null when unconfigured or absence cannot be established. Process existence
  does not establish UI responsiveness or application health. No process is started or stopped.
- Inventory runs on startup, once per hour after a successful enqueue, and when the cached
  kiosk executable changes. A restart triggers a fresh inventory. Inventory sources have
  three-second deadlines inside the collection budget. It reports OS/platform/kernel,
  architecture, CPU model, RAM, state-volume capacity and network addresses.
- Windows software discovery reads **HKLM** uninstall registrations in both 64-bit and
  32-bit views, deduplicating by name and version. It discovers registered Java versions,
  .NET Framework v4 Full/Client and modern .NET shared frameworks, plus runtime names from
  uninstall entries. The configured kiosk executable's version is read from its file version
  resource. Executables are never run, and `Win32_Product` is never queried.
- Software is capped at 2,000 entries, runtimes at 100, and interfaces at 100. Registry
  enumerations are bounded. Missing keys are normal; permission failures and truncation
  are diagnostics. Software lists are not a complete security or licensing audit: per-user,
  portable and unregistered runtimes may not appear.
- Inventory and policy reports enter the same bounded durable outbox as metrics and
  heartbeats, including stable idempotency keys for offline delivery and response-loss replay.
  The latest cached policy is observed on the next collection cycle, including while offline.

Collectors are synchronous and cooperate with context cancellation; no timeout goroutines
are abandoned. Some native registry, file and process calls cannot be interrupted mid-call,
so their wall-clock duration can exceed a deadline. The service loop remains serial.

## Read-only policy comparison

`expectedShell` is compared with HKLM's Winlogon `Shell` value. `startupEntries` are exact
expected **Run value data strings**, including quoting and arguments, present in either HKLM
32-bit or 64-bit Run view. Extra Run entries are allowed. This is machine-scope comparison;
it does not assert the effective shell/startup configuration of a logged-on kiosk user.
HKCU, Startup folders, RunOnce, scheduled tasks, Assigned Access and Shell Launcher are not
part of this observation scope. A missing expected entry is drift; inaccessible data is unknown.

Reports preserve the policy revision and have `compliant`, `drifted` or `unknown` status.
Known drift takes precedence over errors, and errors remain in the report. `autoLaunch`,
arguments and restart enforcement cannot be established by this registry-only observation;
when requested, the report explicitly records that limitation. The bootstrap interval still
controls sampling; policy `monitorIntervalSeconds` is desired state and is not applied.
A compliant report means only the supported observations matched. No registry values,
startup entries, processes or system settings are modified.

## Diagnostics and development platforms

The existing SQLite `kv` table keeps latest non-secret snapshots:

- `monitoring-snapshot`: partial metrics plus source, duration and error diagnostics.
- `inventory-snapshot`: inventory plus source diagnostics.
- `policy-observation`: most recent comparison report.

Collection diagnostics are local; the closed v1 inventory wire model has no diagnostic
field. An incomplete inventory can therefore be sent with empty/zero unavailable fields;
consult its local snapshot before treating it as complete. Inventory errors are also logged.
Snapshots replace previous snapshots rather than growing a history. The outbox retains its
existing item and payload limits.

Non-Windows console mode remains a development harness: basic hardware inventory and process
observation work, but Windows software/file-version discovery returns an explicit unsupported
diagnostic and policy comparison reports unknown. This does not add macOS production support.

## Windows validation

Run all module tests on Windows amd64. Native tests exercise temporary HKCU registry fixtures
(including short enumeration/EOF behavior and DWORD values) and a system DLL version resource.
They do not change machine configuration. Also verify on a disposable Windows machine:

1. Compare installed 32-bit/64-bit applications, registered Java and .NET with inventory.
2. Configure an absolute kiosk executable in the server policy; start and stop it manually,
   checking process metrics and true/false/null heartbeat behavior under the service account.
3. Compare a known Winlogon shell and machine Run entry; change only the expected policy and
   confirm drift. Test restricted registry/process access and confirm unknown/errors.
4. Disconnect the server, restart the agent, then reconnect; confirm durable inventory and
   policy delivery without duplicate accepted events.
5. Confirm no shell/startup mutation or child installer/runtime process occurs. Run the
   existing SCM/DPAPI/Session 0 validation checklist separately.

Phase 4 adds the local console; Phase 5 adds signed deployment simulations. Real installation,
update recovery and production hardening remain future work.

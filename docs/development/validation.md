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
in `docs/deployment/windows-service.md`. Later sections record UI and simulated deployment/update
execution validation; real installer and binary replacement acceptance remain future work.

## Phase 3 validation

Validated this change on macOS arm64 with Go 1.27.3:

- Complete unit/integration suite across all three modules, including the Phase 1/2 regressions.
- Race-enabled suite across all three modules and `go vet`.
- Windows amd64 agent cross-build and Windows discovery test-package cross-compilation.
- New coverage: source deadlines and partial failures, heartbeat after collector timeout,
  exact executable matching, 32/64-bit uninstall merging and runtime discovery, access-denied
  versus drift, inventory cadence and policy changes, offline reports and inventory response-loss
  replay through the strict mock API.

Build caches were placed under `/tmp`. Sandbox restrictions on loopback listeners and process
inspection required rerunning the test suites with those permissions; tests were not skipped.
Windows-native registry/version-resource tests are included for Windows CI but were only
cross-compiled here. Native runtime acceptance still requires the [Phase 3 checklist](phase3.md).

## Phase 4 validation

Validated on 6 November 2026 on macOS arm64 with Go 1.27.3 and Node 26.12.0:

- Full Go unit/integration suite and race-enabled suite across all three modules.
- Go vet across all modules and desktop-tagged UI/frontend packages.
- Six frontend tests, including scoped stop confirmation, unavailable telemetry,
  theme persistence, blocked browser storage and discarding authorization replies
  received after the service disconnects.
- TypeScript checking and production Vite asset build.
- Native macOS desktop build and Windows amd64 desktop cross-build with the GUI subsystem.

The socket/process integration tests passed with sandbox restrictions lifted; no tests
were skipped. Vitest explicitly installs jsdom's browser storage because Node's native
storage global can otherwise shadow it. Use the documented Node 24/26 toolchain;
this machine also has an older Node 20 executable that is unsuitable for these dependencies.

The macOS linker reports deployment-target warnings for locally built dependencies;
this build does not establish compatibility with older macOS releases. Neither native
GUI interaction nor Windows execution was tested here. Complete the interactive
[Phase 4 acceptance checklist](phase4.md#validation-and-remaining-native-checks) before
calling native acceptance complete. CI was not run from this session.

## Phase 5 validation

Validated on 6 November 2026 on macOS arm64 with Go 1.27.3 and Node 26.12.0:

- Full Go unit/integration regression suite across agent, contracts and mock modules.
- Race-enabled suite and Go vet; desktop-tagged UI/frontend vet.
- All seven simulation scenarios, with SQLite close/reopen at every checkpoint.
- End-to-end signed mock job with first ACK, status and renewal responses deliberately lost,
  agent restart between retries, correct terminal rollback report and continued telemetry.
- Device/signature binding, real-package rejection, opt-in configuration, concurrency admission,
  immutable duplicate decisions, lease expiry/invalid renewal, changed trust, overall deadline,
  technician cancellation and cancellation of an execution context.
- Schema-1-to-2 migration preserving existing data. Injected history-write failures prove
  admission and transitions roll back atomically with their report events.
- Eight frontend tests, including simulation history labels and scoped cancellation; TypeScript
  checking and production asset build.
- Native macOS desktop build and Windows amd64 desktop cross-build with GUI subsystem.

Go socket/process integration tests required the same sandbox exceptions as earlier phases.
The macOS linker still emits deployment-target warnings; older macOS compatibility is not
established. Native Windows execution, interactive UI acceptance and CI execution were not
performed here. Tests exercise deterministic simulations, not real download/installer/rollback
or reboot effects. See [Phase 5 scope and setup](phase5.md).

## Phase 6 validation

Validated on 6 November 2026 on macOS arm64 with Go 1.27.3 and Node 26.12.0:

- `sh scripts/check.sh`: full Go unit/integration suite, race-enabled suite and vet across all
  three modules; native headless agent/mock builds and Windows amd64 headless cross-build.
- Recovery coverage includes all six update scenarios, journal reopen at each checkpoint,
  stale checkpoint/abort fencing, atomic history failure, lease/deadline expiry, revoked trust,
  bounded launch failure, credential isolation and detached helper survival after launcher exit.
- Integration coverage verifies offline rollback with the service stopped, lost renewal/report
  responses, service restart, durable report delivery, schema-2-to-3 migration and mutual
  deployment/update exclusion. Contract tests verify update signatures cannot cross kinds/devices.
- `python3 scripts/phase6-smoke.py`: production agent executable launches the actual detached
  updater; the mock is paused and agent stopped during verification. The helper restores known-good
  without either process, then the restarted agent delivers exactly five reports to the resumed mock.
  Only fresh temporary state and generated development keys are used.
- Nine frontend tests, including Updates history, pending reports and known-good mismatch;
  TypeScript checking and production Vite asset build.
- Desktop-tagged UI/frontend vet, native macOS desktop build and Windows amd64 desktop cross-build
  with GUI subsystem.

The initial sandboxed Go run could not bind loopback/Unix sockets or inspect processes. The full
check script passed with those restrictions lifted; no tests were skipped. The initial npm test
invocation selected Node 20 through its script PATH and failed before collecting tests.
Frontend checks then passed using the explicit Node 26 executable:

```sh
cd apps/agent/frontend
/usr/local/bin/node node_modules/vitest/vitest.mjs run
/usr/local/bin/node node_modules/typescript/bin/tsc --noEmit
/usr/local/bin/node node_modules/vite/bin/vite.js build
```

The Node path above is specific to this validation host. Use a supported Node on the script PATH
for the usual `npm test` / `npm run build` commands. Go build caches were under
`/tmp/kiosentra-phase6-cache`.

CI now includes the real-binary smoke test on Linux/macOS, but remote CI was not executed here.
Native Windows execution, interactive GUI acceptance, real binary replacement, SCM changes and
boot-triggered recovery are not established. macOS desktop linking still emits deployment-target
warnings, so older macOS compatibility is not established. Phase 6 acceptance is limited to the
[documented simulation scope](phase6.md); native production effects remain later work.

## Phase 7 validation

Validated on 6 November 2026 on macOS arm64, Go 1.27.3 and Node 26.12.0:

- Full Go unit/integration suite, race-enabled suite and vet across all three modules via
  `sh scripts/check.sh`; native headless agent/mock builds and Windows amd64 cross-build.
- New Go coverage for isolated configuration files, malformed/oversized/missing settings, named
  technician scope enforcement, single-use grants, actor attribution without token disclosure,
  invalid technician profiles and rejection of shared maintenance secrets outside development.
- Ten frontend tests, including required technician ID, identity-bound authorization requests,
  clearing grants when the selected identity changes, and no storage of technician credentials.
  TypeScript checking and Vite production build also passed using the explicit Node 26 executable.
- `python3 scripts/phase6-smoke.py --protected-settings`: real binaries use isolated service/reader
  config files despite conflicting environment settings, expose a redacted enrolled status over IPC,
  launch a separate recovery executable, recover while service/central are unavailable, then deliver
  all five update reports after service restart. All state and keys are temporary fixtures.
- Desktop-tagged UI/frontend vet, native macOS desktop build and Windows amd64 desktop cross-build.
- All Windows scripts parsed successfully with the official PowerShell 7.6.9 macOS arm64 distribution,
  downloaded under `/tmp/kiosentra-powershell` and verified against its release asset SHA-256
  `a1c7e98ac403da4be5ba75c1c5855af272de7787a91d478c4267466fe58fc1af`.
  Parsing found and fixed a pre-existing smart-apostrophe string error in `register-ui-login.ps1`.

As in Phase 6, Go socket/process tests and the real-binary smoke test needed the sandbox restrictions
lifted; no tests were disabled. Go caches used `/tmp/kiosentra-phase6-cache`. macOS desktop linker
deployment-target warnings remain, so older macOS compatibility is not established.

**Not run:** Windows catalog generation/Authenticode signing, actual installer/uninstaller execution,
native ACL/service-account DPAPI/SCM recovery/task acceptance, reboot tests, interactive UI tests or remote
CI. `Test-Provisioning.ps1` and Windows CI now encode development packaging, tamper rejection,
provisioning, bootstrap cleanup, restart/identity persistence, crash recovery and task dispatch checks.
Their presence is not evidence of a Windows pass. Real signing requires a certificate; independent
security review remains open. The user-approved Phase 7 delivery scope is tools and local validation;
see [Phase 7 acceptance gates](phase7.md#repeatable-verification-and-remaining-gates).

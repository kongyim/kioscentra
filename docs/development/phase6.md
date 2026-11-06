# Phase 6 — independent simulated update recovery

Status: simulation implementation and local automated acceptance completed on 6 November 2026.
The [validation record](validation.md#phase-6-validation) lists the executed checks and platform limits.

Phase 6 adds a detached `--mode=updater` process, a separate durable recovery journal, simulated
candidate/known-good targets, bounded health fixtures, rollback and eventual central reporting.
The helper runs without the service database, device credentials, IPC key or enrollment token.
It can finish recovery while the service is stopped and the central server is unreachable.

**This is simulation only.** Targets are version strings in a separate SQLite database. The code
never copies/replaces an executable, changes SCM, starts/stops the actual service, installs a
scheduled task, downloads an artifact or reboots Windows. There are no real version directories
or replacement binaries. These effects need a future native adapter and acceptance tests.

## Enable and submit a job

Use the existing mock and agent setup. Generate development signing keys with
`go run ./tools/mock-server/cmd/simulation-keygen` as in [Phase 5](phase5.md). Keep the private key
only in the mock's `KIOSENTRA_MOCK_SIMULATION_PRIVATE_KEY` environment. In the agent environment:

```sh
export KIOSENTRA_DEVELOPMENT=true
export KIOSENTRA_SIMULATED_UPDATES=true
export KIOSENTRA_SIMULATION_PUBLIC_KEY='<publicKey from the generator>'
export KIOSENTRA_INTERVAL=5s
```

Both simulation flags default to false and are independent: enabling updates does not enable
deployments. Update opt-in requires development mode and a valid base64 Ed25519 public key.
Build a real executable before running the service so it can launch a second instance of itself:

```sh
go build -o bin/kiosentra-agent ./apps/agent/cmd/kiosentra-agent
./bin/kiosentra-agent --mode=console
```

Use the desktop-tagged executable instead if you also need the UI. `go run` is unsuitable for
persistent helper recovery because its temporary executable may disappear. The distribution
binary must remain available and unchanged for the lifetime of the simulated recovery.

After enrollment, substitute its device UUID and use the authenticated mock admin endpoint:

```sh
curl --fail --silent --show-error \
  -H "Authorization: Bearer $KIOSENTRA_MOCK_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"deviceId":"<device-id>","kind":"agent_update","targetVersion":"2.0.0","leaseSeconds":300,"scenario":"health_timeout"}' \
  http://127.0.0.1:8080/dev/jobs
```

The optional scenario defaults to `success`. Update fixtures are distinct from deployment fixtures:

| Scenario | Simulated outcome |
| --- | --- |
| `success` | staged → switching → verifying → succeeded |
| `hash_failure` | failed before switching; known-good unchanged |
| `helper_launch_failure` | failed after three service launch attempts |
| `service_start_failure` | rolling_back → rolled_back |
| `health_timeout` | health fixture waits up to two seconds, then rolls back |
| `rollback_failure` | failed; candidate and known-good references retained |

A failed rollback leaves a visible mismatch and blocks subsequent update admission. For these
throwaway fixtures, use a separate fresh development state to start another independent test;
there is no production repair or destructive reset command. Deployment cancellation does not
cancel an update helper. To stop forward update progress, disable the update flag and restart the
service; it records an abort request and existing recovery proceeds to its safe result.

## Trust and admission

Only signed `agent_update` jobs with `packageType=simulation` and exact recognized fixture URLs
are eligible. The Phase 5 canonical signing representation binds job kind, job ID, enrolled device
ID and release metadata. Deployment signatures cannot be reused as update authorization. The
helper rechecks the signature and verifies deterministic fixture size/SHA-256 before switching.
URLs are fixture identifiers and are never fetched. These keys are development trust roots,
not a production release-signature policy.

The service persists acceptance in its job ledger, receives the ACK response, and confirms a
300-second lease before handing off normal work. Renewal retries reuse the job ID as the request
ID; a lost response cannot launch an unconfirmed helper. Only one deployment/update handoff may
be active. Terminal updates retain their admission slot until all recovery reports are delivered.
Rejected job IDs cannot later become accepted; submit a new ID when retrying a rejected job.

## Journal and recovery lifecycle

Service schema 3 adds `update_handoffs` atomically, preserving earlier jobs and deployment history.
Older agents refuse schema 3. The helper journal is independently versioned (schema 1) at:

```text
<KIOSENTRA_STATE_DIR>/update-simulation/journal.db
<KIOSENTRA_STATE_DIR>/update-simulation/helper.lock
```

The journal uses WAL/FULL synchronous transactions. It records immutable job input, enrolled device
ID, previous known-good version, checkpoint, overall/health deadlines, abort flag, launch attempts
and stable status event IDs. There is a 10,000-update cap and a finite acyclic transition graph.
Terminal records remain for replay protection. Backups must include SQLite WAL state as appropriate;
there is no automatic downgrade or journal pruning.

Handoff is idempotent across the two databases: the accepted job remains in the service ledger
until its journal is terminal and reports are delivered. A crash before journal creation retries
preparation; a crash after creation resumes the existing journal without extending its deadlines.
The child is launched only after preparation commits.

The helper uses its own process lock and checkpoint comparisons to fence duplicate/stale helpers.
`switching` records intent first; the next transaction changes the simulated current version and
enters `verifying`. Success promotes known-good atomically with its event. Rollback records intent,
then restores the previous reference atomically with its terminal event. Failed event writes roll
back pointer/checkpoint changes too. An abort arriving between a helper read and write invalidates
that write, so the next step rereads the abort.

The overall deadline is one minute from staging. Health timeout has a persisted two-second deadline.
Before switching, expired lease/deadline, revoked trust or abort fails closed. After switch intent,
those conditions trigger rollback; rollback itself does not depend on central connectivity, an
unexpired lease or the new candidate. The helper polls every 200 ms. Health and service-start
results are deterministic fixtures, not tests of the real agent's readiness.

Unix helpers start a new session; Windows helpers use detached/new-process-group flags. Native
Windows containment or job-object policies can still affect lifetime and require acceptance testing.
The launcher passes only simulation configuration and necessary OS temporary-directory variables,
not service secrets. The helper does not access the service's SQLite database or central API.

## Restart and reporting

A restarted service relaunches a nonterminal prepared helper before central sync/backoff. A live
helper's lock prevents duplicate execution. After a simulated reboot (all processes terminated),
restart the service or explicitly run the helper with the same state directory and trust key:

```sh
./bin/kiosentra-agent --mode=updater
```

An unprovisioned Phase 6 build has no OS-persisted automatic recovery trigger or independently
installed fallback helper. [Phase 7 tooling](phase7.md) can provision a separate protected recovery
copy and a Windows startup task for simulations; Windows task/reboot acceptance is still pending.
Without that provisioning, if both processes stop, neither restarts itself.
The helper executable itself is not updated in this phase. A real update system must place a
known-good recovery executor outside candidate directories and define its own protected handoff.

The helper commits update reports locally even while the service is absent. The service later sends
ordered `/agent/update-status` events using stable IDs, marking them delivered after confirmation.
Lost responses replay the same event; offline reports remain durable. Central reporting requires
the service to return with valid credentials. Existing credential expiry/mock-reset limitations
still apply. Update status wire objects contain target version rather than job ID; local history
retains the job ID for correlation. The mock job ACK and device update reports are separate views.

The console's **Updates** page shows the newest 100 events, report delivery status, simulated current
version and known-good version. A mismatch is highlighted. Real agent version on About remains
unchanged. The safe IPC projection excludes full job metadata, signatures and trust material.

## Validation and remaining native work

To repeat the Go regression checks and real-binary recovery smoke test on macOS/Linux:

```sh
sh scripts/check.sh
python3 scripts/phase6-smoke.py
```

The smoke test uses fresh temporary state and development signing keys, starts its own loopback
mock and agent, pauses the mock, and stops the agent while the detached helper verifies a candidate.
It checks independent rollback, restoration of known-good, and delivery of all five update reports
after resuming the mock and restarting the agent. It requires Python 3.8+ and Go; it does not use
existing device state. The POSIX signal-based smoke test is also configured in Linux/macOS CI.
Run the frontend tests/build and desktop builds described in [Phase 4](phase4.md) for UI changes.

Tests cover restart at each journal checkpoint, detached helper survival after launcher exit,
signature/kind/device binding, wrong hash, expired lease, trust revocation, aborted switch,
health timeout, rollback failure, launch failure, checkpoint fencing, atomic history failure,
schema migration, mutual deployment/update exclusion, lost renewal/report responses and offline
recovery followed by service restart. See [validation record](validation.md).

Real binary switching, immutable version directories, signed real-artifact download, SCM start/stop,
service-account ACLs, boot triggers, updater replacement, native health probes, Windows reboots and
rollback of real external effects are not established. Cross-compilation and simulated process
restart must not be reported as native Windows update acceptance.

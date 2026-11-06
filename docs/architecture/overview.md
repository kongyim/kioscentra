# Architecture and implementation plan

## Phase 1 / Phase 2 plan

1. Establish a Go workspace with agent, contracts and mock modules.
2. Define the v1 wire contract and strict decoding/validation.
3. Add console/SCM lifecycle, validated environment configuration and JSON logging.
4. Persist identity, credentials, telemetry, policies and job decisions in SQLite.
5. Add typed client and isolated development mock; integrate enrollment and reporting.
6. Exercise offline recovery, request replay, device isolation and Windows compilation.

## Boundaries

The executable selects console or SCM startup and invokes the same `app.Run` lifecycle.
`configuration` owns local bootstrap settings. `monitoring.Basic` supplies timed metrics with local per-source diagnostics.
`watchdog.Processes` observes kiosk PIDs; `inventory.System` and the read-only Windows adapter
collect inventory and compare supported policy fields. See [Phase 3](../development/phase3.md).
`app.Agent` orchestrates one serial cycle; there are no unbounded background worker pools.
`api.Client` owns timeout-bound HTTPS calls. `storage.Store` owns SQLite transactions,
protected blobs, a bounded outbox and durable job decisions and simulation transitions. The contracts module contains
only wire models, validators and the OpenAPI document. The mock imports contracts, not agent code.
The agent imports the mock only in integration tests.

Future package directories have README boundaries rather than nonfunctional interfaces.
Wails v3 and React are included behind the `desktop` build tag. Node builds the embedded frontend;
no Node runtime is distributed. Headless agent builds do not require desktop assets.
Phase 4 IPC exposes a safe service snapshot and scoped maintenance actions; see
[desktop architecture and provisioning](../development/phase4.md).

## Data flow

Each interval: collect with deadlines → append metrics, heartbeat, policy observations and
due inventory to the outbox →
check communication backoff → enroll if needed → drain at most 100 events → retrieve and
cache desired policy → poll jobs → persist admission/rejection decisions → acknowledge them →
advance at most one authorized simulation checkpoint.
A lost HTTP response causes the same durable request ID to be retried. The mock stores the
request hash and response; altered replays get 409. Enrollment retries reuse the persisted
public key. Successful acknowledgements are recorded so jobs cannot be re-executed after restart.

Metrics collection continues during central-server backoff. Queue saturation drops new
samples with a warning; existing events remain durable. No server response controls the
kiosk process in this milestone. UI availability is irrelevant to the service core.

## Persistence and compatibility

SQLite uses WAL, FULL synchronous writes, a five-second busy timeout and a single connection.
One OS file lock allows one service core per state directory. Durable tables are `kv`,
`outbox` and `jobs`. Protected identity and credentials are in `kv`; desired policy is
stored separately as ordinary JSON. Bootstrap settings are environment-backed with a durable non-secret diagnostic snapshot; the enrolled
server origin is durable and must match on restart. Schema version 3 is recorded with
`user_version`; newer schema versions are rejected. An additive transaction migrates schema 1/2 to 3.

Telemetry is bounded to 1,000 events by default. Job IDs are retained up to 10,000; reaching
capacity fails closed rather than evicting replay protection. This is a milestone limit,
not a production retention policy. Deployment checkpoints and a dedicated status-history queue were added in Phase 5.
Phase 6 adds service handoff rows and an independent helper journal. See
[deployment simulations](../development/phase5.md) and [update recovery](../development/phase6.md).

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
`configuration` owns local bootstrap settings. `monitoring.Basic` supplies Phase 2 metrics.
`app.Agent` orchestrates one serial cycle; there are no unbounded background worker pools.
`api.Client` owns timeout-bound HTTPS calls. `storage.Store` owns SQLite transactions,
protected blobs, a bounded outbox and durable job decisions. The contracts module contains
only wire models, validators and the OpenAPI document. The mock imports contracts, not agent code.
The agent imports the mock only in integration tests.

Future package directories have README boundaries rather than nonfunctional interfaces.
Wails v3 and React dependencies are intentionally absent until Phase 4. No Node runtime
is required to build or distribute the implemented agent.

## Data flow

Each interval: collect with a deadline → append metrics and heartbeat to the outbox →
check communication backoff → enroll if needed → drain at most 100 events → retrieve and
cache desired policy → poll jobs → persist rejection decisions → acknowledge them.
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
server origin is durable and must match on restart. Schema version 1 is recorded with
`user_version`; newer schema versions are rejected, and explicit migrations must be added before changing this schema.

Telemetry is bounded to 1,000 events by default. Job IDs are retained up to 10,000; reaching
capacity fails closed rather than evicting replay protection. This is a milestone limit,
not a production retention policy. Recovery/update metadata tables and deployment history
will be introduced with the corresponding engines in Phases 5 and 6.

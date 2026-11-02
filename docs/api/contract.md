# Agent API v1

Canonical specification: [`openapi-v1.yaml`](../../packages/contracts/openapi-v1.yaml).
It uses JSON syntax, which is valid YAML 1.2, to allow dependency-free structural checks.
The base path is `/api/v1`. Go types are shared only as a convenience; the protocol does not
require a Go server. All specified object fields are required, including explicit `null`
for unavailable kiosk status and release metadata. Unknown fields and null non-nullable
values are rejected. Timestamps are UTC RFC 3339.

Reports are authenticated with a device-scoped opaque bearer credential. The server derives
device identity from that credential. Enrollment uses its body token instead. Every mutation
other than enrollment requires a UUID `Idempotency-Key`; retries preserve both ID and payload.
The mock compares raw JSON payload bytes, so clients must resend the same serialization.
Identical replays return the original response; conflicting bodies return 409. Entries are
scoped to device, method and path. The mock caps receipts at 10,000 per device and returns 503
rather than forgetting replay protection. Production retention is an outstanding server decision.

`GET /agent/jobs` returns unacknowledged, unexpired jobs, sorted by UUID. `limit` is 1–100;
`cursor` is the last returned ID. Polling begins at the first page on every cycle so newly
inserted jobs are not permanently skipped. Leases are UTC expiry timestamps. Acknowledgement
must occur before expiry; updates/renewals require acceptance and valid ownership. Rejected
jobs need no status report. A terminal job cannot be updated or renewed. Full deployment
transition validation belongs to Phase 5; the mock currently validates enum/acceptance/terminal
boundaries only. The agent polls at most ten pages per cycle.

Policy responses include a quoted revision ETag and support `If-None-Match` / 304. The current
agent fetches the full policy each cycle and caches its typed revision; conditional client
fetching is a later optimization. Inventory and policy reports have typed clients and mock
handlers but are not automatically produced before Phase 3.

Rotation changes the bearer credential while retaining the enrolled public key. The mock
accepts the old token for five minutes **only** to replay the original rotation request.
No other route accepts the old credential. Persist a rotation request ID before requesting;
automatic scheduling, crash-safe rotation orchestration and key replacement are future work.

Latest release returns `null` until an agent-update simulation job is created. Its metadata
is deliberately untrusted, uses a non-resolving example URL and cannot authorize execution.
The production signing/trust pipeline and rollout targeting remain deferred.

Errors contain `code`, `message`, `requestId`. Clients log status/code without server body or
credentials. The mock has no production server or admin UI and exposes no arbitrary execution.

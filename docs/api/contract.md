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
jobs need no status report. A terminal job cannot be updated or renewed. Both the mock and persistent central enforce the simulation transition graph. The agent polls at most ten pages per cycle.

Policy responses include a quoted revision ETag and support `If-None-Match` / 304. The current
agent fetches the full policy each cycle and caches its typed revision; conditional client
fetching is a later optimization. Phase 3 automatically produces inventory and
read-only policy reports through the durable outbox; see [observation scope](../development/phase3.md).

Rotation changes the bearer credential while retaining the enrolled public key. Both central
and mock accept the old token for five minutes **only** to replay the original rotation request.
No other route accepts the old credential. The Agent renews one hour (or two reporting intervals, whichever is longer) before expiry, persists an
encrypted request ID before requesting, and atomically saves the encrypted new credential while
clearing the pending request. Pending requests retry even if the local old expiry has elapsed, using an independent
timer and backoff capped at 30 seconds instead of waiting for the telemetry interval.
Central persists rotation receipts and migrates its database from schema 1 to 2 on startup. A
reused request ID with different bytes returns 409; a superseded rotation cannot restore an old
credential. Offline expiry without a committed rotation or a missed five-minute recovery window
requires operator recovery. Identity key replacement/revocation remain future work.

Latest release returns `null` until an agent-update simulation job is created. Its metadata
is deliberately untrusted, uses a non-resolving example URL and cannot authorize execution.
The production signing/trust pipeline and rollout targeting remain deferred.

Errors contain `code`, `message`, `requestId`. Clients log status/code without server body or
credentials. The mock has no production server or admin UI and exposes no arbitrary execution.

## Phase 5 simulation statuses

The wire objects are unchanged. Opted-in agents may accept signed development simulation
jobs. They persist and report `accepted`, then advance through downloading, verifying,
installing, validating and succeeded; installation/validation failures may enter rolling_back
and rolled_back. Active states may fail or cancel; terminal states cannot transition.
The mock now enforces this graph. Status retries retain their event IDs; lease-renewal retries
retain their checkpoint IDs. See [Phase 5](../development/phase5.md) for the device-bound
simulation signing convention and the explicit lack of real installation effects.

## Phase 6 simulated update reports

The existing update-status wire object is unchanged. Signed `agent_update` simulations report
staged, switching, verifying and a terminal success/failure/rollback outcome through the device's
`/agent/update-status` route. These durable events use stable request IDs across retries. They are
separate from deployment job statuses and contain target version rather than job ID; local recovery
history retains the job ID. Actual agent-update packages remain unsupported.


## Persistent central simulation jobs

See the [admin job API and provisioning guide](../../apps/server/README.md#signed-deployment-simulation-jobs).
Central supports signed deployment simulations only; job creation requires a separately
provisioned development signing key. Admin creation/cancel/retry requests have persistent,
exact-byte idempotency. Job records and receipts commit atomically; retries create linked
new job IDs rather than resetting terminal history.

Central omits cancelled jobs from polling. Cancellation revokes renewal, including replay
of a committed renewal, with 410. This revocation exception prevents stale receipts from
authorizing more simulation work. Ordered status reports may arrive after cancellation or
lease expiry and are persisted without granting a lease. Completion racing cancellation
is preserved. Accepted cancellation remains unconfirmed until an Agent terminal report;
new work is blocked while unconfirmed. The mock retains its previous development semantics.

Admin job detail supports newest-first event pagination with `limit` (1–100, default
100) and an exclusive positive receipt-sequence `cursor`. The additive `nextCursor`
response field is empty at the end; `historyTruncated` indicates older events remain.
The embedded job record reflects current state on every page.

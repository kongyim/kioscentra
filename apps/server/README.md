# Central server MVP

A runnable, single-instance Go server backed by SQLite. It uses the existing Agent v1
contract and does not depend on the development mock or Agent internals. Registrations,
credential hashes, telemetry and replay receipts survive process restarts. An Agent with
an unexpired saved credential resumes reporting without another enrollment.

This is an MVP, not production acceptance. The [web admin](../admin/README.md) supports device queries and signed deployment simulation jobs. Real deployment,
self-update, credential revocation, central SSO/RBAC, rate limiting, retention,
and high availability remain future work. Credentials expire after **24 hours**, matching
the existing Agent contract. The Agent automatically renews one hour before expiry.
A server restart alone does not extend expiry. Offline expiry before renewal or a lost
rotation result not recovered within five minutes still requires operator recovery.

## Local quick start

Run from the repository root on macOS/Linux. Stop the mock if it occupies port 8080.
The first block generates and saves local development secrets **once**. Subsequent runs
reuse them. `.local-state` is ignored by Git. Keep this directory private; on Windows,
provision appropriate NTFS ACLs instead of relying on Unix file modes.

```sh
umask 077
mkdir -p .local-state/central
if [ ! -f .local-state/central/server.env ]; then
  {
    printf "export KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN='%s'\n" "$(openssl rand -hex 32)"
    printf "export KIOSENTRA_CENTRAL_ADMIN_TOKEN='%s'\n" "$(openssl rand -hex 32)"
    printf "export KIOSENTRA_CENTRAL_CREDENTIAL_KEY='%s'\n" "$(openssl rand -base64 32)"
    printf "export KIOSENTRA_DEVELOPMENT_KEY='%s'\n" "$(openssl rand -base64 32)"
  } > .local-state/central/server.env
fi
. .local-state/central/server.env

go run ./apps/server/cmd/kiosentra-server \
  --database="$PWD/.local-state/central/central.db" \
  --admin-directory="$PWD/apps/admin/public"
```

Open **http://127.0.0.1:8080/admin/** and use the private admin token to view devices.
See the [web admin guide](../admin/README.md) for session behavior and frontend checks.

In another terminal, start an Agent with its own persistent state directory. This is
separate from any previous mock-server identity. Use the same working directory and
URL on later runs; the Agent binds its identity to the server origin.

```sh
. .local-state/central/server.env
export KIOSENTRA_SERVER_URL=http://127.0.0.1:8080
export KIOSENTRA_DEVELOPMENT=true
export KIOSENTRA_ENROLLMENT_TOKEN="$KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN"
export KIOSENTRA_STATE_DIR="$PWD/.local-state/central-agent"
go run ./apps/agent/cmd/kiosentra-agent --mode=console
```

Inspect the devices from a third terminal:

```sh
. .local-state/central/server.env
curl --fail --silent --show-error \
  -H "Authorization: Bearer $KIOSENTRA_CENTRAL_ADMIN_TOKEN" \
  'http://127.0.0.1:8080/api/v1/admin/devices?limit=20' | python3 -m json.tool
```

Stop and restart **only the central server**, using the same command, database and
credential key. Leave the Agent running. It retries after connectivity returns; the
device ID and earlier reports remain available. Normal reporting is every 30 seconds;
backoff can delay recovery further. No fresh Agent state is necessary.

## Configuration and transport

| Setting | Requirement |
| --- | --- |
| `--database` | Absolute SQLite path, required; use a private dedicated directory |
| `--listen` | Defaults to `127.0.0.1:8080` |
| `--admin-directory` | Optional absolute `apps/admin/public` path; enables `/admin/` |
| `--tls-cert`, `--tls-key` | Both PEM paths required for a remote listener |
| `KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN` | Random bootstrap secret, 32–256 characters |
| `KIOSENTRA_CENTRAL_ADMIN_TOKEN` | Independent random admin secret, at least 32 characters |
| `KIOSENTRA_CENTRAL_CREDENTIAL_KEY` | Base64 of exactly 32 random bytes; preserve with database |

Plain HTTP is restricted to a literal loopback listener. Remote access requires TLS,
for example `--listen=:8443 --tls-cert=/private/server.crt --tls-key=/private/server.key`.
Use a certificate trusted by the Agent; it does not bypass certificate verification.
The executable bounds request bodies, headers and HTTP timeouts. Tokens must not be
supplied as command-line arguments, query parameters or committed configuration.

Device bearer tokens are domain-separated HMAC-SHA256 values derived from the private
credential key and persisted identity/expiry. Only their SHA-256 hashes are stored.
This permits an enrollment retry to return the same credential after a lost response
or restart without storing plaintext bearer tokens. Startup rejects a different key
for an existing database. This key is **not database encryption**: inventory, telemetry
and identity metadata are stored in plaintext. Protect and back up both the database
and key; use a SQLite-aware backup or stop the server before copying the database.
Never rotate the credential key by simply changing the environment value.

The current enrollment wire contract has no private-key proof. The bootstrap token
therefore authorizes both new enrollment and retrieval of the current credential for
an existing public key. Restrict its distribution and remove it from Agent provisioning
after enrollment. Expired identities return 409 on enrollment; automatic re-enrollment is
not implemented. Authenticated rotation before expiry is supported. Existing mock credentials cannot be imported into this server.

## Agent endpoints

All paths below have the `/api/v1` prefix. Reporting requires a valid device bearer
credential, `Content-Type: application/json`, a UUID `Idempotency-Key`, and a validated
closed v1 object. Credentials are valid for 24 hours.

| Method/path | Behavior |
| --- | --- |
| `POST /enrollments` | Register or replay an unexpired enrollment by public key |
| `POST /agent/credentials/rotate` | Renew credential for 24 hours; durable exact-request replay |
| `POST /agent/heartbeat` | Persist heartbeat history |
| `POST /agent/metrics` | Persist metrics history |
| `PUT /agent/inventory` | Persist inventory history |
| `POST /agent/policy/report` | Persist observed policy state |
| `GET /agent/policy` | Safe static read-only policy; supports ETag/304 |
| `GET /agent/jobs` | Unacknowledged, unexpired, uncancelled deployment simulations; UUID pagination |
| `POST /agent/jobs/{id}/ack` | Accept or reject a job |
| `POST /agent/jobs/{id}/status` | Persist ordered simulation status history |
| `POST /agent/jobs/{id}/renew` | Renew a live accepted job; cancellation fences renewal |
| `GET /agent/releases/latest` | JSON `null`; no published release |

A report, its deduplication receipt and last-seen update commit atomically. Replay of
an identical request returns 204 without another report or last-seen refresh. A changed
body under the same device/method/path/request ID returns 409. Comparison uses exact
request bytes, not normalized JSON. All receipts remain durable; no pruning is provided
in this MVP. Unsupported routes, including mock `/dev/*` endpoints, return 404.

## Automatic credential renewal

The Agent starts rotation with at most one hour remaining (or two reporting intervals,
whichever is longer), before sending queued reports. It saves an encrypted pending request ID first. A transport or
storage failure preserves that intent and the old local credential for retry; it does
not create another request ID. Pending rotation retries use an independent timer and
backoff capped at 30 seconds, even when telemetry samples are an hour apart. A valid response must retain the device ID, change the
token and extend expiry. Saving the protected replacement and clearing the intent is
one local SQLite transaction; only then does the Agent switch its in-memory client.

Central atomically records the request/body hash and changes the device token hash.
The previous token stops working on normal APIs immediately. For five minutes, it can
only replay that exact request to obtain the same result, including after server/Agent
restart or the old local expiry. Changed bodies return 409. A result superseded by a
later rotation cannot be used to restore an older token. Receipts contain hashes and
expiry metadata, not bearer tokens, and are currently retained without pruning.

Central schemas 1 and 2 migrate additively to schema 3 on startup; existing identities,
rotations and reports are retained. Older server binaries reject schema 3. Back up with SQLite-aware
procedures before an upgrade. Agent schema remains 3; the pending intent uses its
existing encrypted key/value storage.

If a device stays offline past expiry without a committed rotation, or cannot recover
a committed result inside the five-minute replay window, synchronization fails closed
and requires operator recovery. The Agent retains its identity, pending intent and
bounded queued telemetry; it does not silently re-enroll. This change does not add
credential revocation or identity-key replacement.

## Authenticated admin queries

Use `Authorization: Bearer <admin token>`. Device credentials cannot call these routes.
Queries exclude device token hashes, access tokens, public keys and server secrets.

| GET path | Response |
| --- | --- |
| `/api/v1/admin/devices?limit=20&cursor=<device-id>&search=<text>&status=<online-or-offline>` | `{items, nextCursor, total, online, offline}`; devices ordered by UUID |
| `/api/v1/admin/devices/<device-id>` | Device metadata and latest received heartbeat, metrics, inventory, policyReport; absent reports are `null` |
| `/api/v1/admin/devices/<device-id>/reports?limit=20&cursor=<sequence>&order=desc` | `{items, nextCursor}`; descending receipt sequence (`asc` is the default) |

Search is a literal case-insensitive substring of hostname/device ID (maximum 256 bytes).
Counts include all search matches before status filtering; an omitted status means all.
For descending history, the cursor fetches sequences smaller than the last returned item.

Omit the cursor for the first page; use `nextCursor` until it is empty. Limits are 1–100,
default 20. Device-list pagination is a live view, not a snapshot of concurrent enrollments.
The device view includes `deviceId`, enrollment hostname/version, `enrolledAt`,
`credentialExpiresAt`, `lastSeenAt`, and `online`. `online` means a new valid report
arrived within 90 seconds; it is connectivity, not kiosk health. Read `heartbeat` for
kiosk health/version. Latest projections follow **receipt order**, so an old queued
sample can temporarily be the latest received report. Sample timestamps remain in bodies.
History and receipt storage are unbounded; capacity monitoring/retention is required
before scaling beyond a development pilot. Admin responses can contain sensitive
hardware/network/software inventory and require the same care as the database.

## Verification

```sh
go test ./apps/server/...
go test ./apps/agent/internal/app -run TestPersistentCentral -count=1
go test -race ./apps/server/... ./apps/agent/internal/app
go vet ./apps/server/...
go build -o bin/kiosentra-server ./apps/server/cmd/kiosentra-server
go build -o bin/kiosentra-agent ./apps/agent/cmd/kiosentra-agent
python3 scripts/central-smoke.py
```

Tests cover storage reopen, lost enrollment/report responses, concurrent replay,
transaction rollback, body validation, expiry, auth isolation, pagination, offline
status, wrong credential key, future schema rejection and real Agent sync after both
Agent and central storage restart. The smoke script also starts both real binaries,
restarts them with the same state and no Agent bootstrap token, and verifies identity,
history and resumed reporting. The repository check script and CI include this module;
CI runs the real-binary smoke on macOS/Linux.

## Signed deployment simulation jobs

The central server can dispatch only the existing `deployment` simulation fixtures. It
never accepts arbitrary URLs, commands, packages or job kinds. Real installation and
central `agent_update` dispatch remain unavailable. Creation is disabled unless
`KIOSENTRA_CENTRAL_SIMULATION_PRIVATE_KEY` supplies a base64 Ed25519 private key (64 bytes).
The Agent separately requires development mode, simulation opt-in and the matching public
key. Agents without that opt-in reject jobs and the dashboard records the rejection.

Generate a development key pair once, keeping the private key on the server:

```sh
umask 077
mkdir -p .local-state/central
# Run only when creating a NEW key pair. Preserve the existing key file on later runs.
( set -C; go run ./tools/mock-server/cmd/simulation-keygen > .local-state/central/simulation-keys.json )
```

In the server terminal, alongside the existing server environment:

```sh
export KIOSENTRA_CENTRAL_SIMULATION_PRIVATE_KEY="$(python3 -c 'import json; print(json.load(open(".local-state/central/simulation-keys.json"))["privateKey"])')"
go run ./apps/server/cmd/kiosentra-server \
  --database="$PWD/.local-state/central/central.db" \
  --admin-directory="$PWD/apps/admin/public"
```

Provision **only the publicKey value** to each development Agent through its normal
configuration. On the same-host development setup, add these settings to the Agent's
existing environment before restarting it:

```sh
export KIOSENTRA_SIMULATED_DEPLOYMENTS=true
export KIOSENTRA_SIMULATION_PUBLIC_KEY="$(python3 -c 'import json; print(json.load(open(".local-state/central/simulation-keys.json"))["publicKey"])')"
# KIOSENTRA_DEVELOPMENT=true and the existing server/state settings are also required.
go run ./apps/agent/cmd/kiosentra-agent --mode=console
```

Open a device in `/admin/` to create a simulation, inspect state/history, request
cancellation or retry an unsuccessful job. One outstanding job per device is allowed.
A retry creates a new ID/signature and `retryOf` link; the previous record is immutable
except for its own delayed agent reports. A successful job cannot be retried via `/retry`;
create a new simulation explicitly instead.

All admin paths below use the `/api/v1` prefix and the existing admin bearer credential.
Every POST requires `Content-Type: application/json` and a UUID `Idempotency-Key`.

| Method/path | Request and response |
| --- | --- |
| `POST /admin/jobs` | Closed object with `deviceId`, `targetVersion` (nonempty, max 256), `scenario`, `leaseSeconds` (30–3600); returns 201 JobRecord |
| `GET /admin/jobs?deviceId=…&limit=20&cursor=…` | Required device UUID; 1–100 records, UUID ascending; `{items,nextCursor,simulationEnabled}` |
| `GET /admin/jobs/{id}?limit=100&cursor=…` | JobRecord plus newest-first receipt events, `historyTruncated` and `nextCursor` |
| `POST /admin/jobs/{id}/cancel` | Empty object `{}`; returns JobRecord with cancellation requested or pending job cancelled |
| `POST /admin/jobs/{id}/retry` | Empty object `{}`; returns 201 new JobRecord linked to a rejected, expired, failed, rolled-back or confirmed-cancelled job |

The closed scenario catalog is `success`, `download_failure`, `hash_failure`,
`install_failure`, `validation_failure`, `rollback_failure`, and `timeout`.
JobRecord contains `job` (the signed Agent contract), `deviceId`, `scenario`,
`initialLeaseSeconds`, optional `retryOf`, nullable `ack`, `status`, `cancelRequestedAt`,
and derived `state`. Events contain sequence, actor (`admin` or device UUID), request
path, server receipt time and request payload. Shared admin credentials do not identify
a human operator. Keys and credentials are never returned in job records.

Mutations and exact-byte replay receipts commit in one SQLite transaction. Retries must
use the same request ID and JSON bytes; changed payloads return 409. Replaying committed
creation after restart returns the same job, even if it has since progressed. Use GET for
current state. The dashboard keeps uncertain request IDs in page memory and reuses them
when the same operation is retried. After reloading or leaving the session, inspect history
before submitting again because the browser no longer has that request ID.

Cancellation revokes **future renewals**, including replay of a previously committed
renewal (410). The Agent renews before every simulation step, so it stops at its next
renewal attempt or local lease expiry while offline. A previously authorized step can
finish concurrently. The server records `cancellation_requested` for accepted work until
it receives the actual terminal result; it does not claim that an offline Agent has stopped.
New jobs and retries remain blocked while cancellation is unconfirmed. An Agent unable
to authenticate after offline credential expiry needs operator recovery. Pending jobs
can be cancelled immediately because they have not been accepted.

Ordered delayed status reports remain accepted after lease expiry/cancellation so the
history can converge on the Agent's actual result. They do not grant execution permission.
Terminal state transitions are rejected; identical committed status/ack requests remain
replayable. A successful completion racing cancellation is retained as success.

Limits are 1,000 retained jobs per device and 4,096 committed mutation receipts per job.
Capacity returns 503 rather than discarding deduplication or audit history. No retention
or pruning tool is included. Job detail defaults to 100 events per page; `limit` accepts 1–100. Pass the returned
`nextCursor` as `cursor` to retrieve older events until it is empty. Cursors are positive
receipt sequences and exclude the boundary event. `historyTruncated` indicates more
older events are available. New receipts do not shift subsequent older pages; the
JobRecord itself always reflects current state. Existing callers retain the newest-100
default. All retained events are accessible without directly opening SQLite. Back up with WAL-aware procedures. Keep the same signing key on restart and
provision new public keys deliberately; replacing it can invalidate pending work on Agents.

Run the real-process acceptance test with built Agent/server binaries:

```sh
python3 scripts/central-jobs-smoke.py
```

It uses isolated temporary keys/state and checks signed execution, checkpoint recovery
after both processes restart, Agent-confirmed cancellation, idempotent linked retry and
failure history. It never installs software. CI runs it on Linux/macOS.

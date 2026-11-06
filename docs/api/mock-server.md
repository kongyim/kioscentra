# Development mock server

Run `go run ./tools/mock-server/cmd/mock-server` after setting the two mock token variables.
The default listen address is `127.0.0.1:8080`; `--listen` accepts another literal loopback
address only. State is intentionally in memory and resets when the process exits.

Inspect enrollment, telemetry and decisions from the shell holding the admin token:

```sh
curl --fail -H "Authorization: Bearer $KIOSENTRA_MOCK_ADMIN_TOKEN" http://127.0.0.1:8080/dev/state
```

Copy a returned `deviceId` into the following JSON. Create a deployment simulation:

```sh
curl --fail -X POST http://127.0.0.1:8080/dev/jobs \
  -H "Authorization: Bearer $KIOSENTRA_MOCK_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"deviceId":"<device UUID>","kind":"deployment","targetVersion":"2.0.0","leaseSeconds":300}'
```

Allowed kinds: `deployment`, `agent_update`, `diagnostics`. Lease duration: 30–3,600 seconds.
The server generates the stable UUID; callers cannot supply arbitrary scripts, commands or
installer arguments. Deployment/update jobs carry simulation-only release metadata with an
untrusted key and signature. Creating an `agent_update` job also exposes that simulated release
through `/api/v1/agent/releases/latest`. The Phase 2 agent persists and acknowledges a rejection;
it does not download or run the artifact. Inspect `/dev/state` to verify that outcome.

The library `mock.New(Options)` returns an `http.Handler` suitable for `httptest`; an injectable
clock makes lease tests deterministic. No fixed test port is required. Tests cover enrollment,
report replay, credential isolation, strict requests, policy ETags, lease expiry and rotation.

Limits: 1 MiB request bodies, 1,000 devices, 1,000 retained reports and jobs per device, 10,000
idempotency receipts per device. Reports evict oldest entries; jobs and receipts fail closed
at capacity. Restart the development server to reset state. These are development guardrails,
not production capacity or retention guarantees. All error bodies follow the v1 error shape.

## Signed development simulations (Phase 5)

With `KIOSENTRA_MOCK_SIMULATION_PRIVATE_KEY` configured, deployment jobs receive
device-bound signatures. `/dev/jobs` accepts optional `scenario`, defaulting to `success`.
See [Phase 5](../development/phase5.md) for fixtures and agent trust configuration.
The agent still rejects all jobs unless simulation is explicitly enabled. Status transitions
follow the simulation graph; existing idempotency receipts are checked before lease expiry.

Phase 6 also signs `agent_update` simulation jobs with the same configured development key.
Its separate fixture catalog and `/agent/update-status` reporting are described in
[Phase 6](../development/phase6.md). No real release or installer endpoint has been enabled.

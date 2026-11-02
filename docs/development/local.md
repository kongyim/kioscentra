# Local development

Use the root README quick start. Go workspace patterns must enumerate modules.
No `.env` loader is present; configure the parent shell or service environment explicitly.
Do not put real tokens in checked-in shell scripts. The development server requires two
distinct tokens of at least 16 characters and never prints them.

| Variable | Default / meaning |
| --- | --- |
| `KIOSENTRA_SERVER_URL` | `https://localhost:8443`; origin only, no path/query/user information |
| `KIOSENTRA_STATE_DIR` | Windows `%ProgramData%\Kiosentra`; otherwise user config directory `/kiosentra`; must be absolute |
| `KIOSENTRA_ENROLLMENT_TOKEN` | Required only before successful enrollment |
| `KIOSENTRA_DEVELOPMENT` | `false`; explicitly permit HTTP on a literal loopback address |
| `KIOSENTRA_DEVELOPMENT_KEY` | Non-Windows development: base64 of 32 random bytes; reuse across restarts |
| `KIOSENTRA_INTERVAL` | `30s`, valid `1s` through `1h` |
| `KIOSENTRA_MOCK_ENROLLMENT_TOKEN` | Mock bootstrap token; required |
| `KIOSENTRA_MOCK_ADMIN_TOKEN` | Separate mock administrative token; required |

Each request and collector has a ten-second timeout. Retry backoff is one second plus
full jitter over an exponentially growing window capped at 256 seconds. Actual retry
occurs on the next collection tick, so a 30-second interval imposes that granularity.
The outbox is FIFO and drains up to 100 records per cycle. Persistent 4xx responses are
retained for diagnosis; do not silently discard critical events. A malformed first event
can block later telemetry until operator recovery; quarantine tooling is deferred.

`go test -race` covers host-side integration behavior with ephemeral loopback listeners.
If a restricted environment blocks listening, run the same command with permission to
bind loopback rather than disabling the tests. `scripts/check.sh` runs the standard checks.

Build metadata can be supplied with:

```sh
go build -ldflags '-X kiosentra/apps/agent/internal/app.Version=0.1.0 -X kiosentra/apps/agent/internal/app.Commit=local' -o bin/kiosentra-agent ./apps/agent/cmd/kiosentra-agent
```

For a fresh mock session, choose a new state directory; do not erase useful device state
just to fix a server outage. Preserve your development encryption key outside the repository.

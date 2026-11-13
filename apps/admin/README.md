# Kiosentra web admin

A responsive Traditional Chinese dashboard for the persistent central server.
The browser uses the existing authenticated admin APIs on the **same origin**. No frontend
runtime dependencies, build step, external fonts, CDN or separate web server are required.

## Start

First follow the [central server setup](../server/README.md#local-quick-start) to generate
local secrets. From the repository root, source the existing secrets and run:

```sh
. .local-state/central/server.env
go run ./apps/server/cmd/kiosentra-server \
  --database="$PWD/.local-state/central/central.db" \
  --admin-directory="$PWD/apps/admin/public"
```

Open **http://127.0.0.1:8080/admin/** and enter the value of
`KIOSENTRA_CENTRAL_ADMIN_TOKEN` from your private server configuration. An enrollment
or device token cannot log in. Start the Agent using the central guide to see real data.
Without enrolled devices the dashboard shows an empty state; it never inserts demo data.

The optional `--admin-directory` must be absolute and contain the four files in `public/`.
Files are loaded at server startup, so restart the server after editing them. When enabled,
`/` and `/admin` redirect to `/admin/`. Ship this directory alongside the server binary.
Without the flag the server retains its API-only behavior. Remote listeners require the
existing TLS certificate/key flags; the browser should connect over HTTPS.

## Available views

- Device totals, online/offline counts and a paginated device list (20 per page).
- Server-side, case-insensitive substring search by hostname/device ID; online/offline filter.
  Search applies on submit. Counts describe all devices matching the search, before status
  filtering. Pagination and counts are live views rather than a transactional snapshot.
- Device details: latest CPU/memory/disk readings, uptime, kiosk state, health, expiry,
  hardware, networks, installed software/runtimes and policy observations.
- Local search through the selected device's reported software/runtimes.
- Most recently received reports first, with older/newer pages and expandable JSON.
- Manual refresh and optional automatic refresh every 20 seconds. Background tabs,
  active inputs/selects and expanded reports pause automatic refresh to avoid disrupting work.
- Empty, loading, stale-data and authentication-error states. Missing metrics remain unknown,
  and are never rendered as zero. `online` means a new report arrived within 90 seconds;
  it is not a claim that the kiosk application is healthy.

The list's version/hostname are enrollment metadata. Details prefer the latest reported
version/hostname. Latest reports use receipt order; offline backlog can contain older samples.
The UI displays collected timestamps where available. Dates use the browser's local timezone.

## Deployment simulation controls

Each device detail page shows simulation jobs and their acknowledgement/status history.
When server signing is enabled, a form creates a signed deployment simulation with a
version, fixture scenario and initial lease. See [server setup](../server/README.md#signed-deployment-simulation-jobs)
for the private key and separate Agent development opt-in/public key configuration.
Without server signing, history remains visible and creation/retry controls are hidden.
Cancellation remains available for existing active work.

Cancellation of an accepted job is shown as requested until the Agent confirms a terminal
result. A step already authorized may finish. Retry creates a new signed job linked to the
old record. Unconfirmed cancellation blocks new work; offline devices need to reconnect.
History shows the latest 100 events and labels truncation; older events remain in SQLite.
The shared admin token does not provide individual operator attribution.

On a lost response, resubmit the same operation/content in the same page session to reuse
its idempotency key. After reload/logout, inspect job history before creating work again.
Form values survive refreshes, and duplicate submission is disabled while a request runs.

## Session and security boundary

The static login screen is public when enabled; every data request still requires the
admin bearer token. It stays only in page memory, never in a URL, cookie, localStorage or
sessionStorage. The password input is cleared immediately after submission. Reloading,
leaving the page, logging out, or a 401 clears the session. Logging out does **not** revoke
the server-wide token. Use a trusted workstation; the existing shared admin credential is
not named-user login, role-based access control or SSO.

API URLs are fixed to this origin and redirects are rejected. Superseded requests are
cancelled/ignored so a late response cannot repopulate another device view or a logged-out
page. Server-provided text, including report JSON, is HTML-escaped. The Go asset handler
serves only the four allowed filenames and sends a restrictive CSP, no-store, nosniff and
no-referrer headers. Simulation creation, cancellation and retries require the same admin bearer token.
Real deployment and token issuance are unavailable.

## Checks

Node **24.11+** is needed only for development checks, not to serve the dashboard:

```sh
cd apps/admin
npm ci --ignore-scripts
npm run check
npm test
```

Tests use jsdom and Node's built-in test runner. They exercise authenticated requests,
server filters/cursors, safe text rendering, missing readings, details, 401/logout,
late-response isolation and stale-data recovery. CI runs these checks independently of
the Wails local console.

From the repository root, the optional real-browser smoke uses isolated temporary data:

```sh
go build -o bin/kiosentra-server ./apps/server/cmd/kiosentra-server
# Defaults to macOS Chrome. Else set CHROME_BIN to your Chrome executable.
node scripts/admin-smoke.mjs
```

It starts a real server, seeds 25 **test-only** devices, opens a fresh headless Chrome profile,
checks login/search/pagination/details/escaping/mobile/logout and simulation creation/cancellation/retry, writes screenshots to the
ignored `.cache/admin-qa/` directory, then removes its temporary database/profile and stops
its processes. It does not read or change your normal browser profile or actual device data.

Job history shows up to 100 events per page, newest first. Use 較舊紀錄 / 較新紀錄
to navigate older receipts. Refresh keeps the current history page; selecting a job
starts at its newest events. Failed navigation keeps the last successful page so
you can retry without skipping history.

# Security model

The agent is for explicitly managed devices. There is no remote shell, script execution,
installer launch or policy remediation. All jobs are rejected in Phase 2.

HTTPS uses Go's normal certificate validation and system trust roots. Redirects are refused,
including redirects to another credential recipient. HTTP is allowed only with explicit
development mode and a literal loopback server address. The persisted server origin prevents
reusing credentials with a newly configured host. API requests have timeouts and 1 MiB body
limits. The mock CLI binds only literal loopback addresses and has distinct device, enrollment
and administrative credentials. All devices see only their own jobs and reports.

Windows protects private identity keys and device credentials with **user/account-scoped
DPAPI**, with UI forbidden. Run consistently under the same service account. Install the state
directory with an ACL writable only by that account and administrators; Go file permission
bits alone do not enforce Windows ACLs. Moving from an interactive account to LocalSystem
requires a separate state directory and enrollment. Do not use machine-wide DPAPI as a shortcut.

Non-Windows development uses AES-256-GCM and a mandatory external random key supplied through
`KIOSENTRA_DEVELOPMENT_KEY`. That key is never saved in the ordinary config/database. This is
an explicit test facility, not the future macOS Keychain implementation. Losing the key makes
local identity unrecoverable. Protect environment provisioning and state backups accordingly.

No code logs bearer tokens, enrollment tokens or private keys. Development inspection excludes
credentials/public keys. The SQLite database still contains non-secret operational information;
restrict access. Structured enrollment, job rejection, sync failure and lifecycle events provide
basic local audit evidence, not a tamper-proof centralized audit trail.

The mock is not a production identity authority: reusable bootstrap tokens, in-memory secrets,
public-key enrollment deduplication and simplified lease handling are for tests only. A production
server needs scoped enrollment/issuance, key possession verification, credential revocation,
rate limits, durable idempotency, fleet authorization and security review. Automatic agent
credential rotation is intentionally deferred; the typed route and mock rotation replay behavior
are implemented. The Phase 2 agent stops central sync on expired credentials without discarding
identity or running jobs. Local collection remains active within the bounded queue.

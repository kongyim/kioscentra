# Security model

The agent is for explicitly managed devices. There is no remote shell, script execution,
installer launch or policy remediation. Real execution remains disabled. Phase 5 allows only opted-in signed development simulations.

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

## Phase 4 local console

The service keeps its database/credentials private and exposes an explicit projection over
ACL-restricted local transport. A separate IPC key authenticates/encrypts frames. Maintenance
requires a distinct high-entropy technician secret and one-use, expiring action scopes.
Phase 7 adds named local technician profiles with allowed scopes and actor-attributed audit messages;
shared-secret authorization is now development-only. See [Phase 7 provisioning and trust boundaries](../development/phase7.md).
This is not central SSO or tamper-evident auditing.

## Phase 5 simulation trust

A separate Ed25519 development key signs device-bound simulation jobs. The agent accepts only
a closed fixture catalog and rechecks trust before resume; it never fetches artifact URLs.
Admission, state and report IDs are durable, with terminal replay protection and lease checks
at each safe step. Cancellation uses the existing scoped technician authorization.
See [simulation boundaries](../development/phase5.md); this is not production release trust.

## Phase 6 recovery isolation

Agent-update simulation signatures bind the update kind and enrolled device. The helper receives
only simulation configuration, keeps an independent journal and process lock, and cannot read
credentials through its journal/API. It has no real download or binary-switch capability.
After switch intent, recovery can restore the journal's known-good reference despite expired lease
or revoked trust. OS accounts remain trusted; this is not a privilege-separation sandbox.
Phase 7 tools configure service-account ACLs, a separate protected helper and an opt-in simulation
startup task. Native Windows ACL/task/reboot behavior has not been accepted in this environment.

## Phase 7 provisioning review

Explicit configuration files are authoritative over inherited application environment values and
bounded to 64 KiB. The Windows provisioner separates service secrets from reader settings, uses a
virtual service account and gives the service write access to state but not executors/configuration.
Enrollment confirmation precedes bootstrap-token removal. Administrative configuration and the OS
administrator remain trusted; plaintext bootstrap/IPC material relies on those ACLs.

Package tools use SHA-256 catalogs and pinned Authenticode publishers. Unsigned fixtures require
explicit development opt-in. Installer scripts themselves must come from a trusted reviewed source
or have their signatures verified before execution. Catalog checks inside an already untrusted script
cannot establish the script's own trust. Real release signing and independent security review remain
acceptance gates. See [Phase 7](../development/phase7.md) for local test evidence and open native checks.

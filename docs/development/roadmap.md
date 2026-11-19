# Remaining delivery phases

Baseline: 13 November 2026. Phases 1–7 provide monitoring, persistent central/admin,
local console and deployment/update simulations. They do not establish production readiness.
This is the implementation backlog requested by the user, not a declaration that all phases are done.

| Phase | Deliverable | Completion gate | Status |
| --- | --- | --- | --- |
| 8 | Real deployment authorization and artifact staging | Device-bound, expiring signatures isolated from simulations; HTTPS origin allowlist; bounded, verified downloads; negative tests and isolated retries; usable staging CLI | Implemented; host tests/race/vet and Windows cross-build passed; native runtime pending |
| 9 | First real MSI deployment | Central creation and UI, explicit Agent opt-in, verified MSI identity/version, durable installer intent, bounded execution, cancellation and crash reconciliation; no blind reinstall after uncertain outcome | 9A executor/fixture sources implemented; 9B privileged broker, central/UI integration and recovery pending; see [Phase 9](phase9.md) |
| 10 | Windows acceptance and release packaging | Disposable Windows MSI fixture; native SCM/DPAPI/ACL/Session 0/tray checks; interrupted install/reboot tests; approved signed-package evidence | Pending; requires Windows host and signing certificate |
| 11 | Real Agent self-update | Release catalog/targeting, independent helper, versioned binary switch, health protocol, offline restoration, helper-update fallback and Windows reboot matrix | Pending; depends on 9–10 |
| 12 | Central identities and device lifecycle | Named users, roles, operation audit, revocation/retirement, expired-device recovery; SSO integration when provider is chosen | Pending |
| 13 | Managed policies and kiosk recovery | Versioned policy editing/assignment, observation versus enforcement controls, bounded restart policy and audited remediation | Pending |
| 14 | Operational durability | Dead-letter handling, retention without weakening replay protection, rate limits, backup/restore drill, monitoring and load tests; deployment topology from measured scale | Pending |
| 15 | Production release acceptance | End-to-end signed install/update/rollback, independent security review, operator runbooks and recorded acceptance evidence | Pending; depends on all prior gates |

## Delivery rules

- Keep each phase usable and covered by meaningful regression tests.
- Preserve monitoring, existing simulation behavior and stored data during migration.
- Separate host-side tests, cross-compilation, Windows runtime and production acceptance.
- Do not enable remote installation merely because staging or simulation passes.
- Package-specific restoration must be demonstrated before advertising rollback. MSI
  transaction rollback is not a general mechanism to restore an earlier application version.
- External requirements stay visibly pending; record exact evidence when they are satisfied.

## Current implementation boundary

Phase 8 establishes an offline-signed authorization envelope and a standalone staging tool.
It does not enable installation in the Agent or change the central simulation API. Phase 9
must integrate authorization, replay admission, leases, installer identity and status reporting
as one end-to-end feature before exposing real deployment controls.

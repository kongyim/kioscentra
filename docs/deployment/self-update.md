# Self-update and recovery — simulation implemented; native updates deferred

Phase 6 implements an opt-in signed simulation with a detached updater process, independent
SQLite journal, simulated target switching, bounded health fixture and offline rollback.
See [implemented behavior and setup](../development/phase6.md). No real binary switching,
service control, artifact downloading or production-ready self-update is implemented.

The following remains the design required before enabling **real** updates:

Plan an independent recovery executor, initially a controlled second instance of the distribution
binary in an updater mode. Persist an update journal before launching it. Keep binaries in versioned
immutable directories and switch the service target only after staging and verification; never
replace the running executable in place. The helper must be outside the directory being replaced.
It must survive service termination, perform bounded health checks, restore the known-good target
on failure and restart/report recovery independently of the new agent.

The release manifest binds version, package type, HTTPS artifact URL, SHA-256, size and trusted key
ID. Phases 5/6 define a canonical device-bound signing representation for development simulations.
Freeze and review a production release format and trust-root policy before real artifact execution.
The `signature` field is base64 of a detached signature. Do not treat the mock's zero-filled signature or `mock-untrusted` key as valid.
Rollout targeting and release authorization belong to the future central server.

Recover the updater itself by retaining a separately installed known-good helper/supervisor and an
OS-persisted recovery trigger. Updating that recovery component needs its own staged handoff and a
fallback that the candidate cannot delete. Exact SCM/task ownership, ACLs, journal fencing and health
protocol need native Windows design/testing. Strict single-file distribution is secondary to recovery
reliability; extracted versioned executors are acceptable.

Validate crashes at every journal transition, interrupted reboot, helper launch failure, corrupt
artifacts, failed service start, health timeout, rollback failure and loss of central connectivity.
Do not enable real updates or label them production-ready until those tests pass on Windows.

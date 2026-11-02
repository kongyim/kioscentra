# Self-update and recovery design — Phase 6, not implemented

The contract includes release metadata, agent-update jobs and update status reporting. The mock
can return untrusted simulation metadata. No binary switching, download verification, updater state
machine, recovery process or production-ready self-update is implemented in this milestone.

Plan an independent recovery executor, initially a controlled second instance of the distribution
binary in an updater mode. Persist an update journal before launching it. Keep binaries in versioned
immutable directories and switch the service target only after staging and verification; never
replace the running executable in place. The helper must be outside the directory being replaced.
It must survive service termination, perform bounded health checks, restore the known-good target
on failure and restart/report recovery independently of the new agent.

The release manifest binds version, package type, HTTPS artifact URL, SHA-256, size and trusted key
ID. Before Phase 5/6, freeze a canonical UTF-8 signing representation and implement Ed25519 verification
against provisioned trust roots; the current `signature` field is base64 of a detached signature and
is only a wire contract. Do not treat the mock's zero-filled signature or `mock-untrusted` key as valid.
Rollout targeting and release authorization belong to the future central server.

Recover the updater itself by retaining a separately installed known-good helper/supervisor and an
OS-persisted recovery trigger. Updating that recovery component needs its own staged handoff and a
fallback that the candidate cannot delete. Exact SCM/task ownership, ACLs, journal fencing and health
protocol need native Windows design/testing. Strict single-file distribution is secondary to recovery
reliability; extracted versioned executors are acceptable.

Validate crashes at every journal transition, interrupted reboot, helper launch failure, corrupt
artifacts, failed service start, health timeout, rollback failure and loss of central connectivity.
Do not enable real updates or label them production-ready until those tests pass on Windows.

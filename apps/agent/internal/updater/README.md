# Independent update recovery simulation — Phase 6

The service persists an accepted update handoff and launches a detached instance of the
same executable in `--mode=updater`. The helper uses a separate journal and process lock,
checks signed simulation fixtures, advances simulated version pointers, and restores the
known-good pointer on failure without reading service credentials or calling the central API.
The service delivers journal reports later and the console exposes safe history.

No executable replacement, real service control, downloads or boot recovery trigger exists.
See [Phase 6 setup, recovery and limitations](../../../../docs/development/phase6.md).

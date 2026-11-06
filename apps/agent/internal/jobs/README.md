# Job orchestration

The agent loop in `internal/app` polls jobs and persists immutable admission decisions.
`internal/deployment` advances only explicitly enabled signed simulations. Real jobs and
agent updates remain rejected. Durable decisions, checkpoints and report history live in
`internal/storage`; there is no separate general-purpose job executor.

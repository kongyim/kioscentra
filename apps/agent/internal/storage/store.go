package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"kiosentra/packages/contracts"
)

var ErrQueueFull = errors.New("telemetry queue is full")

type Store struct {
	db  *sql.DB
	p   Protector
	max int
}
type Event struct {
	ID, Method, Path string
	Body             json.RawMessage
}

func Open(dir string, p Protector, max int) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "state.db")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	var schemaVersion int
	if e = db.QueryRow(`PRAGMA user_version`).Scan(&schemaVersion); e != nil {
		db.Close()
		return nil, e
	}
	if schemaVersion > 3 {
		db.Close()
		return nil, errors.New("state schema newer than this agent")
	}
	s := &Store{db, p, max}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
BEGIN;
CREATE TABLE IF NOT EXISTS kv(key TEXT PRIMARY KEY,value BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS outbox(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT UNIQUE NOT NULL,method TEXT NOT NULL,path TEXT NOT NULL,body BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,body BLOB NOT NULL,ack BLOB NOT NULL,reported INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS deployments(id TEXT PRIMARY KEY REFERENCES jobs(id),state TEXT NOT NULL,record BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS deployment_events(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT UNIQUE NOT NULL,job_id TEXT NOT NULL REFERENCES deployments(id),body BLOB NOT NULL,delivery TEXT NOT NULL DEFAULT 'pending');
CREATE TABLE IF NOT EXISTS update_handoffs(id TEXT PRIMARY KEY REFERENCES jobs(id),complete INTEGER NOT NULL DEFAULT 0);
PRAGMA user_version=3;
COMMIT;`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Put(ctx context.Context, key string, v any, secret bool) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if secret {
		b, e = s.p.Seal(b)
		if e != nil {
			return e
		}
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, b)
	return e
}
func (s *Store) Get(ctx context.Context, key string, v any, secret bool) error {
	var b []byte
	if e := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key=?`, key).Scan(&b); e != nil {
		return e
	}
	if secret {
		var e error
		b, e = s.p.Open(b)
		if e != nil {
			return e
		}
	}
	return json.Unmarshal(b, v)
}
func (s *Store) Enqueue(ctx context.Context, method, path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > contracts.MaxBody {
		return errors.New("outbox payload exceeds body limit")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM outbox`).Scan(&count); e != nil {
		return e
	}
	if count >= s.max {
		return ErrQueueFull
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO outbox(id,method,path,body) VALUES(?,?,?,?)`, contracts.ID(), method, path, b); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Pending(ctx context.Context, limit int) ([]Event, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,method,path,body FROM outbox ORDER BY seq LIMIT ?`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var v Event
		if e = rows.Scan(&v.ID, &v.Method, &v.Path, &v.Body); e != nil {
			return nil, e
		}
		events = append(events, v)
	}
	return events, rows.Err()
}
func (s *Store) DeleteEvent(ctx context.Context, id string) error {
	_, e := s.db.ExecContext(ctx, `DELETE FROM outbox WHERE id=?`, id)
	return e
}

// RememberJob commits the decision before any acknowledgement is sent. IDs are retained
// for replay protection; the bounded ledger fails closed instead of evicting IDs.
func (s *Store) RememberJob(ctx context.Context, j contracts.Job, a contracts.Ack) error {
	b, e := json.Marshal(j)
	if e != nil {
		return e
	}
	ab, e := json.Marshal(a)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var exists int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE id=?`, j.ID).Scan(&exists); e != nil {
		return e
	}
	if exists != 0 {
		return nil
	}
	if a.Accepted {
		var active int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM deployments WHERE state NOT IN ('succeeded','failed','rolled_back','cancelled')`).Scan(&active); e != nil {
			return e
		}
		var updates int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM update_handoffs WHERE complete=0`).Scan(&updates); e != nil {
			return e
		}
		if active != 0 || updates != 0 {
			return errors.New("another simulated deployment or update is active")
		}
	}
	result, e := tx.ExecContext(ctx, `INSERT INTO jobs(id,body,ack,created_at) SELECT ?,?,?,? WHERE (SELECT count(*) FROM jobs)<10000`, j.ID, b, ab, time.Now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return e
	}
	count, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if count != 1 {
		return errors.New("job ledger capacity reached")
	}
	if a.Accepted && j.Kind == "agent_update" {
		if _, e = tx.ExecContext(ctx, `INSERT INTO update_handoffs(id) VALUES(?)`, j.ID); e != nil {
			return e
		}
	} else if a.Accepted {
		now := time.Now().UTC()
		record := Deployment{Job: j, Status: contracts.JobStatus{State: "accepted", Message: "Simulation accepted; no software will be installed", UpdatedAt: now}, StartedAt: now, Deadline: now.Add(10 * time.Minute), LeaseExpiresAt: j.LeaseExpiresAt}
		if e = writeDeployment(ctx, tx, record); e != nil {
			return e
		}
	}
	return tx.Commit()
}

type Decision struct {
	ID  string
	Ack contracts.Ack
	Job contracts.Job
}

func (s *Store) Decisions(ctx context.Context) ([]Decision, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,ack,body FROM jobs WHERE reported=0 LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		var d Decision
		var b, body []byte
		if e = rows.Scan(&d.ID, &b, &body); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &d.Ack); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(body, &d.Job); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) MarkReported(ctx context.Context, id string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE jobs SET reported=1 WHERE id=?`, id)
	return e
}

// ConsoleData is the only storage projection exposed over local IPC. Secret kv
// records and raw outbox/job payloads are deliberately not addressable by clients.
func (s *Store) ConsoleData(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	for _, key := range []string{"monitoring-snapshot", "inventory-snapshot", "policy-observation", "policy", "configuration", "heartbeat", "sync-status"} {
		var value json.RawMessage
		if e := s.Get(ctx, key, &value, false); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				out[key] = nil
				continue
			}
			return nil, e
		}
		out[key] = value
	}
	var queued int
	if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM outbox`).Scan(&queued); e != nil {
		return nil, e
	}
	out["queuedEvents"] = queued
	rows, e := s.db.QueryContext(ctx, `SELECT id,body,ack,reported FROM jobs ORDER BY created_at DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	jobs := []any{}
	for rows.Next() {
		var id string
		var body, ack []byte
		var reported bool
		if e = rows.Scan(&id, &body, &ack, &reported); e != nil {
			return nil, e
		}
		var j contracts.Job
		var a contracts.Ack
		if e = json.Unmarshal(body, &j); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(ack, &a); e != nil {
			return nil, e
		}
		jobs = append(jobs, map[string]any{"id": id, "kind": j.Kind, "targetVersion": j.TargetVersion, "accepted": a.Accepted, "reason": a.Reason, "reported": reported})
	}
	out["jobs"] = jobs
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	deployments, e := s.DeploymentHistory(ctx)
	if e != nil {
		return nil, e
	}
	out["deployments"] = deployments
	return out, nil
}

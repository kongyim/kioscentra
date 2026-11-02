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
	if schemaVersion > 1 {
		db.Close()
		return nil, errors.New("state schema newer than this agent")
	}
	s := &Store{db, p, max}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS kv(key TEXT PRIMARY KEY,value BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS outbox(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT UNIQUE NOT NULL,method TEXT NOT NULL,path TEXT NOT NULL,body BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,body BLOB NOT NULL,ack BLOB NOT NULL,reported INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL);
PRAGMA user_version=1;`)
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
	_, e = s.db.ExecContext(ctx, `INSERT INTO jobs(id,body,ack,created_at) SELECT ?,?,?,? WHERE (SELECT count(*) FROM jobs)<10000 ON CONFLICT(id) DO NOTHING`, j.ID, b, ab, time.Now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return e
	}
	var exists int
	e = s.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE id=?`, j.ID).Scan(&exists)
	if e == nil && exists == 0 {
		return errors.New("job ledger capacity reached")
	}
	return e
}

type Decision struct {
	ID  string
	Ack contracts.Ack
}

func (s *Store) Decisions(ctx context.Context) ([]Decision, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,ack FROM jobs WHERE reported=0 LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		var d Decision
		var b []byte
		if e = rows.Scan(&d.ID, &b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &d.Ack); e != nil {
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

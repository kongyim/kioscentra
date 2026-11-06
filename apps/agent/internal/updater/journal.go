// Package updater implements independent development-only update recovery simulation.
package updater

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"kiosentra/packages/contracts"
	_ "modernc.org/sqlite"
)

var ErrCheckpoint = errors.New("update checkpoint changed; retry from journal")

type Journal struct {
	db  *sql.DB
	Dir string
}
type Record struct {
	Job            contracts.Job `json:"job"`
	DeviceID       string        `json:"deviceId"`
	State          string        `json:"state"`
	Message        string        `json:"message"`
	Previous       string        `json:"previous"`
	StartedAt      time.Time     `json:"startedAt"`
	Deadline       time.Time     `json:"deadline"`
	HealthDeadline time.Time     `json:"healthDeadline"`
	LaunchAttempts int           `json:"launchAttempts"`
	Abort          bool          `json:"abort"`
}
type Event struct {
	ID        string                 `json:"id"`
	JobID     string                 `json:"jobId"`
	Status    contracts.UpdateStatus `json:"status"`
	Delivered bool                   `json:"delivered"`
}
type Target struct {
	Current   string `json:"current"`
	KnownGood string `json:"knownGood"`
}

func Open(stateDir string) (*Journal, error) {
	dir := filepath.Join(stateDir, "update-simulation")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "journal.db")
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
	_, e = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;`)
	if e != nil {
		db.Close()
		return nil, e
	}
	var version int
	if e = db.QueryRow(`PRAGMA user_version`).Scan(&version); e != nil || version > 1 {
		db.Close()
		return nil, errors.New("unsupported update journal schema")
	}
	_, e = db.Exec(`BEGIN;
 CREATE TABLE IF NOT EXISTS updates(id TEXT PRIMARY KEY,state TEXT NOT NULL,record BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT UNIQUE NOT NULL,job_id TEXT NOT NULL,body BLOB NOT NULL,delivered INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS target(id INTEGER PRIMARY KEY CHECK(id=1),current TEXT NOT NULL,known_good TEXT NOT NULL);
 PRAGMA user_version=1; COMMIT;`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Journal{db: db, Dir: dir}, nil
}
func (j *Journal) Close() error { return j.db.Close() }
func Terminal(s string) bool    { return s == "succeeded" || s == "failed" || s == "rolled_back" }
func (j *Journal) Get(ctx context.Context, id string) (Record, error) {
	var r Record
	var b []byte
	e := j.db.QueryRowContext(ctx, `SELECT record FROM updates WHERE id=?`, id).Scan(&b)
	if e == nil {
		e = json.Unmarshal(b, &r)
	}
	return r, e
}
func (j *Journal) Active(ctx context.Context) ([]Record, error) {
	rows, e := j.db.QueryContext(ctx, `SELECT record FROM updates WHERE state NOT IN ('succeeded','failed','rolled_back') ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (j *Journal) Target(ctx context.Context) (Target, error) {
	var t Target
	e := j.db.QueryRowContext(ctx, `SELECT current,known_good FROM target WHERE id=1`).Scan(&t.Current, &t.KnownGood)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return t, e
}

// Prepare is idempotent across a crash between the service ledger and this journal.
func (j *Journal) Prepare(ctx context.Context, job contracts.Job, device, version string, now time.Time) error {
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var exists int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM updates WHERE id=?`, job.ID).Scan(&exists); e != nil {
		return e
	}
	if exists != 0 {
		return nil
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM updates`).Scan(&count); e != nil {
		return e
	}
	if count >= 10000 {
		return errors.New("update journal capacity reached")
	}
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM updates WHERE state NOT IN ('succeeded','failed','rolled_back')`).Scan(&count); e != nil {
		return e
	}
	if count != 0 {
		return errors.New("another update is active")
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO target(id,current,known_good) VALUES(1,?,?) ON CONFLICT(id) DO NOTHING`, version, version); e != nil {
		return e
	}
	var target Target
	if e = tx.QueryRowContext(ctx, `SELECT current,known_good FROM target WHERE id=1`).Scan(&target.Current, &target.KnownGood); e != nil {
		return e
	}
	if target.Current != target.KnownGood {
		return errors.New("simulated target requires operator recovery")
	}
	r := Record{Job: job, DeviceID: device, State: "staged", Message: "Simulation journal staged; no binary replacement", Previous: target.KnownGood, StartedAt: now, Deadline: now.Add(time.Minute)}
	if e = writeRecord(ctx, tx, r, now); e != nil {
		return e
	}
	return tx.Commit()
}
func writeRecord(ctx context.Context, tx *sql.Tx, r Record, now time.Time) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO updates(id,state,record) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,record=excluded.record`, r.Job.ID, r.State, b); e != nil {
		return e
	}
	status := contracts.UpdateStatus{TargetVersion: r.Job.TargetVersion, State: r.State, Message: r.Message, UpdatedAt: now}
	if e = status.Validate(); e != nil {
		return e
	}
	b, e = json.Marshal(status)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO events(id,job_id,body) VALUES(?,?,?)`, contracts.ID(), r.Job.ID, b)
	return e
}
func transition(from, to string) bool {
	if Terminal(from) {
		return false
	}
	if to == "failed" {
		return true
	}
	switch from {
	case "staged":
		return to == "switching"
	case "switching":
		return to == "verifying" || to == "rolling_back"
	case "verifying":
		return to == "succeeded" || to == "rolling_back"
	case "rolling_back":
		return to == "rolled_back"
	}
	return false
}

// Commit fences the checkpoint and changes the simulated pointer atomically with history.
func (j *Journal) Commit(ctx context.Context, old Record, next, message string, now time.Time) error {
	if !transition(old.State, next) {
		return errors.New("invalid update transition")
	}
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var b []byte
	if e = tx.QueryRowContext(ctx, `SELECT record FROM updates WHERE id=? AND state=?`, old.Job.ID, old.State).Scan(&b); errors.Is(e, sql.ErrNoRows) {
		return ErrCheckpoint
	} else if e != nil {
		return e
	}
	var r Record
	if e = json.Unmarshal(b, &r); e != nil {
		return e
	}
	// A revocation arriving after the helper's read must be observed before switching.
	if r.Abort && !old.Abort {
		return ErrCheckpoint
	}
	r.State, r.Message = next, message
	if next == "verifying" {
		r.HealthDeadline = now.Add(2 * time.Second)
		if _, e = tx.ExecContext(ctx, `UPDATE target SET current=? WHERE id=1`, r.Job.TargetVersion); e != nil {
			return e
		}
	}
	if next == "succeeded" {
		if _, e = tx.ExecContext(ctx, `UPDATE target SET known_good=current WHERE id=1`); e != nil {
			return e
		}
	}
	if next == "rolled_back" {
		if _, e = tx.ExecContext(ctx, `UPDATE target SET current=?,known_good=? WHERE id=1`, r.Previous, r.Previous); e != nil {
			return e
		}
	}
	if e = writeRecord(ctx, tx, r, now); e != nil {
		return e
	}
	return tx.Commit()
}
func (j *Journal) RequestAbort(ctx context.Context, id string) error {
	_, e := j.db.ExecContext(ctx, `UPDATE updates SET record=json_set(record,'$.abort',json('true')) WHERE id=? AND state NOT IN ('succeeded','failed','rolled_back')`, id)
	return e
}
func (j *Journal) LaunchFailed(ctx context.Context, id string, now time.Time) error {
	// Launcher never rewrites a checkpoint after the helper has started.
	_, e := j.db.ExecContext(ctx, `UPDATE updates SET record=json_set(record,'$.launchAttempts',json_extract(record,'$.launchAttempts')+1) WHERE id=? AND state='staged'`, id)
	if e != nil {
		return e
	}
	r, e := j.Get(ctx, id)
	if e != nil {
		return e
	}
	if r.State == "staged" && r.LaunchAttempts >= 3 {
		return j.Commit(ctx, r, "failed", "Simulation helper failed to launch after three attempts", now)
	}
	return nil
}
func (j *Journal) Events(ctx context.Context, pending bool) ([]Event, error) {
	clause := "ORDER BY seq DESC LIMIT 100"
	if pending {
		clause = "WHERE delivered=0 ORDER BY seq LIMIT 100"
	}
	rows, e := j.db.QueryContext(ctx, `SELECT id,job_id,body,delivered FROM events `+clause)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var v Event
		var b []byte
		if e = rows.Scan(&v.ID, &v.JobID, &b, &v.Delivered); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v.Status); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (j *Journal) MarkDelivered(ctx context.Context, id string) error {
	_, e := j.db.ExecContext(ctx, `UPDATE events SET delivered=1 WHERE id=?`, id)
	return e
}
func (j *Journal) Pending(ctx context.Context, id string) (bool, error) {
	var n int
	e := j.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE job_id=? AND delivered=0`, id).Scan(&n)
	return n != 0, e
}

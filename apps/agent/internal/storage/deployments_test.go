package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func TestSchemaOneMigrationAndDeploymentTransactions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE kv(key TEXT PRIMARY KEY,value BLOB NOT NULL); INSERT INTO kv VALUES('retained','"yes"'); PRAGMA user_version=1;`)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(dir, protector(t), 2)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var retained string
	if e = s.Get(ctx, "retained", &retained, false); e != nil || retained != "yes" {
		t.Fatal("migration lost state", e)
	}
	now := time.Now().UTC()
	j := contracts.Job{ID: contracts.ID(), Kind: "deployment", CreatedAt: now, LeaseExpiresAt: now.Add(time.Minute)}
	if e = s.RememberJob(ctx, j, contracts.Ack{Accepted: true, Reason: "simulation"}); e != nil {
		t.Fatal(e)
	}
	events, _ := s.DeploymentEvents(ctx)
	if len(events) != 1 {
		t.Fatal("acceptance not atomic")
	}
	if e = s.TransitionDeployment(ctx, j.ID, "accepted", contracts.JobStatus{State: "succeeded", Message: "skip", UpdatedAt: now}); e == nil {
		t.Fatal("skipped stages")
	}
	if e = s.TransitionDeployment(ctx, j.ID, "accepted", contracts.JobStatus{State: "cancelled", Message: "cancel", UpdatedAt: now}); e != nil {
		t.Fatal(e)
	}
	if e = s.TransitionDeployment(ctx, j.ID, "cancelled", contracts.JobStatus{State: "downloading", Message: "rerun", UpdatedAt: now}); e == nil {
		t.Fatal("terminal state reopened")
	}
	projection, e := s.ConsoleData(ctx)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(projection["deployments"])
	var history []DeploymentEvent
	if e = json.Unmarshal(b, &history); e != nil || len(history) != 2 {
		t.Fatalf("safe history absent: %s %v", b, e)
	}
	var version int
	if e = s.db.QueryRow(`PRAGMA user_version`).Scan(&version); e != nil || version != 3 {
		t.Fatal("schema version", version, e)
	}
}

func TestDeploymentAdmissionAndTransitionRollbackTogether(t *testing.T) {
	ctx := context.Background()
	s, e := Open(t.TempDir(), protector(t), 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Now().UTC()
	j := contracts.Job{ID: contracts.ID(), Kind: "deployment", CreatedAt: now, LeaseExpiresAt: now.Add(time.Minute)}
	breakWrites := func() {
		t.Helper()
		_, e = s.db.Exec(`CREATE TRIGGER fail_event BEFORE INSERT ON deployment_events BEGIN SELECT RAISE(ABORT,'simulated disk failure'); END;`)
		if e != nil {
			t.Fatal(e)
		}
	}
	breakWrites()
	if e = s.RememberJob(ctx, j, contracts.Ack{Accepted: true, Reason: "test"}); e == nil {
		t.Fatal("expected failed history write")
	}
	decisions, e := s.Decisions(ctx)
	if e != nil || len(decisions) != 0 {
		t.Fatal("partial admission committed", e)
	}
	if _, e = s.Deployment(ctx, j.ID); e != sql.ErrNoRows {
		t.Fatal("partial checkpoint committed", e)
	}
	if _, e = s.db.Exec(`DROP TRIGGER fail_event`); e != nil {
		t.Fatal(e)
	}
	if e = s.RememberJob(ctx, j, contracts.Ack{Accepted: true, Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	breakWrites()
	if e = s.TransitionDeployment(ctx, j.ID, "accepted", contracts.JobStatus{State: "downloading", Message: "test", UpdatedAt: now}); e == nil {
		t.Fatal("expected failed transition")
	}
	d, e := s.Deployment(ctx, j.ID)
	if e != nil || d.Status.State != "accepted" {
		t.Fatal("checkpoint changed without history", e)
	}
	history, e := s.DeploymentHistory(ctx)
	if e != nil || len(history) != 1 {
		t.Fatal("unexpected history", e)
	}
}

func TestSchemaTwoMigrationPreservesDeploymentAndExcludesUpdateOverlap(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(dir, protector(t), 1)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	job := contracts.Job{ID: contracts.ID(), Kind: "deployment", CreatedAt: now, LeaseExpiresAt: now.Add(time.Minute)}
	if e = s.RememberJob(ctx, job, contracts.Ack{Accepted: true, Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	before, e := s.DeploymentEvents(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`DROP TABLE update_handoffs; PRAGMA user_version=2;`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(dir, protector(t), 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	after, e := s.DeploymentEvents(ctx)
	if e != nil || len(after) != 1 || before[0].ID != after[0].ID {
		t.Fatal("schema migration changed durable deployment history", e)
	}
	update := job
	update.ID = contracts.ID()
	update.Kind = "agent_update"
	if e = s.RememberJob(ctx, update, contracts.Ack{Accepted: true, Reason: "test"}); e == nil {
		t.Fatal("concurrent update admitted")
	}
	if e = s.TransitionDeployment(ctx, job.ID, "accepted", contracts.JobStatus{State: "cancelled", Message: "test", UpdatedAt: now}); e != nil {
		t.Fatal(e)
	}
	if e = s.RememberJob(ctx, update, contracts.Ack{Accepted: true, Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	pending, e := s.UpdateJobs(ctx)
	if e != nil || len(pending) != 1 {
		t.Fatal("update handoff missing", e)
	}
	job.ID = contracts.ID()
	if e = s.RememberJob(ctx, job, contracts.Ack{Accepted: true, Reason: "test"}); e == nil {
		t.Fatal("concurrent deployment admitted")
	}
}

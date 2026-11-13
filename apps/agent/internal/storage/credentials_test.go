package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func TestCredentialRotationAtomicCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := protector(t)
	s, err := Open(dir, p, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	old := contracts.Credential{DeviceID: contracts.ID(), AccessToken: "old-secret", ExpiresAt: time.Now().UTC()}
	next := old
	next.AccessToken = "new-secret"
	next.ExpiresAt = next.ExpiresAt.Add(24 * time.Hour)
	if err = s.Put(ctx, "credential", old, true); err != nil {
		t.Fatal(err)
	}
	intent := contracts.ID()
	if err = s.Put(ctx, "credential-rotation", intent, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_clear BEFORE DELETE ON kv WHEN OLD.key='credential-rotation' BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitCredentialRotation(ctx, next); err == nil {
		t.Fatal("partial commit accepted")
	}
	var got contracts.Credential
	var pending string
	if err = s.Get(ctx, "credential", &got, true); err != nil || got != old {
		t.Fatal("old credential lost", err)
	}
	if err = s.Get(ctx, "credential-rotation", &pending, true); err != nil || pending != intent {
		t.Fatal("intent lost", err)
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_clear"); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitCredentialRotation(ctx, next); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir, p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Get(ctx, "credential", &got, true); err != nil || got != next {
		t.Fatal("new credential missing", err)
	}
	if err = s.Get(ctx, "credential-rotation", &pending, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("intent survived commit", err)
	}
	var raw []byte
	if err = s.db.QueryRow("SELECT value FROM kv WHERE key='credential'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(next.AccessToken)) {
		t.Fatal("plaintext credential stored")
	}
}

package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func protector(t *testing.T) Protector {
	t.Helper()
	p, e := DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestDurabilityAndEncryption(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := protector(t)
	s, e := Open(dir, p, 2)
	if e != nil {
		t.Fatal(e)
	}
	secret := "sensitive-token-never-plaintext"
	if e = s.Put(ctx, "credential", secret, true); e != nil {
		t.Fatal(e)
	}
	if e = s.Enqueue(ctx, "POST", "/agent/heartbeat", map[string]string{"status": "online"}); e != nil {
		t.Fatal(e)
	}
	events, e := s.Pending(ctx, 10)
	if e != nil || len(events) != 1 {
		t.Fatalf("pending %v %v", events, e)
	}
	id := events[0].ID
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("plaintext credential on disk")
	}
	s, e = Open(dir, p, 2)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var got string
	if e = s.Get(ctx, "credential", &got, true); e != nil || got != secret {
		t.Fatalf("credential restore: %q %v", got, e)
	}
	events, e = s.Pending(ctx, 10)
	if e != nil || len(events) != 1 || events[0].ID != id {
		t.Fatal("outbox identity did not survive restart")
	}
	if e = s.DeleteEvent(ctx, id); e != nil {
		t.Fatal(e)
	}
}
func TestCapacityAndReplay(t *testing.T) {
	ctx := context.Background()
	s, e := Open(t.TempDir(), protector(t), 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Enqueue(ctx, "POST", "/x", 1); e != nil {
		t.Fatal(e)
	}
	if e = s.Enqueue(ctx, "POST", "/x", 2); !errors.Is(e, ErrQueueFull) {
		t.Fatalf("expected bounded queue: %v", e)
	}
	j := contracts.Job{ID: contracts.ID(), CreatedAt: time.Now().UTC()}
	a := contracts.Ack{Reason: "disabled"}
	if e = s.RememberJob(ctx, j, a); e != nil {
		t.Fatal(e)
	}
	if e = s.RememberJob(ctx, j, contracts.Ack{Accepted: true, Reason: "changed"}); e != nil {
		t.Fatal(e)
	}
	d, e := s.Decisions(ctx)
	if e != nil || len(d) != 1 || d[0].Ack.Accepted {
		t.Fatal("duplicate changed durable decision")
	}
	if e = s.MarkReported(ctx, j.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.RememberJob(ctx, j, a); e != nil {
		t.Fatal(e)
	}
	d, e = s.Decisions(ctx)
	if e != nil || len(d) != 0 {
		t.Fatal("replayed acknowledged job")
	}
}
func TestLockAndWrongKey(t *testing.T) {
	dir := t.TempDir()
	unlock, e := Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Lock(dir); e == nil {
		t.Fatal("second owner allowed")
	}
	if e = unlock(); e != nil {
		t.Fatal(e)
	}
	unlock, e = Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	p := protector(t)
	b, e := p.Seal([]byte("secret"))
	if e != nil {
		t.Fatal(e)
	}
	b[len(b)-1] ^= 1
	if _, e = p.Open(b); e == nil {
		t.Fatal("tampering accepted")
	}
	q, e := DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = q.Open(b); e == nil {
		t.Fatal("wrong key accepted")
	}
}

package installer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"kiosentra/apps/agent/internal/ipc"
	"kiosentra/packages/contracts"
)

func brokerFixture(t *testing.T) (*Broker, contracts.ArtifactAuthorization, contracts.InstallationLease, *fixtureAdapter, ed25519.PrivateKey) {
	t.Helper()
	e, a, f, _, _ := fixtureEngine(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b, err := NewBroker(*e, map[string]ed25519.PublicKey{"lease-1": pub})
	if err != nil {
		t.Fatal(err)
	}
	l := contracts.InstallationLease{DeviceID: a.DeviceID, JobID: a.JobID, AuthorizationSHA256: contracts.ArtifactDigest(a), IssuedAt: time.Now().UTC().Add(-time.Second), ExpiresAt: time.Now().UTC().Add(time.Minute), KeyID: "lease-1"}
	if err = contracts.SignInstallationLease(&l, priv); err != nil {
		t.Fatal(err)
	}
	return b, a, l, f, priv
}
func submit(t *testing.T, b *Broker, a contracts.ArtifactAuthorization, l contracts.InstallationLease) {
	t.Helper()
	if _, err := b.Handle(context.Background(), BrokerRequest{Method: "submit", JobID: a.JobID, Authorization: &a, Lease: &l}); err != nil {
		t.Fatal(err)
	}
}
func startBroker(t *testing.T, b *Broker) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return cancel
}
func waitState(t *testing.T, b *Broker, id, state string) BrokerStatus {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		v, err := b.Handle(context.Background(), BrokerRequest{Method: "status", JobID: id})
		if err != nil {
			t.Fatal(err)
		}
		if v.Result.State == state {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _ := b.Handle(context.Background(), BrokerRequest{Method: "status", JobID: id})
	t.Fatalf("wanted %s got %+v", state, v)
	return v
}

func TestBrokerDurableSubmitReplayAndRestart(t *testing.T) {
	b, a, l, f, _ := brokerFixture(t)
	submit(t, b, a, l)
	submit(t, b, a, l)
	// Reconstruct from persisted submission before the worker starts.
	b2, err := NewBroker(b.engine, b.leaseKeys)
	if err != nil {
		t.Fatal(err)
	}
	startBroker(t, b2)
	waitState(t, b2, a.JobID, "succeeded")
	submit(t, b2, a, l)
	if f.installs != 1 {
		t.Fatal("duplicate execution", f.installs)
	}
}
func TestBrokerCancellationAndLeaseValidation(t *testing.T) {
	b, a, l, f, key := brokerFixture(t)
	bad := l
	bad.JobID = contracts.ID()
	if _, err := b.Handle(context.Background(), BrokerRequest{Method: "submit", JobID: a.JobID, Authorization: &a, Lease: &bad}); err == nil {
		t.Fatal("accepted unrelated lease")
	}
	submit(t, b, a, l)
	if _, err := b.Handle(context.Background(), BrokerRequest{Method: "cancel", JobID: a.JobID}); err != nil {
		t.Fatal(err)
	}
	l.ExpiresAt = l.ExpiresAt.Add(time.Second)
	_ = contracts.SignInstallationLease(&l, key)
	if _, err := b.Handle(context.Background(), BrokerRequest{Method: "renew", JobID: a.JobID, Lease: &l}); err == nil {
		t.Fatal("revived cancelled work")
	}
	startBroker(t, b)
	waitState(t, b, a.JobID, "cancelled")
	if f.installs != 0 {
		t.Fatal("executed cancelled request")
	}
}
func TestBrokerExpiredLeaseCannotBeRevived(t *testing.T) {
	b, a, l, f, key := brokerFixture(t)
	submit(t, b, a, l)
	now := l.ExpiresAt.Add(time.Second)
	b.engine.Now = func() time.Time { return now }
	l.IssuedAt = now
	l.ExpiresAt = now.Add(time.Minute)
	_ = contracts.SignInstallationLease(&l, key)
	if _, err := b.Handle(context.Background(), BrokerRequest{Method: "renew", JobID: a.JobID, Lease: &l}); err == nil {
		t.Fatal("revived expired lease")
	}
	if err := b.execute(context.Background(), a.JobID); err != nil {
		t.Fatal(err)
	}
	v := waitState(t, b, a.JobID, "cancelled")
	if f.installs != 0 || v.Result.State != "cancelled" {
		t.Fatal("executed expired lease")
	}
}

type waitingAdapter struct {
	identity contracts.MSIIdentity
	started  chan struct{}
	calls    atomic.Int32
}

func (f *waitingAdapter) Inspect(context.Context, string) (contracts.MSIIdentity, error) {
	return f.identity, nil
}
func (f *waitingAdapter) InstalledVersion(context.Context, string) (string, error) { return "", nil }
func (f *waitingAdapter) Install(ctx context.Context, _ string) (int, error) {
	f.calls.Add(1)
	close(f.started)
	<-ctx.Done()
	return -1, ctx.Err()
}
func TestBrokerCancellationWhileInstallingRemainsUncertain(t *testing.T) {
	b, a, l, _, _ := brokerFixture(t)
	f := &waitingAdapter{identity: *a.MSI, started: make(chan struct{})}
	b.engine.Adapter = f
	submit(t, b, a, l)
	startBroker(t, b)
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("installer not started")
	}
	// Status and cancel remain responsive while the execution mutex is held.
	if _, err := b.Handle(context.Background(), BrokerRequest{Method: "cancel", JobID: a.JobID}); err != nil {
		t.Fatal(err)
	}
	waitState(t, b, a.JobID, "needs_operator")
	if f.calls.Load() != 1 {
		t.Fatal("wrong execution count")
	}
}
func TestBrokerRecoveryDoesNotNeedUnexpiredAuthorization(t *testing.T) {
	b, a, l, f, _ := brokerFixture(t)
	submit(t, b, a, l)
	if err := b.engine.Journal.save(Result{JobID: a.JobID, State: "installing", UpdatedAt: time.Now().UTC()}, contracts.ArtifactDigest(a)); err != nil {
		t.Fatal(err)
	}
	b.engine.Now = func() time.Time { return a.ExpiresAt.Add(time.Hour) }
	reopened, err := NewBroker(b.engine, b.leaseKeys)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.execute(context.Background(), a.JobID); err != nil {
		t.Fatal(err)
	}
	waitState(t, reopened, a.JobID, "needs_operator")
	if f.installs != 0 {
		t.Fatal("replayed uncertain install")
	}
}
func TestBrokerEncryptedIPCRejectsWrongKeyAndDomain(t *testing.T) {
	b, a, l, _, _ := brokerFixture(t)
	dir, err := os.MkdirTemp("", "kb-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	// Unix socket paths must fit the platform's short path limit.
	options := ipc.Options{StateDir: dir, Development: true, Key: bytes.Repeat([]byte{9}, 32)}
	listener, err := ipc.Listen(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ipc.ServeRPC(ctx, listener, options.Key, BrokerDomain, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var r BrokerRequest
			if err := contracts.Decode(bytes.NewReader(raw), &r); err != nil {
				return nil, err
			}
			return b.Handle(ctx, r)
		})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	request := BrokerRequest{Method: "submit", JobID: a.JobID, Authorization: &a, Lease: &l}
	client := ipc.RPCClient{Options: options, Domain: BrokerDomain}
	var result BrokerStatus
	wrong := client
	wrong.Options.Key = bytes.Repeat([]byte{8}, 32)
	if wrong.Call(ctx, request, &result) == nil {
		t.Fatal("wrong key accepted")
	}
	wrong = client
	wrong.Domain = "console"
	if wrong.Call(ctx, request, &result) == nil {
		t.Fatal("wrong domain accepted")
	}
	if err = client.Call(ctx, request, &result); err != nil || result.Result.State != "queued" {
		t.Fatalf("%+v %v", result, err)
	}
}

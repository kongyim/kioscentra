package deployment

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"kiosentra/apps/agent/internal/api"
	"kiosentra/apps/agent/internal/storage"
	"kiosentra/packages/contracts"
)

type remoteFixture struct {
	now                   time.Time
	statuses              map[string]contracts.JobStatus
	renewals              map[string]contracts.Lease
	loseStatus, loseRenew bool
	unavailable           bool
}

func (r *remoteFixture) Status(_ context.Context, _ string, id string, s contracts.JobStatus) error {
	if r.unavailable {
		return &api.HTTPError{Status: 410}
	}
	if old, ok := r.statuses[id]; ok && old != s {
		return errors.New("report mutated on retry")
	}
	r.statuses[id] = s
	if r.loseStatus {
		r.loseStatus = false
		return errors.New("response lost")
	}
	return nil
}
func (r *remoteFixture) Renew(_ context.Context, _ string, id string, _ contracts.Renew) (contracts.Lease, error) {
	if r.unavailable {
		return contracts.Lease{}, &api.HTTPError{Status: 410}
	}
	lease, ok := r.renewals[id]
	if !ok {
		lease = contracts.Lease{LeaseExpiresAt: r.now.Add(300 * time.Second)}
		r.renewals[id] = lease
	}
	if r.loseRenew {
		r.loseRenew = false
		return contracts.Lease{}, errors.New("renew response lost")
	}
	return lease, nil
}
func fixture(t *testing.T, scenario string, acknowledged ...bool) (*Engine, contracts.Job, func()) {
	t.Helper()
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p, err := storage.DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(dir, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := &remoteFixture{now: now, statuses: map[string]contracts.JobStatus{}, renewals: map[string]contracts.Lease{}}
	e := &Engine{Store: store, Remote: r, Enabled: true, PublicKey: pub, DeviceID: contracts.ID(), Now: func() time.Time { return r.now }}
	j := contracts.Job{ID: contracts.ID(), Kind: "deployment", TargetVersion: "2.0", CreatedAt: now, LeaseExpiresAt: now.Add(time.Minute)}
	if err = contracts.SignSimulation(e.DeviceID, &j, scenario, priv); err != nil {
		t.Fatal(err)
	}
	ack, err := e.Decision(ctx, j)
	if err != nil || !ack.Accepted {
		t.Fatalf("admission: %+v %v", ack, err)
	}
	if err = store.RememberJob(ctx, j, ack); err != nil {
		t.Fatal(err)
	}
	if len(acknowledged) == 0 || acknowledged[0] {
		if err = store.MarkReported(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { e.Store.Close() })
	reopen := func() {
		t.Helper()
		if err = e.Store.Close(); err != nil {
			t.Fatal(err)
		}
		e.Store, err = storage.Open(dir, p, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	return e, j, reopen
}
func TestScenariosResumeAtEveryCheckpoint(t *testing.T) {
	cases := map[string]string{"success": "succeeded", "download_failure": "failed", "hash_failure": "failed", "install_failure": "rolled_back", "validation_failure": "rolled_back", "rollback_failure": "failed", "timeout": "failed"}
	for scenario, want := range cases {
		t.Run(scenario, func(t *testing.T) {
			e, j, reopen := fixture(t, scenario)
			ctx := context.Background()
			// Deployment history does not compete with a saturated telemetry queue.
			if err := e.Store.Enqueue(ctx, "POST", "/agent/metrics", nil); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 10; i++ {
				reopen()
				if err := e.Advance(ctx); err != nil {
					t.Fatal(err)
				}
				d, err := e.Store.Deployment(ctx, j.ID)
				if err != nil {
					t.Fatal(err)
				}
				if contracts.TerminalState(d.Status.State) {
					if d.Status.State != want {
						t.Fatalf("got %s want %s", d.Status.State, want)
					}
					before, _ := e.Store.DeploymentHistory(ctx)
					if err = e.Store.RememberJob(ctx, j, contracts.Ack{Accepted: true, Reason: "duplicate"}); err != nil {
						t.Fatal(err)
					}
					if err = e.Advance(ctx); err != nil {
						t.Fatal(err)
					}
					after, _ := e.Store.DeploymentHistory(ctx)
					if len(before) != len(after) {
						t.Fatal("terminal job ran twice")
					}
					for _, v := range after {
						if v.Delivery != "delivered" {
							t.Fatal("report not delivered")
						}
					}
					return
				}
			}
			t.Fatal("did not terminate")
		})
	}
}
func TestLostStatusAndRenewalResponsesAcrossRestart(t *testing.T) {
	e, j, reopen := fixture(t, "success")
	ctx := context.Background()
	r := e.Remote.(*remoteFixture)
	r.loseStatus = true
	if err := e.Advance(ctx); err == nil {
		t.Fatal("expected lost status response")
	}
	d, _ := e.Store.Deployment(ctx, j.ID)
	if d.Status.State != "accepted" {
		t.Fatal("advanced before report acknowledgement")
	}
	reopen()
	r.loseRenew = true
	if err := e.Advance(ctx); err == nil {
		t.Fatal("expected lost renewal")
	}
	reopen()
	if err := e.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.renewals) != 1 || len(r.statuses) != 2 {
		t.Fatalf("replayed with new IDs: %+v", r)
	}
}
func TestFailClosedAndCancellation(t *testing.T) {
	for _, reason := range []string{"expiry", "disabled", "key_changed", "deadline", "technician", "server_expiry"} {
		t.Run(reason, func(t *testing.T) {
			e, j, reopen := fixture(t, "success")
			ctx := context.Background()
			r := e.Remote.(*remoteFixture)
			want := "cancelled"
			switch reason {
			case "expiry":
				r.now = r.now.Add(61 * time.Second)
			case "disabled":
				e.Enabled = false
			case "key_changed":
				e.PublicKey = make([]byte, 32)
				want = "failed"
			case "deadline":
				r.now = r.now.Add(11 * time.Minute)
				if err := e.Store.SaveDeploymentLease(ctx, j.ID, r.now.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
				want = "failed"
			case "technician":
				if err := e.Cancel(ctx); err != nil {
					t.Fatal(err)
				}
			case "server_expiry":
				r.unavailable = true
			}
			reopen()
			if err := e.Advance(ctx); err != nil {
				t.Fatal(err)
			}
			d, _ := e.Store.Deployment(ctx, j.ID)
			if d.Status.State != want {
				t.Fatalf("got %s want %s", d.Status.State, want)
			}
			if reason == "server_expiry" {
				if err := e.Flush(ctx); err != nil {
					t.Fatal(err)
				}
				events, _ := e.Store.DeploymentHistory(ctx)
				for _, v := range events {
					if v.Delivery != "unavailable" {
						t.Fatal(v)
					}
				}
			}
		})
	}
}
func TestAdmissionRejectsUntrustedOrConcurrentWork(t *testing.T) {
	e, j, _ := fixture(t, "success")
	ctx := context.Background()
	ack, err := e.Decision(ctx, j)
	if err != nil || ack.Accepted {
		t.Fatal("concurrent admission allowed")
	}
	if err = e.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*contracts.Job){func(j *contracts.Job) { j.ID = contracts.ID() }, func(j *contracts.Job) { j.Kind = "agent_update" }, func(j *contracts.Job) { j.Release.PackageType = "msi" }, func(j *contracts.Job) { j.Release.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64)) }, func(j *contracts.Job) { j.Release.URL = "https://other.example/payload" }} {
		copy := j
		r := *j.Release
		copy.Release = &r
		mutate(&copy)
		ack, err := e.Decision(ctx, copy)
		if err != nil || ack.Accepted {
			t.Fatalf("accepted mutated job: %v %v", ack, err)
		}
	}
	e.Enabled = false
	ack, err = e.Decision(ctx, j)
	if err != nil || ack.Accepted {
		t.Fatal("disabled admission allowed")
	}
}

func TestNoProgressWithCancelledContext(t *testing.T) {
	e, j, _ := fixture(t, "success")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Advance(ctx); err == nil {
		t.Fatal("cancelled cycle progressed")
	}
	d, err := e.Store.Deployment(context.Background(), j.ID)
	if err != nil || d.Status.State != "accepted" {
		t.Fatal("cancelled cycle changed state", err)
	}
	if len(e.Remote.(*remoteFixture).renewals) != 0 {
		t.Fatal("cancelled cycle renewed")
	}
}

func TestNoProgressBeforeDurableAcknowledgement(t *testing.T) {
	e, j, _ := fixture(t, "success", false)
	ctx := context.Background()
	if err := e.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := e.Store.Deployment(ctx, j.ID)
	if err != nil || d.Status.State != "accepted" {
		t.Fatal("unacknowledged job progressed", err)
	}
	r := e.Remote.(*remoteFixture)
	if len(r.statuses) != 0 || len(r.renewals) != 0 {
		t.Fatal("reported or renewed unacknowledged job")
	}
}
func TestInvalidRenewalCannotAdvance(t *testing.T) {
	for _, offset := range []time.Duration{-time.Second, time.Hour} {
		t.Run(offset.String(), func(t *testing.T) {
			e, j, _ := fixture(t, "success")
			ctx := context.Background()
			d, err := e.Store.Deployment(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			r := e.Remote.(*remoteFixture)
			r.renewals[d.RenewalID] = contracts.Lease{LeaseExpiresAt: r.now.Add(offset)}
			if err = e.Advance(ctx); err != nil {
				t.Fatal(err)
			}
			d, err = e.Store.Deployment(ctx, j.ID)
			if err != nil || d.Status.State != "cancelled" {
				t.Fatal("invalid renewal progressed", err)
			}
		})
	}
}

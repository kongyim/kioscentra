package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"kiosentra/apps/agent/internal/api"
	"kiosentra/packages/contracts"
)

func TestCredentialRenewalScheduleAndInvalidResponses(t *testing.T) {
	for _, kind := range []string{"not_due", "expired", "wrong_device", "same_token", "expired_response", "shorter_expiry", "pending_after_expiry"} {
		t.Run(kind, func(t *testing.T) {
			a := phase3Agent(t)
			ctx := context.Background()
			a.identity.PublicKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
			a.credential = contracts.Credential{DeviceID: contracts.ID(), AccessToken: "old-token", ExpiresAt: time.Now().UTC().Add(30 * time.Minute)}
			next := a.credential
			next.AccessToken = "new-token"
			next.ExpiresAt = time.Now().UTC().Add(24 * time.Hour)
			switch kind {
			case "not_due":
				a.credential.ExpiresAt = time.Now().UTC().Add(2 * time.Hour)
			case "expired", "pending_after_expiry":
				a.credential.ExpiresAt = time.Now().UTC().Add(-time.Minute)
			case "wrong_device":
				next.DeviceID = contracts.ID()
			case "same_token":
				next.AccessToken = a.credential.AccessToken
			case "expired_response":
				next.ExpiresAt = time.Now().UTC().Add(-time.Minute)
			case "shorter_expiry":
				next.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/v1/agent/credentials/rotate" {
					t.Error("wrong route")
				}
				_ = json.NewEncoder(w).Encode(next)
			}))
			defer server.Close()
			a.Client = api.New(server.URL, time.Second)
			a.Client.SetCredential(a.credential)
			if err := a.Store.Put(ctx, "credential", a.credential, true); err != nil {
				t.Fatal(err)
			}
			if kind == "pending_after_expiry" {
				if err := a.Store.Put(ctx, "credential-rotation", pendingRotation{RequestID: contracts.ID(), DeviceID: a.credential.DeviceID, PublicKey: a.identity.PublicKey}, true); err != nil {
					t.Fatal(err)
				}
			}
			old := a.credential
			err := a.renewCredential(ctx)
			if kind == "not_due" {
				if err != nil || calls.Load() != 0 {
					t.Fatal("early renewal", err, calls.Load())
				}
				return
			}
			if kind == "pending_after_expiry" {
				if err != nil || calls.Load() != 1 || a.credential != next {
					t.Fatal("pending recovery failed", err)
				}
				return
			}
			if err == nil || a.credential != old {
				t.Fatal("invalid renewal accepted")
			}
			if kind == "expired" && calls.Load() != 0 {
				t.Fatal("expired token initiated new rotation")
			}
			var stored contracts.Credential
			if e := a.Store.Get(ctx, "credential", &stored, true); e != nil || stored != old {
				t.Fatal("invalid response persisted", e)
			}
		})
	}
}

func TestRotationRetryDoesNotWaitForHourlySample(t *testing.T) {
	a := phase3Agent(t)
	a.Config.Interval = time.Hour
	old := contracts.Credential{DeviceID: contracts.ID(), AccessToken: "old", ExpiresAt: time.Now().UTC().Add(90 * time.Minute)}
	next := old
	next.AccessToken = "new"
	next.ExpiresAt = time.Now().UTC().Add(24 * time.Hour)
	var rotations atomic.Int32
	completed := make(chan struct{}, 1)
	var requestID atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/enrollments":
			_ = json.NewEncoder(w).Encode(old)
		case "/api/v1/agent/credentials/rotate":
			if rotations.Add(1) == 1 {
				requestID.Store(r.Header.Get("Idempotency-Key"))
				w.WriteHeader(503)
				return
			}
			if r.Header.Get("Idempotency-Key") != requestID.Load().(string) {
				t.Error("retry ID changed")
			}
			_ = json.NewEncoder(w).Encode(next)
		case "/api/v1/agent/policy":
			_ = json.NewEncoder(w).Encode(contracts.Policy{SchemaVersion: 1, Revision: "test", Arguments: []string{}, MonitorIntervalSeconds: 30, RestartPolicy: "never", StartupEntries: []string{}})
		case "/api/v1/agent/jobs":
			_ = json.NewEncoder(w).Encode(contracts.JobPage{Items: []contracts.Job{}})
			select {
			case completed <- struct{}{}:
			default:
			}
		default:
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	a.Config.ServerURL = server.URL
	a.Config.EnrollmentToken = "test-enrollment"
	a.Client = api.New(server.URL, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	select {
	case <-completed:
	case <-time.After(8 * time.Second):
		cancel()
		<-done
		t.Fatal("rotation retry waited for hourly telemetry")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if rotations.Load() != 2 || a.credential != next {
		t.Fatal("rotation did not finish")
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"kiosentra/tools/mock-server/mock"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kiosentra/apps/agent/internal/api"
	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/apps/agent/internal/diagnostics"
	"kiosentra/apps/agent/internal/ipc"
	"kiosentra/apps/agent/internal/storage"
	"kiosentra/packages/contracts"
)

type inventoryFixture struct{ calls int }

func (f *inventoryFixture) Collect(context.Context, string) (contracts.Inventory, []diagnostics.Result, error) {
	f.calls++
	return contracts.Inventory{CollectedAt: time.Now().UTC(), Hostname: "host", OS: "windows", Architecture: "amd64", Software: []contracts.Software{{Name: "Kiosk", Version: "1"}}, Runtimes: []contracts.Software{}, Networks: []contracts.Network{}}, []diagnostics.Result{}, nil
}

type stoppedProcess struct{}

func (stoppedProcess) Observe(context.Context, string) ([]contracts.ProcessMetric, *bool, error) {
	running := false
	return []contracts.ProcessMetric{}, &running, nil
}

type expiredCollector struct{}

func (expiredCollector) Collect(ctx context.Context) (contracts.Metrics, uint64, error) {
	<-ctx.Done()
	return contracts.Metrics{}, 0, ctx.Err()
}
func phase3Agent(t *testing.T) *Agent {
	t.Helper()
	p, e := storage.DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	s, e := storage.Open(dir, p, 100)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	c := configuration.Config{ServerURL: "http://127.0.0.1:8080", StateDir: dir, Development: true, Interval: time.Second, RequestTimeout: time.Second, MaxQueue: 100}
	a, e := New(c, s, api.New(c.ServerURL, time.Second), fixedCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestPhase3OfflineReportsAndInventoryCadence(t *testing.T) {
	a := phase3Agent(t)
	ctx := context.Background()
	inv := &inventoryFixture{}
	a.Inventory = inv
	a.Processes = stoppedProcess{}
	a.PolicyObserver = func(_ context.Context, p contracts.Policy) contracts.PolicyReport {
		return contracts.PolicyReport{Revision: p.Revision, ObservedAt: time.Now().UTC(), Compliance: "drifted", Drift: []string{"shell differs"}, Errors: []string{}}
	}
	policy := contracts.Policy{SchemaVersion: 1, Revision: "r1", KioskExecutable: "C:\\kiosk.exe", RestartPolicy: "never", MonitorIntervalSeconds: 30}
	if e := a.Store.Put(ctx, "policy", policy, false); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e := a.sample(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if inv.calls != 1 {
		t.Fatal("inventory not throttled")
	}
	events, e := a.Store.Pending(ctx, 100)
	if e != nil {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Path]++
		if event.Path == "/agent/heartbeat" {
			var h contracts.Heartbeat
			if e = json.Unmarshal(event.Body, &h); e != nil {
				t.Fatal(e)
			}
			if h.KioskRunning == nil || *h.KioskRunning || h.Status != "degraded" || h.Health != "kiosk_stopped" {
				t.Fatal(h)
			}
		}
	}
	if counts["/agent/inventory"] != 1 || counts["/agent/policy/report"] != 2 || counts["/agent/metrics"] != 2 {
		t.Fatal(counts)
	}
	policy.KioskExecutable = "C:\\other.exe"
	if e = a.Store.Put(ctx, "policy", policy, false); e != nil {
		t.Fatal(e)
	}
	if e = a.sample(ctx); e != nil {
		t.Fatal(e)
	}
	if inv.calls != 2 {
		t.Fatal("new executable did not refresh inventory")
	}
}
func TestCollectorTimeoutStillQueuesHeartbeat(t *testing.T) {
	a := phase3Agent(t)
	a.Collector = expiredCollector{}
	a.Config.RequestTimeout = time.Millisecond
	if e := a.sample(context.Background()); e != nil {
		t.Fatal(e)
	}
	events, e := a.Store.Pending(context.Background(), 10)
	if e != nil {
		t.Fatal(e)
	}
	if len(events) != 1 || events[0].Path != "/agent/heartbeat" {
		t.Fatal(events)
	}
	var h contracts.Heartbeat
	json.Unmarshal(events[0].Body, &h)
	if h.Status != "degraded" {
		t.Fatal(h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = a.sample(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

// The mock validates the actual closed v1 wire objects. Losing an inventory
// response must reuse the outbox request ID just like metrics do.
func TestPhase3SyncReplaysInventoryAndReportsPolicy(t *testing.T) {
	a := phase3Agent(t)
	m, e := mock.New(mock.Options{EnrollmentToken: "phase3-enrollment", AdminToken: "phase3-admin-token"})
	if e != nil {
		t.Fatal(e)
	}
	lost := false
	inventoryIDs := []string{}
	policyReports := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/agent/inventory" {
			inventoryIDs = append(inventoryIDs, r.Header.Get("Idempotency-Key"))
			if !lost {
				lost = true
				rr := httptest.NewRecorder()
				m.ServeHTTP(rr, r)
				if rr.Code != 204 {
					t.Errorf("inventory rejected: %d %s", rr.Code, rr.Body.String())
				}
				w.WriteHeader(503)
				return
			}
		}
		if r.URL.Path == "/api/v1/agent/policy/report" {
			policyReports++
		}
		m.ServeHTTP(w, r)
	}))
	defer server.Close()
	a.Config.ServerURL = server.URL
	a.Config.EnrollmentToken = "phase3-enrollment"
	a.Client = api.New(server.URL, time.Second)
	a.Inventory = &inventoryFixture{}
	a.PolicyObserver = func(_ context.Context, p contracts.Policy) contracts.PolicyReport {
		return contracts.PolicyReport{Revision: p.Revision, ObservedAt: time.Now().UTC(), Compliance: "unknown", Drift: []string{}, Errors: []string{"unsupported"}}
	}
	ctx := context.Background()
	if e = a.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Cycle(ctx); e == nil {
		t.Fatal("lost response should fail sync")
	}
	a.nextAttempt = time.Time{}
	if e = a.Cycle(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Cycle(ctx); e != nil {
		t.Fatal(e)
	}
	if len(inventoryIDs) != 2 || inventoryIDs[0] == "" || inventoryIDs[0] != inventoryIDs[1] {
		t.Fatal(inventoryIDs)
	}
	if policyReports != 1 {
		t.Fatal("policy report not delivered", policyReports)
	}
	events, e := a.Store.Pending(ctx, 100)
	if e != nil || len(events) != 0 {
		t.Fatal(events, e)
	}
}

func TestInvalidIPCDoesNotAffectCollection(t *testing.T) {
	a := phase3Agent(t)
	t.Setenv("KIOSENTRA_IPC_KEY", "invalid")
	if _, e := a.startConsole(context.Background(), &ipc.LogBuffer{}); e == nil {
		t.Fatal("invalid IPC configuration accepted")
	}
	if e := a.sample(context.Background()); e != nil {
		t.Fatal("collection affected", e)
	}
}

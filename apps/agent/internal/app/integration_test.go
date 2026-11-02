package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kiosentra/apps/agent/internal/api"
	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/apps/agent/internal/storage"
	"kiosentra/packages/contracts"
	"kiosentra/tools/mock-server/mock"
)

type fixedCollector struct{}

func (fixedCollector) Collect(context.Context) (contracts.Metrics, uint64, error) {
	return contracts.Metrics{CollectedAt: time.Now().UTC(), CPUPercent: 12, MemoryTotalBytes: 100, MemoryUsedBytes: 40, DiskTotalBytes: 200, DiskUsedBytes: 50, Processes: []contracts.ProcessMetric{}}, 42, nil
}
func TestOfflineRestartEnrollmentReportingAndRejection(t *testing.T) {
	ctx := context.Background()
	m, e := mock.New(mock.Options{EnrollmentToken: "integration-enrollment", AdminToken: "integration-admin-token"})
	if e != nil {
		t.Fatal(e)
	}
	var offline atomic.Bool
	offline.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		m.ServeHTTP(w, r)
	}))
	defer server.Close()
	dir := t.TempDir()
	p, e := storage.DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	c := configuration.Config{ServerURL: server.URL, StateDir: dir, Development: true, EnrollmentToken: "integration-enrollment", Interval: time.Second, RequestTimeout: time.Second, MaxQueue: 20}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	open := func() (*Agent, *storage.Store) {
		s, e := storage.Open(dir, p, 20)
		if e != nil {
			t.Fatal(e)
		}
		a, e := New(c, s, api.New(server.URL, time.Second), fixedCollector{}, logger)
		if e != nil {
			t.Fatal(e)
		}
		if e = a.Initialize(ctx); e != nil {
			t.Fatal(e)
		}
		return a, s
	}
	a, s := open()
	if e = a.Cycle(ctx); e == nil {
		t.Fatal("offline sync unexpectedly succeeded")
	}
	pending, e := s.Pending(ctx, 100)
	if e != nil || len(pending) != 2 {
		t.Fatalf("offline queue: %d %v", len(pending), e)
	}
	pub := a.identity.PublicKey
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	offline.Store(false)
	a, s = open()
	defer s.Close()
	if a.identity.PublicKey != pub {
		t.Fatal("identity regenerated after restart")
	}
	if e = a.Cycle(ctx); e != nil {
		t.Fatal(e)
	}
	id := a.credential.DeviceID
	pending, e = s.Pending(ctx, 100)
	if e != nil || len(pending) != 0 {
		t.Fatal("queue did not drain")
	}
	request := func(method, path string, in, out any) {
		t.Helper()
		b, _ := json.Marshal(in)
		req, e := http.NewRequest(method, server.URL+path, bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer integration-admin-token")
		req.Header.Set("Content-Type", "application/json")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			t.Fatalf("dev request status %d", res.StatusCode)
		}
		if out != nil {
			if e = json.NewDecoder(res.Body).Decode(out); e != nil {
				t.Fatal(e)
			}
		}
	}
	var job contracts.Job
	request("POST", "/dev/jobs", mock.CreateJob{DeviceID: id, Kind: "deployment", TargetVersion: "2.0.0", LeaseSeconds: 300}, &job)
	if e = a.Cycle(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Cycle(ctx); e != nil {
		t.Fatal(e)
	}
	var views []mock.DeviceView
	request("GET", "/dev/state", nil, &views)
	if len(views) != 1 || len(views[0].Reports) != 8 || len(views[0].Jobs) != 1 {
		t.Fatalf("unexpected state: %#v", views)
	}
	if views[0].Jobs[0].Ack == nil || views[0].Jobs[0].Ack.Accepted {
		t.Fatal("deployment not safely rejected")
	}
	decisions, e := s.Decisions(ctx)
	if e != nil || len(decisions) != 0 {
		t.Fatal("acknowledgement not committed")
	}
	var policy contracts.Policy
	if e = s.Get(ctx, "policy", &policy, false); e != nil || policy.Revision != "mock-1" {
		t.Fatal("policy not cached")
	}
	// Credential is restored without needing the enrollment token.
	c.EnrollmentToken = ""
	b, e := New(c, s, api.New(server.URL, time.Second), fixedCollector{}, logger)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	if b.credential.DeviceID != id {
		t.Fatal("enrollment identity changed")
	}
	if e = b.Cycle(ctx); e != nil {
		t.Fatal(e)
	}
	c.ServerURL = "https://different.example"
	b, e = New(c, s, api.New(c.ServerURL, time.Second), fixedCollector{}, logger)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Initialize(ctx); e == nil {
		t.Fatal("credentials allowed to move to another origin")
	}
}
func TestBackoffBounds(t *testing.T) {
	for i := 0; i < 50; i++ {
		d := Backoff(i)
		if d < time.Second || d > 257*time.Second {
			t.Fatalf("unbounded backoff %s", d)
		}
	}
}

func TestLostResponsesSurviveRestart(t *testing.T) {
	ctx := context.Background()
	m, e := mock.New(mock.Options{EnrollmentToken: "lost-response-enrollment", AdminToken: "lost-response-admin"})
	if e != nil {
		t.Fatal(e)
	}
	var loseMetric, loseAck atomic.Bool
	loseMetric.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lose := r.URL.Path == "/api/v1/agent/metrics" && loseMetric.Swap(false)
		if strings.HasSuffix(r.URL.Path, "/ack") && loseAck.Swap(false) {
			lose = true
		}
		if lose {
			rr := httptest.NewRecorder()
			m.ServeHTTP(rr, r)
			if rr.Code != 204 {
				t.Errorf("expected processed request, got %d", rr.Code)
			}
			w.WriteHeader(503)
			return
		}
		m.ServeHTTP(w, r)
	}))
	defer server.Close()
	p, e := storage.DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	c := configuration.Config{ServerURL: server.URL, StateDir: t.TempDir(), EnrollmentToken: "lost-response-enrollment", Development: true, Interval: time.Second, RequestTimeout: time.Second, MaxQueue: 20}
	open := func() (*Agent, *storage.Store) {
		s, e := storage.Open(c.StateDir, p, 20)
		if e != nil {
			t.Fatal(e)
		}
		a, e := New(c, s, api.New(server.URL, time.Second), fixedCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if e != nil {
			t.Fatal(e)
		}
		if e = a.Initialize(ctx); e != nil {
			t.Fatal(e)
		}
		return a, s
	}
	a, s := open()
	if e = a.Cycle(ctx); e == nil {
		t.Fatal("lost response should leave queued event")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	a, s = open()
	if e = a.Cycle(ctx); e != nil {
		t.Fatal(e)
	}
	inspect := func() []mock.DeviceView {
		req, _ := http.NewRequest("GET", server.URL+"/dev/state", nil)
		req.Header.Set("Authorization", "Bearer lost-response-admin")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var views []mock.DeviceView
		if e = json.NewDecoder(res.Body).Decode(&views); e != nil {
			t.Fatal(e)
		}
		return views
	}
	views := inspect()
	if len(views) != 1 || len(views[0].Reports) != 4 {
		t.Fatalf("lost response duplicated reports: %#v", views)
	}
	body, _ := json.Marshal(mock.CreateJob{DeviceID: a.credential.DeviceID, Kind: "agent_update", TargetVersion: "2.0", LeaseSeconds: 300})
	req, _ := http.NewRequest("POST", server.URL+"/dev/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer lost-response-admin")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatal(res.StatusCode)
	}
	loseAck.Store(true)
	if e = a.Cycle(ctx); e == nil {
		t.Fatal("lost acknowledgement should be retried")
	}
	decisions, e := s.Decisions(ctx)
	if e != nil || len(decisions) != 1 {
		t.Fatal("decision was lost")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	a, s = open()
	defer s.Close()
	if e = a.Cycle(ctx); e != nil {
		t.Fatal(e)
	}
	decisions, e = s.Decisions(ctx)
	if e != nil || len(decisions) != 0 {
		t.Fatal("acknowledgement retry not committed")
	}
	views = inspect()
	if len(views[0].Jobs) != 1 || views[0].Jobs[0].Ack == nil || views[0].Jobs[0].Ack.Accepted {
		t.Fatal("job replay was not safely rejected")
	}
	release, e := a.Client.LatestRelease(ctx)
	if e != nil || release == nil || release.PackageType != "simulation" {
		t.Fatal("missing simulated update metadata", e)
	}
	// A running loop exits promptly when its owner cancels, even with a long collection interval.
	a.Config.Interval = time.Hour
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Run(runCtx) }()
	cancel()
	select {
	case e := <-done:
		if e != nil && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not respect cancellation")
	}
}

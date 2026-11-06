package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
	"kiosentra/apps/agent/internal/updater"
	"kiosentra/packages/contracts"
	"kiosentra/tools/mock-server/mock"
)

func TestUpdateRecoveryOfflineAndLostReportsAcrossServiceRestart(t *testing.T) {
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	backend, err := mock.New(mock.Options{EnrollmentToken: "update-enrollment-token", AdminToken: "update-administrator-token", SimulationPrivateKey: base64.StdEncoding.EncodeToString(priv)})
	if err != nil {
		t.Fatal(err)
	}
	var offline, lostReport, lostRenew atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		recorder := httptest.NewRecorder()
		backend.ServeHTTP(recorder, r)
		lose := false
		if recorder.Code < 300 {
			if strings.HasSuffix(r.URL.Path, "/renew") {
				lose = lostRenew.CompareAndSwap(false, true)
			}
			if strings.HasSuffix(r.URL.Path, "/update-status") {
				lose = lostReport.CompareAndSwap(false, true)
			}
		}
		if lose {
			w.WriteHeader(503)
			return
		}
		for k, v := range recorder.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(recorder.Code)
		w.Write(recorder.Body.Bytes())
	}))
	defer server.Close()
	dir := t.TempDir()
	p, err := storage.DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	c := configuration.Config{ServerURL: server.URL, StateDir: dir, Development: true, EnrollmentToken: "update-enrollment-token", Interval: time.Second, RequestTimeout: time.Second, MaxQueue: 100, SimulatedUpdates: true, SimulationPublicKey: base64.StdEncoding.EncodeToString(pub)}
	launches := 0
	open := func() (*Agent, *storage.Store) {
		t.Helper()
		s, e := storage.Open(dir, p, 100)
		if e != nil {
			t.Fatal(e)
		}
		a, e := New(c, s, api.New(server.URL, time.Second), fixedCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if e != nil {
			t.Fatal(e)
		}
		a.LaunchUpdater = func(string, ed25519.PublicKey) error { launches++; return nil }
		if e = a.Initialize(ctx); e != nil {
			t.Fatal(e)
		}
		return a, s
	}
	a, s := open()
	defer func() { s.Close() }()
	if err = a.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	create := mock.CreateJob{DeviceID: a.credential.DeviceID, Kind: "agent_update", TargetVersion: "2.0", LeaseSeconds: 300, Scenario: "health_timeout"}
	b, _ := json.Marshal(create)
	request := httptest.NewRequest("POST", "/dev/jobs", bytes.NewReader(b))
	request.Header.Set("Authorization", "Bearer update-administrator-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	backend.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var job contracts.Job
	if err = json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	// Renewal reply loss cannot launch a helper or silently extend the original request.
	if err = a.Cycle(ctx); err == nil || launches != 0 {
		t.Fatal("lost renewal launched helper", err, launches)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	a, s = open()
	if err = a.Cycle(ctx); err == nil || launches != 1 {
		t.Fatal("expected first status reply loss", err, launches)
	}
	// Simulate the service being stopped and central being unavailable. Only the helper runs.
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	offline.Store(true)
	if err = updater.Run(ctx, dir, pub); err != nil {
		t.Fatal(err)
	}
	journal, err := updater.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	r, err := journal.Get(ctx, job.ID)
	if err != nil || r.State != "rolled_back" {
		t.Fatal("offline recovery failed", r.State, err)
	}
	target, err := journal.Target(ctx)
	if err != nil || target.Current != Version || target.KnownGood != Version {
		t.Fatal("known-good not restored", target, err)
	}
	a, s = open()
	if err = a.Cycle(ctx); err == nil {
		t.Fatal("expected offline sync")
	}
	offline.Store(false)
	a.nextAttempt = time.Time{}
	if err = a.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.UpdateJobs(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal("completed handoff retained", err)
	}
	events, err := journal.Events(ctx, false)
	if err != nil || len(events) != 5 {
		t.Fatal("unexpected update history", len(events), err)
	}
	for _, v := range events {
		if !v.Delivered {
			t.Fatal("report not delivered", v)
		}
	}
	request = httptest.NewRequest("GET", "/dev/state", nil)
	request.Header.Set("Authorization", "Bearer update-administrator-token")
	response = httptest.NewRecorder()
	backend.ServeHTTP(response, request)
	var views []mock.DeviceView
	if err = json.Unmarshal(response.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	updateReports := 0
	for _, v := range views[0].Reports {
		if v.Path == "/agent/update-status" {
			updateReports++
		}
	}
	if updateReports != 5 {
		t.Fatalf("lost response duplicated reports: %d", updateReports)
	}
	if len(views[0].Reports) <= updateReports {
		t.Fatal("telemetry absent")
	}
}

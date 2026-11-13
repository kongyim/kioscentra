package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kiosentra/apps/agent/internal/api"
	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/apps/agent/internal/storage"
	"kiosentra/apps/server/central"
)

func TestPersistentCentralWithAgentRestartAndLostResponse(t *testing.T) {
	ctx := context.Background()
	options := central.Options{DatabasePath: filepath.Join(t.TempDir(), "central.db"), EnrollmentToken: strings.Repeat("e", 32), AdminToken: strings.Repeat("a", 32), CredentialKey: bytes.Repeat([]byte{7}, 32)}
	backend, err := central.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { backend.Close() }()
	var mu sync.Mutex
	offline, loseResponse := false, true
	// Keep the origin stable while stopping/reopening the backing central server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if offline {
			w.WriteHeader(503)
			return
		}
		if loseResponse && r.URL.Path == "/api/v1/agent/metrics" {
			loseResponse = false
			receipt := httptest.NewRecorder()
			backend.ServeHTTP(receipt, r)
			if receipt.Code != 204 {
				t.Errorf("metrics not committed: %d", receipt.Code)
			}
			w.WriteHeader(503)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	defer server.Close()
	protector, err := storage.DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	config := configuration.Config{ServerURL: server.URL, StateDir: t.TempDir(), Development: true, EnrollmentToken: options.EnrollmentToken, Interval: time.Second, RequestTimeout: 3 * time.Second, MaxQueue: 100}
	openAgent := func() (*Agent, *storage.Store) {
		t.Helper()
		store, e := storage.Open(config.StateDir, protector, 100)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { store.Close() })
		agent, e := New(config, store, api.New(server.URL, 3*time.Second), fixedCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if e != nil {
			t.Fatal(e)
		}
		agent.Inventory = &inventoryFixture{}
		if e = agent.Initialize(ctx); e != nil {
			t.Fatal(e)
		}
		return agent, store
	}
	agent, store := openAgent()
	if err = agent.Cycle(ctx); err == nil {
		t.Fatal("lost response should fail sync")
	}
	credential := agent.credential
	if credential.DeviceID == "" {
		t.Fatal("agent did not enroll")
	}
	pending, err := store.Pending(ctx, 100)
	if err != nil || len(pending) != 3 {
		t.Fatal("lost response did not preserve outbox", len(pending), err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	err = backend.Close()
	if err == nil {
		backend, err = central.Open(options)
	}
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// No bootstrap token remains: the restored device credential must work alone.
	config.EnrollmentToken = ""
	agent, store = openAgent()
	if agent.credential != credential {
		t.Fatal("agent credential changed")
	}
	if err = agent.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err = store.Pending(ctx, 100)
	if err != nil || len(pending) != 0 {
		t.Fatal("outbox did not drain", err)
	}
	inspect := func() central.ReportPage {
		t.Helper()
		req, _ := http.NewRequest("GET", server.URL+"/api/v1/admin/devices/"+credential.DeviceID+"/reports", nil)
		req.Header.Set("Authorization", "Bearer "+options.AdminToken)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal("admin query", res.StatusCode)
		}
		var page central.ReportPage
		if e = json.NewDecoder(res.Body).Decode(&page); e != nil {
			t.Fatal(e)
		}
		return page
	}
	reports := inspect()
	if len(reports.Items) != 6 {
		t.Fatalf("lost response duplicated or lost reports: %d", len(reports.Items))
	}
	mu.Lock()
	offline = true
	mu.Unlock()
	if err = agent.Cycle(ctx); err == nil {
		t.Fatal("offline cycle succeeded")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	offline = false
	mu.Unlock()
	agent, store = openAgent()
	if err = agent.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if agent.credential != credential {
		t.Fatal("offline restart changed identity")
	}
	reports = inspect()
	if len(reports.Items) != 11 {
		t.Fatalf("offline reports missing: %d", len(reports.Items))
	}
}

func TestAutomaticRotationRecoversLostResponseAfterRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Add(-23*time.Hour - 30*time.Minute)
	options := central.Options{DatabasePath: filepath.Join(t.TempDir(), "central.db"), EnrollmentToken: strings.Repeat("e", 32), AdminToken: strings.Repeat("a", 32), CredentialKey: bytes.Repeat([]byte{7}, 32), Now: func() time.Time { return now }}
	backend, err := central.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { backend.Close() }()
	var mu sync.Mutex
	var rotationIDs []string
	lose := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/v1/enrollments" {
			backend.ServeHTTP(w, r)
			now = time.Now().UTC()
			return
		}
		if r.URL.Path == "/api/v1/agent/credentials/rotate" {
			rotationIDs = append(rotationIDs, r.Header.Get("Idempotency-Key"))
			if lose {
				lose = false
				rr := httptest.NewRecorder()
				backend.ServeHTTP(rr, r)
				if rr.Code != 200 {
					t.Errorf("rotation failed: %d", rr.Code)
				}
				w.WriteHeader(503)
				return
			}
		}
		backend.ServeHTTP(w, r)
	}))
	defer server.Close()
	protector, err := storage.DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	config := configuration.Config{ServerURL: server.URL, StateDir: t.TempDir(), Development: true, EnrollmentToken: options.EnrollmentToken, Interval: time.Second, RequestTimeout: 3 * time.Second, MaxQueue: 100}
	openAgent := func() (*Agent, *storage.Store) {
		t.Helper()
		store, e := storage.Open(config.StateDir, protector, 100)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { store.Close() })
		a, e := New(config, store, api.New(server.URL, 3*time.Second), fixedCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if e != nil {
			t.Fatal(e)
		}
		if e = a.Initialize(ctx); e != nil {
			t.Fatal(e)
		}
		return a, store
	}
	a, store := openAgent()
	if err = a.Cycle(ctx); err == nil {
		t.Fatal("lost rotation response accepted")
	}
	old := a.credential
	var pending pendingRotation
	if err = store.Get(ctx, "credential-rotation", &pending, true); err != nil {
		t.Fatal("intent missing", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	err = backend.Close()
	if err == nil {
		backend, err = central.Open(options)
	}
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	config.EnrollmentToken = ""
	a, store = openAgent()
	if a.credential != old {
		t.Fatal("uncommitted credential changed")
	}
	if err = a.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if a.credential.DeviceID != old.DeviceID || a.credential.AccessToken == old.AccessToken || time.Until(a.credential.ExpiresAt) < 23*time.Hour {
		t.Fatal("renewal not applied")
	}
	if err = store.Get(ctx, "credential-rotation", &pending, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("intent not cleared", err)
	}
	events, err := store.Pending(ctx, 100)
	if err != nil || len(events) != 0 {
		t.Fatal("reports did not drain", err)
	}
	renewed := a.credential
	store.Close()
	a, store = openAgent()
	if a.credential != renewed {
		t.Fatal("renewal not persisted")
	}
	if err = a.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(rotationIDs) != 2 || rotationIDs[0] != rotationIDs[1] || rotationIDs[0] != pending.RequestID {
		t.Fatal("rotation request IDs not durable", rotationIDs)
	}
}

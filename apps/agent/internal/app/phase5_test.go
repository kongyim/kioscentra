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
	"sync"
	"testing"
	"time"

	"kiosentra/apps/agent/internal/api"
	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/apps/agent/internal/storage"
	"kiosentra/packages/contracts"
	"kiosentra/tools/mock-server/mock"
)

func TestSimulationEndToEndWithLostRepliesAndRestart(t *testing.T) {
	ctx := context.Background()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	backend, err := mock.New(mock.Options{EnrollmentToken: "simulation-enrollment", AdminToken: "simulation-admin-token", SimulationPrivateKey: base64.StdEncoding.EncodeToString(priv)})
	if err != nil {
		t.Fatal(err)
	}
	// The server commits each mutation, then its first response is lost.
	var mu sync.Mutex
	lost := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		backend.ServeHTTP(recorder, r)
		suffix := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		lose := (suffix == "ack" || suffix == "status" || suffix == "renew") && !lost[suffix] && recorder.Code < 300
		if lose {
			lost[suffix] = true
		}
		mu.Unlock()
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
	protector, err := storage.DevelopmentProtector(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := configuration.Config{ServerURL: server.URL, StateDir: dir, Development: true, EnrollmentToken: "simulation-enrollment", Interval: time.Second, RequestTimeout: time.Second, MaxQueue: 100, SimulatedDeployments: true, SimulationPublicKey: base64.StdEncoding.EncodeToString(pub)}
	open := func() (*Agent, *storage.Store) {
		t.Helper()
		store, err := storage.Open(dir, protector, 100)
		if err != nil {
			t.Fatal(err)
		}
		agent, err := New(cfg, store, api.New(server.URL, time.Second), fixedCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		if err = agent.Initialize(ctx); err != nil {
			t.Fatal(err)
		}
		return agent, store
	}
	agent, store := open()
	defer func() { store.Close() }()
	if err = agent.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	create := mock.CreateJob{DeviceID: agent.credential.DeviceID, Kind: "deployment", TargetVersion: "2.1", LeaseSeconds: 300, Scenario: "validation_failure"}
	b, _ := json.Marshal(create)
	request := httptest.NewRequest("POST", "/dev/jobs", bytes.NewReader(b))
	request.Header.Set("Authorization", "Bearer simulation-admin-token")
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
	finished := false
	for i := 0; i < 15; i++ {
		err = agent.Cycle(ctx) // Lost responses are expected and persist work for retry.
		if err != nil && !strings.Contains(err.Error(), "503") {
			t.Fatal(err)
		}
		d, getErr := store.Deployment(ctx, job.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if contracts.TerminalState(d.Status.State) {
			if d.Status.State != "rolled_back" {
				t.Fatal(d.Status)
			}
			finished = true
			break
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
		agent, store = open()
	}
	if !finished {
		t.Fatal("simulation failed to finish across restarts")
	}
	request = httptest.NewRequest("GET", "/dev/state", nil)
	request.Header.Set("Authorization", "Bearer simulation-admin-token")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	backend.ServeHTTP(response, request)
	var views []mock.DeviceView
	if err = json.Unmarshal(response.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || len(views[0].Jobs) != 1 || views[0].Jobs[0].Status == nil || views[0].Jobs[0].Status.State != "rolled_back" {
		t.Fatalf("remote terminal state missing: %s", response.Body.String())
	}
	if len(views[0].Reports) < 10 {
		t.Fatal("telemetry stopped during deployment")
	}
	history, err := store.DeploymentHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 7 {
		t.Fatalf("unexpected or duplicated transitions: %+v", history)
	}
	for _, v := range history {
		if v.Delivery != "delivered" {
			t.Fatal("undelivered transition", v)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lost) != 3 {
		t.Fatal("did not exercise all response losses", lost)
	}
}

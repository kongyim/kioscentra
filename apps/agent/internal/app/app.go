package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand/v2"
	"os"
	"time"

	"kiosentra/apps/agent/internal/api"
	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/apps/agent/internal/deployment"
	"kiosentra/apps/agent/internal/diagnostics"
	"kiosentra/apps/agent/internal/inventory"
	"kiosentra/apps/agent/internal/ipc"
	"kiosentra/apps/agent/internal/monitoring"
	win "kiosentra/apps/agent/internal/platform/windows"
	"kiosentra/apps/agent/internal/storage"
	"kiosentra/apps/agent/internal/watchdog"
	"kiosentra/packages/contracts"
)

var Version = "0.1.0-dev"
var Commit = "unknown"

type Identity struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}
type Agent struct {
	Actions             <-chan string
	LaunchUpdater       func(string, ed25519.PublicKey) error
	Config              configuration.Config
	Store               *storage.Store
	Client              *api.Client
	Collector           monitoring.Collector
	Inventory           inventory.Collector
	Processes           watchdog.Observer
	PolicyObserver      func(context.Context, contracts.Policy) contracts.PolicyReport
	nextInventory       time.Time
	inventoryExecutable string
	Log                 *slog.Logger
	credential          contracts.Credential
	rotationPending     bool
	identity            Identity
	hostname            string
	failures            int
	nextAttempt         time.Time
}

func New(c configuration.Config, s *storage.Store, client *api.Client, collector monitoring.Collector, log *slog.Logger) (*Agent, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	h, e := os.Hostname()
	if e != nil {
		return nil, e
	}
	return &Agent{Config: c, Store: s, Client: client, Collector: collector, Log: log, hostname: h}, nil
}
func (a *Agent) Initialize(ctx context.Context) error {
	// Keep effective non-secret bootstrap settings for offline diagnostics.
	snapshot := struct {
		ServerURL             string
		IntervalSeconds       int64
		RequestTimeoutSeconds int64
		MaxQueue              int
		Development           bool
		SimulatedDeployments  bool
		SimulatedUpdates      bool
	}{a.Config.ServerURL, int64(a.Config.Interval / time.Second), int64(a.Config.RequestTimeout / time.Second), a.Config.MaxQueue, a.Config.Development, a.Config.SimulatedDeployments, a.Config.SimulatedUpdates}
	if e := a.Store.Put(ctx, "configuration", snapshot, false); e != nil {
		return e
	}
	// Prevent a configuration change from sending an existing device credential to another origin.
	var origin string
	e := a.Store.Get(ctx, "server-origin", &origin, false)
	if errors.Is(e, sql.ErrNoRows) {
		if e = a.Store.Put(ctx, "server-origin", a.Config.ServerURL, false); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else if origin != a.Config.ServerURL {
		return errors.New("server origin differs from enrolled state; use a separate state directory")
	}
	e = a.Store.Get(ctx, "identity", &a.identity, true)
	if errors.Is(e, sql.ErrNoRows) {
		pub, priv, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		a.identity = Identity{base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv)}
		if e = a.Store.Put(ctx, "identity", a.identity, true); e != nil {
			return e
		}
	} else if e != nil {
		return fmt.Errorf("load protected identity: %w", e)
	}
	e = a.Store.Get(ctx, "credential", &a.credential, true)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return fmt.Errorf("load protected credential: %w", e)
	}
	a.Client.SetCredential(a.credential)
	return nil
}

// Backoff is capped exponential backoff with full jitter and a minimum delay.
func Backoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	if failures > 8 {
		failures = 8
	}
	cap := time.Second * time.Duration(1<<failures)
	return time.Second + time.Duration(mathrand.Int64N(int64(cap)))
}

// sample persists telemetry with the parent context so an expired collector deadline
// cannot suppress the heartbeat or the diagnostics explaining that timeout.
func (a *Agent) sample(ctx context.Context) error {
	collectCtx, cancel := context.WithTimeout(ctx, a.Config.RequestTimeout)
	metrics, uptime, collectErr := a.Collector.Collect(collectCtx)
	cancel()
	health, status := "ok", "online"
	results := []diagnostics.Result{}
	if d, ok := a.Collector.(interface{ CollectionDiagnostics() []diagnostics.Result }); ok {
		results = append(results, d.CollectionDiagnostics()...)
	}
	if collectErr != nil {
		health, status = "metrics_unavailable", "degraded"
		a.Log.Warn("metrics collection failed", "error", collectErr)
	}
	var policy contracts.Policy
	policyErr := a.Store.Get(ctx, "policy", &policy, false)
	if policyErr != nil && !errors.Is(policyErr, sql.ErrNoRows) {
		return policyErr
	}
	var running *bool
	if a.Processes != nil && policyErr == nil {
		r := diagnostics.Run(ctx, a.Config.RequestTimeout, "kiosk-process", func(child context.Context) error {
			var err error
			metrics.Processes, running, err = a.Processes.Observe(child, policy.KioskExecutable)
			return err
		})
		results = append(results, r)
		if r.Error != "" {
			health, status = "process_unavailable", "degraded"
		}
		if running != nil && !*running {
			health, status = "kiosk_stopped", "degraded"
		}
	}
	var errs []error
	if collectErr == nil {
		if err := metrics.Validate(); err != nil {
			errs = append(errs, err)
		} else if err = a.Store.Enqueue(ctx, "POST", "/agent/metrics", metrics); err != nil {
			errs = append(errs, err)
		}
	}
	heartbeat := contracts.Heartbeat{AgentVersion: Version, Status: status, UptimeSeconds: uptime, KioskRunning: running, LastActivity: time.Now().UTC(), Health: health}
	if err := a.Store.Put(ctx, "heartbeat", heartbeat, false); err != nil {
		errs = append(errs, err)
	}
	if err := a.Store.Enqueue(ctx, "POST", "/agent/heartbeat", heartbeat); err != nil {
		errs = append(errs, err)
	}
	// The latest partial readings remain local; v1 metrics has no unavailable fields.
	if err := a.Store.Put(ctx, "monitoring-snapshot", struct {
		Metrics     contracts.Metrics
		Diagnostics []diagnostics.Result
	}{metrics, results}, false); err != nil {
		errs = append(errs, err)
	}
	if a.PolicyObserver != nil && policyErr == nil {
		var report contracts.PolicyReport
		r := diagnostics.Run(ctx, a.Config.RequestTimeout, "policy", func(child context.Context) error { report = a.PolicyObserver(child, policy); return nil })
		if r.Error != "" {
			report.Errors = append(report.Errors, r.Error)
			if report.Compliance == "compliant" {
				report.Compliance = "unknown"
			}
		}
		if err := report.Validate(); err != nil {
			errs = append(errs, err)
		} else {
			if err = a.Store.Put(ctx, "policy-observation", report, false); err != nil {
				errs = append(errs, err)
			}
			if err = a.Store.Enqueue(ctx, "POST", "/agent/policy/report", report); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if a.Inventory != nil && (time.Now().After(a.nextInventory) || policy.KioskExecutable != a.inventoryExecutable) {
		child, done := context.WithTimeout(ctx, a.Config.RequestTimeout)
		inv, ds, err := a.Inventory.Collect(child, policy.KioskExecutable)
		done()
		if err != nil {
			a.Log.Warn("inventory incomplete", "error", err)
		}
		if err = a.Store.Put(ctx, "inventory-snapshot", struct {
			Inventory   contracts.Inventory
			Diagnostics []diagnostics.Result
		}{inv, ds}, false); err != nil {
			errs = append(errs, err)
		}
		if err = inv.Validate(); err != nil {
			errs = append(errs, err)
		} else if err = a.Store.Enqueue(ctx, "PUT", "/agent/inventory", inv); err != nil {
			errs = append(errs, err)
		} else {
			a.nextInventory = time.Now().Add(time.Hour)
			a.inventoryExecutable = policy.KioskExecutable
		}
	}
	return errors.Join(errs...)
}
func (a *Agent) sync(ctx context.Context) error {
	if a.credential.AccessToken == "" {
		if a.Config.EnrollmentToken == "" {
			return errors.New("enrollment token required for initial enrollment")
		}
		v, e := a.Client.Enroll(ctx, contracts.Enrollment{EnrollmentToken: a.Config.EnrollmentToken, PublicKey: a.identity.PublicKey, Hostname: a.hostname, AgentVersion: Version})
		if e != nil {
			return e
		}
		if e = a.Store.Put(ctx, "credential", v, true); e != nil {
			return e
		}
		a.credential = v
		a.Client.SetCredential(v)
		a.Log.Info("device enrolled", "device_id", v.DeviceID)
	}
	if e := a.renewCredential(ctx); e != nil {
		return e
	}
	events, e := a.Store.Pending(ctx, 100)
	if e != nil {
		return e
	}
	for _, v := range events {
		if e = a.Client.Do(ctx, v.Method, v.Path, v.ID, v.Body, nil); e != nil {
			return e
		}
		if e = a.Store.DeleteEvent(ctx, v.ID); e != nil {
			return e
		}
	}
	p, e := a.Client.Policy(ctx)
	if e != nil {
		return e
	}
	if p.Validate() != nil {
		return errors.New("unsupported policy")
	}
	if e = a.Store.Put(ctx, "policy", p, false); e != nil {
		return e
	}
	// Admission is fail-closed; only opted-in signed simulations can be accepted.
	engine := a.deploymentEngine()
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 10; page++ {
		jobs, e := a.Client.Jobs(ctx, cursor)
		if e != nil {
			return e
		}
		if len(jobs.Items) > 100 {
			return errors.New("oversized jobs page")
		}
		for _, j := range jobs.Items {
			if e = j.Validate(); e != nil {
				return e
			}
			var decision contracts.Ack
			var err error
			if j.Kind == "agent_update" {
				decision, err = a.updateDecision(ctx, j)
			} else {
				decision, err = engine.Decision(ctx, j)
			}
			if err != nil {
				return err
			}
			if e = a.Store.RememberJob(ctx, j, decision); e != nil {
				return e
			}
		}
		if jobs.NextCursor == "" {
			break
		}
		if seen[jobs.NextCursor] {
			return errors.New("repeated jobs cursor")
		}
		seen[jobs.NextCursor] = true
		cursor = jobs.NextCursor
	}
	decisions, e := a.Store.Decisions(ctx)
	if e != nil {
		return e
	}
	for _, d := range decisions {
		e = a.Client.Ack(ctx, d.ID, d.Ack)
		var he *api.HTTPError
		if errors.As(e, &he) && (he.Status == 404 || he.Status == 410) {
			a.Log.Warn("job no longer available", "job_id", d.ID)
			if d.Job.Kind == "agent_update" {
				if err := a.Store.CompleteUpdate(ctx, d.ID); err != nil {
					return err
				}
			}
			e = nil
		}
		if e != nil {
			return e
		}
		if e = a.Store.MarkReported(ctx, d.ID); e != nil {
			return e
		}
		a.Log.Info("job decision acknowledged", "job_id", d.ID, "accepted", d.Ack.Accepted, "reason", d.Ack.Reason)
	}
	if e = a.recoverUpdates(ctx, true); e != nil {
		return e
	}
	if e = a.flushUpdates(ctx); e != nil {
		return e
	}
	return engine.Advance(ctx)
}

func (a *Agent) deploymentEngine() *deployment.Engine {
	key, _ := base64.StdEncoding.DecodeString(a.Config.SimulationPublicKey)
	return &deployment.Engine{Store: a.Store, Remote: a.Client, Enabled: a.Config.SimulatedDeployments, PublicKey: key, DeviceID: a.credential.DeviceID}
}

// Cycle keeps collecting while communications are in backoff. Outbox capacity is bounded.
func (a *Agent) Cycle(ctx context.Context) (result error) {
	defer func() {
		status := map[string]any{"observedAt": time.Now().UTC(), "centralReachable": result == nil && a.failures == 0, "consecutiveFailures": a.failures}
		if err := a.Store.Put(ctx, "sync-status", status, false); err != nil && ctx.Err() == nil {
			a.Log.Warn("sync status not saved", "error", err)
		}
	}()
	if e := a.recoverUpdates(ctx, false); e != nil {
		a.Log.Warn("update recovery deferred", "error", e)
	}
	if e := a.deploymentEngine().Reconcile(ctx); e != nil {
		return e
	}
	if e := a.sample(ctx); e != nil {
		a.Log.Warn("telemetry not queued", "error", e)
	}
	if time.Now().Before(a.nextAttempt) {
		return nil
	}
	if e := a.sync(ctx); e != nil {
		a.failures++
		delay := Backoff(a.failures)
		if a.rotationPending && delay > 30*time.Second {
			delay = 30 * time.Second
		}
		a.nextAttempt = time.Now().Add(delay)
		a.Log.Warn("central sync failed", "error", e, "retry_after", delay)
		return e
	}
	a.failures = 0
	a.nextAttempt = time.Time{}
	return nil
}
func (a *Agent) Run(ctx context.Context) error {
	if e := a.Initialize(ctx); e != nil {
		return e
	}
	a.Log.Info("agent started", "version", Version)
	ticker := time.NewTicker(a.Config.Interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		_ = a.Cycle(ctx)
		var rotationRetry <-chan time.Time
		if a.rotationPending {
			rotationRetry = time.After(max(time.Until(a.nextAttempt), time.Second))
		}
		select {
		case <-ctx.Done():
			a.Log.Info("agent stopped")
			return nil
		case <-ticker.C:
		case <-rotationRetry:
		case action := <-a.Actions:
			if action == "stop_service" {
				a.Log.Info("service stopped by authorized technician")
				return nil
			}
			if action == "cancel_deployment" {
				if e := a.deploymentEngine().Cancel(ctx); e != nil {
					a.Log.Error("simulation cancellation failed", "error", e)
				}
			}
			if action == "refresh_inventory" {
				a.nextInventory = time.Time{}
			}
		}
	}
}
func Run(ctx context.Context, log *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logs := &ipc.LogBuffer{}
	log = slog.New(logs.Handler(log.Handler()))
	c, e := configuration.Load()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(c.StateDir, 0700); e != nil {
		return e
	}
	unlock, e := storage.Lock(c.StateDir)
	if e != nil {
		return e
	}
	defer func() {
		if e := unlock(); e != nil {
			log.Error("state lock release failed", "error", e)
		}
	}()
	p, e := storage.PlatformProtector(c.Development, c.DevelopmentKey)
	if e != nil {
		return e
	}
	s, e := storage.Open(c.StateDir, p, c.MaxQueue)
	if e != nil {
		return e
	}
	defer func() {
		if e := s.Close(); e != nil {
			log.Error("state close failed", "error", e)
		}
	}()
	a, e := New(c, s, api.New(c.ServerURL, c.RequestTimeout), &monitoring.Basic{DiskPath: c.StateDir}, log)
	if e != nil {
		return e
	}
	a.Inventory = inventory.System{DiskPath: c.StateDir}
	a.Processes = watchdog.Processes{}
	a.PolicyObserver = win.Policy
	cleanup, err := a.startConsole(ctx, logs)
	if err != nil {
		log.Error("local console IPC unavailable; service continues", "error", err)
	} else {
		defer cleanup()
	}
	return a.Run(ctx)
}

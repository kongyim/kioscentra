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
	"kiosentra/apps/agent/internal/monitoring"
	"kiosentra/apps/agent/internal/storage"
	"kiosentra/packages/contracts"
)

var Version = "0.1.0-dev"
var Commit = "unknown"

type Identity struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}
type Agent struct {
	Config      configuration.Config
	Store       *storage.Store
	Client      *api.Client
	Collector   monitoring.Collector
	Log         *slog.Logger
	credential  contracts.Credential
	identity    Identity
	hostname    string
	failures    int
	nextAttempt time.Time
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
	}{a.Config.ServerURL, int64(a.Config.Interval / time.Second), int64(a.Config.RequestTimeout / time.Second), a.Config.MaxQueue, a.Config.Development}
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
func (a *Agent) sample(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.Config.RequestTimeout)
	defer cancel()
	metrics, uptime, e := a.Collector.Collect(ctx)
	health := "ok"
	status := "online"
	if e != nil {
		health = "metrics_unavailable"
		status = "degraded"
		a.Log.Warn("metrics collection failed", "error", e)
	} else {
		if e = metrics.Validate(); e != nil {
			return e
		}
		if e = a.Store.Enqueue(ctx, "POST", "/agent/metrics", metrics); e != nil {
			return e
		}
	}
	return a.Store.Enqueue(ctx, "POST", "/agent/heartbeat", contracts.Heartbeat{AgentVersion: Version, Status: status, UptimeSeconds: uptime, KioskRunning: nil, LastActivity: time.Now().UTC(), Health: health})
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
	if !a.credential.ExpiresAt.After(time.Now()) {
		return errors.New("device credential expired; operator recovery required (automatic rotation deferred)")
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
	// All execution is disabled in this milestone. Preserve each decision before sending it.
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
			reason := "job execution is disabled in Phase 2"
			if !j.LeaseExpiresAt.After(time.Now()) {
				reason = "job lease expired"
			}
			if e = a.Store.RememberJob(ctx, j, contracts.Ack{Accepted: false, Reason: reason}); e != nil {
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
			e = nil
		}
		if e != nil {
			return e
		}
		if e = a.Store.MarkReported(ctx, d.ID); e != nil {
			return e
		}
		a.Log.Info("job rejected", "job_id", d.ID, "reason", d.Ack.Reason)
	}
	return nil
}

// Cycle keeps collecting while communications are in backoff. Outbox capacity is bounded.
func (a *Agent) Cycle(ctx context.Context) error {
	if e := a.sample(ctx); e != nil {
		a.Log.Warn("telemetry not queued", "error", e)
	}
	if time.Now().Before(a.nextAttempt) {
		return nil
	}
	if e := a.sync(ctx); e != nil {
		a.failures++
		delay := Backoff(a.failures)
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
		select {
		case <-ctx.Done():
			a.Log.Info("agent stopped")
			return nil
		case <-ticker.C:
		}
	}
}
func Run(ctx context.Context, log *slog.Logger) error {
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
	a, e := New(c, s, api.New(c.ServerURL, c.RequestTimeout), monitoring.Basic{DiskPath: c.StateDir}, log)
	if e != nil {
		return e
	}
	return a.Run(ctx)
}

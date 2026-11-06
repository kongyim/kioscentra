// Package deployment runs only deterministic, signed development simulations.
// It cannot fetch artifacts, launch processes, change software or reboot the device.
package deployment

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"kiosentra/apps/agent/internal/api"
	"kiosentra/apps/agent/internal/storage"
	"kiosentra/packages/contracts"
)

type Remote interface {
	Status(context.Context, string, string, contracts.JobStatus) error
	Renew(context.Context, string, string, contracts.Renew) (contracts.Lease, error)
}
type Engine struct {
	Store     *storage.Store
	Remote    Remote
	Enabled   bool
	PublicKey ed25519.PublicKey
	DeviceID  string
	Now       func() time.Time
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}
func (e *Engine) Decision(ctx context.Context, j contracts.Job) (contracts.Ack, error) {
	reject := func(reason string) (contracts.Ack, error) { return contracts.Ack{Reason: reason}, nil }
	if !e.Enabled {
		return reject("simulated deployments disabled; real execution unavailable")
	}
	if err := contracts.VerifySimulation(e.DeviceID, j, e.PublicKey); err != nil {
		return reject(err.Error())
	}
	if !j.LeaseExpiresAt.After(e.now()) {
		return reject("job lease expired")
	}
	active, err := e.Store.ActiveDeployments(ctx)
	if err != nil {
		return contracts.Ack{}, err
	}
	updates, err := e.Store.UpdateJobs(ctx)
	if err != nil {
		return contracts.Ack{}, err
	}
	if len(active) != 0 || len(updates) != 0 {
		return reject("another simulated deployment is active")
	}
	return contracts.Ack{Accepted: true, Reason: "Signed development simulation only; no software changes"}, nil
}
func (e *Engine) transition(ctx context.Context, d storage.Deployment, state, message string, progress int) error {
	return e.Store.TransitionDeployment(ctx, d.Job.ID, d.Status.State, contracts.JobStatus{State: state, Progress: progress, Message: message, UpdatedAt: e.now()})
}

// Reconcile runs even while central communications are in backoff. All stages are
// instantaneous pure simulation steps; every persisted state is a safe restart point.
func (e *Engine) Reconcile(ctx context.Context) error {
	active, err := e.Store.ActiveDeployments(ctx)
	if err != nil {
		return err
	}
	for _, d := range active {
		state, reason := "", ""
		switch {
		case !e.Enabled:
			state, reason = "cancelled", "Simulation disabled locally"
		case !d.LeaseExpiresAt.After(e.now()):
			state, reason = "cancelled", "Lease expired; simulation stopped at persisted safe point"
		case !d.Deadline.After(e.now()):
			state, reason = "failed", "Simulation exceeded its ten-minute deadline"
		default:
			if err = contracts.VerifySimulation(e.DeviceID, d.Job, e.PublicKey); err != nil {
				state, reason = "failed", "Simulation trust check failed on resume"
			}
		}
		if state != "" {
			if err = e.transition(ctx, d, state, reason, d.Status.Progress); err != nil {
				return err
			}
		}
	}
	return nil
}
func (e *Engine) Cancel(ctx context.Context) error {
	active, err := e.Store.ActiveDeployments(ctx)
	if err != nil {
		return err
	}
	for _, d := range active {
		if err = e.transition(ctx, d, "cancelled", "Simulation cancelled by authorized technician", d.Status.Progress); err != nil {
			return err
		}
	}
	return nil
}
func unavailable(err error) bool {
	var h *api.HTTPError
	return errors.As(err, &h) && (h.Status == 404 || h.Status == 410)
}

// Flush has its own durable queue so expired deployment reports cannot block telemetry.
func (e *Engine) Flush(ctx context.Context) error {
	events, err := e.Store.DeploymentEvents(ctx)
	if err != nil {
		return err
	}
	for _, v := range events {
		acked, err := e.Store.DeploymentAcknowledged(ctx, v.JobID)
		if err != nil {
			return err
		}
		if !acked {
			continue
		}
		delivery := "delivered"
		if err = e.Remote.Status(ctx, v.JobID, v.ID, v.Status); err != nil {
			if !unavailable(err) {
				return err
			}
			delivery = "unavailable"
		}
		if err = e.Store.MarkDeploymentEvent(ctx, v.ID, delivery); err != nil {
			return err
		}
	}
	return nil
}

// Advance performs at most one step per cycle, after ACK and pending status delivery.
// Renewal is retried with the same persisted idempotency key after a lost response.
func (e *Engine) Advance(ctx context.Context) error {
	if err := e.Reconcile(ctx); err != nil {
		return err
	}
	if err := e.Flush(ctx); err != nil {
		return err
	}
	active, err := e.Store.ActiveDeployments(ctx)
	if err != nil {
		return err
	}
	for _, d := range active {
		acked, err := e.Store.DeploymentAcknowledged(ctx, d.Job.ID)
		if err != nil {
			return err
		}
		if !acked {
			continue
		}
		pending, err := e.Store.DeploymentReportsPending(ctx, d.Job.ID)
		if err != nil {
			return err
		}
		if pending {
			continue
		}
		child, cancel := context.WithDeadline(ctx, d.LeaseExpiresAt)
		lease, err := e.Remote.Renew(child, d.Job.ID, d.RenewalID, contracts.Renew{LeaseSeconds: 300})
		cancel()
		if unavailable(err) {
			return e.transition(ctx, d, "cancelled", "Server lease unavailable", d.Status.Progress)
		}
		if err != nil {
			return err
		}
		if !lease.LeaseExpiresAt.After(e.now()) || lease.LeaseExpiresAt.After(e.now().Add(305*time.Second)) {
			return e.transition(ctx, d, "cancelled", "Invalid renewed lease", d.Status.Progress)
		}
		if err = e.Store.SaveDeploymentLease(ctx, d.Job.ID, lease.LeaseExpiresAt); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !lease.LeaseExpiresAt.After(e.now()) {
			return e.transition(ctx, d, "cancelled", "Lease expired before simulation step", d.Status.Progress)
		}
		if !d.Deadline.After(e.now()) {
			return e.transition(ctx, d, "failed", "Simulation exceeded its ten-minute deadline", d.Status.Progress)
		}
		next, message, progress := (Simulator{}).Step(d)
		if err = e.transition(ctx, d, next, message, progress); err != nil {
			return err
		}
	}
	return e.Flush(ctx)
}

// Simulator is deliberately a pure adapter: repeats after crashes have no OS effects.
// A real installer must introduce its own idempotency, cancellation and rollback contract.
type Simulator struct{}

func (Simulator) Step(d storage.Deployment) (string, string, int) {
	scenario, _ := contracts.SimulationScenario(d.Job.Release.URL)
	switch d.Status.State {
	case "accepted":
		return "downloading", "Preparing in-memory simulation fixture; no network download", 10
	case "downloading":
		if scenario == "download_failure" {
			return "failed", "Simulated interrupted download", 10
		}
		return "verifying", "Simulation fixture prepared", 30
	case "verifying":
		payload := contracts.SimulationPayload(d.Job.TargetVersion, scenario)
		digest := sha256.Sum256(payload)
		if int64(len(payload)) != d.Job.Release.SizeBytes || hex.EncodeToString(digest[:]) != d.Job.Release.SHA256 {
			return "failed", "Simulation fixture size or SHA-256 mismatch", 30
		}
		return "installing", "Simulation signature, size and SHA-256 verified", 50
	case "installing":
		if scenario == "timeout" {
			return "failed", "Simulated installer timeout; no software was changed", 50
		}
		if scenario == "install_failure" {
			return "rolling_back", "Simulated installation failed; simulating rollback", 60
		}
		return "validating", "Simulated installation complete; no software was changed", 75
	case "validating":
		if scenario == "validation_failure" || scenario == "rollback_failure" {
			return "rolling_back", "Simulated version validation failed", 80
		}
		return "succeeded", "Simulation succeeded; no software was installed", 100
	case "rolling_back":
		if scenario == "rollback_failure" {
			return "failed", "Simulated rollback failed; no software was changed", 90
		}
		return "rolled_back", "Simulated previous version restored; no software was changed", 100
	}
	return "failed", "Unsupported simulation checkpoint", 0
}

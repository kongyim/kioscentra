package app

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"kiosentra/apps/agent/internal/updater"
	"kiosentra/packages/contracts"
)

func (a *Agent) updateKey() ed25519.PublicKey {
	key, _ := base64.StdEncoding.DecodeString(a.Config.SimulationPublicKey)
	return key
}
func (a *Agent) updateDecision(ctx context.Context, job contracts.Job) (contracts.Ack, error) {
	reject := func(reason string) (contracts.Ack, error) { return contracts.Ack{Reason: reason}, nil }
	if !a.Config.SimulatedUpdates {
		return reject("simulated updates disabled; real updates unavailable")
	}
	if err := contracts.VerifyUpdateSimulation(a.credential.DeviceID, job, a.updateKey()); err != nil {
		return reject(err.Error())
	}
	if !job.LeaseExpiresAt.After(time.Now()) {
		return reject("update job lease expired")
	}
	updates, err := a.Store.UpdateJobs(ctx)
	if err != nil {
		return contracts.Ack{}, err
	}
	deployments, err := a.Store.ActiveDeployments(ctx)
	if err != nil {
		return contracts.Ack{}, err
	}
	if len(updates) != 0 || len(deployments) != 0 {
		return reject("another simulation is active or awaiting reports")
	}
	journal, err := updater.Open(a.Config.StateDir)
	if err != nil {
		return contracts.Ack{}, err
	}
	defer journal.Close()
	target, err := journal.Target(ctx)
	if err != nil {
		return contracts.Ack{}, err
	}
	if target.Current != target.KnownGood {
		return reject("simulated target requires operator recovery")
	}
	return contracts.Ack{Accepted: true, Reason: "Signed independent update simulation; no binary replacement"}, nil
}

// recoverUpdates runs outside sync/backoff. Once prepared, the helper has everything
// needed for bounded offline recovery and does not read the service database.
func (a *Agent) recoverUpdates(ctx context.Context, prepare bool) error {
	jobs, err := a.Store.UpdateJobs(ctx)
	if err != nil || len(jobs) == 0 {
		return err
	}
	journal, err := updater.Open(a.Config.StateDir)
	if err != nil {
		return err
	}
	defer journal.Close()
	for _, job := range jobs {
		acked, err := a.Store.DeploymentAcknowledged(ctx, job.ID)
		if err != nil {
			return err
		}
		if !acked {
			continue
		}
		r, err := journal.Get(ctx, job.ID)
		if errors.Is(err, sql.ErrNoRows) {
			if !prepare {
				continue
			}
			// Never hand a running update to a helper before a confirmed bounded lease.
			if a.Config.SimulatedUpdates && job.LeaseExpiresAt.After(time.Now()) {
				lease, e := a.Client.Renew(ctx, job.ID, job.ID, contracts.Renew{LeaseSeconds: 300})
				if e != nil {
					return e
				}
				if !lease.LeaseExpiresAt.After(time.Now()) || lease.LeaseExpiresAt.After(time.Now().Add(305*time.Second)) {
					return errors.New("invalid update handoff lease")
				}
				job.LeaseExpiresAt = lease.LeaseExpiresAt
			}
			if err = journal.Prepare(ctx, job, a.credential.DeviceID, Version, time.Now().UTC()); err != nil {
				return err
			}
			r, err = journal.Get(ctx, job.ID)
		}
		if err != nil {
			return err
		}
		if updater.Terminal(r.State) {
			continue
		}
		key := a.updateKey()
		if !a.Config.SimulatedUpdates || contracts.VerifyUpdateSimulation(r.DeviceID, r.Job, key) != nil {
			if err = journal.RequestAbort(ctx, job.ID); err != nil {
				return err
			}
		}
		// Local preflight can fail a never-started handoff without a functioning child.
		if r.State == "staged" && (!a.Config.SimulatedUpdates || !r.Deadline.After(time.Now()) || !r.Job.LeaseExpiresAt.After(time.Now()) || contracts.VerifyUpdateSimulation(r.DeviceID, r.Job, key) != nil) {
			r, err = journal.Get(ctx, job.ID)
			if err != nil {
				return err
			}
			if err = journal.Step(ctx, r, key, time.Now().UTC()); err != nil && !errors.Is(err, updater.ErrCheckpoint) {
				return err
			}
			continue
		}
		if len(key) != ed25519.PublicKeySize {
			key = make([]byte, ed25519.PublicKeySize)
		} // revoked trust permits rollback, never forward progress
		scenario, _ := contracts.UpdateSimulationScenario(job.Release.URL)
		launch := a.LaunchUpdater
		if launch == nil {
			launch = updater.Launch
			if a.Config.RecoveryExecutable != "" {
				launch = func(dir string, key ed25519.PublicKey) error {
					return updater.LaunchExecutable(a.Config.RecoveryExecutable, dir, key)
				}
			}
		}
		if scenario == "helper_launch_failure" {
			err = errors.New("simulated helper launch failure")
		} else {
			err = launch(a.Config.StateDir, key)
		}
		if err != nil {
			a.Log.Warn("simulation helper launch failed", "job_id", job.ID, "error", err)
			if e := journal.LaunchFailed(ctx, job.ID, time.Now().UTC()); e != nil {
				return e
			}
		}
	}
	return nil
}
func (a *Agent) flushUpdates(ctx context.Context) error {
	jobs, err := a.Store.UpdateJobs(ctx)
	if err != nil || len(jobs) == 0 {
		return err
	}
	journal, err := updater.Open(a.Config.StateDir)
	if err != nil {
		return err
	}
	defer journal.Close()
	events, err := journal.Events(ctx, true)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err = a.Client.UpdateStatus(ctx, event.ID, event.Status); err != nil {
			return err
		}
		if err = journal.MarkDelivered(ctx, event.ID); err != nil {
			return err
		}
	}
	for _, job := range jobs {
		r, err := journal.Get(ctx, job.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		pending, err := journal.Pending(ctx, job.ID)
		if err != nil {
			return err
		}
		if updater.Terminal(r.State) && !pending {
			if err = a.Store.CompleteUpdate(ctx, job.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"kiosentra/packages/contracts"
)

// Step has no filesystem target or SCM effects: it updates only simulated pointers.
// Once switching begins, rollback remains possible without the service or central server.
func (j *Journal) Step(ctx context.Context, r Record, key ed25519.PublicKey, now time.Time) error {
	trust := contracts.VerifyUpdateSimulation(r.DeviceID, r.Job, key)
	scenario := ""
	if trust == nil {
		scenario, _ = contracts.UpdateSimulationScenario(r.Job.Release.URL)
	}
	invalid := r.Abort || trust != nil || !r.Deadline.After(now) || !r.Job.LeaseExpiresAt.After(now)
	next, message := "", ""
	switch r.State {
	case "staged":
		if invalid {
			next, message = "failed", "Simulation stopped before switch: revoked, untrusted or expired"
			break
		}
		payload := contracts.SimulationPayload(r.Job.TargetVersion, scenario)
		hash := sha256.Sum256(payload)
		if int64(len(payload)) != r.Job.Release.SizeBytes || hex.EncodeToString(hash[:]) != r.Job.Release.SHA256 {
			next, message = "failed", "Simulation artifact size or SHA-256 mismatch"
			break
		}
		next, message = "switching", "Simulated switch intent persisted; real executable is unchanged"
	case "switching":
		if invalid || scenario == "service_start_failure" {
			next, message = "rolling_back", "Simulated candidate could not start; recovery intent persisted"
			break
		}
		next, message = "verifying", "Simulated candidate selected; checking bounded health fixture"
	case "verifying":
		if invalid || scenario == "rollback_failure" {
			next, message = "rolling_back", "Simulated candidate unhealthy; recovery intent persisted"
			break
		}
		if scenario == "health_timeout" {
			if now.Before(r.HealthDeadline) {
				return nil
			}
			next, message = "rolling_back", "Simulated health deadline expired; recovery intent persisted"
			break
		}
		next, message = "succeeded", "Update simulation healthy; no executable or service was changed"
	case "rolling_back":
		if scenario == "rollback_failure" {
			next, message = "failed", "Simulated rollback failed; known-good reference retained for operator recovery"
			break
		}
		next, message = "rolled_back", "Simulated known-good target restored independently of the service"
	default:
		return nil
	}
	return j.Commit(ctx, r, next, message, now.UTC())
}
func Run(ctx context.Context, stateDir string, key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize {
		return errors.New("updater simulation requires an Ed25519 trust key")
	}
	journal, e := Open(stateDir)
	if e != nil {
		return e
	}
	defer journal.Close()
	lock := flock.New(filepath.Join(journal.Dir, "helper.lock"))
	ok, e := lock.TryLock()
	if e != nil {
		return e
	}
	if !ok {
		return nil
	}
	defer lock.Unlock()
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		active, e := journal.Active(ctx)
		if e != nil {
			return e
		}
		if len(active) == 0 {
			return nil
		}
		for _, r := range active {
			if e = journal.Step(ctx, r, key, time.Now().UTC()); e != nil && !errors.Is(e, ErrCheckpoint) {
				return e
			}
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Launch starts a detached, credential-free helper. It is deliberately not bound to
// the service's context. A process lock fences concurrent/restarted helper instances.
func Launch(stateDir string, key ed25519.PublicKey) error {
	lock := flock.New(filepath.Join(stateDir, "update-simulation", "helper.lock"))
	ok, e := lock.TryLock()
	if e != nil {
		return e
	}
	if !ok {
		return nil
	}
	if e = lock.Unlock(); e != nil {
		return e
	}
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	cmd := exec.Command(executable, "--mode=updater")
	cmd.Env = helperEnvironment(stateDir, key)
	detach(cmd)
	if e = cmd.Start(); e != nil {
		return e
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func helperEnvironment(stateDir string, key ed25519.PublicKey) []string {
	env := []string{"KIOSENTRA_STATE_DIR=" + stateDir, "KIOSENTRA_DEVELOPMENT=true", "KIOSENTRA_SIMULATED_UPDATES=true", "KIOSENTRA_SIMULATION_PUBLIC_KEY=" + base64.StdEncoding.EncodeToString(key)}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
		if v := os.Getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return env
}

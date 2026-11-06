package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func fixture(t *testing.T, scenario string) (*Journal, contracts.Job, ed25519.PublicKey, string, time.Time) {
	t.Helper()
	dir := t.TempDir()
	journal, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	job := contracts.Job{ID: contracts.ID(), Kind: "agent_update", TargetVersion: "2.0", CreatedAt: now, LeaseExpiresAt: now.Add(5 * time.Minute)}
	device := contracts.ID()
	if e = contracts.SignSimulation(device, &job, scenario, priv); e != nil {
		t.Fatal(e)
	}
	if e = contracts.VerifyUpdateSimulation(device, job, pub); e != nil {
		t.Fatal(e)
	}
	if e = journal.Prepare(context.Background(), job, device, "1.0", now); e != nil {
		t.Fatal(e)
	}
	return journal, job, pub, dir, now
}
func TestSimulationRecoveryAtEveryJournalCheckpoint(t *testing.T) {
	for scenario, want := range map[string]string{"success": "succeeded", "hash_failure": "failed", "service_start_failure": "rolled_back", "health_timeout": "rolled_back", "rollback_failure": "failed"} {
		t.Run(scenario, func(t *testing.T) {
			journal, job, key, dir, now := fixture(t, scenario)
			defer func() { journal.Close() }()
			ctx := context.Background()
			for i := 0; i < 12; i++ {
				r, e := journal.Get(ctx, job.ID)
				if e != nil {
					t.Fatal(e)
				}
				if Terminal(r.State) {
					if r.State != want {
						t.Fatalf("got %s want %s", r.State, want)
					}
					target, e := journal.Target(ctx)
					if e != nil {
						t.Fatal(e)
					}
					if scenario == "success" {
						if target.Current != "2.0" || target.KnownGood != "2.0" {
							t.Fatal(target)
						}
					} else if scenario == "rollback_failure" {
						if target.Current != "2.0" || target.KnownGood != "1.0" {
							t.Fatal("lost recovery reference", target)
						}
					} else if target.Current != "1.0" || target.KnownGood != "1.0" {
						t.Fatal("failed to restore target", target)
					}
					before, _ := journal.Events(ctx, false)
					if e = journal.Prepare(ctx, job, r.DeviceID, "ignored", now); e != nil {
						t.Fatal(e)
					}
					if e = journal.Step(ctx, r, key, now); e != nil {
						t.Fatal(e)
					}
					after, _ := journal.Events(ctx, false)
					if len(after) != len(before) {
						t.Fatal("terminal update repeated")
					}
					return
				}
				if e = journal.Step(ctx, r, key, now); e != nil {
					t.Fatal(e)
				}
				if e = journal.Close(); e != nil {
					t.Fatal(e)
				}
				journal, e = Open(dir)
				if e != nil {
					t.Fatal(e)
				}
				now = now.Add(time.Second)
			}
			t.Fatal("did not terminate")
		})
	}
}
func TestRevocationAndExpiredLeaseRestoreWithoutCentralService(t *testing.T) {
	for _, checkpoint := range []string{"staged", "switching", "verifying", "rolling_back"} {
		for _, reason := range []string{"abort", "lease", "trust", "deadline"} {
			t.Run(checkpoint+"/"+reason, func(t *testing.T) {
				journal, job, key, _, now := fixture(t, "health_timeout")
				defer journal.Close()
				ctx := context.Background()
				for {
					r, _ := journal.Get(ctx, job.ID)
					if r.State == checkpoint {
						break
					}
					if e := journal.Step(ctx, r, key, now); e != nil {
						t.Fatal(e)
					}
					now = now.Add(time.Second)
				}
				switch reason {
				case "abort":
					if e := journal.RequestAbort(ctx, job.ID); e != nil {
						t.Fatal(e)
					}
				case "lease":
					now = now.Add(6 * time.Minute)
				case "trust":
					key = make([]byte, 32)
				case "deadline":
					now = now.Add(70 * time.Second)
				}
				for i := 0; i < 4; i++ {
					r, _ := journal.Get(ctx, job.ID)
					if Terminal(r.State) {
						break
					}
					if e := journal.Step(ctx, r, key, now); e != nil {
						t.Fatal(e)
					}
				}
				r, _ := journal.Get(ctx, job.ID)
				target, _ := journal.Target(ctx)
				if !Terminal(r.State) || target.Current != "1.0" || target.KnownGood != "1.0" {
					t.Fatalf("unsafe recovery: %+v %+v", r, target)
				}
			})
		}
	}
}
func TestJournalAtomicityAndFencing(t *testing.T) {
	journal, job, key, _, now := fixture(t, "success")
	defer journal.Close()
	ctx := context.Background()
	r, _ := journal.Get(ctx, job.ID)
	if e := journal.Step(ctx, r, key, now); e != nil {
		t.Fatal(e)
	}
	if e := journal.Step(ctx, r, key, now); !errors.Is(e, ErrCheckpoint) {
		t.Fatal("stale helper not fenced", e)
	}
	r, _ = journal.Get(ctx, job.ID)
	if _, e := journal.db.Exec(`CREATE TRIGGER fail_history BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'disk failure'); END;`); e != nil {
		t.Fatal(e)
	}
	if e := journal.Step(ctx, r, key, now); e == nil {
		t.Fatal("expected history failure")
	}
	target, _ := journal.Target(ctx)
	current, _ := journal.Get(ctx, job.ID)
	if target.Current != "1.0" || current.State != "switching" {
		t.Fatal("pointer changed without committed history")
	}
	if _, e := journal.db.Exec(`DROP TRIGGER fail_history`); e != nil {
		t.Fatal(e)
	}
	if e := journal.RequestAbort(ctx, job.ID); e != nil {
		t.Fatal(e)
	}
	if e := journal.Step(ctx, r, key, now); !errors.Is(e, ErrCheckpoint) {
		t.Fatal("stale helper ignored abort", e)
	}
}
func TestHelperLaunchFailuresBounded(t *testing.T) {
	journal, job, _, _, now := fixture(t, "helper_launch_failure")
	defer journal.Close()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if e := journal.LaunchFailed(ctx, job.ID, now); e != nil {
			t.Fatal(e)
		}
	}
	r, _ := journal.Get(ctx, job.ID)
	if r.State != "failed" || r.LaunchAttempts != 3 {
		t.Fatal(r)
	}
	target, _ := journal.Target(ctx)
	if target.Current != "1.0" {
		t.Fatal("failed launch switched target")
	}
}

// Executed only in a dedicated test subprocess. The launcher exits before its child
// finishes health checking, proving recovery is independent of the launching process.
func TestRecoveryHelperProcess(t *testing.T) {
	mode := os.Getenv("KIOSENTRA_UPDATER_TEST_MODE")
	if mode == "" {
		return
	}
	if mode == "launcher" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryHelperProcess$")
		cmd.Env = append(os.Environ(), "KIOSENTRA_UPDATER_TEST_MODE=worker")
		detach(cmd)
		if e := cmd.Start(); e != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	key, e := base64.StdEncoding.DecodeString(os.Getenv("KIOSENTRA_UPDATER_TEST_KEY"))
	if e != nil {
		os.Exit(3)
	}
	if e = Run(context.Background(), os.Getenv("KIOSENTRA_UPDATER_TEST_DIR"), key); e != nil {
		os.Exit(4)
	}
	os.Exit(0)
}
func TestDetachedHelperSurvivesLauncherExit(t *testing.T) {
	journal, job, key, dir, _ := fixture(t, "health_timeout")
	defer journal.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryHelperProcess$")
	cmd.Env = append(os.Environ(), "KIOSENTRA_UPDATER_TEST_MODE=launcher", "KIOSENTRA_UPDATER_TEST_DIR="+dir, "KIOSENTRA_UPDATER_TEST_KEY="+base64.StdEncoding.EncodeToString(key))
	if e := cmd.Run(); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, e := journal.Get(context.Background(), job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if Terminal(r.State) {
			if r.State != "rolled_back" {
				t.Fatal(r.State)
			}
			events, _ := journal.Events(context.Background(), true)
			if len(events) != 5 {
				t.Fatalf("duplicate/lost transitions: %d", len(events))
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("independent helper did not recover")
}

func TestHelperDoesNotInheritServiceSecrets(t *testing.T) {
	t.Setenv("KIOSENTRA_ENROLLMENT_TOKEN", "never-forward-enrollment")
	t.Setenv("KIOSENTRA_DEVELOPMENT_KEY", "never-forward-storage-key")
	t.Setenv("KIOSENTRA_IPC_KEY", "never-forward-ipc-key")
	t.Setenv("KIOSENTRA_MAINTENANCE_SHA256", "never-forward-maintenance")
	for _, entry := range helperEnvironment(t.TempDir(), make([]byte, 32)) {
		for _, secret := range []string{"never-forward-enrollment", "never-forward-storage-key", "never-forward-ipc-key", "never-forward-maintenance"} {
			if strings.Contains(entry, secret) {
				t.Fatal("helper received service secret")
			}
		}
	}
}

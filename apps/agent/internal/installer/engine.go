// Package installer implements a one-shot, journaled MSI executor. It is not wired
// into the Agent service: central dispatch and the privileged broker are later gates.
package installer

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"kiosentra/packages/contracts"
)

type Stager interface {
	Stage(context.Context, contracts.ArtifactAuthorization) (string, error)
}
type Adapter interface {
	Inspect(context.Context, string) (contracts.MSIIdentity, error)
	InstalledVersion(context.Context, string) (string, error)
	Install(context.Context, string) (int, error)
}
type Result = contracts.InstallationResult
type Engine struct {
	Journal       *Journal
	Stager        Stager
	Adapter       Adapter
	DeviceID      string
	Keys          map[string]ed25519.PublicKey
	Timeout       time.Duration
	Now           func() time.Time
	BeforeInstall func(context.Context) error
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) Execute(ctx context.Context, a contracts.ArtifactAuthorization) (Result, error) {
	r := Result{JobID: a.JobID, ExitCode: -1}
	if e.Journal == nil || e.Stager == nil || e.Adapter == nil || e.Timeout <= 0 || e.Timeout > time.Hour {
		return r, errors.New("invalid installer configuration")
	}
	e.Journal.mu.Lock()
	defer e.Journal.mu.Unlock()
	if err := contracts.VerifyArtifact(a, e.DeviceID, e.Keys, e.now()); err != nil {
		return r, err
	}
	if a.MSI == nil {
		return r, errors.New("signed MSI product identity required for execution")
	}
	b, _ := json.Marshal(a)
	h := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(h[:])
	save := func(state, message string) (Result, error) {
		r.State = state
		r.Message = message
		r.UpdatedAt = e.now()
		return r, e.Journal.save(r, fingerprint)
	}
	old, found, err := e.Journal.result(a.JobID, fingerprint)
	if err != nil {
		return r, err
	}
	if found {
		r = old
		if old.State == "installing" {
			return save("needs_operator", "Previous executor ended with an uncertain installer outcome; automatic replay prohibited")
		}
		if old.State != "staging" {
			return old, nil
		}
	}
	blocked, err := e.Journal.blocked()
	if err != nil {
		return r, err
	}
	if blocked {
		return r, errors.New("an earlier installation requires operator recovery or reboot acceptance")
	}
	if _, err = save("staging", "Preparing verified MSI artifact"); err != nil {
		return r, err
	}
	deadline := e.now().Add(e.Timeout)
	if a.ExpiresAt.Before(deadline) {
		deadline = a.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	path, err := e.Stager.Stage(ctx, a)
	if err != nil {
		return save("failed", "Artifact staging failed; installer was not launched")
	}
	// Recheck the bytes immediately before inspecting and executing. Staging paths
	// and all parent directories must be writable only by the trusted executor.
	if err = verifyFile(path, a.Release); err != nil {
		return save("failed", "Staged artifact changed; installer was not launched")
	}
	identity, err := e.Adapter.Inspect(ctx, path)
	if err != nil || !strings.EqualFold(identity.ProductCode, a.MSI.ProductCode) || identity.ProductVersion != a.MSI.ProductVersion {
		return save("failed", "MSI product identity does not match signed authorization; installer was not launched")
	}
	installed, err := e.Adapter.InstalledVersion(ctx, a.MSI.ProductCode)
	if err != nil {
		return save("failed", "Installed product state unavailable; installer was not launched")
	}
	if installed == a.MSI.ProductVersion {
		return save("succeeded", "Expected MSI product version is already installed; no installer launched")
	}
	if installed != "" {
		return save("failed", "Different version of this product is installed; upgrades require a separately validated adapter")
	}
	if err = ctx.Err(); err != nil {
		return save("failed", "Installation cancelled or authorization expired before launch")
	}
	if err = contracts.VerifyArtifact(a, e.DeviceID, e.Keys, e.now()); err != nil {
		return save("failed", "Authorization no longer valid before launch")
	}
	if err = verifyFile(path, a.Release); err != nil {
		return save("failed", "Artifact changed during preflight; installer was not launched")
	}
	if e.BeforeInstall != nil {
		if err = e.BeforeInstall(ctx); err != nil {
			return save("cancelled", "Execution lease or local cancellation denied launch")
		}
	}
	if ctx.Err() != nil {
		return save("cancelled", "Execution stopped before installer intent")
	}
	if _, err = save("installing", "Installer intent persisted; crash or interruption requires operator reconciliation"); err != nil {
		return r, err
	}
	r.ExitCode, err = e.Adapter.Install(ctx, path)
	if err != nil || ctx.Err() != nil {
		return save("needs_operator", "Installer interrupted or outcome unavailable; do not automatically retry")
	}
	if r.ExitCode == 3010 {
		return save("reboot_required", "MSI requested a reboot; native post-reboot acceptance required")
	}
	if r.ExitCode != 0 {
		return save("needs_operator", "MSI returned a non-success result; inspect package effects before another installation")
	}
	installed, err = e.Adapter.InstalledVersion(ctx, a.MSI.ProductCode)
	if err != nil || installed != a.MSI.ProductVersion {
		return save("needs_operator", "MSI exited successfully but installed product version was not confirmed")
	}
	return save("succeeded", "MSI installation completed and product version verified")
}

func verifyFile(path string, r contracts.Release) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != r.SizeBytes {
		return errors.New("invalid staged MSI file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, r.SizeBytes+1))
	if err != nil || n != r.SizeBytes || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), r.SHA256) {
		return errors.New("staged MSI digest mismatch")
	}
	return nil
}

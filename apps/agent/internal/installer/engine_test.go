package installer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

type fixtureAdapter struct {
	identity     contracts.MSIIdentity
	installed    string
	code         int
	installs     int
	crash        bool
	interrupt    bool
	wrongVersion bool
}

func (f *fixtureAdapter) Inspect(context.Context, string) (contracts.MSIIdentity, error) {
	return f.identity, nil
}
func (f *fixtureAdapter) InstalledVersion(context.Context, string) (string, error) {
	return f.installed, nil
}
func (f *fixtureAdapter) Install(ctx context.Context, path string) (int, error) {
	f.installs++
	if f.crash {
		panic("fixture executor crash after launch")
	}
	if f.interrupt {
		return -1, errors.New("fixture lost installer outcome")
	}
	if f.code == 0 && !f.wrongVersion {
		f.installed = f.identity.ProductVersion
	}
	return f.code, nil
}

type fixtureStager struct{ path string }

func (f fixtureStager) Stage(context.Context, contracts.ArtifactAuthorization) (string, error) {
	return f.path, nil
}

func fixtureEngine(t *testing.T) (*Engine, contracts.ArtifactAuthorization, *fixtureAdapter, ed25519.PrivateKey, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	payload := []byte("fixture adapter bytes; never a real MSI")
	path := filepath.Join(t.TempDir(), "fixture.msi")
	if err = os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(payload)
	identity := contracts.MSIIdentity{ProductCode: "{" + contracts.ID() + "}", ProductVersion: "1.0.0"}
	a := contracts.ArtifactAuthorization{SchemaVersion: 1, DeviceID: contracts.ID(), JobID: contracts.ID(), IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), MSI: &identity, Release: contracts.Release{Version: "1.0.0", PackageType: "msi", URL: "https://packages.example/fixture.msi", SHA256: hex.EncodeToString(h[:]), SizeBytes: int64(len(payload)), KeyID: "release-1"}}
	if err = contracts.SignArtifact(&a, priv); err != nil {
		t.Fatal(err)
	}
	f := &fixtureAdapter{identity: identity}
	e := &Engine{Journal: j, Stager: fixtureStager{path}, Adapter: f, DeviceID: a.DeviceID, Keys: map[string]ed25519.PublicKey{"release-1": pub}, Timeout: time.Minute}
	return e, a, f, priv, dir
}

func TestMSIFixtureExecutionAndReplay(t *testing.T) {
	e, a, f, _, dir := fixtureEngine(t)
	r, err := e.Execute(context.Background(), a)
	if err != nil || r.State != "succeeded" || f.installs != 1 {
		t.Fatalf("%+v %v installs=%d", r, err, f.installs)
	}
	if err = e.Journal.Close(); err != nil {
		t.Fatal(err)
	}
	e.Journal, err = OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Journal.Close()
	r, err = e.Execute(context.Background(), a)
	if err != nil || r.State != "succeeded" || f.installs != 1 {
		t.Fatal("replayed an installed job", r, err, f.installs)
	}
}

func TestMSIFixtureCrashFencesReplayAndNewJobs(t *testing.T) {
	e, a, f, priv, dir := fixtureEngine(t)
	f.crash = true
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("fixture did not crash")
			}
		}()
		_, _ = e.Execute(context.Background(), a)
	}()
	if err := e.Journal.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	e.Journal, err = OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Journal.Close()
	f.crash = false
	r, err := e.Execute(context.Background(), a)
	if err != nil || r.State != "needs_operator" || f.installs != 1 {
		t.Fatal("uncertain execution retried", r, err)
	}
	a.JobID = contracts.ID()
	if err = contracts.SignArtifact(&a, priv); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(context.Background(), a); err == nil || f.installs != 1 {
		t.Fatal("new work bypassed recovery fence")
	}
}

func TestMSIFixtureNegativePaths(t *testing.T) {
	cases := map[string]struct {
		configure func(*Engine, *fixtureAdapter)
		state     string
		installs  int
	}{
		"wrong product":              {func(e *Engine, f *fixtureAdapter) { f.identity.ProductCode = "{" + contracts.ID() + "}" }, "failed", 0},
		"different existing version": {func(e *Engine, f *fixtureAdapter) { f.installed = "0.9.0" }, "failed", 0},
		"already installed":          {func(e *Engine, f *fixtureAdapter) { f.installed = "1.0.0" }, "succeeded", 0},
		"reboot":                     {func(e *Engine, f *fixtureAdapter) { f.code = 3010 }, "reboot_required", 1},
		"installer failure":          {func(e *Engine, f *fixtureAdapter) { f.code = 1603 }, "needs_operator", 1},
		"unexpected reboot":          {func(e *Engine, f *fixtureAdapter) { f.code = 1641 }, "needs_operator", 1},
		"interruption":               {func(e *Engine, f *fixtureAdapter) { f.interrupt = true }, "needs_operator", 1},
		"unconfirmed version":        {func(e *Engine, f *fixtureAdapter) { f.wrongVersion = true }, "needs_operator", 1},
		"changed bytes": {func(e *Engine, f *fixtureAdapter) {
			if err := os.WriteFile(e.Stager.(fixtureStager).path, []byte("modified"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "failed", 0},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			e, a, f, _, _ := fixtureEngine(t)
			v.configure(e, f)
			r, err := e.Execute(context.Background(), a)
			if err != nil || r.State != v.state || f.installs != v.installs {
				t.Fatalf("%+v %v installs=%d", r, err, f.installs)
			}
		})
	}
}

func TestMSIJournalLockAndChangedAuthorization(t *testing.T) {
	e, a, _, priv, dir := fixtureEngine(t)
	if other, err := OpenJournal(dir); err == nil {
		other.Close()
		t.Fatal("two executors own journal")
	}
	if _, err := e.Execute(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	a.Release.URL = "https://packages.example/other.msi"
	if err := contracts.SignArtifact(&a, priv); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), a); err == nil {
		t.Fatal("accepted different authorization for same job")
	}
}

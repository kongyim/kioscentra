package artifact

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kiosentra/apps/agent/internal/installer"
	"kiosentra/packages/contracts"
)

func setup(t *testing.T, handler http.HandlerFunc) (*Stager, contracts.ArtifactAuthorization, ed25519.PrivateKey) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	h := sha256.Sum256([]byte("fixture"))
	a := contracts.ArtifactAuthorization{SchemaVersion: 1, DeviceID: contracts.ID(), JobID: contracts.ID(), IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Hour), Release: contracts.Release{Version: "1.0", PackageType: "msi", URL: server.URL + "/app.msi", SHA256: hex.EncodeToString(h[:]), SizeBytes: 7, KeyID: "release-1"}}
	if err := contracts.SignArtifact(&a, priv); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Directory: t.TempDir(), DeviceID: a.DeviceID, Keys: map[string]ed25519.PublicKey{"release-1": pub}, Origins: []string{server.URL}, MaxBytes: 1024, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.config.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	// Test server CA only; production always uses system trust and verified TLS.
	s.client.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(s.Close)
	return s, a, priv
}

func TestStageVerifiedBytesAndAuthorization(t *testing.T) {
	s, a, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("unexpected request credentials or encoding")
		}
		_, _ = w.Write([]byte("fixture"))
	})
	first, err := s.Stage(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Stage(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("reused destination")
	}
	b, err := os.ReadFile(first)
	if err != nil || string(b) != "fixture" {
		t.Fatalf("bytes: %q %v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(filepath.Dir(first), "authorization.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved contracts.ArtifactAuthorization
	if err = json.Unmarshal(b, &saved); err != nil || saved != a {
		t.Fatal("authorization not retained")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(first), "artifact.part")); !os.IsNotExist(err) {
		t.Fatal("partial file retained")
	}
}

func TestStageFailuresLeaveNoArtifact(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"hash":  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("badbyte")) },
		"short": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("short")) },
		"oversize chunked": func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", 302) },
		"status":   func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) },
		"encoded": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write([]byte("fixture"))
		},
		"timeout": func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			s, a, _ := setup(t, handler)
			if name == "timeout" {
				s.client.Timeout = 30 * time.Millisecond
			}
			if _, err := s.Stage(context.Background(), a); err == nil {
				t.Fatal("accepted invalid artifact")
			}
			entries, err := os.ReadDir(s.config.Directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("partial artifacts: %v %v", entries, err)
			}
		})
	}
}

func TestStageRejectsBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	s, a, priv := setup(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte("fixture")) })
	for _, mutate := range []func(*contracts.ArtifactAuthorization){
		func(v *contracts.ArtifactAuthorization) { v.DeviceID = contracts.ID() },
		func(v *contracts.ArtifactAuthorization) {
			v.Release.SizeBytes = 1025
			_ = contracts.SignArtifact(v, priv)
		},
		func(v *contracts.ArtifactAuthorization) {
			v.Release.URL = "https://other.example/app.msi"
			_ = contracts.SignArtifact(v, priv)
		},
		func(v *contracts.ArtifactAuthorization) {
			v.IssuedAt = v.IssuedAt.Add(-2 * time.Hour)
			v.ExpiresAt = v.ExpiresAt.Add(-2 * time.Hour)
			_ = contracts.SignArtifact(v, priv)
		},
	} {
		v := a
		mutate(&v)
		if _, err := s.Stage(context.Background(), v); err == nil {
			t.Fatal("accepted invalid admission")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("contacted network before authorization")
	}
}

func TestStageRejectsExpiryDuringDownloadAndUntrustedTLS(t *testing.T) {
	s, a, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("fixture")) })
	var calls int
	s.now = func() time.Time {
		calls++
		if calls > 1 {
			return a.ExpiresAt
		}
		return a.IssuedAt
	}
	if _, err := s.Stage(context.Background(), a); err == nil {
		t.Fatal("accepted expired completion")
	}
	s.now = time.Now
	s.client.CloseIdleConnections()
	s.client.Transport.(*http.Transport).TLSClientConfig = nil
	if _, err := s.Stage(context.Background(), a); err == nil {
		t.Fatal("accepted untrusted TLS")
	}
}

func TestOriginConfiguration(t *testing.T) {
	s, _, _ := setup(t, func(http.ResponseWriter, *http.Request) {})
	for _, origin := range []string{"http://example.com", "https://example.com/", "https://user:pass@example.com", "https://example.com?", "https://example.com?q=1", "https://example.com/#x", "https://simulation.invalid"} {
		c := s.config
		c.Origins = []string{origin}
		if bad, err := New(c); err == nil {
			bad.Close()
			t.Fatalf("accepted %s", origin)
		}
	}
}

func TestCancelledAttemptAndUnsafeDirectory(t *testing.T) {
	s, a, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fixture"))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Stage(ctx, a); err == nil {
		t.Fatal("staged cancelled request")
	}
	entries, err := os.ReadDir(s.config.Directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("cancelled attempt left files")
	}
	if runtime.GOOS == "windows" {
		return // Windows ACL acceptance is a separate native gate.
	}
	if err = os.Chmod(s.config.Directory, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Stage(context.Background(), a); err == nil {
		t.Fatal("accepted shared root")
	}
	if err = os.Chmod(s.config.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(s.config.Directory, link); err != nil {
		t.Fatal(err)
	}
	s.config.Directory = link
	if _, err = s.Stage(context.Background(), a); err == nil {
		t.Fatal("accepted symlink root")
	}
}

type installationFixture struct {
	identity  contracts.MSIIdentity
	installed bool
	calls     int
}

func (f *installationFixture) Inspect(context.Context, string) (contracts.MSIIdentity, error) {
	return f.identity, nil
}
func (f *installationFixture) InstalledVersion(context.Context, string) (string, error) {
	if f.installed {
		return f.identity.ProductVersion, nil
	}
	return "", nil
}
func (f *installationFixture) Install(context.Context, string) (int, error) {
	f.calls++
	f.installed = true
	return 0, nil
}

func TestHTTPSStagingThroughDurableInstallerFixture(t *testing.T) {
	var requests atomic.Int32
	s, a, priv := setup(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte("fixture")) })
	a.Release.Version = "1.0.0"
	a.MSI = &contracts.MSIIdentity{ProductCode: "{" + contracts.ID() + "}", ProductVersion: "1.0.0"}
	if err := contracts.SignArtifact(&a, priv); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	j, err := installer.OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &installationFixture{identity: *a.MSI}
	e := installer.Engine{Journal: j, Stager: s, Adapter: f, DeviceID: a.DeviceID, Keys: s.config.Keys, Timeout: time.Minute}
	r, err := e.Execute(context.Background(), a)
	if err != nil || r.State != "succeeded" || requests.Load() != 1 || f.calls != 1 {
		j.Close()
		t.Fatalf("%+v %v requests=%d installs=%d", r, err, requests.Load(), f.calls)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = installer.OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	e.Journal = j
	r, err = e.Execute(context.Background(), a)
	if err != nil || r.State != "succeeded" || requests.Load() != 1 || f.calls != 1 {
		t.Fatal("restart repeated download or execution", r, err)
	}
	status, err := j.Status(a.JobID)
	if err != nil || status.State != "succeeded" {
		t.Fatal("durable status unavailable", status, err)
	}
}

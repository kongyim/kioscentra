// Package artifact stages authenticated MSI bytes without executing them.
package artifact

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"kiosentra/packages/contracts"
)

const MaxSizeBytes int64 = 2 << 30

type Config struct {
	Directory string
	DeviceID  string
	Keys      map[string]ed25519.PublicKey
	Origins   []string
	MaxBytes  int64
	Timeout   time.Duration
}

type Stager struct {
	config  Config
	origins map[string]bool
	client  *http.Client
	now     func() time.Time
}

func New(c Config) (*Stager, error) {
	if !filepath.IsAbs(c.Directory) || !contracts.ValidID(c.DeviceID) || len(c.Keys) == 0 ||
		c.MaxBytes < 1 || c.MaxBytes > MaxSizeBytes || c.Timeout <= 0 || c.Timeout > 15*time.Minute {
		return nil, errors.New("invalid artifact staging configuration")
	}
	keys := make(map[string]ed25519.PublicKey, len(c.Keys))
	for id, key := range c.Keys {
		if strings.TrimSpace(id) == "" || len(id) > 128 || id == contracts.SimulationKeyID || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid artifact trust key")
		}
		keys[id] = append(ed25519.PublicKey(nil), key...)
	}
	c.Keys = keys
	origins := map[string]bool{}
	for _, origin := range c.Origins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
			u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Path != "" ||
			u.Hostname() == "simulation.invalid" {
			return nil, errors.New("artifact origins must be HTTPS origins without a path, credentials, query or fragment")
		}
		origins[u.Scheme+"://"+u.Host] = true
	}
	if len(origins) == 0 {
		return nil, errors.New("artifact origin allowlist is required")
	}
	// Never reuse the Agent's authenticated client, cookies or proxy credentials.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	return &Stager{config: c, origins: origins, now: time.Now, client: &http.Client{
		Transport: transport, Timeout: c.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("artifact redirects are forbidden") },
	}}, nil
}

func (s *Stager) Close() { s.client.CloseIdleConnections() }

// Stage uses a fresh private directory for every attempt. Failed/partial attempts
// are removed; an existing verified artifact is never overwritten or trusted on reuse.
// The caller must revalidate bytes immediately before any future execution.
func (s *Stager) Stage(ctx context.Context, a contracts.ArtifactAuthorization) (string, error) {
	if err := contracts.VerifyArtifact(a, s.config.DeviceID, s.config.Keys, s.now()); err != nil {
		return "", err
	}
	if a.Release.SizeBytes > s.config.MaxBytes {
		return "", errors.New("artifact exceeds local size limit")
	}
	u, _ := url.Parse(a.Release.URL)
	if !s.origins[u.Scheme+"://"+u.Host] {
		return "", errors.New("artifact origin is not allowed")
	}
	info, err := os.Lstat(s.config.Directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return "", errors.New("staging root must be a private directory; provision Windows ACLs separately")
	}
	root, err := os.OpenRoot(s.config.Directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	name := "stage-" + contracts.ID()
	if err = root.Mkdir(name, 0700); err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = root.RemoveAll(name)
		}
	}()
	dir, err := root.OpenRoot(name)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	f, err := dir.OpenFile("artifact.part", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	ctx, cancel := context.WithDeadline(ctx, a.ExpiresAt)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Release.URL, nil)
	if err != nil {
		return "", errors.New("invalid artifact request")
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", errors.New("artifact download failed (transport, timeout, cancellation or redirect)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || (resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity") {
		return "", errors.New("artifact response must be unencoded HTTP 200")
	}
	if resp.ContentLength >= 0 && resp.ContentLength != a.Release.SizeBytes {
		return "", errors.New("artifact content length mismatch")
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, a.Release.SizeBytes+1))
	if err != nil {
		return "", errors.New("artifact body interrupted")
	}
	if n != a.Release.SizeBytes || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.Release.SHA256) {
		return "", errors.New("artifact size or SHA-256 mismatch")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = contracts.VerifyArtifact(a, s.config.DeviceID, s.config.Keys, s.now()); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	// Keep the signed authorization beside the bytes for review and later verification.
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	manifest, err := dir.OpenFile("authorization.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := manifest.Write(b)
	syncErr := manifest.Sync()
	closeErr := manifest.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = contracts.VerifyArtifact(a, s.config.DeviceID, s.config.Keys, s.now()); err != nil {
		return "", err
	}
	if err = dir.Rename("artifact.part", "artifact.msi"); err != nil {
		return "", err
	}
	complete = true
	return filepath.Join(s.config.Directory, name, "artifact.msi"), nil
}

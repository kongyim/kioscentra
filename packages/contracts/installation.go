package contracts

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const MaxInstallationLease = 90 * time.Second

// InstallationLease authorizes a short execution window separately from release trust.
// The Agent transports it but cannot manufacture or extend it.
type InstallationLease struct {
	DeviceID            string    `json:"deviceId"`
	JobID               string    `json:"jobId"`
	AuthorizationSHA256 string    `json:"authorizationSHA256"`
	IssuedAt            time.Time `json:"issuedAt"`
	ExpiresAt           time.Time `json:"expiresAt"`
	KeyID               string    `json:"keyId"`
	Signature           string    `json:"signature"`
}

func ArtifactDigest(a ArtifactAuthorization) string {
	b, _ := json.Marshal(a)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (l InstallationLease) signingBytes() []byte {
	l.Signature = ""
	b, _ := json.Marshal(struct {
		Domain string `json:"domain"`
		InstallationLease
	}{"kiosentra-installation-lease-v1", l})
	return b
}
func (l InstallationLease) validate() error {
	h, err := hex.DecodeString(l.AuthorizationSHA256)
	if !ValidID(l.DeviceID) || !ValidID(l.JobID) || err != nil || len(h) != 32 || !UTC(l.IssuedAt) || !UTC(l.ExpiresAt) || !l.ExpiresAt.After(l.IssuedAt) || l.ExpiresAt.Sub(l.IssuedAt) > MaxInstallationLease || !text(l.KeyID) || len(l.KeyID) > 128 || l.KeyID == SimulationKeyID {
		return errors.New("invalid installation lease")
	}
	return nil
}
func SignInstallationLease(l *InstallationLease, key ed25519.PrivateKey) error {
	if l == nil || len(key) != ed25519.PrivateKeySize {
		return errors.New("invalid installation lease signing input")
	}
	if err := l.validate(); err != nil {
		return err
	}
	l.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, l.signingBytes()))
	return nil
}
func VerifyInstallationLease(l InstallationLease, a ArtifactAuthorization, keys map[string]ed25519.PublicKey, now time.Time) error {
	if err := l.validate(); err != nil {
		return err
	}
	if l.DeviceID != a.DeviceID || l.JobID != a.JobID || l.AuthorizationSHA256 != ArtifactDigest(a) || now.Before(l.IssuedAt) || !now.Before(l.ExpiresAt) || l.ExpiresAt.After(a.ExpiresAt) {
		return errors.New("installation lease binding or expiry rejected")
	}
	key := keys[l.KeyID]
	sig, err := base64.StdEncoding.DecodeString(l.Signature)
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, l.signingBytes(), sig) {
		return errors.New("installation lease signature rejected")
	}
	return nil
}

type InstallationResult struct {
	JobID     string    `json:"jobId"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
	ExitCode  int       `json:"exitCode"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (r InstallationResult) Validate() error {
	if !ValidID(r.JobID) || !UTC(r.UpdatedAt) || len(r.Message) > 1024 {
		return errors.New("invalid installation result")
	}
	switch r.State {
	case "queued", "staging", "installing", "succeeded", "failed", "cancelled", "needs_operator", "reboot_required":
		return nil
	}
	return errors.New("invalid installation state")
}
func InstallationTerminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled" || state == "needs_operator" || state == "reboot_required"
}

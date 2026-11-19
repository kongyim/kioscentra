package contracts

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ArtifactAuthorization is deliberately separate from simulation jobs. Expiry is
// immutable and signed; a renewable execution lease cannot extend this permission.
type ArtifactAuthorization struct {
	SchemaVersion int          `json:"schemaVersion"`
	DeviceID      string       `json:"deviceId"`
	JobID         string       `json:"jobId"`
	IssuedAt      time.Time    `json:"issuedAt"`
	ExpiresAt     time.Time    `json:"expiresAt"`
	Release       Release      `json:"release"`
	MSI           *MSIIdentity `json:"msi,omitempty"`
}

// MSIIdentity is optional for staging, but mandatory for execution admission.
type MSIIdentity struct {
	ProductCode    string `json:"productCode"`
	ProductVersion string `json:"productVersion"`
}

func (m MSIIdentity) Validate() error {
	if len(m.ProductCode) != 38 || m.ProductCode[0] != '{' || m.ProductCode[37] != '}' || !ValidID(m.ProductCode[1:37]) {
		return errors.New("MSI product code must be a braced GUID")
	}
	parts := strings.Split(m.ProductVersion, ".")
	if len(parts) != 3 {
		return errors.New("MSI product version must have three numeric components")
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		limit := 255
		if i == 2 {
			limit = 65535
		}
		if err != nil || n < 0 || n > limit || strconv.Itoa(n) != p {
			return errors.New("invalid MSI product version")
		}
	}
	return nil
}

const MaxArtifactAuthorizationLifetime = 24 * time.Hour

func (a ArtifactAuthorization) validate() error {
	if a.SchemaVersion != 1 || !ValidID(a.DeviceID) || !ValidID(a.JobID) ||
		!UTC(a.IssuedAt) || !UTC(a.ExpiresAt) || !a.ExpiresAt.After(a.IssuedAt) ||
		a.ExpiresAt.Sub(a.IssuedAt) > MaxArtifactAuthorizationLifetime {
		return errors.New("invalid artifact authorization")
	}
	r := a.Release
	if a.MSI != nil {
		if err := a.MSI.Validate(); err != nil {
			return err
		}
		if a.MSI.ProductVersion != r.Version {
			return errors.New("MSI and release versions must match")
		}
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r.PackageType != "msi" || strings.TrimSpace(r.Version) == "" || len(r.Version) > 256 ||
		r.SizeBytes <= 0 || r.KeyID == SimulationKeyID || len(r.KeyID) > 128 {
		return errors.New("unsupported artifact release")
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" ||
		u.Hostname() == "simulation.invalid" || len(r.URL) > 4096 {
		return errors.New("invalid artifact URL")
	}
	return nil
}

func (a ArtifactAuthorization) signingBytes() []byte {
	a.Release.Signature = ""
	b, _ := json.Marshal(struct {
		Domain string `json:"domain"`
		ArtifactAuthorization
	}{"kiosentra-artifact-authorization-v1", a})
	return b
}

// SignArtifact signs release metadata, device, job and the fixed validity window.
func SignArtifact(a *ArtifactAuthorization, key ed25519.PrivateKey) error {
	if a == nil || len(key) != ed25519.PrivateKeySize {
		return errors.New("invalid artifact signing input")
	}
	candidate := *a
	candidate.Release.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	if err := candidate.validate(); err != nil {
		return err
	}
	candidate.Release.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, candidate.signingBytes()))
	*a = candidate
	return nil
}

func VerifyArtifact(a ArtifactAuthorization, deviceID string, keys map[string]ed25519.PublicKey, now time.Time) error {
	if err := a.validate(); err != nil {
		return err
	}
	if a.DeviceID != deviceID || now.Before(a.IssuedAt) || !now.Before(a.ExpiresAt) {
		return errors.New("artifact device binding or validity window rejected")
	}
	key := keys[a.Release.KeyID]
	sig, err := base64.StdEncoding.DecodeString(a.Release.Signature)
	if len(key) != ed25519.PublicKeySize || err != nil || !ed25519.Verify(key, a.signingBytes(), sig) {
		return errors.New("artifact signing key or signature rejected")
	}
	return nil
}

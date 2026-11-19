package contracts

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestInstallationLeaseBindingAndExpiry(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	a := ArtifactAuthorization{DeviceID: ID(), JobID: ID(), ExpiresAt: now.Add(time.Hour)}
	l := InstallationLease{DeviceID: a.DeviceID, JobID: a.JobID, AuthorizationSHA256: ArtifactDigest(a), IssuedAt: now, ExpiresAt: now.Add(time.Minute), KeyID: "central-1"}
	if err := SignInstallationLease(&l, priv); err != nil {
		t.Fatal(err)
	}
	keys := map[string]ed25519.PublicKey{"central-1": pub}
	if err := VerifyInstallationLease(l, a, keys, now); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*InstallationLease){func(v *InstallationLease) { v.JobID = ID() }, func(v *InstallationLease) { v.DeviceID = ID() }, func(v *InstallationLease) { v.ExpiresAt = v.ExpiresAt.Add(time.Second) }, func(v *InstallationLease) { v.KeyID = "unknown" }} {
		v := l
		mutate(&v)
		if VerifyInstallationLease(v, a, keys, now) == nil {
			t.Fatal("accepted changed lease")
		}
	}
	if VerifyInstallationLease(l, a, keys, l.ExpiresAt) == nil {
		t.Fatal("accepted expired lease")
	}
	a.Release.URL = "https://other.example/package.msi"
	if VerifyInstallationLease(l, a, keys, now) == nil {
		t.Fatal("lease authorized different artifact")
	}
	l.ExpiresAt = now.Add(91 * time.Second)
	if SignInstallationLease(&l, priv) == nil {
		t.Fatal("signed excessive lease")
	}
}

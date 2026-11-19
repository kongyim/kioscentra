package contracts

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestArtifactAuthorizationBindsAllMetadata(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	h := sha256.Sum256([]byte("fixture"))
	a := ArtifactAuthorization{SchemaVersion: 1, DeviceID: ID(), JobID: ID(), IssuedAt: now, ExpiresAt: now.Add(time.Hour), Release: Release{
		Version: "1.0", PackageType: "msi", URL: "https://packages.example/app.msi", SHA256: hex.EncodeToString(h[:]), KeyID: "release-1", SizeBytes: 7,
	}}
	if err := SignArtifact(&a, priv); err != nil {
		t.Fatal(err)
	}
	keys := map[string]ed25519.PublicKey{"release-1": pub}
	if err := VerifyArtifact(a, a.DeviceID, keys, now); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*ArtifactAuthorization){
		"device":  func(v *ArtifactAuthorization) { v.DeviceID = ID() },
		"job":     func(v *ArtifactAuthorization) { v.JobID = ID() },
		"issued":  func(v *ArtifactAuthorization) { v.IssuedAt = v.IssuedAt.Add(-time.Minute) },
		"expiry":  func(v *ArtifactAuthorization) { v.ExpiresAt = v.ExpiresAt.Add(time.Minute) },
		"version": func(v *ArtifactAuthorization) { v.Release.Version = "2.0" },
		"url":     func(v *ArtifactAuthorization) { v.Release.URL = "https://packages.example/other.msi" },
		"size":    func(v *ArtifactAuthorization) { v.Release.SizeBytes++ },
		"hash":    func(v *ArtifactAuthorization) { v.Release.SHA256 = hex.EncodeToString(make([]byte, 32)) },
		"key":     func(v *ArtifactAuthorization) { v.Release.KeyID = "unknown" },
		"type":    func(v *ArtifactAuthorization) { v.Release.PackageType = "exe" },
		"schema":  func(v *ArtifactAuthorization) { v.SchemaVersion = 2 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			v := a
			mutate(&v)
			if VerifyArtifact(v, a.DeviceID, keys, now) == nil {
				t.Fatal("accepted modified authorization")
			}
		})
	}
	for _, when := range []time.Time{now.Add(-time.Nanosecond), a.ExpiresAt, a.ExpiresAt.Add(time.Hour)} {
		if VerifyArtifact(a, a.DeviceID, keys, when) == nil {
			t.Fatal("accepted outside validity window")
		}
	}
	if VerifyArtifact(a, ID(), keys, now) == nil || VerifyArtifact(a, a.DeviceID, nil, now) == nil {
		t.Fatal("accepted missing trust/device")
	}
	a.ExpiresAt = now.Add(25 * time.Hour)
	if SignArtifact(&a, priv) == nil {
		t.Fatal("signed excessive lifetime")
	}
}

func TestArtifactRejectsSimulationSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	device := ID()
	j := Job{ID: ID(), Kind: "deployment", CreatedAt: now, LeaseExpiresAt: now.Add(time.Hour), TargetVersion: "1.0"}
	if err := SignSimulation(device, &j, "success", priv); err != nil {
		t.Fatal(err)
	}
	a := ArtifactAuthorization{SchemaVersion: 1, DeviceID: device, JobID: j.ID, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Release: *j.Release}
	a.Release.PackageType = "msi"
	a.Release.KeyID = "release-1"
	a.Release.URL = "https://packages.example/app.msi"
	if VerifyArtifact(a, device, map[string]ed25519.PublicKey{"release-1": pub}, now) == nil {
		t.Fatal("simulation signature crossed domains")
	}
}

func TestSignedMSIIdentity(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	h := sha256.Sum256([]byte("fixture"))
	a := ArtifactAuthorization{SchemaVersion: 1, DeviceID: ID(), JobID: ID(), IssuedAt: now, ExpiresAt: now.Add(time.Hour), MSI: &MSIIdentity{ProductCode: "{" + ID() + "}", ProductVersion: "1.0.0"}, Release: Release{Version: "1.0.0", PackageType: "msi", URL: "https://packages.example/app.msi", SHA256: hex.EncodeToString(h[:]), SizeBytes: 7, KeyID: "release-1"}}
	if err := SignArtifact(&a, priv); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifact(a, a.DeviceID, map[string]ed25519.PublicKey{"release-1": pub}, now); err != nil {
		t.Fatal(err)
	}
	a.MSI.ProductCode = "{" + ID() + "}"
	if VerifyArtifact(a, a.DeviceID, map[string]ed25519.PublicKey{"release-1": pub}, now) == nil {
		t.Fatal("product identity not signed")
	}
	a.MSI.ProductVersion = "2.0.0"
	if SignArtifact(&a, priv) == nil {
		t.Fatal("signed mismatched release version")
	}
	for _, version := range []string{"1.0", "1.0.0.0", "1.0.-1", "256.0.0", "1.0.65536", "1.00.0", "1.0.+1"} {
		if (MSIIdentity{ProductCode: "{" + ID() + "}", ProductVersion: version}).Validate() == nil {
			t.Fatalf("accepted %s", version)
		}
	}
}

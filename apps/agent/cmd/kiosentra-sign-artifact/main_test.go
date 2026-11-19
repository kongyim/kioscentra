package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func TestOfflineSigningAndOverwriteProtection(t *testing.T) {
	dir := t.TempDir()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := filepath.Join(dir, "private.key")
	input := filepath.Join(dir, "input.json")
	output := filepath.Join(dir, "signed.json")
	if err := os.WriteFile(key, []byte(base64.StdEncoding.EncodeToString(priv)), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	h := sha256.Sum256([]byte("fixture"))
	a := contracts.ArtifactAuthorization{SchemaVersion: 1, DeviceID: contracts.ID(), JobID: contracts.ID(), IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Hour), Release: contracts.Release{Version: "1.0", PackageType: "msi", URL: "https://packages.example/app.msi", SHA256: hex.EncodeToString(h[:]), KeyID: "release-1", SizeBytes: 7}}
	b, _ := json.Marshal(a)
	if err := os.WriteFile(input, b, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--input", input, "--private-key-file", key, "--output", output}
	if err := run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	if err = contracts.VerifyArtifact(a, a.DeviceID, map[string]ed25519.PublicKey{"release-1": pub}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if run(context.Background(), args) == nil {
		t.Fatal("overwrote existing signed authorization")
	}
	after, _ := os.ReadFile(output)
	if string(after) != string(b) {
		t.Fatal("changed signed file")
	}
}

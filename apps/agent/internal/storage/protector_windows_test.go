//go:build windows

package storage

import (
	"bytes"
	"testing"
)

func TestDPAPIRoundTrip(t *testing.T) {
	p, err := PlatformProtector(false, "")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("native-windows-protected-test-credential")
	sealed, err := p.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("DPAPI emitted plaintext")
	}
	got, err := p.Open(sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := p.Open(sealed); err == nil {
		t.Fatal("DPAPI accepted modified ciphertext")
	}
}

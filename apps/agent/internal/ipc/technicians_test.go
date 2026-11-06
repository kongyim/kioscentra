package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTechnicianConfigurationRejectsMalformedProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "technicians.json")
	hash := strings.Repeat("a", 64)
	for _, body := range []string{
		`{}`, `null`, `{"bad actor":{"sha256":"` + hash + `","scopes":["refresh_inventory"]}}`,
		`{"alice":{"sha256":"` + hash + `","scopes":["execute_shell"]}}`,
		`{"alice":{"sha256":"short","scopes":["refresh_inventory"]}}`,
		`{"alice":{"sha256":"` + hash + `","scopes":["refresh_inventory"],"admin":true}}`,
		`{"alice":{"sha256":"` + hash + `","scopes":["refresh_inventory"]}} {}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadTechnicians(path); err == nil {
			t.Fatal("invalid technician configuration accepted")
		}
	}
	if err := os.WriteFile(path, []byte(`{"alice":{"sha256":"`+hash+`","scopes":["refresh_inventory"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if profiles, err := LoadTechnicians(path); err != nil || len(profiles) != 1 {
		t.Fatal(profiles, err)
	}
}

func TestSharedMaintenanceSecretIsDevelopmentOnly(t *testing.T) {
	t.Setenv("KIOSENTRA_CONFIG_FILE", "")
	t.Setenv("KIOSENTRA_TECHNICIANS_FILE", "")
	t.Setenv("KIOSENTRA_MAINTENANCE_SHA256", strings.Repeat("a", 64))
	if _, err := LoadOptions(t.TempDir(), false); err == nil {
		t.Fatal("shared production maintenance secret accepted")
	}
}

func TestNamedTechnicianScopeAndAudit(t *testing.T) {
	hash := sha256.Sum256([]byte("random-token-test"))
	s, err := NewServer("")
	if err != nil {
		t.Fatal(err)
	}
	s.Technicians = map[string]Technician{"alice": {SHA256: hex.EncodeToString(hash[:]), Scopes: []string{"refresh_inventory"}}}
	var audit []string
	s.Audit = func(message string) { audit = append(audit, message) }
	now := time.Now()
	s.now = func() time.Time { return now }
	for _, request := range []Request{
		{Method: "authorize", Technician: "alice", Secret: "random-token-test", Scope: "stop_service"},
		{Method: "authorize", Technician: "bob", Secret: "random-token-test", Scope: "refresh_inventory"},
		{Method: "authorize", Technician: "alice", Secret: "wrong", Scope: "refresh_inventory"},
	} {
		if _, err := s.dispatch(context.Background(), request); err == nil {
			t.Fatal("unauthorized technician received grant")
		}
		now = now.Add(6 * time.Second)
	}
	v, err := s.dispatch(context.Background(), Request{Method: "authorize", Technician: "alice", Secret: "random-token-test", Scope: "refresh_inventory"})
	if err != nil {
		t.Fatal(err)
	}
	g := v.(Grant)
	if g.Technician != "alice" {
		t.Fatal("grant lost attribution")
	}
	action := Request{Method: "action", Token: g.Token, Scope: g.Scope}
	if _, err = s.dispatch(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if _, err = s.dispatch(context.Background(), action); err == nil {
		t.Fatal("grant replay accepted")
	}
	joined := strings.Join(audit, "\n")
	if !strings.Contains(joined, "action requested: refresh_inventory actor=alice") || strings.Contains(joined, "random-token-test") || strings.Contains(joined, g.Token) {
		t.Fatal("invalid audit attribution or secret exposure")
	}
}

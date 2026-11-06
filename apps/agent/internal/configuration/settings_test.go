package configuration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectedSettingsAreIsolatedAndFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.json")
	t.Setenv("KIOSENTRA_CONFIG_FILE", path)
	t.Setenv("KIOSENTRA_ENROLLMENT_TOKEN", "must-not-inherit")
	t.Setenv("KIOSENTRA_SIMULATED_UPDATES", "true")
	for _, body := range []string{`{"UNKNOWN":"value"}`, `{"KIOSENTRA_DEVELOPMENT":true}`, `{"KIOSENTRA_DEVELOPMENT":null}`, `null`, strings.Repeat(" ", 65537)} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Settings(); err == nil {
			t.Fatalf("accepted malformed settings %q", body[:min(len(body), 50)])
		}
	}
	if err := os.WriteFile(path, []byte(`{"KIOSENTRA_SERVER_URL":"https://central.example"}`), 0600); err != nil {
		t.Fatal(err)
	}
	get, err := Settings()
	if err != nil {
		t.Fatal(err)
	}
	if get("KIOSENTRA_SERVER_URL") != "https://central.example" || get("KIOSENTRA_ENROLLMENT_TOKEN") != "" || get("KIOSENTRA_SIMULATED_UPDATES") != "" {
		t.Fatal("file configuration inherited environment")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Settings(); err == nil {
		t.Fatal("missing configured file silently fell back")
	}
}

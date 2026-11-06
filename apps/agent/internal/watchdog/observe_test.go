package watchdog

import (
	"context"
	"os"
	"testing"
)

func TestObserveCurrentExecutable(t *testing.T) {
	path, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ps, running, e := (Processes{}).Observe(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	if running == nil || !*running {
		t.Fatal("own process not found")
	}
	found := false
	for _, p := range ps {
		if p.PID == int32(os.Getpid()) {
			found = true
		}
	}
	if !found {
		t.Fatal("own process metrics missing")
	}
}
func TestUnknownAndInvalidExecutable(t *testing.T) {
	for _, path := range []string{"", "kiosk.exe"} {
		_, running, e := (Processes{}).Observe(context.Background(), path)
		if running != nil {
			t.Fatal("unknown must not be false")
		}
		if path != "" && e == nil {
			t.Fatal("relative executable accepted")
		}
	}
	if SameExecutable("kiosk.exe", "kiosk.exe") {
		t.Fatal("basename matched")
	}
}

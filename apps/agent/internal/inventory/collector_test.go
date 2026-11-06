package inventory

import (
	"context"
	"testing"
)

func TestCancellationHasPerSourceDiagnostics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, ds, e := (System{DiskPath: t.TempDir()}).Collect(ctx, "")
	if e == nil || len(ds) != 6 {
		t.Fatal(ds, e)
	}
	for _, d := range ds {
		if d.Error == "" {
			t.Fatalf("missing cancellation for %s", d.Source)
		}
	}
	if v.Software == nil || v.Runtimes == nil || v.Networks == nil {
		t.Fatal("null arrays")
	}
}

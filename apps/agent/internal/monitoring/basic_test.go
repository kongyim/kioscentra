package monitoring

import (
	"context"
	"testing"
)

func TestPartialCollectionDiagnostics(t *testing.T) {
	b := &Basic{DiskPath: "/kiosentra-nonexistent-volume-test/path"}
	v, _, e := b.Collect(context.Background())
	if e == nil {
		t.Fatal("missing disk error")
	}
	if v.MemoryTotalBytes == 0 {
		t.Fatal("successful memory result lost")
	}
	if len(b.Diagnostics) != 4 {
		t.Fatal(b.Diagnostics)
	}
	found := false
	for _, r := range b.Diagnostics {
		if r.Source == "disk" && r.Error != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("disk failure not attributed")
	}
}

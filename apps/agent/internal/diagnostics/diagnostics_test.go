package diagnostics

import (
	"context"
	"testing"
	"time"
)

func TestTimeoutAndCancelledSource(t *testing.T) {
	r := Run(context.Background(), time.Millisecond, "slow", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	if r.Source != "slow" || r.Error == "" {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	r = Run(ctx, time.Second, "cancelled", func(context.Context) error { called = true; return nil })
	if called || r.Error == "" {
		t.Fatal(r)
	}
}

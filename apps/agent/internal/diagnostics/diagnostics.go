// Package diagnostics records bounded, source-specific collection outcomes locally.
package diagnostics

import (
	"context"
	"time"
)

type Result struct {
	Source     string `json:"source"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error"`
}

// Run is synchronous: collectors must honor ctx. No abandoned goroutines accumulate
// when an OS API is slow. Native calls that cannot be interrupted may exceed the deadline.
func Run(ctx context.Context, timeout time.Duration, source string, fn func(context.Context) error) Result {
	start := time.Now()
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := child.Err()
	if err == nil {
		err = fn(child)
	}
	if err == nil {
		err = child.Err()
	}
	r := Result{Source: source, DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		r.Error = err.Error()
		if len(r.Error) > 1024 {
			r.Error = r.Error[:1024]
		}
	}
	return r
}

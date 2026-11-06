// Package watchdog observes kiosk processes. It never launches, kills or restarts them.
package watchdog

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
	"kiosentra/packages/contracts"
)

type Observer interface {
	Observe(context.Context, string) ([]contracts.ProcessMetric, *bool, error)
}
type Processes struct{}

func SameExecutable(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if resolved, e := filepath.EvalSymlinks(a); e == nil {
		a = resolved
	}
	if resolved, e := filepath.EvalSymlinks(b); e == nil {
		b = resolved
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func (Processes) Observe(ctx context.Context, executable string) ([]contracts.ProcessMetric, *bool, error) {
	out := []contracts.ProcessMetric{}
	if executable == "" {
		return out, nil, nil
	}
	if !filepath.IsAbs(executable) {
		return out, nil, errors.New("kiosk executable must be an absolute path")
	}
	ps, e := process.ProcessesWithContext(ctx)
	if e != nil {
		return out, nil, e
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Pid < ps[j].Pid })
	running := false
	var unknown bool
	var errs []error
	for _, p := range ps {
		if e = ctx.Err(); e != nil {
			return out, nil, e
		}
		path, err := p.ExeWithContext(ctx)
		if err != nil { // A disappeared process is normal; inaccessible processes make absence uncertain.
			exists, check := process.PidExistsWithContext(ctx, p.Pid)
			if check != nil || exists {
				unknown = true
			}
			continue
		}
		if !SameExecutable(path, executable) {
			continue
		}
		running = true
		if len(out) == 1000 {
			errs = append(errs, errors.New("process metric limit reached"))
			break
		}
		c, ce := p.CPUPercentWithContext(ctx)
		m, me := p.MemoryInfoWithContext(ctx)
		if ce != nil || me != nil {
			errs = append(errs, fmt.Errorf("pid %d metrics unavailable", p.Pid))
			continue
		}
		out = append(out, contracts.ProcessMetric{PID: p.Pid, CPUPercent: c, MemoryBytes: m.RSS})
	}
	if unknown && !running {
		return out, nil, errors.New("process access incomplete; kiosk absence unknown")
	}
	return out, &running, errors.Join(errs...)
}

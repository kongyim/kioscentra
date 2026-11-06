package monitoring

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"kiosentra/apps/agent/internal/diagnostics"
	"kiosentra/packages/contracts"
)

type Collector interface {
	Collect(context.Context) (contracts.Metrics, uint64, error)
}
type Basic struct {
	DiskPath    string
	Diagnostics []diagnostics.Result
}

// Collect preserves successful readings when another source fails. Diagnostics
// distinguish unavailable values from actual zero readings in the local snapshot.
func (b *Basic) Collect(ctx context.Context) (contracts.Metrics, uint64, error) {
	v := contracts.Metrics{CollectedAt: time.Now().UTC(), Processes: []contracts.ProcessMetric{}}
	var up uint64
	b.Diagnostics = nil
	var errs []error
	run := func(name string, fn func(context.Context) error) {
		r := diagnostics.Run(ctx, 2*time.Second, name, fn)
		b.Diagnostics = append(b.Diagnostics, r)
		if r.Error != "" {
			errs = append(errs, fmt.Errorf("%s: %s", name, r.Error))
		}
	}
	run("cpu", func(ctx context.Context) error {
		c, e := cpu.PercentWithContext(ctx, 250*time.Millisecond, false)
		if e == nil && len(c) > 0 {
			v.CPUPercent = c[0]
		}
		return e
	})
	run("memory", func(ctx context.Context) error {
		m, e := mem.VirtualMemoryWithContext(ctx)
		if e == nil {
			v.MemoryTotalBytes = m.Total
			v.MemoryUsedBytes = m.Used
		}
		return e
	})
	run("disk", func(ctx context.Context) error {
		d, e := disk.UsageWithContext(ctx, b.DiskPath)
		if e == nil {
			v.DiskTotalBytes = d.Total
			v.DiskUsedBytes = d.Used
		}
		return e
	})
	run("uptime", func(ctx context.Context) error { var e error; up, e = host.UptimeWithContext(ctx); return e })
	return v, up, errors.Join(errs...)
}
func (b *Basic) CollectionDiagnostics() []diagnostics.Result { return b.Diagnostics }

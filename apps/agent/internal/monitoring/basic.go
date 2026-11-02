package monitoring

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"kiosentra/packages/contracts"
)

type Collector interface {
	Collect(context.Context) (contracts.Metrics, uint64, error)
}

// Basic is the Phase 2 telemetry source; process and inventory collectors follow in Phase 3.
type Basic struct{ DiskPath string }

func (b Basic) Collect(ctx context.Context) (contracts.Metrics, uint64, error) {
	v := contracts.Metrics{CollectedAt: time.Now().UTC(), Processes: []contracts.ProcessMetric{}}
	c, e := cpu.PercentWithContext(ctx, 250*time.Millisecond, false)
	if e != nil {
		return v, 0, e
	}
	if len(c) > 0 {
		v.CPUPercent = c[0]
	}
	m, e := mem.VirtualMemoryWithContext(ctx)
	if e != nil {
		return v, 0, e
	}
	v.MemoryTotalBytes = m.Total
	v.MemoryUsedBytes = m.Used
	d, e := disk.UsageWithContext(ctx, b.DiskPath)
	if e != nil {
		return v, 0, e
	}
	v.DiskTotalBytes = d.Total
	v.DiskUsedBytes = d.Used
	up, e := host.UptimeWithContext(ctx)
	return v, up, e
}

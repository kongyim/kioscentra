// Package inventory collects hardware and read-only Windows software inventory.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"kiosentra/apps/agent/internal/diagnostics"
	win "kiosentra/apps/agent/internal/platform/windows"
	"kiosentra/packages/contracts"
)

type Collector interface {
	Collect(context.Context, string) (contracts.Inventory, []diagnostics.Result, error)
}
type System struct{ DiskPath string }

func (s System) Collect(ctx context.Context, executable string) (contracts.Inventory, []diagnostics.Result, error) {
	v := contracts.Inventory{CollectedAt: time.Now().UTC(), Architecture: runtime.GOARCH, Software: []contracts.Software{}, Runtimes: []contracts.Software{}, Networks: []contracts.Network{}}
	results := []diagnostics.Result{}
	var errs []error
	run := func(name string, fn func(context.Context) error) {
		r := diagnostics.Run(ctx, 3*time.Second, name, fn)
		results = append(results, r)
		if r.Error != "" {
			errs = append(errs, fmt.Errorf("%s: %s", name, r.Error))
		}
	}
	run("host", func(ctx context.Context) error {
		h, e := host.InfoWithContext(ctx)
		if e == nil {
			v.Hostname = h.Hostname
			v.OS = h.OS + " " + h.Platform + " " + h.PlatformVersion + " (" + h.KernelVersion + ")"
		}
		return e
	})
	run("cpu", func(ctx context.Context) error {
		c, e := cpu.InfoWithContext(ctx)
		if e == nil && len(c) > 0 {
			v.CPU = c[0].ModelName
		}
		return e
	})
	run("memory", func(ctx context.Context) error {
		m, e := mem.VirtualMemoryWithContext(ctx)
		if e == nil {
			v.MemoryBytes = m.Total
		}
		return e
	})
	run("storage", func(ctx context.Context) error {
		d, e := disk.UsageWithContext(ctx, s.DiskPath)
		if e == nil {
			v.StorageBytes = d.Total
		}
		return e
	})
	run("network", func(ctx context.Context) error {
		ns, e := gnet.InterfacesWithContext(ctx)
		if e != nil {
			return e
		}
		sort.Slice(ns, func(i, j int) bool { return ns[i].Name < ns[j].Name })
		for _, n := range ns {
			if len(v.Networks) == 100 {
				return errors.New("network interface limit reached")
			}
			addresses := []string{}
			for _, a := range n.Addrs {
				addresses = append(addresses, a.Addr)
			}
			sort.Strings(addresses)
			v.Networks = append(v.Networks, contracts.Network{Name: n.Name, Addresses: addresses})
		}
		return nil
	})
	run("windows-software", func(ctx context.Context) error { var e error; v.Software, v.Runtimes, e = win.Software(ctx); return e })
	if executable != "" {
		run("custom-executable", func(ctx context.Context) error {
			version, e := win.FileVersion(ctx, executable)
			if e == nil {
				if len(v.Software) >= 2000 {
					return errors.New("software entry limit reached")
				}
				v.Software = append(v.Software, contracts.Software{Name: executable, Version: version})
			}
			return e
		})
	}
	return v, results, errors.Join(errs...)
}

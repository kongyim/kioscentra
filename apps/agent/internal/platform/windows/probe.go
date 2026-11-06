// Package windows implements read-only machine-scope Windows discovery.
package windows

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"kiosentra/packages/contracts"
)

var ErrUnsupported = errors.New("Windows discovery unsupported on this platform")

// Registry is deliberately read-only. Paths are relative to HKLM; view is 32 or 64.
type Registry interface {
	Subkeys(context.Context, string, int) ([]string, error)
	Values(context.Context, string, int) (map[string]string, error)
}
type Probe struct{ Registry Registry }

const uninstall = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
const runKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
const shellKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`

func (p Probe) Software(ctx context.Context) ([]contracts.Software, []contracts.Software, error) {
	software := []contracts.Software{}
	runtimes := []contracts.Software{}
	seen := map[string]bool{}
	runtimeSeen := map[string]bool{}
	var errs []error
	add := func(dst *[]contracts.Software, seen map[string]bool, name, version string, limit int) {
		if name == "" {
			return
		}
		key := name + "\x00" + version
		if seen[key] {
			return
		}
		seen[key] = true
		if len(*dst) >= limit {
			errs = append(errs, errors.New("inventory entry limit reached"))
			return
		}
		*dst = append(*dst, contracts.Software{Name: name, Version: version})
	}
	for _, view := range []int{64, 32} {
		keys, e := p.Registry.Subkeys(ctx, uninstall, view)
		if e != nil {
			errs = append(errs, fmt.Errorf("uninstall %d: %w", view, e))
			keys = nil
		}
		sort.Strings(keys)
		for _, k := range keys {
			if e = ctx.Err(); e != nil {
				return software, runtimes, e
			}
			v, e := p.Registry.Values(ctx, uninstall+`\`+k, view)
			if e != nil {
				errs = append(errs, fmt.Errorf("uninstall %d/%s: %w", view, k, e))
				continue
			}
			add(&software, seen, v["DisplayName"], v["DisplayVersion"], 2000)
			name := strings.ToLower(v["DisplayName"])
			if strings.Contains(name, "java") || strings.Contains(name, "jdk") || strings.Contains(name, ".net") {
				add(&runtimes, runtimeSeen, v["DisplayName"], v["DisplayVersion"], 100)
			}
		}
		// Java registry registrations cover runtimes without uninstall entries.
		for _, root := range []string{`SOFTWARE\JavaSoft\Java Runtime Environment`, `SOFTWARE\JavaSoft\Java Development Kit`, `SOFTWARE\JavaSoft\JRE`, `SOFTWARE\JavaSoft\JDK`} {
			keys, e := p.Registry.Subkeys(ctx, root, view)
			if e != nil {
				errs = append(errs, e)
				continue
			}
			for _, version := range keys {
				add(&runtimes, runtimeSeen, "Java "+root[strings.LastIndex(root, `\`)+1:], version, 100)
			}
		}
		for _, flavor := range []string{"Full", "Client"} {
			v, e := p.Registry.Values(ctx, `SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\`+flavor, view)
			if e != nil {
				errs = append(errs, e)
				continue
			}
			if v["Version"] != "" {
				add(&runtimes, runtimeSeen, ".NET Framework v4 "+flavor, v["Version"], 100)
			}
		}
		const dotnet = `SOFTWARE\dotnet\Setup\InstalledVersions`
		arches, e := p.Registry.Subkeys(ctx, dotnet, view)
		if e != nil {
			errs = append(errs, e)
			continue
		}
		for _, arch := range arches {
			root := dotnet + `\` + arch + `\sharedfx`
			frameworks, e := p.Registry.Subkeys(ctx, root, view)
			if e != nil {
				errs = append(errs, e)
				continue
			}
			for _, framework := range frameworks {
				values, e := p.Registry.Values(ctx, root+`\`+framework, view)
				if e != nil {
					errs = append(errs, e)
					continue
				}
				versions := make([]string, 0, len(values))
				for version := range values {
					versions = append(versions, version)
				}
				sort.Strings(versions)
				for _, version := range versions {
					add(&runtimes, runtimeSeen, framework+" ("+arch+")", version, 100)
				}
			}
		}
	}
	sort.Slice(software, func(i, j int) bool {
		if software[i].Name == software[j].Name {
			return software[i].Version < software[j].Version
		}
		return software[i].Name < software[j].Name
	})
	sort.Slice(runtimes, func(i, j int) bool {
		if runtimes[i].Name == runtimes[j].Name {
			return runtimes[i].Version < runtimes[j].Version
		}
		return runtimes[i].Name < runtimes[j].Name
	})
	return software, runtimes, errors.Join(errs...)
}

func (p Probe) Policy(ctx context.Context, want contracts.Policy) contracts.PolicyReport {
	r := contracts.PolicyReport{Revision: want.Revision, ObservedAt: time.Now().UTC(), Compliance: "compliant", Drift: []string{}, Errors: []string{}}
	fail := func(e error) {
		if e != nil {
			r.Errors = append(r.Errors, e.Error())
		}
	}
	if want.ExpectedShell != "" {
		values, e := p.Registry.Values(ctx, shellKey, 64)
		fail(e)
		if e == nil && !strings.EqualFold(strings.TrimSpace(values["Shell"]), strings.TrimSpace(want.ExpectedShell)) {
			r.Drift = append(r.Drift, "machine Winlogon shell differs")
		}
	}
	// StartupEntries are exact machine Run value DATA, not value names or parsed commands.
	if len(want.StartupEntries) > 0 {
		entries := map[string]bool{}
		complete := true
		for _, view := range []int{64, 32} {
			values, e := p.Registry.Values(ctx, runKey, view)
			if e != nil {
				complete = false
				fail(e)
			}
			for _, value := range values {
				entries[value] = true
			}
		}
		if complete {
			for _, entry := range want.StartupEntries {
				if !entries[entry] {
					r.Drift = append(r.Drift, "missing machine Run entry: "+entry)
				}
			}
		}
	}
	// These settings require execution/session ownership which is outside read-only discovery.
	if want.AutoLaunch {
		r.Errors = append(r.Errors, "autoLaunch effective behavior cannot be established by registry observation")
	}
	if len(want.Arguments) > 0 {
		r.Errors = append(r.Errors, "kiosk arguments are not observed")
	}
	if want.RestartPolicy != "never" {
		r.Errors = append(r.Errors, "restart policy is not enforced in read-only mode")
	}
	if e := ctx.Err(); e != nil {
		fail(e)
	}
	if len(r.Drift) > 0 {
		r.Compliance = "drifted"
	} else if len(r.Errors) > 0 {
		r.Compliance = "unknown"
	}
	return r
}

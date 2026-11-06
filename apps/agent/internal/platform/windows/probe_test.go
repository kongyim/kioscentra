package windows

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"kiosentra/packages/contracts"
)

type fakeRegistry struct {
	sub    map[string][]string
	values map[string]map[string]string
	denied string
	views  map[int]bool
}

func key(path string, view int) string { return fmt.Sprintf("%d:%s", view, path) }
func (f fakeRegistry) Subkeys(ctx context.Context, p string, v int) ([]string, error) {
	f.views[v] = true
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if p == f.denied {
		return nil, errors.New("access denied")
	}
	return f.sub[key(p, v)], nil
}
func (f fakeRegistry) Values(ctx context.Context, p string, v int) (map[string]string, error) {
	f.views[v] = true
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if p == f.denied {
		return nil, errors.New("access denied")
	}
	return f.values[key(p, v)], nil
}
func fixture() fakeRegistry {
	return fakeRegistry{sub: map[string][]string{}, values: map[string]map[string]string{}, views: map[int]bool{}}
}
func TestSoftwareBothViewsDeduplicatedAndRuntimes(t *testing.T) {
	f := fixture()
	for _, view := range []int{32, 64} {
		f.sub[key(uninstall, view)] = []string{"java", "app", "empty"}
		f.values[key(uninstall+`\java`, view)] = map[string]string{"DisplayName": "Java Runtime", "DisplayVersion": "21"}
		f.values[key(uninstall+`\app`, view)] = map[string]string{"DisplayName": "Kiosk", "DisplayVersion": fmt.Sprint(view)}
	}
	f.values[key(`SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\Full`, 64)] = map[string]string{"Version": "4.8"}
	f.sub[key(`SOFTWARE\dotnet\Setup\InstalledVersions`, 64)] = []string{"x64"}
	f.sub[key(`SOFTWARE\dotnet\Setup\InstalledVersions\x64\sharedfx`, 64)] = []string{"Microsoft.NETCore.App"}
	f.values[key(`SOFTWARE\dotnet\Setup\InstalledVersions\x64\sharedfx\Microsoft.NETCore.App`, 64)] = map[string]string{"8.0.1": "1"}
	sw, rt, e := (Probe{f}).Software(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(sw) != 3 || len(rt) != 3 || !f.views[32] || !f.views[64] {
		t.Fatalf("software=%v runtimes=%v views=%v", sw, rt, f.views)
	}
}
func TestPolicyReadFailureNeverCompliant(t *testing.T) {
	f := fixture()
	f.denied = shellKey
	p := contracts.Policy{Revision: "r1", ExpectedShell: "explorer.exe", RestartPolicy: "never"}
	r := (Probe{f}).Policy(context.Background(), p)
	if r.Compliance != "unknown" || len(r.Errors) == 0 || len(r.Drift) != 0 {
		t.Fatal(r)
	}
	f.denied = ""
	f.values[key(shellKey, 64)] = map[string]string{"Shell": "EXPLORER.EXE"}
	p.StartupEntries = []string{`"C:\Kiosk\app.exe" --start`}
	f.values[key(runKey, 32)] = map[string]string{"Kiosk": p.StartupEntries[0]}
	r = (Probe{f}).Policy(context.Background(), p)
	if r.Compliance != "compliant" {
		t.Fatal(r)
	}
	f.values[key(runKey, 32)] = nil
	r = (Probe{f}).Policy(context.Background(), p)
	if r.Compliance != "drifted" {
		t.Fatal(r)
	}
	f.denied = runKey
	r = (Probe{f}).Policy(context.Background(), p)
	if r.Compliance != "unknown" || len(r.Drift) != 0 {
		t.Fatal(r)
	}
}
func TestCancelledProbeAndPartialSoftware(t *testing.T) {
	f := fixture()
	f.denied = uninstall
	_, _, e := (Probe{f}).Software(context.Background())
	if e == nil {
		t.Fatal("missing access failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := (Probe{fixture()}).Policy(ctx, contracts.Policy{Revision: "r", RestartPolicy: "never"})
	if r.Compliance != "unknown" {
		t.Fatal(r)
	}
}

func TestUninstallFailureDoesNotSuppressRuntimeDiscovery(t *testing.T) {
	f := fixture()
	f.denied = uninstall
	f.values[key(`SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\Full`, 64)] = map[string]string{"Version": "4.8"}
	_, runtimes, e := (Probe{f}).Software(context.Background())
	if e == nil || len(runtimes) != 1 {
		t.Fatal(runtimes, e)
	}
}

func TestSoftwareBoundAndDiagnostics(t *testing.T) {
	f := fixture()
	for i := 0; i < 2001; i++ {
		name := fmt.Sprintf("app%04d", i)
		f.sub[key(uninstall, 64)] = append(f.sub[key(uninstall, 64)], name)
		f.values[key(uninstall+`\`+name, 64)] = map[string]string{"DisplayName": name, "DisplayVersion": "1"}
	}
	software, _, e := (Probe{f}).Software(context.Background())
	if len(software) != 2000 || e == nil {
		t.Fatal(len(software), e)
	}
}

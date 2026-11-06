//go:build windows

package windows

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"kiosentra/packages/contracts"
)

func TestNativeRegistryReadsSmallKeysAndMissingKeys(t *testing.T) {
	path := `Software\Kiosentra-Test-` + contracts.ID()
	k, _, e := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if e != nil {
		t.Fatal(e)
	}
	defer registry.DeleteKey(registry.CURRENT_USER, path)
	if e = k.SetStringValue("DisplayName", "Kiosk"); e != nil {
		t.Fatal(e)
	}
	if e = k.SetDWordValue("Release", 123); e != nil {
		t.Fatal(e)
	}
	k.Close()
	probe := nativeRegistry{root: registry.CURRENT_USER}
	for _, view := range []int{32, 64} {
		values, e := probe.Values(context.Background(), path, view)
		if e != nil {
			t.Fatal(e)
		}
		if values["DisplayName"] != "Kiosk" || values["Release"] != "123" {
			t.Fatal(values)
		}
		names, e := probe.Subkeys(context.Background(), path, view)
		if e != nil || len(names) != 0 {
			t.Fatal(names, e)
		}
		values, e = probe.Values(context.Background(), path+`\Missing`, view)
		if e != nil || len(values) != 0 {
			t.Fatal(values, e)
		}
	}
}
func TestNativeFileVersionAndCancellation(t *testing.T) {
	dir, e := windows.GetSystemDirectory()
	if e != nil {
		t.Fatal(e)
	}
	version, e := FileVersion(context.Background(), filepath.Join(dir, "kernel32.dll"))
	if e != nil || version == "" {
		t.Fatal(version, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = FileVersion(ctx, filepath.Join(dir, "kernel32.dll")); e == nil {
		t.Fatal("cancellation ignored")
	}
	if _, e = FileVersion(context.Background(), "kiosk.exe"); e == nil {
		t.Fatal("relative path accepted")
	}
}

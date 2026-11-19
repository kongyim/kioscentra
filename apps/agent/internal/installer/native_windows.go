//go:build windows

package installer

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"kiosentra/packages/contracts"
)

var msiDLL = windows.NewLazySystemDLL("msi.dll")
var openDatabase = msiDLL.NewProc("MsiOpenDatabaseW")
var openView = msiDLL.NewProc("MsiDatabaseOpenViewW")
var executeView = msiDLL.NewProc("MsiViewExecute")
var fetchView = msiDLL.NewProc("MsiViewFetch")
var recordString = msiDLL.NewProc("MsiRecordGetStringW")
var closeMSI = msiDLL.NewProc("MsiCloseHandle")
var productInfo = msiDLL.NewProc("MsiGetProductInfoExW")

type nativeAdapter struct{ executable string }

func NativeAdapter() (Adapter, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, errors.New("MSI executor requires an elevated operator process")
	}
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	return nativeAdapter{executable: filepath.Join(dir, "msiexec.exe")}, nil
}

func (n nativeAdapter) Inspect(ctx context.Context, path string) (contracts.MSIIdentity, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var identity contracts.MSIIdentity
	if err := ctx.Err(); err != nil {
		return identity, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return identity, err
	}
	var database uint32
	code, _, _ := openDatabase.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&database))) // MSIDBOPEN_READONLY
	if code != 0 {
		return identity, fmt.Errorf("open MSI database: %d", code)
	}
	defer closeMSI.Call(uintptr(database))
	if err = embeddedMedia(database); err != nil {
		return identity, err
	}
	identity.ProductCode, err = property(database, "SELECT `Value` FROM `Property` WHERE `Property` = 'ProductCode'")
	if err != nil {
		return identity, err
	}
	identity.ProductVersion, err = property(database, "SELECT `Value` FROM `Property` WHERE `Property` = 'ProductVersion'")
	if err != nil {
		return identity, err
	}
	if err = ctx.Err(); err != nil {
		return identity, err
	}
	return identity, identity.Validate()
}

// The first adapter supports self-contained cabinets only. Loose/external media
// is outside the signed artifact's byte boundary and requires a later format.
func embeddedMedia(database uint32) error {
	query, _ := windows.UTF16PtrFromString("SELECT `Cabinet` FROM `Media`")
	var view uint32
	code, _, _ := openView.Call(uintptr(database), uintptr(unsafe.Pointer(query)), uintptr(unsafe.Pointer(&view)))
	if code != 0 {
		return fmt.Errorf("MSI media table unavailable: %d", code)
	}
	defer closeMSI.Call(uintptr(view))
	code, _, _ = executeView.Call(uintptr(view), 0)
	if code != 0 {
		return fmt.Errorf("MSI media query failed: %d", code)
	}
	for count := 0; count < 128; count++ {
		var record uint32
		code, _, _ = fetchView.Call(uintptr(view), uintptr(unsafe.Pointer(&record)))
		if code == 259 {
			if count == 0 {
				return errors.New("MSI requires an embedded cabinet")
			}
			return nil
		}
		if code != 0 {
			return fmt.Errorf("MSI media read failed: %d", code)
		}
		var buffer [1024]uint16
		size := uint32(len(buffer))
		code, _, _ = recordString.Call(uintptr(record), 1, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
		closeMSI.Call(uintptr(record))
		if code != 0 || !strings.HasPrefix(windows.UTF16ToString(buffer[:]), "#") || size < 2 {
			return errors.New("external or missing MSI cabinet is unsupported")
		}
	}
	return errors.New("MSI media table exceeds limit")
}

func property(database uint32, query string) (string, error) {
	p, err := windows.UTF16PtrFromString(query)
	if err != nil {
		return "", err
	}
	var view uint32
	code, _, _ := openView.Call(uintptr(database), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&view)))
	if code != 0 {
		return "", fmt.Errorf("open MSI property view: %d", code)
	}
	defer closeMSI.Call(uintptr(view))
	code, _, _ = executeView.Call(uintptr(view), 0)
	if code != 0 {
		return "", fmt.Errorf("execute MSI property view: %d", code)
	}
	var record uint32
	code, _, _ = fetchView.Call(uintptr(view), uintptr(unsafe.Pointer(&record)))
	if code != 0 {
		return "", fmt.Errorf("read MSI property: %d", code)
	}
	defer closeMSI.Call(uintptr(record))
	var buffer [1024]uint16
	size := uint32(len(buffer))
	code, _, _ = recordString.Call(uintptr(record), 1, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if code != 0 {
		return "", fmt.Errorf("MSI property unavailable or oversized: %d", code)
	}
	return windows.UTF16ToString(buffer[:]), nil
}

func (n nativeAdapter) InstalledVersion(ctx context.Context, product string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p, err := windows.UTF16PtrFromString(product)
	if err != nil {
		return "", err
	}
	property, _ := windows.UTF16PtrFromString("VersionString")
	var buffer [1024]uint16
	size := uint32(len(buffer))
	code, _, _ := productInfo.Call(uintptr(unsafe.Pointer(p)), 0, 4, uintptr(unsafe.Pointer(property)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size))) // machine context, null SID
	if code == 1605 {
		return "", nil
	} // ERROR_UNKNOWN_PRODUCT
	if code != 0 {
		return "", fmt.Errorf("query installed MSI version: %d", code)
	}
	return windows.UTF16ToString(buffer[:]), nil
}

func (n nativeAdapter) Install(ctx context.Context, path string) (int, error) {
	if !filepath.IsAbs(path) {
		return -1, errors.New("MSI path must be absolute")
	}
	// Fixed arguments only; no shell, transforms, arbitrary properties or reboot.
	cmd := exec.CommandContext(ctx, n.executable, "/i", path, "/qn", "/norestart", "REBOOT=ReallySuppress", "ALLUSERS=1")
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	// Killing the client does not establish cancellation of Windows Installer's
	// service transaction. The coordinator fences all subsequent work on this error.
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return -1, err
}

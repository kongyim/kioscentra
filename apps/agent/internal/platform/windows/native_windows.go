//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"kiosentra/packages/contracts"
)

type nativeRegistry struct{ root registry.Key }

func (n nativeRegistry) open(ctx context.Context, path string, view int) (registry.Key, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	access := uint32(registry.READ | registry.WOW64_64KEY)
	if view == 32 {
		access = registry.READ | registry.WOW64_32KEY
	}
	root := n.root
	if root == 0 {
		root = registry.LOCAL_MACHINE
	}
	return registry.OpenKey(root, path, access)
}
func (n nativeRegistry) Subkeys(ctx context.Context, path string, view int) ([]string, error) {
	k, e := n.open(ctx, path, view)
	if errors.Is(e, registry.ErrNotExist) {
		return []string{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer k.Close()
	names, e := k.ReadSubKeyNames(4097)
	if errors.Is(e, io.EOF) {
		e = nil
	}
	if len(names) > 4096 {
		return nil, errors.New("registry subkey limit exceeded")
	}
	return names, e
}
func (n nativeRegistry) Values(ctx context.Context, path string, view int) (map[string]string, error) {
	out := map[string]string{}
	k, e := n.open(ctx, path, view)
	if errors.Is(e, registry.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	defer k.Close()
	names, e := k.ReadValueNames(4097)
	if errors.Is(e, io.EOF) {
		e = nil
	}
	if e != nil {
		return out, e
	}
	if len(names) > 4096 {
		return out, errors.New("registry value limit exceeded")
	}
	for _, name := range names {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		v, typ, e := k.GetStringValue(name)
		if errors.Is(e, registry.ErrUnexpectedType) {
			if typ == registry.DWORD || typ == registry.QWORD {
				n, _, err := k.GetIntegerValue(name)
				if err != nil {
					return out, err
				}
				out[name] = fmt.Sprint(n)
			}
			continue
		}
		if e != nil {
			return out, e
		}
		if len(v) > 8192 {
			return out, errors.New("registry string limit exceeded")
		}
		out[name] = v
	}
	return out, nil
}
func Software(ctx context.Context) ([]contracts.Software, []contracts.Software, error) {
	return (Probe{Registry: nativeRegistry{}}).Software(ctx)
}
func Policy(ctx context.Context, p contracts.Policy) contracts.PolicyReport {
	return (Probe{Registry: nativeRegistry{}}).Policy(ctx, p)
}
func FileVersion(ctx context.Context, path string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) {
		return "", errors.New("version discovery requires a local absolute executable path")
	}
	var handle windows.Handle
	size, e := windows.GetFileVersionInfoSize(path, &handle)
	if e != nil {
		return "", e
	}
	if size == 0 || size > 1<<20 {
		return "", errors.New("invalid version resource size")
	}
	buf := make([]byte, size)
	if e = windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])); e != nil {
		return "", e
	}
	var info *windows.VS_FIXEDFILEINFO
	var length uint32
	if e = windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\`, unsafe.Pointer(&info), &length); e != nil {
		return "", e
	}
	if info == nil || length < uint32(unsafe.Sizeof(*info)) || info.Signature != 0xfeef04bd {
		return "", errors.New("invalid file version resource")
	}
	return fmt.Sprintf("%d.%d.%d.%d", info.FileVersionMS>>16, info.FileVersionMS&0xffff, info.FileVersionLS>>16, info.FileVersionLS&0xffff), ctx.Err()
}

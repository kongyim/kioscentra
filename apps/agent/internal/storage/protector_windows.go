//go:build windows

package storage

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type dpapi struct{}

func PlatformProtector(_ bool, _ string) (Protector, error) { return dpapi{}, nil }
func blob(b []byte) windows.DataBlob {
	v := windows.DataBlob{Size: uint32(len(b))}
	if len(b) > 0 {
		v.Data = &b[0]
	}
	return v
}
func (p dpapi) Seal(b []byte) ([]byte, error) {
	in := blob(b)
	var out windows.DataBlob
	if e := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); e != nil {
		return nil, e
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
func (p dpapi) Open(b []byte) ([]byte, error) {
	in := blob(b)
	var out windows.DataBlob
	if e := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); e != nil {
		return nil, e
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}

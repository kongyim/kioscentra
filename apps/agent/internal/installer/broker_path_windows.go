//go:build windows

package installer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Fail closed on mutable configuration, state or ancestor replacement. Agent/UI
// accounts are not trusted writers, even though they can connect to the pipe.
func checkBrokerPath(path string) error {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) {
		return errors.New("broker paths must be absolute local paths")
	}
	trusted := map[string]bool{"S-1-5-18": true, "S-1-5-32-544": true}
	if sid, _, _, err := windows.LookupSID("", `NT SERVICE\TrustedInstaller`); err == nil {
		trusted[sid.String()] = true
	}
	first := true
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("broker path contains a link or special file")
		}
		p, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attrs, err := windows.GetFileAttributes(p)
		if err != nil {
			return err
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("broker paths cannot contain reparse points")
		}
		sd, err := windows.GetNamedSecurityInfo(current, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		owner, _, err := sd.Owner()
		if err != nil || owner == nil || !trusted[owner.String()] {
			return errors.New("broker path owner must be SYSTEM, Administrators or TrustedInstaller")
		}
		acl, _, err := sd.DACL()
		if err != nil || acl == nil {
			return errors.New("broker path requires a non-null DACL")
		}
		// Ancestors may allow creation of unrelated files, but never replacement,
		// deletion of this subtree, or ACL/owner changes by an untrusted principal.
		mask := uint32(0x10000000 | 0x40000000 | 0x00010000 | 0x00040000 | 0x00080000 | 0x40)
		if first {
			mask |= 0x2 | 0x4 | 0x10 | 0x100
		}
		for i := uint32(0); i < uint32(acl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err = windows.GetAce(acl, i, &ace); err != nil {
				return err
			}
			if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
				continue
			}
			if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
				return errors.New("unsupported broker path ACL entry")
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if uint32(ace.Mask)&mask != 0 && !trusted[sid.String()] {
				return errors.New("broker path is writable or replaceable by an untrusted principal")
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		first = false
	}
	return nil
}

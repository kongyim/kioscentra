//go:build windows

package configuration

import "os"

// Windows ACLs are provisioned and verified by tools/windows; Unix mode bits do not describe them.
func checkPrivateMode(os.FileInfo) error { return nil }

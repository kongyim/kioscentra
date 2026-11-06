//go:build !windows

package configuration

import (
	"errors"
	"os"
)

func checkPrivateMode(info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("configuration must be readable only by its owner (chmod 600)")
	}
	return nil
}

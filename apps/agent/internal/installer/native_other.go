//go:build !windows

package installer

import "errors"

func NativeAdapter() (Adapter, error) {
	return nil, errors.New("real MSI execution requires Windows; use fixture adapters in host tests")
}

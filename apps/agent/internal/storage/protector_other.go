//go:build !windows

package storage

import "errors"

func PlatformProtector(development bool, key string) (Protector, error) {
	if !development {
		return nil, errors.New("production credential storage unsupported on this OS; macOS agent is not implemented")
	}
	return DevelopmentProtector(key)
}

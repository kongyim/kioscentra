//go:build !windows

package installer

import "errors"

func checkBrokerPath(string) error { return errors.New("privileged broker service requires Windows") }

//go:build !windows

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
)

func address(o Options) string { return filepath.Join(o.StateDir, "console.sock") }
func Listen(o Options) (net.Listener, error) {
	if !o.Development {
		return nil, errors.New("non-Windows IPC is development-only")
	}
	// The service holds the state-directory lock before entering this function.
	if st, e := os.Lstat(address(o)); e == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("IPC path is not a socket")
		}
		if e = os.Remove(address(o)); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	l, e := net.Listen("unix", address(o))
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(address(o), 0600); e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}
func dial(ctx context.Context, o Options) (net.Conn, error) {
	if !o.Development {
		return nil, errors.New("non-Windows IPC is development-only")
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", address(o))
}

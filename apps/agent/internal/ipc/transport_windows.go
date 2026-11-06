//go:build windows

package ipc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Generic write includes FILE_CREATE_PIPE_INSTANCE. Viewers must not receive it.
const clientAccess = (windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE) &^ windows.FILE_APPEND_DATA

func address(o Options) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(o.StateDir))))
	return fmt.Sprintf(`\\.\pipe\Kiosentra-%x`, sum[:12])
}
func Listen(o Options) (net.Listener, error) {
	acl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
	owner, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	acl += "(A;;GA;;;" + owner.User.Sid.String() + ")"
	if o.ReaderSID != "" {
		sid, e := windows.StringToSid(o.ReaderSID)
		if e != nil {
			return nil, e
		}
		acl += fmt.Sprintf("(A;;0x%x;;;%s)", clientAccess, sid.String())
	}
	return winio.ListenPipe(address(o), &winio.PipeConfig{SecurityDescriptor: acl, InputBufferSize: 16384, OutputBufferSize: 65536})
}
func dial(ctx context.Context, o Options) (net.Conn, error) {
	return winio.DialPipeAccess(ctx, address(o), clientAccess)
}

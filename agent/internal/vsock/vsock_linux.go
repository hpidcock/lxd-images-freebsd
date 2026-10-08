//go:build linux

package vsock

import (
	"context"
	"net"

	"github.com/mdlayher/vsock"
)

// Listen listens on the given port on any context ID.
func Listen(port uint32) (net.Listener, error) {
	return vsock.ListenContextID(CIDAny, port, nil)
}

// DialContext connects to the given context ID and port.
func DialContext(ctx context.Context, cid uint32, port uint32) (net.Conn, error) {
	return vsock.Dial(cid, port, nil)
}

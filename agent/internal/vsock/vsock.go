// Package vsock provides AF_VSOCK listeners and dialers for the lxd-agent.
//
// On FreeBSD the sockets are implemented directly on top of the out-of-tree
// vsock kernel module (see kmod/ in this repository); on Linux the
// github.com/mdlayher/vsock package is used so that the agent can still be
// built and tested on a Linux development host.
package vsock

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/shared"
)

const (
	// CIDAny is the wildcard context ID (VMADDR_CID_ANY).
	CIDAny uint32 = 0xffffffff

	// CIDHost is the context ID of the host (VMADDR_CID_HOST).
	CIDHost uint32 = 2
)

// Addr is a vsock address.
type Addr struct {
	CID  uint32
	Port uint32
}

// Network returns the network name.
func (a *Addr) Network() string {
	return "vsock"
}

// String renders the address as "vm(<cid>):<port>".
func (a *Addr) String() string {
	return "vm(" + strconv.FormatUint(uint64(a.CID), 10) + "):" + strconv.FormatUint(uint64(a.Port), 10)
}

// HTTPClient provides an HTTP client for talking to LXD over vsock.
// It mirrors github.com/canonical/lxd/lxd/vsock.HTTPClient: the dialer returns
// the raw vsock connection and http.Transport performs the TLS handshake.
func HTTPClient(cid uint32, port int, tlsClientCert string, tlsClientKey string, tlsServerCert string) (*http.Client, error) {
	tlsConfig, err := shared.GetTLSConfigMem(tlsClientCert, tlsClientKey, "", tlsServerCert, false)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
			// Setup a VM socket dialer.
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var conn net.Conn
				var err error

				// Retry for up to 1s at 100ms interval to handle various failures.
				for range 10 {
					conn, err = DialContext(ctx, cid, uint32(port))
					if err == nil {
						break
					}

					// Handle some fatal errors.
					msg := err.Error()
					if strings.Contains(msg, "connection timed out") {
						// Retry once.
						conn, err = DialContext(ctx, cid, uint32(port))
						break
					} else if strings.Contains(msg, "connection refused") {
						break
					}

					// Retry the rest.
					time.Sleep(100 * time.Millisecond)
				}

				if err != nil {
					return nil, err
				}

				return conn, nil
			},
			DisableKeepAlives:     true,
			ExpectContinueTimeout: time.Second * 30,
			ResponseHeaderTimeout: time.Second * 3600,
			TLSHandshakeTimeout:   time.Second * 5,
		},
	}

	// Setup redirect policy.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers.
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}

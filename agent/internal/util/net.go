package util

import (
	"crypto/tls"
	"crypto/x509"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// ServerTLSConfig returns a new server-side tls.Config generated from the give certificate info.
func ServerTLSConfig(cert *shared.CertInfo) *tls.Config {
	config := shared.InitTLSConfig()
	config.ClientAuth = tls.RequestClientCert
	config.Certificates = []tls.Certificate{cert.KeyPair()}
	config.NextProtos = []string{"h2"} // Required by gRPC

	if cert.CA() != nil {
		pool := x509.NewCertPool()
		pool.AddCert(cert.CA())
		config.RootCAs = pool
		config.ClientCAs = pool

		logger.Info("LXD is in CA mode, only CA-signed client certificates will be allowed")
	}

	return config
}

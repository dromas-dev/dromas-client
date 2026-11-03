package client

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// ReadCAFile reads the CAs of [Options.RootCAs] from a file of PEM certificates,
// as a CA bundle holds them: text between the blocks is skipped, and a
// certificate need not be a CA's, so a server's own self-signed certificate is
// one. It refuses a file others can write or another user owns, as a config file
// is ([CheckConfigFile]), since a certificate put there would be trusted; a PEM
// block that is not one certificate; and a file with none.
func ReadCAFile(path string) (*x509.CertPool, error) {
	if err := CheckConfigFile(path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the CA file its owner named
	if err != nil {
		return nil, err
	}
	pool, n := x509.NewCertPool(), 0
	for block, rest := pem.Decode(raw); block != nil; block, rest = pem.Decode(rest) {
		n++
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fmt.Errorf("%s: PEM block %d is of type %.40q: a CA file holds certificates alone", path, n, block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: PEM block %d is not a certificate: %w", path, n, err)
		}
		pool.AddCert(cert)
	}
	if n == 0 {
		return nil, fmt.Errorf("%s holds no PEM certificate", path)
	}
	return pool, nil
}

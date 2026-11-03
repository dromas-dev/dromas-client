package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// otherCA is a self-signed CA certificate that signed nothing a test server
// presents.
func otherCA(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "another CA"},
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func poolOf(certs ...*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, c := range certs {
		pool.AddCert(c)
	}
	return pool
}

// withRoots is what a new process has of a mailbox on srv whose default client
// trusts roots alone.
func withRoots(t *testing.T, dir string, srv *httptest.Server, roots *x509.CertPool) *Mailbox {
	t.Helper()
	b, err := newMailbox(dir, Options{Base: srv.URL, RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.http.CloseIdleConnections)
	return b
}

// refusedTLS is whether a call from b to its server failed on the server's
// certificate.
func refusedTLS(t *testing.T, b *Mailbox) bool {
	t.Helper()
	resp, err := b.http.Get(b.base)
	if err == nil {
		_ = resp.Body.Close()
		return false
	}
	var cert *tls.CertificateVerificationError
	if !errors.As(err, &cert) {
		t.Fatalf("a call failed, not on the certificate: %v", err)
	}
	return true
}

// RootCAs are the only CAs the default client trusts: the server's own CA is
// reached, and a server whose CA the set lacks is refused, whatever the system
// trusts.
func TestRootCAsAreTheOnlyCAsTheDefaultClientTrusts(t *testing.T) {
	t.Parallel()
	srv := ticketServer(t, 0)
	if refusedTLS(t, withRoots(t, "", srv, poolOf(srv.Certificate()))) {
		t.Fatal("a server whose CA is in RootCAs was refused")
	}
	if !refusedTLS(t, withRoots(t, "", srv, poolOf(otherCA(t)))) {
		t.Fatal("a server whose CA is not in RootCAs was called")
	}
}

// A ticket kept while one set of CAs was trusted resumes nothing under a set that
// lacks the server's CA: crypto/tls holds a resumed session's chain to the
// RootCAs of the connection that resumes it. The second process, under the same
// CAs, resumes, so the ticket was one to resume.
func TestATicketResumesNothingUnderCAsThatLackItsServers(t *testing.T) {
	t.Parallel()
	srv := ticketServer(t, 0)
	dir := ticketDir(t)
	if connects(t, withRoots(t, dir, srv, poolOf(srv.Certificate()))) {
		t.Fatal("the first connection a mailbox ever made resumed")
	}
	if !connects(t, withRoots(t, dir, srv, poolOf(srv.Certificate()))) {
		t.Fatal("a new process under the same CAs made a full handshake")
	}
	if !refusedTLS(t, withRoots(t, dir, srv, poolOf(otherCA(t)))) {
		t.Fatal("a ticket resumed a session under CAs that lack the server's")
	}
}

// RootCAs set the default client's CAs, so beside a client of the caller's own
// they are refused, before anything is done.
func TestRootCAsBesideAnHTTPClientAreRefused(t *testing.T) {
	t.Parallel()
	if _, err := newMailbox("", Options{RootCAs: x509.NewCertPool(), HTTPClient: &http.Client{}}); err == nil {
		t.Fatal("RootCAs beside an HTTPClient were taken")
	}
}

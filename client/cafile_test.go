package client_test

import (
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
)

// A CA file is the certificates its PEM blocks hold, with the text between them
// skipped, as a CA bundle writes them; a block that is not one certificate, a
// file with none, and a file others can write are refused.
func TestACAFileIsItsCertificates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(nil)
	srv.Close()
	cert := srv.Certificate()
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	write := func(t *testing.T, body string, mode os.FileMode) string {
		t.Helper()
		file := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, mode); err != nil {
			t.Fatal(err)
		}
		return file
	}

	pool, err := client.ReadCAFile(write(t, "# a bundle's comment\n"+certPEM+"\nmore text\n"+certPEM, 0o644))
	if err != nil {
		t.Fatal(err)
	}
	want := x509.NewCertPool()
	want.AddCert(cert)
	if !pool.Equal(want) {
		t.Error("the pool is not the file's certificate")
	}

	for name, c := range map[string]struct{ body, words string }{
		"no certificate":     {"just text\n", "holds no PEM certificate"},
		"an empty file":      {"", "holds no PEM certificate"},
		"a key":              {certPEM + string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}})), `PEM block 2 is of type "PRIVATE KEY"`},
		"a block that fails": {string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not DER")})), "PEM block 1 is not a certificate"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := client.ReadCAFile(write(t, c.body, 0o644)); err == nil || !strings.Contains(err.Error(), c.words) {
				t.Errorf("%v, want %q", err, c.words)
			}
		})
	}
	if runtime.GOOS != "windows" { // modes are Unix's; Windows' access lists are CheckConfigFile's own tests
		if _, err := client.ReadCAFile(write(t, certPEM, 0o666)); err == nil || !strings.Contains(err.Error(), "writable by others") {
			t.Errorf("a CA file others can write: %v", err)
		}
	}
}

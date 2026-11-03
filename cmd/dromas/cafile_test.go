package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// writeCA writes cert as a PEM file, its owner's alone, at path.
func writeCA(t *testing.T, path string, cert *x509.Certificate) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// someCert is a self-signed CA certificate that signed nothing the fake server
// presents. Every httptest server presents one certificate, so another test
// server's would not do.
func someCert(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "another CA"}, NotBefore: time.Unix(0, 0), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
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

// ca-file comes from the flag, then $DROMAS_CA_FILE, then the config file, where a
// relative path is under the file's directory, and dromas config says which.
func TestTheCAFileComesFromTheFirstPlaceThatSaysIt(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	cert := someCert(t)
	file := writeConfig(t, "base-dir = "+tomlString(filepath.Join(root, "b"))+"\nmail-dir = "+tomlString(filepath.Join(root, "m"))+"\nca-file = \"ca.pem\"\n")
	inFile := writeCA(t, filepath.Join(filepath.Dir(file), "ca.pem"), cert)
	t.Setenv("DROMAS_CONFIG", file)
	if v, _ := configLine(t); v["ca-file"] != (setting{inFile, "file"}) {
		t.Errorf("from the file: %+v, want %s", v["ca-file"], inFile)
	}
	inEnv := writeCA(t, filepath.Join(root, "env.pem"), cert)
	t.Setenv("DROMAS_CA_FILE", inEnv)
	if v, _ := configLine(t); v["ca-file"] != (setting{inEnv, "environment"}) {
		t.Errorf("from the environment: %+v", v["ca-file"])
	}
	inFlag := writeCA(t, filepath.Join(root, "flag.pem"), cert)
	if v, _ := configLine(t, "--ca-file", inFlag); v["ca-file"] != (setting{inFlag, "flag"}) {
		t.Errorf("from the flag: %+v", v["ca-file"])
	}
	t.Setenv("DROMAS_CA_FILE", "")
	if err := os.WriteFile(file, []byte("base-dir = "+tomlString(filepath.Join(root, "b"))+"\nmail-dir = "+tomlString(filepath.Join(root, "m"))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, _ := configLine(t); v["ca-file"] != (setting{}) {
		t.Errorf("with none named: %+v, want no setting", v["ca-file"])
	}
}

// A CA file that names no certificate, or is not there, is a wrong config:
// nothing runs, and the refusal names the key.
func TestACAFileThatHoldsNoCertificateIsAWrongConfig(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	empty := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(empty, []byte("no certificate here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"holds none": empty, "is not there": filepath.Join(dir, "none.pem")} {
		var bad *configError
		if err := do(t.Context(), "config", []string{"--ca-file", path}, nil, io.Discard, io.Discard); !errors.As(err, &bad) || bad.key != "ca-file" {
			t.Errorf("a CA file that %s: %v", name, err)
		}
	}
}

// With a CA file, the client trusts its CAs alone: init reaches a server whose
// certificate the file holds, and is refused by one whose certificate it does not
// hold, as it is with no file, by the system's CAs.
func TestTheCAFileIsTheOnlyTrustInTheServersCertificate(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.NewTLS(t, time.Now)
	own := writeCA(t, filepath.Join(t.TempDir(), "own.pem"), srv.Certificate())
	other := writeCA(t, filepath.Join(t.TempDir(), "other.pem"), someCert(t))
	if !strings.HasPrefix(srv.URL, "https://") {
		t.Fatalf("the server is at %s", srv.URL)
	}
	init := func(profile string, args ...string) error {
		return do(t.Context(), "init", append([]string{"--profile", profile, "--server", srv.URL}, args...), nil, io.Discard, io.Discard)
	}
	if err := init("own", "--ca-file", own); err != nil {
		t.Fatalf("with the server's own CA: %v", err)
	}
	for profile, args := range map[string][]string{"another-ca": {"--ca-file", other}, "system-cas": nil} {
		if err := init(profile, args...); err == nil || !strings.Contains(err.Error(), "TLS certificate does not verify") {
			t.Errorf("%s: %v", profile, err)
		}
	}
}

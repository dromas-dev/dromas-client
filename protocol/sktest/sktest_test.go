package sktest_test

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/dromas-dev/dromas-client/protocol/sktest"
)

// signers is a key of each type, made for sktest.Application.
func signers(t *testing.T) []*sktest.Signer {
	t.Helper()
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var out []*sktest.Signer
	for _, k := range []crypto.Signer{ed, p} {
		s, err := sktest.New(k, sktest.Application)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// A signature of each type verifies with golang.org/x/crypto/ssh, the same
// bytes each time, and carries the flags and the counter it was given; one
// without user presence does not verify, as x/crypto requires it.
func TestASignatureIsWhatTheLibraryVerifies(t *testing.T) {
	t.Parallel()
	for _, s := range signers(t) {
		want := map[string]string{ssh.KeyAlgoSKED25519: "", ssh.KeyAlgoSKECDSA256: ""}
		if _, ok := want[s.PublicKey().Type()]; !ok {
			t.Fatalf("a key of the type %s", s.PublicKey().Type())
		}
		s.Counter = 7
		data := []byte("data")
		sig, err := s.Sign(nil, data)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PublicKey().Verify(data, sig); err != nil {
			t.Errorf("%s: %v", sig.Format, err)
		}
		if !bytes.Equal(sig.Rest, []byte{sktest.UserPresent, 0, 0, 0, 7}) || sig.Format != s.PublicKey().Type() {
			t.Errorf("%s: flags and counter %x", sig.Format, sig.Rest)
		}
		again, err := s.Sign(nil, data)
		if err != nil || !bytes.Equal(ssh.Marshal(again), ssh.Marshal(sig)) {
			t.Errorf("%s: a second signature of the same data differs: %v", sig.Format, err)
		}
		if err := s.PublicKey().Verify([]byte("other data"), sig); err == nil {
			t.Errorf("%s: verifies for other data", sig.Format)
		}
		s.Flags = 0
		sig, err = s.Sign(nil, data)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PublicKey().Verify(data, sig); err == nil {
			t.Errorf("%s: a signature without user presence verifies", sig.Format)
		}
	}
}

// A key of another kind, or on another curve, is no security key.
func TestOnlyEd25519AndP256AreSecurityKeys(t *testing.T) {
	t.Parallel()
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sktest.New(p384, sktest.Application); err == nil {
		t.Error("a P-384 key was made a security key")
	}
}

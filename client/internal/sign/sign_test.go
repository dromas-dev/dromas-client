package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dromas-dev/dromas-client/protocol"
)

// Nothing in the module signs but this package: no other file outside
// protocol and sshsig, which make signatures for whoever holds a key, calls
// one of their signing functions, or a method that signs (an ssh.Signer's
// Sign or SignWithAlgorithm, a certificate's SignCert). So a mailbox's keys sign
// only through Identity and Work, and every signature is refused once the
// mailbox is finished, however a new path reaches one. internal/forge signs as a
// test's operator does, as a _test.go file may, so it is read as one: no
// file but a test imports it.
func TestOnlySignSigns(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	signing := map[string][]string{
		"protocol": {"Sign", "NewCertificate", "SignRevocation", "SignRevokedCertificates", "NewPass", "SignEnvelope"},
		"sshsig":   {"Sign"},
	}
	methods := []string{"Sign", "SignWithAlgorithm", "SignCert"}
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if slices.Contains([]string{".git", "testdata", "protocol", "sshsig", filepath.Join("client", "internal", "sign"), filepath.Join("internal", "forge")}, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, im := range f.Imports {
			if im.Path.Value == `"github.com/dromas-dev/dromas-client/internal/forge"` {
				t.Errorf("%s: imports internal/forge, which only a test may", rel)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := c.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && slices.Contains(signing[pkg.Name], sel.Sel.Name) {
				t.Errorf("%s: calls %s.%s: a mailbox signs only through package sign", rel, pkg.Name, sel.Sel.Name)
			} else if slices.Contains(methods, sel.Sel.Name) {
				t.Errorf("%s: calls a method %s: a mailbox signs only through package sign", rel, sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 20 {
		t.Fatalf("read %d files: the walk found too few to have read the module", files)
	}
}

// signers is every signer this package gives, answering finished, and every
// method of each that signs, by name: a method not listed here fails
// TestEverySignatureAsksTheMailbox.
func signers(t *testing.T, finished Finished) map[string]func() error {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	id := NewIdentity(key, finished)
	work := NewWork(key, finished)
	pid, err := protocol.IdentityOf(key.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	a := pid.Address()
	p := &protocol.Prekey{Address: a, Serial: 1}
	_, wpriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wkey, err := ssh.NewPublicKey(wpriv.Public())
	if err != nil {
		t.Fatal(err)
	}
	r, err := protocol.NewRevokedCertificates(a, 1)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]func() error{
		"Identity.Certify":             func() error { _, err := id.Certify(pid, wkey, 2, time.Now(), 2*time.Hour); return err },
		"Identity.Revocation":          func() error { _, err := id.Revocation(a); return err },
		"Identity.RevokedCertificates": func() error { _, err := id.RevokedCertificates(r); return err },
		"Identity.Statement":           func() error { _, err := id.Statement(p); return err },
		"Identity.Message":             func() error { _, err := id.Message([]byte("x"), a); return err },
		"Identity.Pass":                func() error { _, _, err := id.Pass(a, "example.org", time.Now()); return err },
		"Work.Statement":               func() error { _, err := work.Statement(p); return err },
		"Work.Message":                 func() error { _, err := work.Message([]byte("x"), a); return err },
		"Work.Pass":                    func() error { _, _, err := work.Pass(a, "example.org", time.Now()); return err },
	}
}

// Every method of the two signers that signs is tried by the tests below:
// read from the types, so a new one is tried too. Each signs while the
// mailbox is not finished.
func TestEverySignatureAsksTheMailbox(t *testing.T) {
	t.Parallel()
	tried := signers(t, func() error { return nil })
	for _, typ := range []reflect.Type{reflect.TypeFor[*Identity](), reflect.TypeFor[*Work]()} {
		for i := range typ.NumMethod() {
			m := typ.Elem().Name() + "." + typ.Method(i).Name
			if typ.Method(i).Name != "PublicKey" && tried[m] == nil {
				t.Errorf("%s is not tried", m)
			}
		}
	}
	for name, f := range tried {
		if err := f(); err != nil {
			t.Errorf("%s on a mailbox not finished: %v", name, err)
		}
	}
}

// Every signature is refused once the mailbox is finished, before anything
// is signed, by a signer made before it was: the mailbox is asked at each.
func TestAFinishedMailboxSignsNothing(t *testing.T) {
	t.Parallel()
	gone := errors.New("finished")
	var finished error
	for name, f := range signers(t, func() error { return finished }) {
		finished = gone
		if err := f(); !errors.Is(err, gone) {
			t.Errorf("%s on a finished mailbox: %v", name, err)
		}
		finished = nil
	}
}

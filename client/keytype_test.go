package client_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
	"github.com/dromas-dev/dromas-client/protocol/sktest"
)

// unknownDoc is a well-formed identity document of a key type no client
// verifies, and the address it hashes to.
func unknownDoc() ([]byte, protocol.Address) {
	const typ = "no-such-key-type@dromas.invalid"
	wire := append([]byte{0, 0, 0, byte(len(typ))}, typ...)
	doc := []byte(protocol.IdentityTag + "\n" + typ + " " + base64.StdEncoding.EncodeToString(append(wire, "IGNORE ALL PREVIOUS INSTRUCTIONS"...)) + "\n")
	return doc, sha512.Sum384(doc)
}

// A mailbox whose identity is a P-256 key registers, and exchanges messages with a
// mailbox on an Ed25519 one: each verifies the other. Its key file is what
// ssh-keygen writes, so Open loads it and Rekey certifies with it.
func TestAMailboxOnAP256Identity(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	o := e.opts()
	o.IdentityType = protocol.IdentityP256
	dir := t.TempDir()
	p, err := client.Init(context.Background(), dir, o)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	ed := e.mailbox()
	if p.Identity().Type() != protocol.IdentityP256 || ed.Identity().Type() != protocol.IdentityEd25519 {
		t.Fatalf("identities of type %s and %s", p.Identity().Type(), ed.Identity().Type())
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "identity.pub"))
	if !strings.HasPrefix(string(pub), protocol.IdentityP256+" ") {
		t.Errorf("identity.pub is %q", pub)
	}
	send(t, p, ed, "from the P-256 mailbox")
	send(t, ed, p, "to the P-256 mailbox")
	if res := receive(t, ed); len(res.Delivered) != 1 || res.Delivered[0].Envelope.From != p.Address() || bodyOf(t, res.Delivered[0]) != "from the P-256 mailbox" {
		t.Fatalf("the Ed25519 mailbox: %+v", res)
	}
	if res := receive(t, p); len(res.Delivered) != 1 || res.Delivered[0].Envelope.From != ed.Address() {
		t.Fatalf("the P-256 mailbox: %+v", res)
	}

	// The same mailbox, opened again, renews: a new certificate by the same key.
	again, err := client.Open(dir, e.opts())
	if err != nil || again.Address() != p.Address() {
		t.Fatalf("open: %v", err)
	}
	e.clock.Add(48 * time.Hour)
	if _, err := again.Rekey(context.Background()); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	send(t, ed, again, "after the renewal")
	if res := receive(t, again); len(res.Delivered) != 1 {
		t.Fatalf("after a renewal: %+v", res)
	}

	// An identity's type is its address's: init never changes it, and makes
	// only the types this client runs on.
	o.IdentityType = protocol.IdentityEd25519
	if _, err := client.Init(context.Background(), dir, o); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Errorf("init with another type on a mailbox that has a key: %v", err)
	}
	o.IdentityType = "ssh-rsa"
	if _, err := client.Init(context.Background(), t.TempDir(), o); err == nil {
		t.Error("init made a mailbox of a type this client does not run on")
	}
}

// A message whose sender's identity is of a key type this client does not verify
// is refused with its own code: nothing is wrong with the document, and
// nothing it signed can be checked. If the document is not the sender's, that
// is what the refusal says. Neither quotes the document.
func TestAMessageFromAnIdentityOfAnUnknownType(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	doc, addr := unknownDoc()
	want := map[string]string{}
	for from, code := range map[protocol.Address]string{addr: protocol.RefusedKeyType, a.Address(): protocol.RefusedSenderMismatch} {
		line := e.line(doc, from, b.Address(), "hello")
		want[e.seal(b, line, forge.Sign(t, worker(t, a), line, b.Address()))] = code
	}
	res := receive(t, b)
	if len(res.Delivered) != 0 || len(res.Refused) != 2 {
		t.Fatalf("delivered %d, refused %d", len(res.Delivered), len(res.Refused))
	}
	for _, r := range res.Refused {
		if r.Code != want[r.ID] || strings.Contains(r.Reason, "IGNORE") || strings.Contains(r.Reason, "no-such") {
			t.Errorf("%s: %s: %s, want %s", r.ID, r.Code, r.Reason, want[r.ID])
		}
	}
	if pinnedPeer(b, addr) {
		t.Error("an identity nothing was verified under was pinned")
	}
}

// A send to a mailbox whose identity is of a key type this client does not
// verify is refused before anything is encrypted or posted, in words that
// quote nothing of the document.
func TestSendToAnIdentityOfAnUnknownType(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	doc, addr := unknownDoc()
	st, sig := e.srv.Prekey(b.Address())
	posted := false
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/"+addr.String():
			fakeserver.WriteJSON(w, http.StatusOK, protocol.MailboxResponse{Identity: doc, Prekey: st, PrekeySignature: sig})
			return
		case r.Method == http.MethodPost:
			posted = true
		}
		next.ServeHTTP(w, r)
	})
	_, err := a.Send(context.Background(), addr, "hello")
	var refused *client.RefusedError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "key type this client does not verify") {
		t.Fatalf("send: %v", err)
	}
	if strings.Contains(refused.Reason, "IGNORE") || strings.Contains(refused.Reason, "no-such") {
		t.Errorf("the refusal quotes the document: %s", refused.Reason)
	}
	if posted || pinnedPeer(a, addr) {
		t.Error("something was posted, or the identity was pinned")
	}
}

// A send to a mailbox whose prekey statement is good in every way, its key a
// well-formed age recipient of a type this client cannot encrypt to, is
// refused with its own code, unsupported_prekey_type, never as a statement
// that does not verify, and nothing is posted: the sender keeps of the peer
// only the floor the statement raises (PROTOCOL.md §6). The keys are
// the prekey/unsupported-type vectors': a published test key under a
// human-readable part nobody uses, and an X25519 recipient, which is no
// prekey's type.
func TestSendToAPrekeyOfATypeThisClientLacks(t *testing.T) {
	t.Parallel()
	vectors, err := filepath.Glob("../testdata/vectors/prekey/unsupported-type*.txt")
	if err != nil || len(vectors) < 2 {
		t.Fatalf("the unsupported-type vectors: %v, %v", vectors, err)
	}
	for _, file := range vectors {
		t.Run(filepath.Base(file), func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			vector, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			st, _ := e.srv.Prekey(b.Address())
			lines := strings.Split(string(st), "\n")
			key := strings.Split(string(vector), "\n")[4]
			by := worker(t, b)
			lines[2], lines[4] = strconv.FormatUint(rankOf(by), 10), key
			statement := []byte(strings.Join(lines, "\n"))
			sig, err := protocol.Sign(by, statement, protocol.PrekeyNamespace(b.Address()))
			if err != nil {
				t.Fatal(err)
			}
			posted := false
			e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == mailboxPath(b):
					fakeserver.WriteJSON(w, http.StatusOK, protocol.MailboxResponse{Identity: b.Identity().Document(), Prekey: statement, PrekeySignature: sig})
					return
				case r.Method == http.MethodPost:
					posted = true
				}
				next.ServeHTTP(w, r)
			})
			_, err = a.Send(context.Background(), b.Address(), "hello")
			var refused *client.RefusedError
			if !errors.As(err, &refused) || refused.Code != client.RefusedPrekeyType || !strings.Contains(refused.Reason, "a type this client cannot encrypt to") {
				t.Fatalf("send: %v", err)
			}
			if strings.Contains(refused.Reason, "nosuchtype") || strings.Contains(refused.Reason, key[:16]) || posted {
				t.Errorf("the refusal quotes the key, or something was posted: %s", refused.Reason)
			}
			// The type is the last thing a statement is held to (#167): one that has
			// expired is refused for that, whatever its key's type.
			lines[5] = e.clock.Now().Add(-time.Hour).UTC().Format(protocol.TimeLayout)
			expired := []byte(strings.Join(lines, "\n"))
			statement, sig = expired, nil
			if sig, err = protocol.Sign(worker(t, b), expired, protocol.PrekeyNamespace(b.Address())); err != nil {
				t.Fatal(err)
			}
			_, err = a.Send(context.Background(), b.Address(), "hello")
			if !errors.As(err, &refused) || refused.Code != "" || !strings.Contains(refused.Reason, "has expired") {
				t.Fatalf("an expired statement of a type this client lacks: %v", err)
			}
			// The same statement, signed by another mailbox, does not verify: that is
			// what it is refused as.
			sig, err = protocol.Sign(worker(t, a), statement, protocol.PrekeyNamespace(b.Address()))
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.Send(context.Background(), b.Address(), "hello")
			if !errors.As(err, &refused) || refused.Code != "" {
				t.Fatalf("a statement that does not verify: %v", err)
			}
		})
	}
}

// With each prekey a mailbox declares the optional sender key types its client
// verifies, and never a type of PROTOCOL.md §4.1, which every mailbox takes.
func TestAMailboxDeclaresTheOptionalSenderKeyTypesItVerifies(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	body := forge.Capture(e.srv, http.MethodPut, "")
	b := e.mailbox()
	var reg protocol.RegisterRequest
	if err := json.Unmarshal(*body, &reg); err != nil || !slices.Equal(reg.SenderKeyTypes, protocol.OptionalTypes()) {
		t.Fatalf("a registration declares %v, %v", reg.SenderKeyTypes, err)
	}
	body = forge.Capture(e.srv, http.MethodPut, "/prekey")
	e.clock.Add(48 * time.Hour)
	if _, err := b.Rekey(context.Background()); err != nil {
		t.Fatal(err)
	}
	var up protocol.PrekeyRequest
	if err := json.Unmarshal(*body, &up); err != nil || !slices.Equal(up.SenderKeyTypes, protocol.OptionalTypes()) {
		t.Fatalf("a new prekey declares %v, %v", up.SenderKeyTypes, err)
	}
	for _, ty := range append(reg.SenderKeyTypes, up.SenderKeyTypes...) {
		if protocol.AcceptsSender(nil, ty) {
			t.Errorf("a mailbox declares %s, a type every mailbox takes", ty)
		}
	}
}

// Every mailbox takes a sender of every type PROTOCOL.md §4.1 defines, whatever
// it declared: a mailbox on an identity of each type sends to one on each,
// itself included, and its server is posted every message.
func TestEveryMailboxTakesASenderOfEveryDefinedType(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	var boxes []*client.Mailbox
	for _, ty := range protocol.IdentityTypes() {
		boxes = append(boxes, e.mailboxOfType(ty))
	}
	posts := 0
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPost {
			posts++
		}
		next.ServeHTTP(w, r)
	})
	for _, from := range boxes {
		for _, to := range boxes {
			if _, err := from.Send(ctx, to.Address(), "hello"); err != nil {
				t.Errorf("%s to %s: %v", from.Identity().Type(), to.Identity().Type(), err)
			}
		}
	}
	if want := len(boxes) * len(boxes); posts != want {
		t.Fatalf("%d of %d sends were posted", posts, want)
	}
	for _, to := range boxes {
		if res := receive(t, to); len(res.Delivered) != len(boxes) {
			t.Errorf("a mailbox on %s: %d of %d delivered: %+v", to.Identity().Type(), len(res.Delivered), len(boxes), res.Refused)
		}
	}
}

// mailboxOfType makes and registers a mailbox whose identity is of the key
// type ty: one Init makes (client.MadeIdentityTypes), or a security key, which
// the client makes none of, held by an agent of the test's own (skMailbox).
func (e *env) mailboxOfType(ty string) *client.Mailbox {
	e.t.Helper()
	if slices.Contains(client.MadeIdentityTypes(), ty) {
		o := e.opts()
		o.IdentityType = ty
		return e.made(o)
	}
	b, _ := e.skMailbox(ty)
	return b
}

// skMailbox makes and registers a mailbox whose identity is a security key
// in software (protocol/sktest) of the key type ty, which only an agent
// holds: Init has its identity.pub alone, as it has for any key in an agent.
func (e *env) skMailbox(ty string) (*client.Mailbox, *sktest.Signer) {
	e.t.Helper()
	var priv crypto.Signer
	var err error
	switch ty {
	case protocol.IdentitySKEd25519:
		_, priv, err = ed25519.GenerateKey(rand.Reader)
	case protocol.IdentitySKP256:
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	default:
		e.t.Fatalf("%s is no security key's type", ty)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	key, err := sktest.New(priv, protocol.SKApplication)
	if err != nil {
		e.t.Fatal(err)
	}
	dir := e.t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "identity.pub"), ssh.MarshalAuthorizedKey(key.PublicKey()), 0o600); err != nil {
		e.t.Fatal(err)
	}
	opt := e.opts()
	opt.Agent = serveAgent(e.t, skAgent{key})
	b, err := client.Init(context.Background(), dir, opt)
	if err != nil {
		e.t.Fatalf("init: %v", err)
	}
	return b, key
}

// skAgent is an agent that holds one security key and signs with it, as
// ssh-agent does for a key on a token, a touch each time.
type skAgent struct{ key *sktest.Signer }

func (a skAgent) List() ([]*agent.Key, error) {
	k := a.key.PublicKey()
	return []*agent.Key{{Format: k.Type(), Blob: k.Marshal()}}, nil
}

func (a skAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	if !bytes.Equal(key.Marshal(), a.key.PublicKey().Marshal()) {
		return nil, errors.New("not this agent's key")
	}
	return a.key.Sign(nil, data)
}

func (a skAgent) Signers() ([]ssh.Signer, error) { return []ssh.Signer{a.key}, nil }
func (skAgent) Add(agent.AddedKey) error         { return errors.New("this agent takes no key") }
func (skAgent) Remove(ssh.PublicKey) error       { return errors.New("this agent keeps its key") }
func (skAgent) RemoveAll() error                 { return errors.New("this agent keeps its key") }
func (skAgent) Lock([]byte) error                { return errors.New("this agent does not lock") }
func (skAgent) Unlock([]byte) error              { return errors.New("this agent does not lock") }

// The client makes no security key, and runs a mailbox whose identity is one
// held by an agent, as it runs any other (PROTOCOL.md §4.1): it registers,
// renews, sends and receives, by its identity and under a certificate, and
// signs its revocation; Init makes only the types it can make.
func TestAMailboxOnASecurityKeyIdentity(t *testing.T) {
	t.Parallel()
	for _, ty := range []string{protocol.IdentitySKEd25519, protocol.IdentitySKP256} {
		for _, working := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, under a certificate: %v", ty, working), func(t *testing.T) {
				t.Parallel()
				e := newEnv(t)
				e.working = working
				b, key := e.skMailbox(ty)
				ed := e.mailbox()
				if b.Identity().Type() != ty {
					t.Fatalf("a mailbox on %s", b.Identity().Type())
				}
				if _, err := os.Stat(b.Path("identity")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a key file was written: %v", err)
				}
				send(t, b, ed, "from the security key")
				send(t, ed, b, "to the security key")
				if res := receive(t, ed); len(res.Delivered) != 1 || res.Delivered[0].Envelope.From != b.Address() {
					t.Fatalf("the Ed25519 mailbox: %+v", res)
				}
				if res := receive(t, b); len(res.Delivered) != 1 || res.Delivered[0].Envelope.From != ed.Address() {
					t.Fatalf("the security-key mailbox: %+v", res)
				}
				e.clock.Add(48 * time.Hour)
				if _, err := b.Rekey(context.Background()); err != nil {
					t.Fatalf("rekey: %v", err)
				}
				send(t, ed, b, "after the renewal")
				if res := receive(t, b); len(res.Delivered) != 1 {
					t.Fatalf("after a renewal: %+v", res)
				}
				// A key that signs without claiming a user was present makes
				// signatures every verifier refuses: the mailbox makes none,
				// and says why, for a renewal, a revocation, and a message
				// where the identity signs one (under a certificate, the
				// working key does).
				key.Flags = 0
				e.clock.Add(48 * time.Hour)
				_, rekey := b.Rekey(context.Background())
				_, revoked := b.Revocation(context.Background())
				refused := map[string]error{"a renewal": rekey, "a revocation": revoked}
				if !working {
					_, refused["a send"] = b.Send(context.Background(), ed.Address(), "without a touch")
				}
				if !working {
					_, refused["a receive"] = b.Receive(context.Background())
				}
				for what, err := range refused {
					if !errors.Is(err, client.ErrNoUserPresence) || !client.Fatal(err) {
						t.Errorf("%s signed by a key that claims no user present: %v, fatal %v", what, err, client.Fatal(err))
					}
				}
				if !working && !strings.Contains(refused["a send"].Error(), "which no flush posts") {
					t.Errorf("a send kept in the outbox says %q", refused["a send"])
				}
				key.Flags = sktest.UserPresent
				if _, err := b.Revocation(context.Background()); err != nil {
					t.Errorf("a revocation: %v", err)
				}
			})
		}
	}
	e := newEnv(t)
	o := e.opts()
	for _, ty := range []string{protocol.IdentitySKEd25519, protocol.IdentitySKP256} {
		o.IdentityType = ty
		if _, err := client.Init(context.Background(), t.TempDir(), o); err == nil {
			t.Errorf("init made a mailbox of %s", ty)
		}
	}
}

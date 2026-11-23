package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"github.com/dromas-dev/dromas-client/protocol"
	"github.com/dromas-dev/dromas-client/protocol/sktest"
)

// An identity whose security key, in an agent, signs without claiming a user
// was present, as one enrolled with no-touch-required does, makes no
// signature any verifier takes: recv ends with exit status 4 and "fatal", so a
// service manager does not start it again to ask the key once more.
func TestRecvOnASecurityKeyThatClaimsNoUserIsFatal(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := sktest.New(priv, protocol.SKApplication)
	if err != nil {
		t.Fatal(err)
	}
	socket := serveSecurityKey(t, key)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "identity.pub"), ssh.MarshalAuthorizedKey(key.PublicKey()), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := fakeserver.NewInMemory(t, time.Now)
	opt := client.Options{Base: srv.URL, HTTPClient: srv.Client(), Agent: socket}
	b, err := client.Init(context.Background(), dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	useMailbox(t, b.Dir())
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	// Two hours on, the pass init signed has run out, so recv signs one.
	key.Flags = 0
	opt.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	var out, errs bytes.Buffer
	err = doWith(context.Background(), opt, "recv", []string{"--agent", socket}, strings.NewReader(""), &out, &errs)
	var rep bytes.Buffer
	st := report(err, &rep)
	lines := printed(t, rep.String())
	if !errors.Is(err, client.ErrNoUserPresence) || st != 4 || len(lines) != 1 || lines[0]["type"] != "error" || lines[0]["fatal"] != true {
		t.Fatalf("recv: exit %d, %s; err %v", st, rep.String(), err)
	}
}

// serveSecurityKey serves an agent that holds key alone on a socket of its
// own, as ssh-agent serves a key on a token, and returns the socket.
func serveSecurityKey(t *testing.T, key *sktest.Signer) string {
	t.Helper()
	// A socket's path is short on some systems (104 bytes on macOS).
	d, err := os.MkdirTemp("", "drk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	socket := filepath.Join(d, "s")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = agent.ServeAgent(securityKeyAgent{key}, c)
				_ = c.Close()
			}()
		}
	}()
	return socket
}

// securityKeyAgent holds one security key and signs with it.
type securityKeyAgent struct{ key *sktest.Signer }

func (a securityKeyAgent) List() ([]*agent.Key, error) {
	k := a.key.PublicKey()
	return []*agent.Key{{Format: k.Type(), Blob: k.Marshal()}}, nil
}

func (a securityKeyAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	if !bytes.Equal(key.Marshal(), a.key.PublicKey().Marshal()) {
		return nil, errors.New("not this agent's key")
	}
	return a.key.Sign(nil, data)
}

func (a securityKeyAgent) Signers() ([]ssh.Signer, error) { return []ssh.Signer{a.key}, nil }
func (securityKeyAgent) Add(agent.AddedKey) error         { return errors.New("this agent takes no key") }
func (securityKeyAgent) Remove(ssh.PublicKey) error       { return errors.New("this agent keeps its key") }
func (securityKeyAgent) RemoveAll() error                 { return errors.New("this agent keeps its key") }
func (securityKeyAgent) Lock([]byte) error                { return errors.New("this agent does not lock") }
func (securityKeyAgent) Unlock([]byte) error              { return errors.New("this agent does not lock") }

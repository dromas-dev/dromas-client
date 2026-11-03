package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"github.com/dromas-dev/dromas-client/protocol"
	"golang.org/x/crypto/ssh"
)

// A crash is a mailbox's process killed between two of the steps at which a
// command writes the mailbox (steps.go names each): the next command finds the
// mailbox whole, and goes on from where the last left it. Each case kills a copy
// of this test binary at one step, with SIGKILL, so nothing of the process
// runs after it: no deferred call, no close, no further write. What it cannot
// show is a power cut, where what was not synced is lost: each step syncs
// before the next begins (writeFile, bbolt's commit), and a test cannot pull
// the power (#316).

// crashChild is the environment variable a copy of this binary reads to run
// one command of a mailbox and die at one step.
const crashChild = "DROMAS_TEST_CRASH"

// crashOrder is what the copy runs: the step it dies at, the mailbox's directory,
// its server's URL, its clock in Unix seconds, the command, and for a send or
// an allow, the address it names.
type crashOrder struct {
	Step, Dir, Base, Command, To string
	Now                          int64
}

// crashCommands are what a copy runs, by name.
var crashCommands = map[string]func(ctx context.Context, b *client.Mailbox, o crashOrder) error{
	"rekey": func(ctx context.Context, b *client.Mailbox, _ crashOrder) error {
		_, err := b.Rekey(ctx)
		return err
	},
	"receive": func(ctx context.Context, b *client.Mailbox, _ crashOrder) error { _, err := b.Receive(ctx); return err },
	"follow": func(ctx context.Context, b *client.Mailbox, o crashOrder) error {
		return followOwed(ctx, b, o.Dir+".handled")
	},
	"remedy": func(ctx context.Context, b *client.Mailbox, _ crashOrder) error {
		_, err := b.RenewAndRevoke(ctx)
		return err
	},
	"init --offline": func(ctx context.Context, b *client.Mailbox, o crashOrder) error {
		now := time.Unix(o.Now, 0).UTC()
		_, err := client.Init(ctx, b.Dir(), client.Options{Base: o.Base, Now: func() time.Time { return now }, WorkingKey: true})
		return err
	},
	"switch": func(ctx context.Context, b *client.Mailbox, _ crashOrder) error {
		_, err := b.RenewUnderCertificate(ctx)
		return err
	},
	"send": func(ctx context.Context, b *client.Mailbox, o crashOrder) error {
		to, err := protocol.ParseAddress(o.To)
		if err != nil {
			return err
		}
		_, err = b.Send(ctx, to, "sent by a process killed on its way")
		return err
	},
	"allow": func(ctx context.Context, b *client.Mailbox, o crashOrder) error {
		to, err := protocol.ParseAddress(o.To)
		if err != nil {
			return err
		}
		_, err = b.Allow(ctx, to)
		return err
	},
}

// TestCrashChild is the copy of this binary that runs one command and is
// killed at one step: in a test run of its own it does nothing.
func TestCrashChild(t *testing.T) {
	raw := os.Getenv(crashChild)
	if raw == "" {
		t.Skip("run by the crash tests, in a process of its own")
	}
	var o crashOrder
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	client.SetStep(func(name string) {
		if name == o.Step {
			killSelf(o.Step)
			time.Sleep(time.Minute) // the signal is on its way
		}
	})
	now := time.Unix(o.Now, 0).UTC()
	if o.Command == newOfflineInit || o.Command == newInit { // there is no mailbox to open
		_, err := client.Init(context.Background(), o.Dir, client.Options{Base: o.Base, Now: func() time.Time { return now }, WorkingKey: o.Command == newOfflineInit})
		fmt.Printf("ended before %s: %v\n", o.Step, err)
		return
	}
	b, err := client.Open(o.Dir, client.Options{Base: o.Base, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	err = crashCommands[o.Command](context.Background(), b, o)
	fmt.Printf("ended before %s: %v\n", o.Step, err)
}

// crash runs command on mailbox b in a copy of this binary, killed at step, and
// fails the test unless it died there.
func crash(t *testing.T, e *env, b *client.Mailbox, command, step string, to protocol.Address) {
	t.Helper()
	crashIn(t, e, b.Dir(), command, step, to)
}

// crashIn is crash, by the mailbox's directory.
func crashIn(t *testing.T, e *env, dir, command, step string, to protocol.Address) {
	t.Helper()
	order, err := json.Marshal(crashOrder{Step: step, Dir: dir, Base: e.srv.URL, Command: command, To: to.String(), Now: e.clock.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1") //nolint:gosec // this test binary, again
	cmd.Env = append(os.Environ(), crashChild+"="+string(order))
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !killedAt(exit, out, step) {
		t.Fatalf("%s did not die at %s: %v\n%s", command, step, err, out)
	}
}

// inbox is the body of each message in a mailbox's inbox/, as many times as it is
// there.
func inbox(t *testing.T, b *client.Mailbox) []string {
	t.Helper()
	return folder(t, b, "inbox")
}

// folder is the body of each message in a folder of a mailbox's mail, as many
// times as it is there.
func folder(t *testing.T, b *client.Mailbox, name string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(b.Dir(), "mail", name, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, path := range names {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var line struct{ Body string }
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		bodies = append(bodies, line.Body)
	}
	slices.Sort(bodies)
	return bodies
}

// whole checks a mailbox after a crash by its next commands, opened anew from its
// directory: a drain takes everything waiting on its server, refusing
// nothing; a renewal goes through, and its server serves the prekey it
// published; messages sealed to the new one are read; and the inbox holds every
// message sent to the mailbox, once: waiting, sent before the crash, and those
// sent here.
func whole(t *testing.T, e *env, b, from *client.Mailbox, waiting ...string) {
	t.Helper()
	ctx := context.Background()
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	send(t, from, x, "after the crash")
	res, err := x.Receive(ctx)
	if err != nil || len(res.Refused) != 0 || e.srv.Count(x.Address()) != 0 {
		t.Fatalf("the drain after the crash: %+v, %v; the server still holds %d", res, err, e.srv.Count(x.Address()))
	}
	e.clock.Add(2 * time.Second)
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatalf("the renewal after the crash: %v", err)
	}
	if served := servedSerial(t, e, x); served != numberOf(t, x) {
		t.Fatalf("the server serves %d, the mailbox published %d", served, numberOf(t, x))
	}
	send(t, from, x, "after the renewal")
	if res := receive(t, x); len(res.Delivered) != 1 {
		t.Fatalf("after the renewal: %+v", res)
	}
	want := append(slices.Clone(waiting), "after the crash", "after the renewal")
	slices.Sort(want)
	if got := inbox(t, x); !slices.Equal(got, want) {
		t.Fatalf("inbox/ holds %q, want %q: each message once", got, want)
	}
}

// A renewal killed at each of its steps: the next drain delivers what was
// sealed to the prekey in use before it, and the mailbox renews again.
func TestARenewalKilledAtEachStepLeavesTheMailboxWhole(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, working := range []bool{false, true} {
		for _, step := range renewalSteps(working) {
			t.Run(fmt.Sprintf("under a certificate %v, %s", working, step), func(t *testing.T) {
				e := newEnv(t)
				e.working = working
				a, b := e.mailbox(), e.mailbox()
				send(t, a, b, "sealed to the prekey in use")
				e.clock.Add(2 * time.Second)
				crash(t, e, b, "rekey", step, protocol.Address{})
				whole(t, e, b, a, "sealed to the prekey in use")
			})
		}
	}
}

// renewalSteps are the steps of a renewal, under a certificate where working
// is set: the identity's makes no working key.
func renewalSteps(working bool) []string {
	if working {
		return client.Steps("renewal")
	}
	return slices.DeleteFunc(slices.Clone(client.Steps("renewal")), func(s string) bool { return s == "working key made" })
}

// newOfflineInit is init --offline in an empty directory, which TestCrashChild runs
// with no mailbox to open first.
const newOfflineInit = "a new init --offline"

// newInit is init in an empty directory, run as newOfflineInit is.
const newInit = "a new init"

// init in an empty directory, killed once it has written the identity key: the key
// is there and identity.pub is not, since identity.pub goes after the key. Run
// again, init writes identity.pub from the key and comes back whole.
func TestANewInitKilledOnceItsKeyIsWrittenWritesIdentityPubAgain(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("identity") {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a := e.mailbox()
			dir := filepath.Join(t.TempDir(), "mailbox")
			crashIn(t, e, dir, newInit, step, protocol.Address{})
			if _, err := os.Stat(filepath.Join(dir, "identity")); err != nil {
				t.Fatalf("the identity key after a crash at %s: %v", step, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "identity.pub")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("identity.pub after a crash at %s, before the key's step is past: %v", step, err)
			}
			b, err := client.Init(t.Context(), dir, e.opts())
			if err != nil {
				t.Fatalf("again after %s: %v", step, err)
			}
			pub, err := os.ReadFile(b.Path("identity.pub"))
			if err != nil {
				t.Fatal(err)
			}
			key, _, _, _, err := ssh.ParseAuthorizedKey(pub)
			if err != nil {
				t.Fatalf("identity.pub: %v", err)
			}
			if !bytes.Equal(key.Marshal(), b.Identity().PublicKey().Marshal()) {
				t.Fatal("identity.pub is not the identity key's")
			}
			landAll(t, b)
			whole(t, e, b, a)
		})
	}
}

// init --offline in an empty directory, killed at each step of its renewal, a path
// of its own, since a new identity has nothing at its server to read: run again,
// it goes on, and comes back whole under a certificate.
func TestANewOfflineInitKilledAtEachStepComesBackUnderACertificate(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, step := range renewalSteps(true) {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a := e.mailbox()
			dir := filepath.Join(t.TempDir(), "mailbox")
			crashIn(t, e, dir, newOfflineInit, step, protocol.Address{})
			opt := e.opts()
			opt.WorkingKey = true
			b, err := client.Init(t.Context(), dir, opt)
			if err != nil {
				t.Fatalf("again after %s: %v", step, err)
			}
			landAll(t, b)
			if !certified(t, b) {
				t.Fatal("not under a certificate")
			}
			whole(t, e, b, a)
		})
	}
}

// A restore told to register under a certificate (init --offline), at a
// server that holds nothing of the address, killed at each step of its
// renewal: run again, it goes on, and comes back whole under a certificate.
func TestAnOfflineInitKilledAtEachStepComesBackUnderACertificate(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, step := range renewalSteps(true) {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a := e.mailbox()
			dir := filepath.Join(t.TempDir(), "mailbox")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			// An identity e's server holds nothing of: a mailbox's at another
			// server.
			key, err := os.ReadFile(filepath.Join(newEnv(t).mailbox().Dir(), "identity"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "identity"), key, 0o600); err != nil {
				t.Fatal(err)
			}
			b, err := client.Open(dir, e.opts())
			if err != nil {
				t.Fatal(err)
			}
			crash(t, e, b, "init --offline", step, protocol.Address{})
			opt := e.opts()
			opt.WorkingKey = true
			x, err := client.Init(context.Background(), dir, opt)
			if err != nil {
				t.Fatalf("init --offline again after a crash at %s: %v", step, err)
			}
			landAll(t, x)
			if !certified(t, x) {
				t.Fatalf("after a crash at %s: not under a certificate", step)
			}
			whole(t, e, x, a)
		})
	}
}

// A switch (cert switch) killed at each step of its renewal: run again, it
// renews under a certificate, and the message the mailbox's identity signed
// before it is taken once.
func TestASwitchKilledAtEachStepFinishesWhenRunAgain(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, step := range renewalSteps(true) {
		t.Run(step, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, b, a, "signed by the identity")
			e.clock.Add(2 * time.Second)
			crash(t, e, b, "switch", step, protocol.Address{})
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			e.clock.Add(2 * time.Second)
			if _, err := x.RenewUnderCertificate(ctx); err != nil {
				t.Fatalf("the switch again after a crash at %s: %v", step, err)
			}
			if !certified(t, x) || servedCertificate(t, e, x) != numberOf(t, x) {
				t.Fatalf("after a crash at %s: not under a certificate", step)
			}
			res := receive(t, a)
			if len(res.Delivered) != 1 || bodyOf(t, res.Delivered[0]) != "signed by the identity" {
				t.Fatalf("the reader after a crash at %s: %+v", step, res)
			}
			whole(t, e, x, a)
		})
	}
}

// A mailbox its server forgot, killed at each step of the registration that
// brings it back: its server holds no ACL of it, whatever became of the
// answer, and the next commands send the ACL again (#324). A recv brings the
// mailbox back where the registration was not taken.
func TestAForgottenMailboxKilledAtEachStepOfItsRegistrationSendsItsACLAgain(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, step := range renewalSteps(false) {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, b, c := e.mailbox(), e.mailbox(), e.mailbox()
			if _, err := b.Block(context.Background(), c.Address()); err != nil || e.srv.ACL(b.Address()) == nil {
				t.Fatalf("the block before the mailbox was forgotten: %v", err)
			}
			e.srv.Forget(b.Address())
			e.clock.Add(2 * time.Second)
			crash(t, e, b, "rekey", step, protocol.Address{})
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.Receive(context.Background()); err != nil {
				t.Fatalf("the recv after the crash: %v", err)
			}
			whole(t, e, b, a)
			if acl := e.srv.ACL(b.Address()); acl == nil || !slices.Contains(acl.Deny.Addrs, c.Address()) {
				t.Fatalf("after a crash at %s the server holds the ACL %+v, which does not deny the sender the mailbox blocked", step, acl)
			}
		})
	}
}

// A recv whose renewal is due, killed at each step of its renewal and of the
// read of its server's ACL that follows it: the read is owed from before the
// renewal (#377), so the next recv makes it, whichever command recorded the
// renewal. After it the server holds the mailbox's own ACL, and recv has said it
// replaced another writer's where one was there, once, and nothing where
// none was: another writer's deny list or pause, the mailbox's own, and a mailbox
// its server forgot, before the renewal's read or between the read and the
// upload, which the renewal registers again (#386). A send between the kill
// and the next recv, whose pass refused has the mailbox read itself and record a
// renewal taken unseen (ownerTaken), leaves the read owed too.
func TestARecvKilledAtEachStepOfItsRenewalReadsItsServersACL(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	const (
		none = iota
		before
		atUpload
	)
	for _, s := range []struct {
		name   string
		other  func(d protocol.Address) *protocol.ACL
		forget int
		send   bool
	}{
		{"another writer's deny list", func(d protocol.Address) *protocol.ACL {
			return &protocol.ACL{Allow: protocol.ACLList{Any: true}, Deny: protocol.NewACLList(false, []protocol.Address{d})}
		}, none, false},
		{"another writer's deny list, a send first", func(d protocol.Address) *protocol.ACL {
			return &protocol.ACL{Allow: protocol.ACLList{Any: true}, Deny: protocol.NewACLList(false, []protocol.Address{d})}
		}, none, true},
		{"another writer's pause", func(protocol.Address) *protocol.ACL { return &protocol.ACL{Deny: protocol.ACLList{Any: true}} }, none, false},
		{"its own", nil, none, false},
		{"forgotten before the renewal's read", nil, before, false},
		{"forgotten between the renewal's read and its upload", nil, atUpload, false},
	} {
		for _, step := range append(renewalSteps(false), client.Steps("heal")...) {
			if step == "held ACL kept" && s.other == nil {
				continue // nothing another writer put is there to keep
			}
			t.Run(s.name+", "+step, func(t *testing.T) {
				ctx := context.Background()
				e := newEnv(t)
				a, b, c, d := e.mailbox(), e.mailbox(), e.mailbox(), e.mailbox()
				if _, err := b.Block(ctx, c.Address()); err != nil {
					t.Fatal(err)
				}
				own := e.srv.ACL(b.Address())
				if s.other != nil {
					e.srv.PutACL(b.Address(), s.other(d.Address()))
				}
				switch s.forget {
				case before:
					e.srv.Forget(b.Address())
				case atUpload:
					var forgot atomic.Bool
					e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
						if r.Method == http.MethodPut && r.URL.Path == mailboxPath(b)+"/prekey" && forgot.CompareAndSwap(false, true) {
							e.srv.Forget(b.Address())
						}
						next.ServeHTTP(w, r)
					})
				}
				e.clock.Add(25 * time.Hour)
				crash(t, e, b, "receive", step, protocol.Address{})
				x, err := client.Open(b.Dir(), e.opts())
				if err != nil {
					t.Fatal(err)
				}
				if s.send {
					if _, err := x.Send(ctx, a.Address(), "sent before the next recv"); err != nil {
						t.Fatalf("the send after the crash: %v", err)
					}
				}
				res, err := x.Receive(ctx)
				if err != nil || res.ACLErr != nil || res.RenewErr != nil {
					t.Fatalf("the recv after the crash: %+v, %v", res, err)
				}
				if res.ACLReplaced != (s.other != nil) || !e.srv.ACL(x.Address()).SameLists(own) {
					t.Fatalf("the recv after a crash at %s: acl_replaced %v; the server holds %+v, the mailbox's own is %+v", step, res.ACLReplaced, e.srv.ACL(x.Address()), own)
				}
				if again := receive(t, x); again.ACLReplaced {
					t.Fatalf("the replacement is reported again: %+v", again)
				}
			})
		}
	}
}

// A renewal killed after its key and working key were made, before its
// statement was signed, leaves files no statement names; the remedy, which
// takes nothing an earlier attempt left, passes over that number, and its
// files go (begin; #337).
func TestARemedyPassingOverAKilledRenewalDropsWhatItLeft(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	b := e.mailbox()
	e.clock.Add(2 * time.Second)
	crash(t, e, b, "rekey", "prekey made", protocol.Address{})
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := x.Record(t, "renewal")
	left, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		t.Fatalf("the renewal under way: %q, %v", raw, err)
	}
	files := []string{prekeyFile(x, left, ".agekey"), workFile(x, left, ""), workFile(x, left, ".pub"), workFile(x, left, "-cert.pub")}
	for _, f := range files {
		if !exists(f) {
			t.Fatalf("the killed renewal left no %s", f)
		}
	}
	e.clock.Add(2 * time.Second)
	if r, err := x.RenewAndRevoke(context.Background()); err != nil || r == nil || r.Prekey == nil || r.Prekey.Serial <= left {
		t.Fatalf("the remedy: %+v, %v", r, err)
	}
	for _, f := range files {
		if exists(f) {
			t.Errorf("the remedy passed over %d and left %s", left, f)
		}
	}
}

// A renewal the server took, whose answer the mailbox never had, is recorded at
// the mailbox's next read of itself, which a pass refused as under a certificate
// not current sends it to; a later renewal the mailbox has begun keeps its
// number.
func TestARenewalTakenUnseenIsRecordedAndALaterOneKeepsItsNumber(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	b := e.mailbox()
	e.clock.Add(2 * time.Second)
	crash(t, e, b, "rekey", "renewal taken", protocol.Address{})
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	took := servedSerial(t, e, x)
	later := []byte(strconv.FormatUint(took+1, 10))
	x.SetRecord(t, "renewal", later)
	receive(t, x)
	if got, _ := x.Record(t, "renewal"); numberOf(t, x) != took || !bytes.Equal(got, later) {
		t.Fatalf("published %d, the renewal under way %q; want %d and %q", numberOf(t, x), got, took, later)
	}
}

// The mailbox's own statement served under another of its certificates, here
// signed again by the working key it used before, by whoever took that key,
// is no renewal the server took: the mailbox records it only once it is served
// under the certificate the mailbox made with it.
func TestItsOwnStatementUnderAnotherCertificateIsNoRenewalTaken(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	b := e.mailbox()
	stolen, first := worker(t, b), numberOf(t, b)
	e.clock.Add(2 * time.Second)
	crash(t, e, b, "rekey", "renewal taken", protocol.Address{})
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	took := servedSerial(t, e, x)
	st, ok := x.Record(t, "prekeys/"+strconv.FormatUint(took, 10)+".txt")
	if !ok {
		t.Fatalf("the mailbox keeps no statement %d", took)
	}
	sig, err := protocol.Sign(stolen, st, protocol.PrekeyNamespace(x.Address()))
	if err != nil {
		t.Fatal(err)
	}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodGet && r.URL.Path == mailboxPath(x) {
			fakeserver.WriteJSON(w, http.StatusOK, protocol.MailboxResponse{Identity: x.Identity().Document(), Prekey: st, PrekeySignature: sig})
			return
		}
		next.ServeHTTP(w, r)
	})
	if _, err := x.Receive(context.Background()); err == nil || numberOf(t, x) != first {
		t.Fatalf("served under certificate %d: recv %v, published %d", first, err, numberOf(t, x))
	}
	e.srv.SetMiddle(nil)
	if receive(t, x); numberOf(t, x) != took {
		t.Fatalf("served under its own certificate: published %d, want %d", numberOf(t, x), took)
	}
}

// The remedy killed at each of its steps: run again, it revokes and renews,
// the revocation covers the certificate in use before it, and the mailbox comes
// back whole.
func TestTheRemedyKilledAtEachStepLeavesTheMailboxWhole(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, step := range append(client.Steps("revocation"), client.Steps("renewal")...) {
		t.Run(step, func(t *testing.T) {
			ctx := context.Background()
			e := newWorkEnv(t)
			a, b := e.mailbox(), e.mailbox()
			first := numberOf(t, b)
			e.clock.Add(2 * time.Second)
			crash(t, e, b, "remedy", step, protocol.Address{})
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			r, err := x.RenewAndRevoke(ctx)
			if err != nil || r == nil || r.NotRevoked != nil || r.NotRenewed != nil {
				t.Fatalf("the remedy again: %+v, %v", r, err)
			}
			held, err := x.Ask(ctx, x.Address())
			if err != nil || held.Certificates == nil || !held.Certificates.Revokes(first) {
				t.Fatalf("after the remedy again: %+v, %v", held, err)
			}
			whole(t, e, x, a)
		})
	}
}

// A restore from the identity alone killed at each step of its renewal and
// of its adoption of its server's ACL: the next recv adopts it, each entry
// once, and the restored mailbox's first renewal finds the two alike, so the
// server's blocks stay (#366).
func TestARestoreKilledAtEachStepAdoptsItsServersACL(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, step := range append(renewalSteps(false), client.Steps("adoption")...) {
		t.Run(step, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			if _, err := b.Block(ctx, a.Address()); err != nil {
				t.Fatal(err)
			}
			own := e.srv.ACL(b.Address())
			r := restore(t, e, b)
			e.clock.Add(2 * time.Second)
			crash(t, e, r, "rekey", step, protocol.Address{})
			x, err := client.Open(r.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.Receive(ctx); err != nil {
				t.Fatalf("the recv after the crash: %v", err)
			}
			e.clock.Add(25 * time.Hour)
			if res := receive(t, x); res.Renewed == nil || res.ACLReplaced || !e.srv.ACL(b.Address()).SameLists(own) {
				t.Fatalf("the first renewal after a crash at %s: %+v; the server holds %+v, the lost mailbox's was %+v", step, res, e.srv.ACL(b.Address()), own)
			}
			if data, err := os.ReadFile(filepath.Join(x.Dir(), "blocked")); err != nil || string(data) != a.Address().String()+"\n" {
				t.Fatalf("blocked after a crash at %s: %q, %v", step, data, err)
			}
		})
	}
}

// A restore killed between its new state file and its folders, and between its
// folders and the record of them: the next command finishes, and only then
// does a missing folder stop one.
func TestARestoreKilledBeforeTheRecordOfItsFoldersFinishes(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("folders") {
		t.Run(step, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			identityAlone(t, b.Dir())
			e.clock.Add(2 * time.Second)
			crashIn(t, e, b.Dir(), "rekey", step, protocol.Address{})
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			// Part of what the crash left, or none of it: the next command makes it.
			if err := os.RemoveAll(x.Path("sent")); err != nil {
				t.Fatal(err)
			}
			if _, err := x.Rekey(ctx); err != nil {
				t.Fatalf("the renewal after a crash at %s: %v", step, err)
			}
			landAll(t, x)
			whole(t, e, x, a)
			defer moveAside(t, x.Path("sent"))()
			if _, err := x.Receive(ctx); err == nil || !strings.Contains(err.Error(), x.Path("sent")) {
				t.Fatalf("recv with sent/ moved aside after a crash at %s: %v", step, err)
			}
		})
	}
}

// A new state file killed once it has adopted the keys it found: the next command
// reads the adoption, and the mailbox is whole, and mail sealed to the adopted key
// is delivered.
func TestAnAdoptionKilledLeavesTheMailboxWhole(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("keys") {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, a, b, "sealed to the key the state file named")
			old := numberOf(t, b)
			if err := os.Remove(filepath.Join(b.Dir(), "state")); err != nil {
				t.Fatal(err)
			}
			e.clock.Add(2 * time.Second)
			crashIn(t, e, b.Dir(), "rekey", step, protocol.Address{})
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			landAll(t, x)
			if got := client.AdoptedKeys(t, x); !slices.Equal(got, []uint64{old}) {
				t.Fatalf("adopted %v after a crash, want %d", got, old)
			}
			if _, err := x.Rekey(t.Context()); err != nil {
				t.Fatal(err)
			}
			whole(t, e, x, a, "sealed to the key the state file named")
		})
	}
}

// A drain killed at each step of deleting a retired prekey: the next command does
// not read the marked record as a key lost, and the next prune, here a renewal's,
// finishes the deletion.
func TestARemovalKilledAtEachStepFinishesAtTheNextPrune(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("removal") {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			old := numberOf(t, b)
			e.clock.Add(2 * time.Second)
			if _, err := b.Rekey(t.Context()); err != nil {
				t.Fatal(err)
			}
			e.clock.Add(2 * time.Hour)
			crash(t, e, b, "receive", step, protocol.Address{})
			if !slices.Contains(client.Statements(t, b), old) {
				t.Fatalf("the crash at %s left no record of prekey %d", step, old)
			}
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			st, err := x.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(st.Attention, func(s string) bool { return strings.Contains(s, "is not at") }) {
				t.Fatalf("status after a crash at %s reads the key as lost: %q", step, st.Attention)
			}
			e.clock.Add(2 * time.Second)
			if _, err := x.Rekey(t.Context()); err != nil {
				t.Fatalf("the renewal after a crash at %s: %v", step, err)
			}
			if exists(prekeyFile(b, old, ".agekey")) || slices.Contains(client.Statements(t, x), old) {
				t.Fatalf("the prune after a crash at %s left prekey %d", step, old)
			}
			whole(t, e, x, a)
		})
	}
}

// A drain killed at each step of a delivery: the message is in the inbox
// once, and leaves the server.
func TestADrainKilledAtEachStepOfADeliveryDeliversOnce(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("delivery") {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, a, b, "waits for a drain that dies")
			crash(t, e, b, "receive", step, protocol.Address{})
			whole(t, e, b, a, "waits for a drain that dies")
		})
	}
}

// followOwed runs one drain of the loop with what it delivers owed, and appends
// the body of each message its handler is handed to the file handled.
func followOwed(ctx context.Context, b *client.Mailbox, handled string) error {
	return b.Follow(ctx, client.FollowOptions{Until: time.Now(), Owed: true}, func(_ context.Context, ev client.Event) error {
		if ev.Kind != client.EventDelivery {
			return nil
		}
		var body string
		if err := json.Unmarshal(ev.Delivery.Envelope.Body, &body); err != nil {
			return err
		}
		f, err := os.OpenFile(handled, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the test's own file
		if err != nil {
			return err
		}
		defer f.Close() //nolint:errcheck // written below, and a test's
		_, err = f.WriteString(body + "\n")
		return err
	})
}

// A loop killed at each step of keeping a message or of handling it: the next
// loop hands over what is owed first, then what the server still holds, so every
// message reaches its handler at least once and inbox/ keeps each once. Only a
// kill after the handler returned, before its record is cleared, runs a handler
// twice. Nothing is owed after, and the mailbox is whole.
func TestALoopKilledAtEachStepHandsEveryMessageOverAtLeastOnce(t *testing.T) {
	t.Parallel()
	for _, step := range append(slices.Clone(client.Steps("delivery")), client.Steps("owed")...) {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, a, b, "first")
			send(t, a, b, "second")
			crash(t, e, b, "follow", step, protocol.Address{})
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			handled := b.Dir() + ".handled"
			if err := followOwed(context.Background(), x, handled); err != nil {
				t.Fatalf("the loop after the crash: %v", err)
			}
			raw, err := os.ReadFile(handled)
			if err != nil {
				t.Fatal(err)
			}
			count := map[string]int{}
			for l := range strings.Lines(string(raw)) {
				count[strings.TrimSpace(l)]++
			}
			twice := 0
			if step == "handler returned" {
				twice = 1
			}
			if count["first"]+count["second"] != 2+twice || count["first"] < 1 || count["second"] < 1 {
				t.Fatalf("handled %v after a kill at %s, want each once and %d twice", count, step, twice)
			}
			if st, err := x.Status(context.Background()); err != nil || st.Owed != 0 {
				t.Fatalf("still owed: %+v, %v", st, err)
			}
			whole(t, e, x, a, "first", "second")
		})
	}
}

// A drain killed at each step of a refusal: the next drain refuses the
// message again, keeps it once with why, and acks it.
func TestADrainKilledAtEachStepOfARefusalKeepsItOnce(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("refusal") {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			messageAs(t, e, a, b, worker(t, b), "signed by another mailbox's key")
			crash(t, e, b, "receive", step, protocol.Address{})
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.Receive(context.Background()); err != nil {
				t.Fatal(err)
			}
			qs, err := x.RefusedMessages(t.Context())
			if err != nil || len(qs) != 1 || qs[0].Code != protocol.RefusedSignature || e.srv.Count(x.Address()) != 0 {
				t.Fatalf("after the crash: %+v, %v; the server holds %d", qs, err, e.srv.Count(x.Address()))
			}
			whole(t, e, x, a)
		})
	}
}

// A drain killed at each step of a fetch's partial: once a cut fetch has
// recorded what it kept, and once a judged message's partial is dropped,
// before its ack. The next drain delivers the message once, asking only for
// the bytes the mailbox did not keep, so the server serves each byte of it once
// in all; and the mailbox keeps no partial after (#381).
func TestADrainKilledAtEachStepOfAPartialDeliversOnce(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("partial") {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			const body = "fetched by a drain that dies. "
			send(t, a, b, strings.Repeat(body, 2000))
			f := &fetches{b: b}
			var size atomic.Int64
			var cut atomic.Bool
			cut.Store(step == "partial kept")
			e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if !f.is(r) {
					next.ServeHTTP(w, r)
					return
				}
				if cut.CompareAndSwap(true, false) {
					ct := honest(next, r).Body.Bytes()
					size.Store(int64(len(ct)))
					cutAfter(f, w, ct, len(ct)/2)
				}
				next.ServeHTTP(f.counted(w), r)
			})
			crash(t, e, b, "receive", step, protocol.Address{})
			before := f.served.Load()
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			res := receive(t, x)
			if step == "partial kept" {
				if len(res.Delivered) != 1 || f.served.Load() != size.Load() {
					t.Fatalf("after the crash: %+v; the server served %d bytes of %d", res, f.served.Load(), size.Load())
				}
			} else if len(res.Delivered) != 0 || f.served.Load() != before || e.srv.Count(x.Address()) != 0 {
				t.Fatalf("after the crash: %+v, a message the dead drain delivered; the server served %d bytes more, and holds %d", res, f.served.Load()-before, e.srv.Count(x.Address()))
			}
			noPartial(t, x)
			e.srv.SetMiddle(nil)
			whole(t, e, x, a, strings.Repeat(body, 2000))
		})
	}
}

// An allow killed at each of its steps loses nothing: each message from the
// sender is in held/ or inbox/, or both. The next allow finishes the move: each
// is in inbox/ once and out of held/, and another sender's message stays held.
func TestAnAllowKilledAtEachStepFinishesWhenRunAgain(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("allow") {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, other, b := e.heldMailbox(), e.heldMailbox(), e.heldMailbox()
			send(t, a, b, "first")
			send(t, a, b, "second")
			send(t, other, b, "from another sender")
			if res := receive(t, b); len(res.Held) != 3 {
				t.Fatalf("the drain before the crash: %+v", res)
			}
			crash(t, e, b, "allow", step, a.Address())
			kept := append(folder(t, b, "held"), folder(t, b, "inbox")...)
			slices.Sort(kept)
			if got, want := slices.Compact(kept), []string{"first", "from another sender", "second"}; !slices.Equal(got, want) {
				t.Fatalf("after the crash, held/ and inbox/ hold %q, want %q", got, want)
			}
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.Allow(context.Background(), a.Address()); err != nil {
				t.Fatalf("the allow after the crash: %v", err)
			}
			if got, want := folder(t, x, "held"), []string{"from another sender"}; !slices.Equal(got, want) {
				t.Fatalf("held/ holds %q, want %q", got, want)
			}
			whole(t, e, x, a, "first", "second")
		})
	}
}

// A send killed at each of its steps: the next flush posts what waits, its
// reader takes it once, and the outbox and sent/ are as a send leaves them.
func TestASendKilledAtEachStepIsReadOnce(t *testing.T) {
	t.Parallel()
	for _, step := range client.Steps("post") {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			crash(t, e, a, "send", step, b.Address())
			x, err := client.Open(a.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := x.Flush(context.Background()); err != nil {
				t.Fatalf("the flush after the crash: %v", err)
			}
			receive(t, b)
			if got, want := inbox(t, b), []string{"sent by a process killed on its way"}; !slices.Equal(got, want) {
				t.Fatalf("the reader's inbox/ holds %q, want %q", got, want)
			}
			if left, _ := filepath.Glob(filepath.Join(x.Dir(), "outbox", "*.P")); len(left) != 0 {
				t.Fatalf("the outbox keeps %v", left)
			}
			if sent, _ := filepath.Glob(filepath.Join(x.Dir(), "mail", "sent", "*.json")); len(sent) != 1 {
				t.Fatalf("sent/ keeps %v", sent)
			}
			whole(t, e, b, x, "sent by a process killed on its way")
		})
	}
}

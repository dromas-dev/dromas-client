package client_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
)

// layoutIn is a layout with every part somewhere of its own under root.
func layoutIn(root string) client.Layout {
	return client.Layout{
		Mail:         filepath.Join(root, "spool", "mail"),
		IdentityFile: filepath.Join(root, "secret", "identity"),
		RotatingKeys: filepath.Join(root, "excluded", "keys"),
		Inbox:        filepath.Join(root, "agent", "inbox"),
	}
}

// Each part of a mailbox goes where its layout says, and the whole of it works
// there: registered, written to, drained, a held sender allowed across directories,
// and opened again. Nothing of it lands in the base directory but what is the base
// directory's.
func TestAMailboxRunsWithEachPartWhereItsLayoutSays(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	root := t.TempDir()
	dir := filepath.Join(root, "base")
	opt := e.opts()
	opt.Layout = layoutIn(root)
	b, err := client.Init(t.Context(), dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	a := e.heldMailbox()
	send(t, a, b, "waits")
	if res := receive(t, b); len(res.Held) != 1 {
		t.Fatalf("%+v", res)
	}
	if _, err := b.Allow(t.Context(), a.Address()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"spool/mail/held", "spool/mail/refused", "spool/mail/sent", "secret/identity", "secret/identity.pub", "excluded/keys/prekeys", "excluded/keys/work", "agent/inbox", "base/state", "base/identity.doc", "base/address", "base/outbox"} {
		if !exists(filepath.Join(root, filepath.FromSlash(want))) {
			t.Errorf("%s is missing", want)
		}
	}
	for _, not := range []string{"inbox", "held", "refused", "sent", "prekeys", "work", "keys", "mail", "identity"} {
		if exists(filepath.Join(dir, not)) {
			t.Errorf("base-dir holds %s", not)
		}
	}
	if n, _ := filepath.Glob(filepath.Join(root, "agent", "inbox", "*.json")); len(n) != 1 {
		t.Errorf("inbox-dir holds %v", n)
	}
	if n, _ := filepath.Glob(filepath.Join(root, "spool", "mail", "held", "*.json")); len(n) != 0 {
		t.Errorf("held/ still holds %v", n)
	}
	again, err := client.Open(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	if again.Address() != b.Address() {
		t.Fatal("opened again, it has another address")
	}
	st, err := again.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Inbox != 1 || st.Paths["inbox"] != opt.Layout.Inbox || st.Paths["mail"] != opt.Layout.Mail || st.Paths["identity"] != opt.Layout.IdentityFile || st.Paths["rotating_keys"] != opt.Layout.RotatingKeys {
		t.Errorf("status: %+v", st)
	}
}

// An identity on a read-only mount is read and never written: what is derived
// from it is the base directory's.
func TestAnIdentityInADirectoryNothingCanWriteToWorks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	root := t.TempDir()
	key, err := os.ReadFile(filepath.Join(e.mailbox().Dir(), "identity"))
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret")
	if err := os.Mkdir(secret, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secret, "identity"), key, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0o500); err != nil { //nolint:gosec // read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o700) }) //nolint:gosec // for its removal
	opt := e.opts()
	opt.Layout.IdentityFile = filepath.Join(secret, "identity")
	b, err := client.Open(filepath.Join(root, "base"), opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Two parts in one place, or one inside another it may not be in, are refused, by
// where they really are: a symlink hides neither. Each may be in the base directory,
// and the mail's folders in the mail's, and those are taken.
func TestALayoutWhosePartsOverlapIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	base := filepath.Join(root, "base")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := os.Symlink(base, link) == nil // Windows may not let this user make one
	file := filepath.Join(root, "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mail := filepath.Join(root, "mail")
	keys := filepath.Join(root, "keys")
	if linked {
		if err := client.CheckLayout(base, client.Layout{Mail: link}); err == nil {
			t.Error("mail is base by a link was taken")
		}
	}
	for name, l := range map[string]client.Layout{
		"mail is base":             {Mail: base},
		"keys is mail":             {Mail: mail, RotatingKeys: mail},
		"keys inside mail":         {Mail: mail, RotatingKeys: filepath.Join(mail, "keys")},
		"mail inside keys":         {RotatingKeys: keys, Mail: filepath.Join(keys, "mail")},
		"inbox inside keys":        {RotatingKeys: keys, Mail: mail, Inbox: filepath.Join(keys, "inbox")},
		"inbox is held":            {Mail: mail, Inbox: filepath.Join(mail, "held")},
		"identity inside mail":     {Mail: mail, IdentityFile: filepath.Join(mail, "identity")},
		"identity inside inbox":    {Mail: mail, Inbox: filepath.Join(root, "inbox"), IdentityFile: filepath.Join(root, "inbox", "identity")},
		"identity inside keys":     {RotatingKeys: keys, IdentityFile: filepath.Join(keys, "identity")},
		"mail is a file":           {Mail: file},
		"identity is a directory":  {IdentityFile: base},
		"base inside mail":         {Mail: root},
		"base inside rotated keys": {RotatingKeys: root},
	} {
		if err := client.CheckLayout(base, l); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	for name, l := range map[string]client.Layout{
		"the defaults":      {},
		"each its own":      layoutIn(root),
		"under the base":    {Mail: filepath.Join(base, "m"), RotatingKeys: filepath.Join(base, "k")},
		"inbox in the mail": {Mail: mail, Inbox: filepath.Join(mail, "elsewhere")},
	} {
		if err := client.CheckLayout(base, l); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A state file belongs to one address: pointing the identity at another key, or
// the base directory at another mailbox, is refused by every command and reading.
func TestAStateFileOfAnotherIdentityIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b, other := e.mailbox(), e.mailbox()
	opt := e.opts()
	opt.Layout.IdentityFile = filepath.Join(other.Dir(), "identity")
	wrong, err := client.Open(b.Dir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Receive(t.Context()); err == nil || !strings.Contains(err.Error(), "belongs to the address") {
		t.Errorf("recv: %v", err)
	}
	if _, err := wrong.Status(t.Context()); err == nil || !strings.Contains(err.Error(), "belongs to the address") {
		t.Errorf("status: %v", err)
	}
}

// A published prekey that is not where the layout says stops recv ahead of its
// renewal, which would hide it while mail sealed to the missing key was refused:
// nothing is acked, status says so, and prekey renew is the deliberate way out.
func TestAPublishedPrekeyMissingFromItsDirectoryStopsRecv(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	send(t, a, b, "waits")
	opt := e.opts()
	opt.Layout.RotatingKeys = filepath.Join(t.TempDir(), "keys")
	moved, err := client.Open(b.Dir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	// Due to renew, where a recv would publish a new prekey and go on.
	e.clock.Add(25 * time.Hour)
	n := numberOf(t, b)
	if _, err := moved.Receive(t.Context()); err == nil || !strings.Contains(err.Error(), "is not at") {
		t.Fatalf("recv: %v", err)
	}
	if numberOf(t, b) != n {
		t.Error("recv renewed over the gap")
	}
	if e.srv.Count(b.Address()) != 1 {
		t.Error("a message was acked")
	}
	st, err := moved.Status(t.Context())
	if err != nil || !slices.ContainsFunc(st.Attention, func(s string) bool { return strings.Contains(s, "dromas prekey renew") }) {
		t.Errorf("status: %+v, %v", st, err)
	}
	if _, err := moved.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := moved.Receive(t.Context()); err != nil {
		t.Errorf("recv after a renewal: %v", err)
	}
}

// What its owner set stands over what the state file recorded, field by field.
func TestAPolicyItsOwnerSetStandsOverOneRecorded(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	if err := b.SetPolicy(t.Context(), client.Policy{Unknown: client.LandInbox, Server: client.AdmitAllowed}); err != nil {
		t.Fatal(err)
	}
	opt := e.opts()
	opt.Policy.Server = client.AdmitAll
	set, err := client.Open(b.Dir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := set.Policy(t.Context()); err != nil || p.Server != client.AdmitAll || p.Unknown != client.LandInbox {
		t.Errorf("%+v, %v", p, err)
	}
	opt.Policy.Server = "everyone"
	if _, err := client.Open(b.Dir(), opt); err == nil {
		t.Error("a policy that is none was taken")
	}
}

// A private key under a .git is said, and nothing is written into the repository.
func TestAKeyInAGitWorkTreeIsSaid(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, "box")
	b, err := client.Init(t.Context(), dir, e.opts())
	if err != nil {
		t.Fatal(err)
	}
	st, err := b.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	said := strings.Join(st.Attention, "\n")
	if !strings.Contains(said, "identity-file") || !strings.Contains(said, "rotating-keys-dir") || !strings.Contains(said, repo) {
		t.Errorf("status: %q", said)
	}
	if exists(filepath.Join(dir, ".gitignore")) || exists(filepath.Join(repo, ".gitignore")) {
		t.Error("a .gitignore was written")
	}
	outside, err := client.Init(t.Context(), filepath.Join(t.TempDir(), "box"), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	if said := outside.KeysInGit(); len(said) != 0 {
		t.Errorf("outside any work tree: %q", said)
	}
}

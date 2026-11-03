package client_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
		"mail is base":              {Mail: base},
		"keys is mail":              {Mail: mail, RotatingKeys: mail},
		"keys inside mail":          {Mail: mail, RotatingKeys: filepath.Join(mail, "keys")},
		"mail inside keys":          {RotatingKeys: keys, Mail: filepath.Join(keys, "mail")},
		"inbox inside keys":         {RotatingKeys: keys, Mail: mail, Inbox: filepath.Join(keys, "inbox")},
		"inbox is held":             {Mail: mail, Inbox: filepath.Join(mail, "held")},
		"identity inside mail":      {Mail: mail, IdentityFile: filepath.Join(mail, "identity")},
		"identity inside inbox":     {Mail: mail, Inbox: filepath.Join(root, "inbox"), IdentityFile: filepath.Join(root, "inbox", "identity")},
		"identity inside keys":      {RotatingKeys: keys, IdentityFile: filepath.Join(keys, "identity")},
		"mail is a file":            {Mail: file},
		"identity is a directory":   {IdentityFile: base},
		"base inside mail":          {Mail: root},
		"base inside rotated keys":  {RotatingKeys: root},
		"identity is the address":   {IdentityFile: filepath.Join(base, "address")},
		"identity is the document":  {IdentityFile: filepath.Join(base, "identity.doc")},
		"identity is the state":     {IdentityFile: filepath.Join(base, "state")},
		"identity is allowed":       {IdentityFile: filepath.Join(base, "allowed")},
		"identity is the ticket":    {IdentityFile: filepath.Join(base, "keys", "tls-ticket")},
		"inbox is the ticket":       {Inbox: filepath.Join(base, "keys", "tls-ticket")},
		"mail inside outbox":        {Mail: filepath.Join(base, "outbox", "m")},
		"keys inside outbox":        {RotatingKeys: filepath.Join(base, "outbox", "k")},
		"keys are partial":          {RotatingKeys: filepath.Join(base, "partial")},
		"inbox is the state":        {Inbox: filepath.Join(base, "state")},
		"mail is the identity .pub": {IdentityFile: filepath.Join(root, "id"), Mail: filepath.Join(root, "id.pub")},
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
	// The state file alone says whose it is, where the address has gone.
	if err := os.Remove(filepath.Join(b.Dir(), "address")); err != nil {
		t.Fatal(err)
	}
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
	for _, f := range []string{"prekeys", "work"} {
		if err := os.MkdirAll(moved.Path(f), 0o700); err != nil {
			t.Fatal(err)
		}
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

// Every file a mailbox keeps in its base directory is a place CheckLayout holds
// apart from what a config names, or a config could name it and be written over.
func TestEveryFileOfTheBaseIsAPlaceOfTheLayout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "so that each folder and list is made")
	receive(t, b)
	if _, err := b.Allow(t.Context(), a.Address()); err != nil {
		t.Fatal(err)
	}
	parts := client.LayoutPartPaths(b.Dir(), client.Layout{})
	es, err := os.ReadDir(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range es {
		if !slices.Contains(parts, filepath.Join(b.Dir(), f.Name())) {
			t.Errorf("%s is no place of the layout", f.Name())
		}
	}
}

// Two names of one file are one place, by os.SameFile and not by how they read:
// a hard link, and a filesystem that ignores case.
func TestTwoNamesOfOnePlaceAreOne(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	base := filepath.Join(root, "base")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	address := filepath.Join(base, "address")
	if err := os.WriteFile(address, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Link(address, link); err != nil {
		t.Fatal(err)
	}
	if err := client.CheckLayout(base, client.Layout{IdentityFile: link}); err == nil {
		t.Error("the address's other name was taken as the identity")
	}
}

// A fault of the disk says nothing of a layout, so CheckLayout gives it as a
// LayoutFault, which names the part it met the fault on. It runs alone, since
// it sets where every read of a path goes.
func TestAFaultOfTheDiskIsNoWrongLayout(t *testing.T) {
	for _, fault := range []error{syscall.EIO, syscall.ESTALE, syscall.ETIMEDOUT, syscall.ENOTCONN} {
		client.SetPathFault(t, func() error { return &fs.PathError{Op: "lstat", Path: "held", Err: fault} })
		err := client.CheckLayout(t.TempDir(), client.Layout{})
		var f *client.LayoutFault
		if !errors.As(err, &f) || !errors.Is(err, fault) || f.Part == "" {
			t.Errorf("%v: %#v", fault, err)
		}
	}
}

// A path that is itself wrong is a wrong layout whatever the disk does, and so is
// one its user may not read along: neither is a fault of the disk. Some of these
// are places that can be on some systems, so only that it is no fault is held.
func TestAPathThatIsWrongIsNoFaultOfTheDisk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	base := filepath.Join(root, "base")
	file := filepath.Join(root, "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	shut := filepath.Join(root, "shut")
	if err := os.Mkdir(shut, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(shut, 0o700) }) //nolint:gosec // back to what a temporary directory's removal needs
	paths := map[string]string{
		"through a file":         filepath.Join(file, "mail"),
		"a name too long":        filepath.Join(root, strings.Repeat("n", 300)),
		"a NUL":                  filepath.Join(root, "a\x00b"),
		"a name Windows refuses": filepath.Join(root, "a<b"),
		"a directory it may not": filepath.Join(shut, "mail"),
	}
	loop := filepath.Join(root, "loop")
	if os.Symlink(loop+"-too", loop) == nil && os.Symlink(loop, loop+"-too") == nil { // Windows may not let this user make one
		paths["links that loop"] = loop
	}
	for name, path := range paths {
		var f *client.LayoutFault
		if err := client.CheckLayout(base, client.Layout{Mail: path}); errors.As(err, &f) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Open holds the default layout's parts apart as it holds any layout's: a link
// inside the base directory makes the mail and the rotating keys one place, and
// Open refuses that before it reads the identity. A link to a place of its own
// is the owner's to make, and Open takes it.
func TestOpenRefusesADefaultLayoutWhosePartsALinkMakesOne(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	base := e.mailbox().Dir()
	mail := filepath.Join(base, "mail")
	if err := os.RemoveAll(mail); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "keys"), mail); err != nil {
		t.Skipf("this user may not make a symlink, as on Windows: %v", err)
	}
	if _, err := client.Open(base, e.opts()); err == nil || !strings.Contains(err.Error(), "mail-dir and rotating-keys-dir are one place") {
		t.Errorf("mail is keys by a link: %v", err)
	}
	if err := os.Remove(mail); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), mail); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Open(base, e.opts()); err != nil {
		t.Errorf("mail elsewhere by a link: %v", err)
	}
}

// Where a filesystem ignores case, two parts named apart are one place once they
// exist: init refuses that before it writes a key.
func TestAnInitWhosePartsAreOneOnThisFilesystemWritesNoKey(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	root := t.TempDir()
	probe := filepath.Join(root, "Probe")
	if err := os.Mkdir(probe, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "probe")); err != nil {
		return // this filesystem holds them apart
	}
	opt := e.opts()
	opt.Layout = client.Layout{Mail: filepath.Join(root, "MAIL"), RotatingKeys: filepath.Join(root, "mail"), IdentityFile: filepath.Join(root, "id", "identity")}
	if _, err := client.Init(t.Context(), filepath.Join(root, "base"), opt); err == nil {
		t.Fatal("init took two names of one place")
	}
	if _, err := os.Stat(filepath.Join(root, "id", "identity")); err == nil {
		t.Error("init wrote a key before it refused")
	}
}

// A mistyped identity-file on a mailbox stops every command, addr among them,
// and neither rewrites its address nor leaves a key at the typo.
func TestAnIdentityFileOnAnotherMailboxChangesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b, other := e.mailbox(), e.mailbox()
	address := filepath.Join(b.Dir(), "address")
	before, err := os.ReadFile(address)
	if err != nil {
		t.Fatal(err)
	}
	opt := e.opts()
	opt.Layout.IdentityFile = filepath.Join(other.Dir(), "identity")
	if _, err := client.Open(b.Dir(), opt); err == nil || !strings.Contains(err.Error(), "belongs to the address") {
		t.Errorf("open: %v", err)
	}
	if after, _ := os.ReadFile(address); !bytes.Equal(before, after) {
		t.Error("a refused open rewrote the address")
	}
	typo := filepath.Join(t.TempDir(), "typo")
	opt.Layout.IdentityFile = typo
	if _, err := client.Init(t.Context(), b.Dir(), opt); err == nil || !strings.Contains(err.Error(), "belongs to the address") {
		t.Errorf("init: %v", err)
	}
	if _, err := os.Stat(typo); err == nil {
		t.Error("a refused init left a key at the typo")
	}
}

// What its owner set of the policy stands over a recorded one, each field alone.
func TestAPolicyItsOwnerSetStandsOverOneRecordedByUnknownToo(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	if err := b.SetPolicy(t.Context(), client.Policy{Unknown: client.LandInbox, Server: client.AdmitAllowed}); err != nil {
		t.Fatal(err)
	}
	opt := e.opts()
	opt.Policy.Unknown = client.LandHeld
	set, err := client.Open(b.Dir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := set.Policy(t.Context()); err != nil || p.Unknown != client.LandHeld || p.Server != client.AdmitAllowed {
		t.Errorf("%+v, %v", p, err)
	}
}

// A rotating keys' directory at a work tree's own root is in it.
func TestRotatingKeysAtAWorkTreesRootAreSaid(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	opt := e.opts()
	opt.Layout.RotatingKeys = repo
	b, err := client.Init(t.Context(), filepath.Join(t.TempDir(), "box"), opt)
	if err != nil {
		t.Fatal(err)
	}
	if said := b.KeysInGit(); len(said) != 1 || !strings.Contains(said[0], "rotating-keys-dir") {
		t.Errorf("%v", said)
	}
}

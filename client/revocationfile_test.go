package client_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
)

// recordLine is the line dromas revocation make prints of b: its revocation
// record, and a line feed.
func recordLine(t *testing.T, b *client.Mailbox) []byte {
	t.Helper()
	rec, err := b.Revocation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return append(line, '\n')
}

// Init writes the mailbox's revocation record to the new file its owner names:
// the line Revocation gives, which a server takes and which verifies, in a file
// its owner's alone. On a registered mailbox, Init writes the record and
// changes nothing else.
func TestInitWritesTheRevocationRecordWhereItsOwnerSays(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	dir := t.TempDir()
	opt := e.opts()
	opt.RevocationFile = filepath.Join(t.TempDir(), "record.json")
	b, err := client.Init(t.Context(), dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(opt.RevocationFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := recordLine(t, b); !bytes.Equal(got, want) {
		t.Fatalf("the file holds %q, and revocation make prints %q", got, want)
	}
	var r protocol.Revocations
	if err := protocol.UnmarshalRequest(got, &r); err != nil {
		t.Fatalf("a server would refuse the record: %v", err)
	}
	if _, err := protocol.VerifyRevocation(b.Address(), r.Identity, r.Revocation); err != nil {
		t.Fatalf("the record does not verify: %v", err)
	}
	if err := client.CheckPrivateFile(opt.RevocationFile); err != nil {
		t.Error(err)
	}
	n, calls := numberOf(t, b), len(e.srv.Requests())
	again := opt
	again.RevocationFile = filepath.Join(t.TempDir(), "again.json")
	c, err := client.Init(t.Context(), dir, again)
	if err != nil {
		t.Fatal(err)
	}
	if c.Address() != b.Address() || numberOf(t, c) != n {
		t.Error("init again changed the mailbox")
	}
	if got, err := os.ReadFile(again.RevocationFile); err != nil || !bytes.Equal(got, recordLine(t, b)) {
		t.Errorf("init again wrote %q, %v", got, err)
	}
	if more := e.srv.Requests()[calls:]; len(more) != 0 {
		t.Errorf("init again called the server: %+v", more)
	}
}

// Init refuses a revocation file inside a directory of the mailbox, or at one,
// before it makes anything: no directory, no key, no call to its server. A path
// through a link that leads into one is refused as the place the link leads to.
// The directory that holds every part of the mailbox is none of them.
func TestARevocationFileInsideTheMailboxIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	root := t.TempDir()
	dir := filepath.Join(root, "base")
	opt := e.opts()
	opt.Layout = layoutIn(root)
	refused := func(what, record string) {
		t.Helper()
		o := opt
		o.RevocationFile = record
		if _, err := client.Init(t.Context(), dir, o); err == nil || !strings.Contains(err.Error(), "is inside") {
			t.Errorf("%s: %v", what, err)
		}
		if exists(record) || exists(opt.Layout.IdentityFile) || len(e.srv.Requests()) != 0 {
			t.Errorf("%s: init made something before it refused", what)
		}
	}
	refused("the base directory itself", dir)
	if _, err := os.Lstat(dir); err == nil {
		t.Error("init made the base directory before it refused")
	}
	dirs := client.RevocationFileDirs(dir, opt.Layout)
	if !strings.Contains(strings.Join(dirs, "\n"), filepath.Dir(opt.Layout.IdentityFile)) {
		t.Fatalf("the identity file's directory is not among %q", dirs)
	}
	for i, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		refused(d, filepath.Join(d, "record.json"))
		link := filepath.Join(root, "link"+strconv.Itoa(i))
		if err := os.Symlink(d, link); err != nil {
			t.Logf("no link: %v", err) // Windows may not let this user make one
			continue
		}
		refused("a link to "+d, filepath.Join(link, "record.json"))
	}
	o := opt
	o.RevocationFile = filepath.Join(root, "record.json")
	if _, err := client.Init(t.Context(), dir, o); err != nil {
		t.Fatalf("a record beside the mailbox's directories: %v", err)
	}
}

// Init refuses a revocation file where anything is, a file or a link that
// leads nowhere, before it makes anything, and replaces nothing. A file made
// there while Init runs is refused at the write, which is an exclusive create:
// the mailbox is registered and no record written, as an init that stops once
// its server took its registration, and the next init writes the record.
func TestARevocationFileThatIsThereIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	root := t.TempDir()
	there := filepath.Join(root, "there")
	if err := os.WriteFile(there, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	places := []string{there}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "nowhere"), dangling); err == nil {
		places = append(places, dangling)
	} else {
		t.Logf("no link: %v", err) // Windows may not let this user make one
	}
	for _, p := range places {
		dir := filepath.Join(t.TempDir(), "mailbox")
		opt := e.opts()
		opt.RevocationFile = p
		if _, err := client.Init(t.Context(), dir, opt); err == nil || !strings.Contains(err.Error(), "is there already") {
			t.Errorf("%s: %v", p, err)
		}
		if _, err := os.Lstat(dir); err == nil || len(e.srv.Requests()) != 0 {
			t.Errorf("%s: init made something before it refused", p)
		}
	}
	if got, err := os.ReadFile(there); err != nil || string(got) != "kept" {
		t.Errorf("the file there holds %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "nowhere")); err == nil {
		t.Error("init wrote where the link leads")
	}

	late := filepath.Join(root, "late")
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPut && !exists(late) {
			if err := os.WriteFile(late, []byte("someone else's"), 0o600); err != nil {
				t.Error(err)
			}
		}
		next.ServeHTTP(w, r)
	})
	dir := t.TempDir()
	opt := e.opts()
	opt.RevocationFile = late
	if _, err := client.Init(t.Context(), dir, opt); err == nil || !strings.Contains(err.Error(), "is there already") {
		t.Fatalf("a file made while init ran: %v", err)
	}
	if got, err := os.ReadFile(late); err != nil || string(got) != "someone else's" {
		t.Errorf("init replaced a file made while it ran: %q, %v", got, err)
	}
	e.srv.SetMiddle(nil)
	b, err := client.Open(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	numberOf(t, b) // registered
	opt.RevocationFile = filepath.Join(root, "record.json")
	if _, err := client.Init(t.Context(), dir, opt); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(opt.RevocationFile); err != nil || !bytes.Equal(got, recordLine(t, b)) {
		t.Errorf("the next init wrote %q, %v", got, err)
	}
}

// Init refuses a revocation file whose directory is not there, or is a file,
// before it makes anything: it makes no directory of its owner's.
func TestARevocationFileWhoseDirectoryIsNotThereIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	root := t.TempDir()
	file := filepath.Join(root, "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, "missing", "record.json"), filepath.Join(file, "record.json")} {
		dir := filepath.Join(t.TempDir(), "mailbox")
		opt := e.opts()
		opt.RevocationFile = p
		if _, err := client.Init(t.Context(), dir, opt); err == nil || !strings.Contains(err.Error(), "no directory there") {
			t.Errorf("%s: %v", p, err)
		}
		if _, err := os.Lstat(dir); err == nil || len(e.srv.Requests()) != 0 {
			t.Errorf("%s: init made something before it refused", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "missing")); err == nil {
		t.Error("init made the record's directory")
	}
}

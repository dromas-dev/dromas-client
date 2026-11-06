package client

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

// Layout says where the parts of a mailbox are that need not be under its
// base directory, the dir given to [Open] and [Init]. Each is a path, and an
// empty one takes its default. Everything else of the mailbox stays in the
// base directory, which is never split: the gate is a flock on it, the state
// file is made by a hard link in it, and the state file tracks partial's
// synced bytes.
type Layout struct {
	// Mail holds inbox/, held/, refused/ and sent/: <dir>/mail if empty.
	Mail string
	// IdentityFile is the identity key, with <file>.pub beside it:
	// <dir>/identity if empty. Only Init writes it, so it may be read-only.
	IdentityFile string
	// RotatingKeys holds prekeys/, work/ and tls-ticket, which are never backed
	// up: <dir>/keys if empty.
	RotatingKeys string
	// Inbox is inbox/, apart from the rest of the mail: <Mail>/inbox if empty.
	Inbox string
}

// resolved is a Layout with every default taken, with the base directory.
func (l Layout) resolved(dir string) Layout {
	if l.Mail == "" {
		l.Mail = filepath.Join(dir, "mail")
	}
	if l.IdentityFile == "" {
		l.IdentityFile = filepath.Join(dir, "identity")
	}
	if l.RotatingKeys == "" {
		l.RotatingKeys = filepath.Join(dir, "keys")
	}
	if l.Inbox == "" {
		l.Inbox = filepath.Join(l.Mail, "inbox")
	}
	return l
}

// Path is where a file of the mailbox is, by its names in client/doc.go: its
// first name says which part, as "sent" and a file in it.
func (b *Mailbox) Path(elem ...string) string { return b.path(elem...) }

// path is where a part of the mailbox is: elem's first name says which part,
// and anything else is in the base directory.
func (b *Mailbox) path(elem ...string) string {
	root := b.dir
	if len(elem) > 0 {
		switch elem[0] {
		case "identity":
			return filepath.Join(append([]string{b.layout.IdentityFile}, elem[1:]...)...)
		case "identity.pub":
			return b.layout.IdentityFile + ".pub"
		case "prekeys", "work", ticketFile:
			root = b.layout.RotatingKeys
		case "inbox":
			root, elem = b.layout.Inbox, elem[1:]
		case "held", "refused", "sent":
			root = b.layout.Mail
		}
	}
	return filepath.Join(append([]string{root}, elem...)...)
}

// layoutPart is one place in a layout, and what it may be inside of: a part
// may be inside the base directory, prekeys/ and work/ inside the rotating
// keys' directory, and the mail's folders inside the mail's, and no other
// part inside another.
type layoutPart struct {
	name, path string
	file       bool
	inside     []string
}

// baseFiles are the base directory's own places, by client/doc.go's list. A path
// the Layout names on one of these would have the client write over it: the
// address, over the file it took the key from.
var baseFiles = []string{"identity.doc", "address", "state", "gate", "partial", "allowed", "blocked", "aliases"}

// publicFiles are the baseFiles that hold nothing private, so nothing judges who
// can reach them: the identity document and the address, which every sender reads,
// and which loadIdentity writes again from the key. identity.pub, beside the
// identity, is public too. Every other file of baseFiles is judged (judgeFiles).
var publicFiles = []string{"identity.doc", "address"}

// keptDirs are the directories, by client/doc.go's names, that keep messages
// recv passed and that inbox list, inbox show and peer allow print or move as
// delivered: each is judged at every open (judgeFiles), and each of its files
// before it is read (judgeKept).
var keptDirs = []string{"inbox", "held"}

// fileRefused is a file of the mailbox judged (judgeFiles): the refusal is the
// file's, whichever command met it.
type fileRefused struct{ error }

func (e *fileRefused) Unwrap() error { return e.error }

// judgeFiles refuses a mailbox with a private file of the base directory that
// others can reach or another user owns (checkPrivateFile), as a private key is
// refused: state, which holds the pass, gate, partial and the lists. It refuses
// inbox-dir and held/ where others can write them or another user owns them
// (checkKeptDir), since whoever can write there can put in a message that reads
// as delivered (#717). A file or directory that is not there is not judged. It
// costs a stat or an access-list read for each, and openState asks it before
// anything opens the state file or the gate, so a file planted before init and
// one left open by another system's tool are refused before bbolt or a lock
// opens them. Options.UnsafeStatePermissions leaves the files unjudged, and
// Options.UnsafeInboxPermissions the directories.
func (b *Mailbox) judgeFiles() error {
	if b.dir == "" {
		return nil
	}
	for _, f := range baseFiles {
		if b.unsafeState || slices.Contains(publicFiles, f) {
			continue
		}
		if err := checkPrivateFile(b.path(f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return &fileRefused{err}
		}
	}
	for _, d := range keptDirs {
		if b.unsafeInbox {
			break
		}
		if err := checkKeptDir(b.path(d)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return &fileRefused{err}
		}
	}
	return nil
}

// judgeKept refuses a file inbox-dir or held/ keeps that others can reach or
// another user owns (checkPrivateFile), before it is read as a delivered
// message: recv makes each its owner's alone, so one that is not was put there
// or opened since (#717). A file that is not there is left to its read.
// Options.UnsafeInboxPermissions leaves it unjudged.
func (b *Mailbox) judgeKept(path string) error {
	if b.unsafeInbox {
		return nil
	}
	if err := checkPrivateFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &fileRefused{err}
	}
	return nil
}

func layoutParts(dir string, l Layout) []layoutPart {
	base := []string{"base-dir"}
	keys := []string{"base-dir", "rotating-keys-dir"}
	mail := []string{"base-dir", "mail-dir"}
	parts := []layoutPart{
		{name: "base-dir", path: dir},
		{name: "mail-dir", path: l.Mail, inside: base},
		{name: "identity-file", path: l.IdentityFile, file: true, inside: base},
		{name: "rotating-keys-dir", path: l.RotatingKeys, inside: base},
		{name: "prekeys/", path: filepath.Join(l.RotatingKeys, "prekeys"), inside: keys},
		{name: "work/", path: filepath.Join(l.RotatingKeys, "work"), inside: keys},
		{name: "inbox-dir", path: l.Inbox, inside: mail},
		{name: "held/", path: filepath.Join(l.Mail, "held"), inside: mail},
		{name: "refused/", path: filepath.Join(l.Mail, "refused"), inside: mail},
		{name: "sent/", path: filepath.Join(l.Mail, "sent"), inside: mail},
		{name: "identity-file's .pub", path: l.IdentityFile + ".pub", file: true, inside: base},
		{name: "outbox/", path: filepath.Join(dir, "outbox"), inside: base},
	}
	for _, f := range baseFiles {
		parts = append(parts, layoutPart{name: f, path: filepath.Join(dir, f), file: true, inside: base})
	}
	return parts
}

// LayoutFault is a fault of the disk that [CheckLayout] met while it read where
// a part of a mailbox is, such as EIO or ESTALE. The fault says nothing of the
// layout, which may be right.
type LayoutFault struct {
	Part, Path string
	Err        error
}

func (f *LayoutFault) Error() string { return fmt.Sprintf("%s, %s: %v", f.Part, f.Path, f.Err) }

// Unwrap gives the fault.
func (f *LayoutFault) Unwrap() error { return f.Err }

// CheckLayout refuses a layout whose parts overlap, whose directory is a file
// or file a directory, or whose base directory is on a network filesystem, as
// far as the system says. Places are compared where they really are, through
// symlinks, and by [os.SameFile] once they exist, so neither a link nor a
// filesystem that ignores case hides an overlap. A fault of the disk is a
// [LayoutFault], and no refusal.
func CheckLayout(dir string, l Layout) error {
	parts := layoutParts(dir, l.resolved(dir))
	seen := places{}
	for i := range parts {
		p := &parts[i]
		where, err := realPath(p.path)
		if err != nil && pathIsWrong(err) {
			return fmt.Errorf("%s, %s: %w", p.name, p.path, err)
		}
		if err != nil {
			return &LayoutFault{Part: p.name, Path: p.path, Err: err}
		}
		p.path = where
		if fi := seen.stat(where); fi != nil && fi.IsDir() == p.file {
			kind := "a directory"
			if p.file {
				kind = "a file"
			}
			return fmt.Errorf("%s, %s, is to be %s, and it is not", p.name, where, kind)
		}
	}
	for i, a := range parts {
		for _, b := range parts[i+1:] {
			switch {
			case seen.same(a.path, b.path):
				return fmt.Errorf("%s and %s are one place, %s: each part of a mailbox has its own", a.name, b.name, a.path)
			case seen.inside(a.path, b.path) && !slices.Contains(a.inside, b.name):
				return fmt.Errorf("%s, %s, is inside %s, %s, and it may not be", a.name, a.path, b.name, b.path)
			case seen.inside(b.path, a.path) && !slices.Contains(b.inside, a.name):
				return fmt.Errorf("%s, %s, is inside %s, %s, and it may not be", b.name, b.path, a.name, a.path)
			}
		}
	}
	return checkBaseLocal(parts[0].path)
}

// checkBaseLocal refuses a base directory on a network filesystem.
func checkBaseLocal(dir string) error {
	if fs := networkFilesystem(dir); fs != "" {
		return fmt.Errorf("base-dir, %s, is on a network filesystem (%s): the mailbox's lock is a flock there, which is not one to rely on, and mail-dir may be on one where base-dir is not", dir, fs)
	}
	return nil
}

// places is what one check has read of each path, so that comparing every pair of
// parts and each one's parents reads each path once.
type places map[string]os.FileInfo

// stat is what path is, or nil where nothing is there.
func (c places) stat(path string) os.FileInfo {
	fi, ok := c[path]
	if !ok {
		fi, _ = os.Stat(path) //nolint:errcheck // nil: nothing to compare
		c[path] = fi
	}
	return fi
}

func (c places) same(a, b string) bool {
	if a == b {
		return true
	}
	fa, fb := c.stat(a), c.stat(b)
	return fa != nil && fb != nil && os.SameFile(fa, fb)
}

// inside is whether a is somewhere under b.
func (c places) inside(a, b string) bool {
	for p := filepath.Dir(a); ; p = filepath.Dir(p) {
		if c.same(p, b) {
			return true
		}
		if p == filepath.Dir(p) {
			return false
		}
	}
}

// wrongPath are the errors a system gives of a path, whatever its disk does: the
// path goes through a file, loops, is too long, or is no name a file can have.
var wrongPath = []syscall.Errno{syscall.ENOTDIR, syscall.ELOOP, syscall.ENAMETOOLONG, syscall.EINVAL}

// wrongPathOnWindows is wrongPath's other half, by their numbers, which mean
// other things elsewhere: ERROR_INVALID_NAME, ERROR_BAD_PATHNAME,
// ERROR_FILENAME_EXCED_RANGE and ERROR_DIRECTORY.
var wrongPathOnWindows = []syscall.Errno{123, 161, 206, 267}

// pathIsWrong reports whether err, from reading where a path is, is about the
// path or this user's right to read along it: a config that names another path
// answers either. Any other answer from the system is a fault of the disk.
func pathIsWrong(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return true // links that loop: filepath.EvalSymlinks's own error
	}
	return errors.Is(err, fs.ErrPermission) || slices.Contains(wrongPath, errno) ||
		runtime.GOOS == "windows" && slices.Contains(wrongPathOnWindows, errno)
}

// failPath is asked before each read of where a path is, and a read it fails
// fails with its error: nil but in a test, which cannot make a disk fail
// (SetPathFault).
var failPath func() error

// realPath is path made absolute, with the symlinks of as much of it as
// exists followed.
func realPath(path string) (string, error) {
	if failPath != nil {
		if err := failPath(); err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rest := ""
	for p := abs; ; p = filepath.Dir(p) {
		where, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(where, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if p == filepath.Dir(p) {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(p), rest)
	}
}

// makeDir makes a directory of the mailbox with its missing parents, each its
// owner's alone (private) and there after a crash once its parent is synced.
// A directory that exists is left as it is, whatever its mode: it is whoever
// made it's.
func makeDir(path string) error {
	var missing []string
	for p := path; ; p = filepath.Dir(p) {
		_, err := os.Stat(p)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		missing = append(missing, p)
		if p == filepath.Dir(p) {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		d := missing[i]
		err := os.Mkdir(d, 0o700)
		if errors.Is(err, fs.ErrExist) { // another process made it, so it is left as this one finds it
			continue
		}
		if err != nil {
			return err
		}
		if err := private(d); err != nil {
			return fmt.Errorf("%s, made its owner's alone: %w", d, err)
		}
		if err := syncDir(filepath.Dir(d)); err != nil {
			return err
		}
	}
	return nil
}

// gitAbove is the work tree path is in, or "" if it is in none: a .git at it or
// above it, of whichever kind. git is not run, since it executes configuration
// it finds inside a repository.
func gitAbove(path string) string {
	where, err := realPath(path)
	if err != nil {
		return ""
	}
	for p := where; ; p = filepath.Dir(p) {
		if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			return p
		}
		if p == filepath.Dir(p) {
			return ""
		}
	}
}

// KeysInGit says, in words, each private key path of this mailbox with a
// .git above it. A work tree is someone's own, so the mailbox writes nothing
// there, and it cannot tell from here whether a file is ignored.
func (b *Mailbox) KeysInGit() []string {
	var out []string
	for _, p := range []struct{ name, path string }{{"identity-file", b.layout.IdentityFile}, {"rotating-keys-dir", b.layout.RotatingKeys}} {
		if root := gitAbove(p.path); root != "" {
			out = append(out, fmt.Sprintf("%s, %s, is inside the git work tree %s: a private key must never be committed, and this client writes no .gitignore, so make sure git ignores it", p.name, p.path, root))
		}
	}
	return out
}

// keyRefusal is the refusal of a private key file others can reach.
func keyRefusal(path, why string) error {
	return fmt.Errorf("the private key %s %s: ssh would refuse it too. %s", path, why, strings.TrimSpace(keyFix))
}

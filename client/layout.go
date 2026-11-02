package client

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	// RotatingKeys holds prekeys/ and work/, which are never backed up:
	// <dir>/keys if empty.
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
		case "prekeys", "work":
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

func layoutParts(dir string, l Layout) []layoutPart {
	base := []string{"base-dir"}
	keys := []string{"base-dir", "rotating-keys-dir"}
	mail := []string{"base-dir", "mail-dir"}
	return []layoutPart{
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
	}
}

// CheckLayout refuses a layout whose parts overlap, whose directory is a file
// or file a directory, or whose base directory is on a network filesystem, as
// far as the system says. Places are compared where they really are, through
// symlinks, and by [os.SameFile] once they exist, so neither a link nor a
// filesystem that ignores case hides an overlap.
func CheckLayout(dir string, l Layout) error {
	parts := layoutParts(dir, l.resolved(dir))
	for i := range parts {
		p := &parts[i]
		where, err := realPath(p.path)
		if err != nil {
			return fmt.Errorf("%s, %s: %w", p.name, p.path, err)
		}
		p.path = where
		if fi, err := os.Stat(where); err == nil && fi.IsDir() == p.file {
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
			case samePlace(a.path, b.path):
				return fmt.Errorf("%s and %s are one place, %s: each part of a mailbox has its own", a.name, b.name, a.path)
			case insidePlace(a.path, b.path) && !slices.Contains(a.inside, b.name):
				return fmt.Errorf("%s, %s, is inside %s, %s, and it may not be", a.name, a.path, b.name, b.path)
			case insidePlace(b.path, a.path) && !slices.Contains(b.inside, a.name):
				return fmt.Errorf("%s, %s, is inside %s, %s, and it may not be", b.name, b.path, a.name, a.path)
			}
		}
	}
	if fs := networkFilesystem(parts[0].path); fs != "" {
		return fmt.Errorf("base-dir, %s, is on a network filesystem (%s): the mailbox's lock is a flock there, which is not one to rely on, and mail-dir may be on one where base-dir is not", parts[0].path, fs)
	}
	return nil
}

// realPath is path made absolute, with the symlinks of as much of it as
// exists followed.
func realPath(path string) (string, error) {
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

func samePlace(a, b string) bool {
	if a == b {
		return true
	}
	fa, erra := os.Stat(a)
	fb, errb := os.Stat(b)
	return erra == nil && errb == nil && os.SameFile(fa, fb)
}

// insidePlace is whether a is somewhere under b.
func insidePlace(a, b string) bool {
	for p := filepath.Dir(a); ; p = filepath.Dir(p) {
		if samePlace(p, b) {
			return true
		}
		if p == filepath.Dir(p) {
			return false
		}
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
		if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
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

// gitAbove is the work tree above path, or "" if it is in none: a .git above
// it, of whichever kind. git is not run, since it executes configuration it
// finds inside a repository.
func gitAbove(path string) string {
	where, err := realPath(path)
	if err != nil {
		return ""
	}
	for p := filepath.Dir(where); ; p = filepath.Dir(p) {
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

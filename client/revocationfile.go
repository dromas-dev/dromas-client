package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// revocationFileDirs are the directories of the mailbox that the record Init
// writes (Options.RevocationFile) may not be in: each directory of its layout
// (layoutParts), and the identity file's.
func (b *Mailbox) revocationFileDirs() []layoutPart {
	dirs := []layoutPart{{name: "identity-file's directory", path: filepath.Dir(b.layout.IdentityFile)}}
	for _, p := range layoutParts(b.dir, b.layout) {
		if !p.file {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// revocationFileThere is the refusal of a revocation file where something is.
func revocationFileThere(name string) error {
	return fmt.Errorf("revocation-file, %s, is there already: init writes the record to a new file, and replaces none", name)
}

// checkRevocationFile refuses Options.RevocationFile where its directory is not
// there, where something is there, a link among them, and where it is inside a
// directory of the mailbox (revocationFileDirs) or is one. Each place is
// compared where it really is, through symlinks, as CheckLayout compares the
// parts. A record kept beside the identity is lost and stolen with it.
func (b *Mailbox) checkRevocationFile() error {
	name := b.revocationFile
	if fi, err := os.Stat(filepath.Dir(name)); err != nil || !fi.IsDir() {
		return fmt.Errorf("revocation-file, %s, is to be in the directory %s, and there is no directory there", name, filepath.Dir(name))
	}
	if _, err := os.Lstat(name); err == nil {
		return revocationFileThere(name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("revocation-file, %s: %w", name, err)
	}
	where, err := realPath(name)
	if err != nil {
		return fmt.Errorf("revocation-file, %s: %w", name, err)
	}
	seen := places{}
	for _, d := range b.revocationFileDirs() {
		dir, err := realPath(d.path)
		if err != nil {
			return fmt.Errorf("%s, %s: %w", d.name, d.path, err)
		}
		if seen.same(where, dir) || seen.inside(where, dir) {
			return fmt.Errorf("revocation-file, %s, is inside %s, %s: a record kept beside the identity is lost and stolen with it, so write it outside every directory of the mailbox", name, d.name, d.path)
		}
	}
	return nil
}

// writeRevocationFile writes the mailbox's revocation record to
// Options.RevocationFile, the line dromas revocation make prints, in a new file
// its owner's alone from its making (newPrivateFile), which replaces nothing.
// The file and its directory are synced before it returns. A write that fails
// removes the file it made.
func (b *Mailbox) writeRevocationFile() error {
	rec, err := b.revocation()
	if err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	name := b.revocationFile
	f, err := newPrivateFile(name)
	if errors.Is(err, fs.ErrExist) {
		return revocationFileThere(name)
	}
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if err == nil {
		err = syncFile(f)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = syncDir(filepath.Dir(name))
	}
	if err != nil {
		os.Remove(name) //nolint:errcheck,gosec // the write's error wins
		return err
	}
	return nil
}

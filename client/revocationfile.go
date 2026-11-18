package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// revocationFileThere is the refusal of a revocation file where something is.
func revocationFileThere(name string) error {
	return fmt.Errorf("revocation-file, %s, is there already: init writes the record to a new file, and replaces none", name)
}

// checkRevocationFile refuses Options.RevocationFile where its directory is not
// there, and where something is there, a link among them. A record kept beside
// the mailbox is lost and stolen with it.
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

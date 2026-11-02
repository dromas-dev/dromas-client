package client

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// writeFile writes a file with mode 0600 through a temporary file and a
// rename, so a reader never sees half of it, and syncs the file and its
// directory, so it survives a power loss once writeFile returns: a message is
// acked off the server only after it is on disk.
func writeFile(name string, data []byte) error { return writeWith(name, data, nil) }

// writeKey is writeFile for a private key, which is protected before it has
// its name (protectKeyFile).
func writeKey(name string, data []byte) error { return writeWith(name, data, protectKeyFile) }

func writeWith(name string, data []byte, protect func(string) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck,gosec // the write error wins
		return err
	}
	if err := syncFile(tmp); err != nil {
		tmp.Close() //nolint:errcheck,gosec // the sync error wins
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if protect != nil {
		if err := protect(tmp.Name()); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		return err
	}
	return syncDir(filepath.Dir(name))
}

// exists reports whether name exists and is not empty, as the recipe's [ -s ].
func exists(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && fi.Size() > 0
}

// removeIfExists removes name; a file already gone is not an error.
func removeIfExists(name string) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

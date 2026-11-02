package client

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// writeFile writes a file its owner's alone (NewPrivateTemp) through a temporary
// file and a rename, so a reader never sees half of it, and syncs the file and its
// directory, so it survives a power loss once writeFile returns: a message is
// acked off the server only after it is on disk.
func writeFile(name string, data []byte) error { return writeWith(name, data, NewPrivateTemp) }

// writeKey is writeFile for a private key, which goes nowhere that cannot keep it
// its owner's alone (newKeyTemp).
func writeKey(name string, data []byte) error { return writeWith(name, data, newKeyTemp) }

func writeWith(name string, data []byte, create func(dir string) (*os.File, error)) error {
	tmp, err := create(filepath.Dir(name))
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
	if err := os.Rename(tmp.Name(), name); err != nil {
		return err
	}
	return syncDir(filepath.Dir(name))
}

// matching is the files of one directory whose names match pattern's last element,
// in order, as filepath.Glob's answer is, but where the directory cannot be read
// it says so: Glob answers none, which reads as an empty folder. A directory
// that is not there holds none.
func matching(pattern string) ([]string, error) {
	dir, name := filepath.Split(pattern)
	es, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		ok, err := filepath.Match(name, e.Name())
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
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

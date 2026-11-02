package client

import (
	"errors"
	"io/fs"
	"path/filepath"
)

// networkFilesystem is the name of the network filesystem the directory is
// on, or of the nearest of its parents that exists, or "" where it is on none
// or the system cannot say.
func networkFilesystem(dir string) string {
	for p := dir; ; p = filepath.Dir(p) {
		name, err := filesystemOf(p)
		if err == nil {
			return name
		}
		if !errors.Is(err, fs.ErrNotExist) || p == filepath.Dir(p) {
			return ""
		}
	}
}

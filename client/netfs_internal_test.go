package client

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// onFilesystems has each lookup of a filesystem answered by answer, for one
// test: a network filesystem is what a test cannot mount.
func onFilesystems(t *testing.T, answer func(path string) (string, error)) {
	t.Helper()
	filesystemAt = answer
	t.Cleanup(func() { filesystemAt = filesystemOf })
}

// A directory is on the filesystem of the nearest of its parents that exists, so
// one not made yet under a mount is on that mount. Where the system cannot say,
// or nothing exists up to the root, it is on none. It runs alone, since it sets
// where every lookup goes.
func TestANetworkFilesystemIsNamedAtTheNearestPlaceThatExists(t *testing.T) {
	mount := filepath.Join(string(filepath.Separator), "mnt", "shared")
	missing := filepath.Join(mount, "mailbox", "not", "made")
	onFilesystems(t, func(p string) (string, error) {
		if p == mount {
			return "nfs", nil
		}
		return "", fs.ErrNotExist
	})
	if got := networkFilesystem(mount); got != "nfs" {
		t.Errorf("at the mount: %q", got)
	}
	if got := networkFilesystem(missing); got != "nfs" {
		t.Errorf("under the mount, not made: %q", got)
	}
	if got := networkFilesystem(filepath.Join(string(filepath.Separator), "elsewhere", "mailbox")); got != "" {
		t.Errorf("nothing exists up to the root: %q", got)
	}
	onFilesystems(t, func(p string) (string, error) {
		if p == mount {
			return "nfs", nil
		}
		return "", &fs.PathError{Op: "statfs", Path: p, Err: syscall.EIO}
	})
	if got := networkFilesystem(missing); got != "" {
		t.Errorf("the system cannot say: %q", got)
	}
}

// A base directory on a network filesystem is refused by name, by CheckLayout
// and by each way into a mailbox, before anything is written. mail-dir on one
// is taken, as the refusal says. It runs alone, since it sets where every lookup
// goes.
func TestABaseDirOnANetworkFilesystemIsRefusedAndAMailDirIsNot(t *testing.T) {
	const words = "is on a network filesystem (nfs): the mailbox's lock is a flock there"
	const server = "https://server.invalid" // a refusal comes before any request
	base := t.TempDir()
	mail, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	onFilesystems(t, func(p string) (string, error) {
		if strings.HasPrefix(p, mail) {
			return "nfs", nil
		}
		return "", nil
	})
	if err := CheckLayout(base, Layout{Mail: mail}); err != nil {
		t.Errorf("mail-dir on a network filesystem: %v", err)
	}
	onFilesystems(t, func(string) (string, error) { return "nfs", nil })
	if err := CheckLayout(base, Layout{}); err == nil || !strings.Contains(err.Error(), words) {
		t.Errorf("CheckLayout: %v", err)
	}
	if _, err := Open(base, Options{Base: server}); err == nil || !strings.Contains(err.Error(), words) {
		t.Errorf("Open: %v", err)
	}
	if _, err := Open(base, Options{Base: server, Layout: Layout{Mail: mail}}); err == nil || !strings.Contains(err.Error(), words) {
		t.Errorf("Open with a layout: %v", err)
	}
	if _, err := Init(t.Context(), base, Options{Base: server}); err == nil || !strings.Contains(err.Error(), words) {
		t.Errorf("Init: %v", err)
	}
	if _, err := Init(t.Context(), base, Options{Base: server, Layout: Layout{Mail: mail}}); err == nil || !strings.Contains(err.Error(), words) {
		t.Errorf("Init with a layout: %v", err)
	}
	if es, err := os.ReadDir(base); err != nil || len(es) != 0 {
		t.Errorf("the base directory holds %v, %v", es, err)
	}
}

// What the system says of a directory on this machine's disk, made or not: no
// network filesystem, and an error only of the kind networkFilesystem reads.
func TestALocalDirectoryIsOnNoNetworkFilesystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if name, err := filesystemOf(dir); name != "" || err != nil {
		t.Errorf("%q, %v", name, err)
	}
	missing := filepath.Join(dir, "not", "made")
	if name, err := filesystemOf(missing); name != "" || err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("not made: %q, %v", name, err)
	}
	if name := networkFilesystem(missing); name != "" {
		t.Errorf("not made, by its parents: %q", name)
	}
}

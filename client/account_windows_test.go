//go:build windows

package client_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/dromas-dev/dromas-client/client"

	"golang.org/x/sys/windows"
)

// The tests here need what CI's windows-accounts job makes (.github/workflows/ci.yml,
// scripts/windows-accounts.ps1): local accounts and volumes with no access list. That
// job runs them under each account, named by accountEnv.
const accountEnv = "DROMAS_TEST_ACCOUNT"

func namedAccount(t *testing.T) string {
	t.Helper()
	a := os.Getenv(accountEnv)
	if a == "" {
		t.Skip("run by the windows-accounts job, under the accounts it makes")
	}
	return a
}

// The job's account is the one the process runs as, and none but SYSTEM holds the
// Administrators, so what a test of it passes is what that account can do.
func TestTheAccountIsTheOneNamed(t *testing.T) {
	name := namedAccount(t)
	t.Parallel()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid
	account, _, _, err := sid.LookupAccount("")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s runs as %s, %s", name, account, sid)
	var is bool
	switch name {
	case "plain", "other":
		is = strings.EqualFold(account, "dromas-"+name)
	case "system":
		is = sid.IsWellKnown(windows.WinLocalSystemSid)
	case "network-service":
		is = sid.IsWellKnown(windows.WinNetworkServiceSid)
	case "local-service":
		is = sid.IsWellKnown(windows.WinLocalServiceSid)
	default:
		t.Fatalf("no account is named %s", name)
	}
	if !is {
		t.Errorf("the account named %s runs as %s, %s", name, account, sid)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	member, err := windows.Token(0).IsMember(admins)
	if err != nil {
		t.Fatal(err)
	}
	if member != (name == "system") {
		t.Errorf("%s: a member of the Administrators: %v", name, member)
	}
}

// What a second user's process is tried at, each right alone, and which of them write.
var rights = []struct {
	name   string
	mask   uint32
	writes bool
}{
	{"read-data", windows.FILE_READ_DATA, false},
	{"read-EA", windows.FILE_READ_EA, false},
	{"execute", windows.FILE_EXECUTE, false},
	{"read-attributes", windows.FILE_READ_ATTRIBUTES, false},
	{"read-control", windows.READ_CONTROL, false},
	{"write-data", windows.FILE_WRITE_DATA, true},
	{"append", windows.FILE_APPEND_DATA, true},
	{"write-EA", windows.FILE_WRITE_EA, true},
	{"write-attributes", windows.FILE_WRITE_ATTRIBUTES, true},
	{"delete", windows.DELETE, true},
	{"write the list", windows.WRITE_DAC, true},
	{"take ownership", windows.WRITE_OWNER, true},
}

// aListJudged is one file the first plain user set a list on, and what it read back
// from that list: whether it is taken as a key and as a config. A file of a mailbox
// made in a directory others can modify is its owner's alone.
type aListJudged struct {
	Name, Path  string
	Key, Config bool
	OfAMailbox  bool
}

// A file's list read back says what a real second user can do with it. The first
// plain user sets each list and judges it; the second, a process of its own, opens
// each file with each right. A file taken as a key opens to it with none; one
// taken as a config, with none that writes; and one refused, with one that the
// refusal was for. Every file of a mailbox made in a directory others can modify,
// keys and state, opens to it with none but what the directory itself gives.
func TestASecondUserReachesWhatTheListSays(t *testing.T) {
	name := namedAccount(t)
	if name != "plain" && name != "other" {
		return // the first plain user's lists, and the second's reach
	}
	reach := os.Getenv("DROMAS_TEST_REACH")
	if reach == "" {
		t.Fatal("DROMAS_TEST_REACH names no folder")
	}
	manifest := filepath.Join(reach, "judged.json")
	if name == "plain" {
		setAndJudge(t, reach, manifest)
		return
	}
	raw, err := os.ReadFile(manifest) //nolint:gosec // the job's own folder
	if err != nil {
		t.Fatal(err)
	}
	var judged []aListJudged
	if err := json.Unmarshal(raw, &judged); err != nil {
		t.Fatal(err)
	}
	if len(judged) < len(accessLists)+15 {
		t.Fatalf("%d files: too few for the lists and a mailbox that sent and received", len(judged))
	}
	for _, f := range judged {
		t.Run(f.Name, func(t *testing.T) {
			var opens []string
			reads, writes := 0, 0
			for _, r := range rights {
				if f.OfAMailbox && r.mask == windows.FILE_READ_ATTRIBUTES {
					continue // a directory others can list gives this of each file in it
				}
				ok, err := opensWith(f.Path, r.mask)
				if err != nil {
					t.Fatalf("%s: %v", r.name, err)
				}
				if ok {
					opens = append(opens, r.name)
					if r.writes {
						writes++
					} else {
						reads++
					}
				}
			}
			t.Logf("opens with: %v", opens)
			switch {
			case f.OfAMailbox && reads+writes != 0:
				t.Errorf("a file of a mailbox opens to another user with %v", opens)
			case f.Key && reads+writes != 0:
				t.Errorf("taken as a key, and opens to another user with %v", opens)
			case !f.OfAMailbox && !f.Key && reads+writes == 0:
				t.Error("refused as a key, and another user reaches nothing")
			case f.Config && writes != 0:
				t.Errorf("taken as a config, and another user writes it with %v", opens)
			case !f.OfAMailbox && !f.Config && writes == 0:
				t.Errorf("refused as a config, and another user writes nothing: it opens with %v", opens)
			}
		})
	}
}

// opensWith is whether this process can open path with these rights and no other:
// NtCreateFile, as CreateFile adds synchronize and read-attributes to what it asks.
// Only a refusal of access is a no: any other error says nothing of them.
func opensWith(path string, mask uint32) (bool, error) {
	name, err := windows.NewNTUnicodeString(`\??\` + path)
	if err != nil {
		return false, err
	}
	oa := windows.OBJECT_ATTRIBUTES{ObjectName: name}
	oa.Length = uint32(unsafe.Sizeof(oa)) //nolint:gosec // a struct's size
	var h windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, mask, &oa, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
	if errors.Is(err, windows.STATUS_ACCESS_DENIED) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, windows.CloseHandle(h)
}

// setAndJudge is the first plain user's half: each of accessLists on a file in a
// directory of its own alone, so no right on the file comes from the directory, the
// list no text form writes, and a mailbox that sent and received in a directory
// others can modify.
func setAndJudge(t *testing.T, reach, manifest string) {
	t.Helper()
	lists, err := os.MkdirTemp(reach, "lists-")
	if err != nil {
		t.Fatal(err)
	}
	setList(t, lists, "")
	var judged []aListJudged
	judge := func(name, path string) {
		judged = append(judged, aListJudged{Name: name, Path: path, Key: client.CheckKeyFile(path) == nil, Config: client.CheckConfigFile(path) == nil})
	}
	newFile := func(i int) string {
		path := filepath.Join(lists, "f"+string(rune('a'+i)))
		if err := os.WriteFile(path, []byte("what a list keeps"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for i, c := range accessLists {
		path := newFile(i)
		setList(t, path, c.entries)
		judge(c.name, path)
	}
	path := newFile(len(accessLists))
	setUnrenderable(t, path)
	judge("a list no text form writes", path)

	e := newEnv(t)
	made, err := os.MkdirTemp(reach, "mailbox-")
	if err != nil {
		t.Fatal(err)
	}
	openToOthers(t, made)
	a, err := client.Init(t.Context(), made, e.opts())
	if err != nil {
		t.Fatal(err)
	}
	b := e.mailbox()
	landAll(t, a)
	for _, x := range [][2]*client.Mailbox{{a, b}, {b, a}} {
		send(t, x[0], x[1], "kept where only its owner reads it")
		if _, err := x[1].Receive(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := filepath.WalkDir(made, func(p string, _ fs.DirEntry, err error) error {
		if err != nil || p == made {
			return err
		}
		rel, err := filepath.Rel(made, p)
		judged = append(judged, aListJudged{Name: "a mailbox's " + filepath.ToSlash(rel), Path: p, OfAMailbox: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(judged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A volume that keeps no access list holds no key: not a new key's temporary, not an
// identity with its base elsewhere, and not a mailbox, whether its base directory is
// made there or was there already. A key or a config already there is refused, as
// open to everyone. Each leaves no key, no identity.pub and no temporary behind.
func TestAVolumeThatKeepsNoAccessListHoldsNoKey(t *testing.T) {
	namedAccount(t)
	t.Parallel()
	volumes := strings.Split(os.Getenv("DROMAS_TEST_NO_ACL_VOLUMES"), ";")
	if len(volumes) != 2 {
		t.Fatalf("DROMAS_TEST_NO_ACL_VOLUMES names %q: a FAT32 and an exFAT volume", volumes)
	}
	seen := map[string]bool{}
	for _, volume := range volumes {
		name := fileSystem(t, volume)
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir, err := os.MkdirTemp(volume, "dromas-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })

			if f, err := client.NewKeyTemp(dir); err == nil {
				_ = f.Close()
				t.Errorf("a new key's temporary: %s", f.Name())
			} else if !strings.Contains(err.Error(), "keeps no access list") {
				t.Errorf("a new key's temporary: %v", err)
			}

			e := newEnv(t)
			opt := e.opts()
			opt.Layout.IdentityFile = filepath.Join(dir, "identity")
			if _, err := client.Init(t.Context(), filepath.Join(t.TempDir(), "made"), opt); err == nil || !strings.Contains(err.Error(), "keeps no access list") {
				t.Errorf("an identity here, its base elsewhere: %v", err)
			}
			if _, err := client.Init(t.Context(), filepath.Join(dir, "made"), e.opts()); err == nil || !strings.Contains(err.Error(), "keeps no access list") {
				t.Errorf("a base directory made here: %v", err)
			}
			premade := filepath.Join(dir, "premade")
			if err := os.Mkdir(premade, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Init(t.Context(), premade, e.opts()); err == nil {
				t.Error("a base directory already here: a mailbox was made")
			}
			if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if base := d.Name(); !d.IsDir() && (base == "identity" || base == "identity.pub" || strings.HasPrefix(base, ".tmp-") || strings.HasSuffix(base, ".agekey")) {
					t.Errorf("left behind: %s", p)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			file := filepath.Join(dir, "already-here")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := client.CheckKeyFile(file); err == nil || !strings.Contains(err.Error(), "accessible by everyone") {
				t.Errorf("as a key: %v", err)
			}
			if err := client.CheckConfigFile(file); err == nil || !strings.Contains(err.Error(), "writable by everyone") {
				t.Errorf("as a config: %v", err)
			}
		})
	}
	if !seen["FAT32"] || !seen["exFAT"] {
		t.Errorf("the volumes are %v: a FAT32 and an exFAT", seen)
	}
}

// fileSystem is the name of the file system at a volume's root.
func fileSystem(t *testing.T, root string) string {
	t.Helper()
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	var name [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumeInformation(p, nil, 0, nil, nil, nil, &name[0], uint32(len(name))); err != nil {
		t.Fatalf("%s: %v", root, err)
	}
	return windows.UTF16ToString(name[:])
}

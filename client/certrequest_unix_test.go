//go:build unix

package client_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/protocol"
)

// The guide's commands for the holder's revocation of certificates, run with
// stock tools beside the identity, write a record the mailbox keeps, and the
// next request is numbered above it (docs/guide.md, "If the working key is
// stolen").
func TestTheGuidesRevocationByTheHolderIsARecordTheMailboxKeeps(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"ssh-keygen", "openssl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	page, err := os.ReadFile(filepath.Join("..", "docs", "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, _ := strings.Cut(string(page), "### If the working key is stolen")
	_, block, ok := strings.Cut(section, "```sh\n")
	block, _, ok2 := strings.Cut(block, "```")
	if !ok || !ok2 {
		t.Fatal("docs/guide.md has no sh block under \"If the working key is stolen\"")
	}
	e := newWorkEnv(t)
	b, _, raw := keptElsewhere(t, e)
	serial := signsUnder(t, b)
	dir := t.TempDir()
	doc, err := os.ReadFile(b.Path("identity.doc"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"identity": raw, "identity.doc": doc} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var script strings.Builder
	for l := range strings.Lines(block) {
		script.WriteString(strings.TrimPrefix(l, "   "))
	}
	sh := strings.NewReplacer("<address>", b.Address().String(), "<serial>", strconv.FormatUint(serial, 10)).Replace(script.String())
	cmd := exec.Command("sh", "-ec", sh) //nolint:gosec // the guide's own commands
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the guide's commands: %v: %s", err, out)
	}
	line, err := os.ReadFile(filepath.Join(dir, "record"))
	if err != nil {
		t.Fatal(err)
	}
	var rec protocol.Revocations
	if err := protocol.Unmarshal(bytes.TrimSpace(line), &rec); err != nil {
		t.Fatalf("the record the guide's commands wrote: %v: %s", err, line)
	}
	if _, _, err := b.KeepRevocation(t.Context(), &rec); err != nil {
		t.Fatalf("keep the record: %v", err)
	}
	r, err := b.RequestCertificate(t.Context())
	if err != nil || r.LowestSerial <= serial {
		t.Fatalf("the request after the revocation through %d: %+v, %v", serial, r, err)
	}
}

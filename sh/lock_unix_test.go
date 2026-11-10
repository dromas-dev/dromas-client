//go:build unix

package sh_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// init holds the mailbox's lock from its records' check through the
// registration's writes: a renewal started while the registration is in flight
// waits for the lock, and refuses once its wait ends with the lock still held
// (dr_init, dr_lock; #824 item 5).
func TestInitHoldsTheLockThroughItsRegistration(t *testing.T) {
	t.Parallel()
	need(t, append(scriptTools, "curl")...)
	srv := fakeserver.New(t, time.Now)
	env := []string{"DR_HOME=" + t.TempDir(), "DR_SERVER_URL=" + srv.URL, "DR_LOCK_WAIT=1"}
	renewErr := filepath.Join(t.TempDir(), "renew.err")
	hook := `eval "orig_authed() $(declare -f dr_authed | tail -n +2)"
dr_authed(){ [ "$2" != PUT ] || bash -c '. ` + abs(t, "dromas.sh") + `; dr_rekey' > /dev/null 2>> ` + renewErr + `; orig_authed "$@"; }
dr_init`
	if _, err := bash(t, env, hook); err != nil {
		t.Fatalf("init: %v", err)
	}
	if e, _ := os.ReadFile(renewErr); !strings.Contains(string(e), "waiting for this mailbox's lock") || !strings.Contains(string(e), "held its lock") {
		t.Errorf("the renewal beside the registration did not wait for the lock: %q", e)
	}
}

// A TERM to a command that holds the mailbox's lock releases the lock only
// once the command's own child has ended: the child is gone when the script
// exits, and so is the lock (dr_lock; #824 item 4).
func TestATermReleasesTheLockOnlyAfterTheChildEnds(t *testing.T) {
	t.Parallel()
	need(t, append(scriptTools, "curl")...)
	srv := fakeserver.New(t, time.Now)
	home := t.TempDir()
	env := []string{"DR_HOME=" + home, "DR_SERVER_URL=" + srv.URL}
	if _, err := bash(t, env, "dr_init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	hook := `eval "orig_authed() $(declare -f dr_authed | tail -n +2)"
dr_authed(){ bash -c 'echo $$ > ` + pidFile + `; exec sleep 3'; orig_authed "$@"; }
dr_rekey`
	cmd := exec.Command("bash", "-c", ". "+abs(t, "dromas.sh")+"\n"+hook)
	cmd.Env = append(os.Environ(), env...)
	var errs bytes.Buffer
	cmd.Stderr = &errs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	for deadline := time.Now().Add(30 * time.Second); pid == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("the child never started: %s", errs.String())
		}
		if b, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(b), "\n") {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("the script exited with its child still running (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(home, "lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock is still there: %v", err)
	}
}

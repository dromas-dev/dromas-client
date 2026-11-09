//go:build unix

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// A callback ended by a signal says which, and a program that does not start
// says that it did not.
func TestExecSaysASignalAndAFailedStart(t *testing.T) {
	c := newCallback(t)
	c.send("killed")
	stdout, _, err := c.recv(context.Background(), append([]string{"--wait", "0s"}, c.exec("kill")...)...)
	if status := report(err, io.Discard); status != 3 {
		t.Fatalf("recv: exit %d, %v", status, err)
	}
	failed := ofType(t, stdout, "exec_failed")
	if len(failed) != 3 || failed[0]["signal"] != "killed" || failed[0]["exit_status"] != nil {
		t.Fatalf("recv printed %s", stdout)
	}
	// There when recv started, gone when the message came.
	c = newCallback(t)
	c.send("no program")
	program := filepath.Join(t.TempDir(), "gone")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { //nolint:gosec // a test's program
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	cmd := c.recvCmdThatRemoves(program, &out, &errs)
	if status := report(cmd(), io.Discard); status != 3 {
		t.Fatalf("recv: exit %d: %s %s", status, out.String(), errs.String())
	}
	if failed := ofType(t, out.String(), "exec_failed"); len(failed) != 3 || failed[0]["start_error"] == nil {
		t.Fatalf("recv printed %s", out.String())
	}
}

// recvCmdThatRemoves is a recv with --exec of program, which is deleted once
// recv's argument check has passed: at its first event, a drain.
func (c *callbackEnv) recvCmdThatRemoves(program string, out, errs io.Writer) func() error {
	return func() error {
		// A fake server's list is the first thing recv asks of it after the check.
		c.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			_ = os.Remove(program)
			next.ServeHTTP(w, r)
		})
		return do(context.Background(), "recv", []string{"--wait", "0s", "--exec", program}, strings.NewReader(""), out, errs)
	}
}

// The binary's own recv --follow ends with exit status 0 at a SIGTERM, as a
// service manager stops it, and at SIGINT, SIGHUP and SIGQUIT, each of which
// restores a terminal the passphrase prompt holds raw (#803).
func TestRecvFollowEndsCleanlyAtASignal(t *testing.T) {
	ctx := context.Background()
	bin := filepath.Join(t.TempDir(), "dromas")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v %s", err, out)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT} {
		srv := fakeserver.New(t, time.Now)
		b, err := client.Init(ctx, t.TempDir(), client.Options{Base: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		holding := make(chan struct{})
		var once atomic.Bool
		srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") && once.CompareAndSwap(false, true) {
				close(holding)
			}
			next.ServeHTTP(w, r)
		})
		cmd := exec.CommandContext(ctx, bin, "recv", "--follow") //nolint:gosec // the binary this test built
		cmd.Env = append(os.Environ(), "DROMAS_CONFIG="+configFor(t, b.Dir()), "DROMAS_SERVER_URL="+srv.URL)
		var errs bytes.Buffer
		cmd.Stderr = &errs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-holding:
		case <-time.After(time.Minute):
			_ = cmd.Process.Kill()
			t.Fatal("recv --follow held no list in a minute")
		}
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("recv --follow at %v: %v %s", sig, err, errs.String())
		}
	}
}

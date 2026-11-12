package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// logged is the lines a log file holds, read back as printed lines.
func logged(t *testing.T, file string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return printed(t, string(raw))
}

// linesOfType is the lines of a type among ls, in their order.
func linesOfType(ls []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, l := range ls {
		if l["type"] == typ {
			out = append(out, l)
		}
	}
	return out
}

// recv --log-file prints nothing on stdout or stderr: each line goes to the
// file, which a second recv appends to. A file recv makes is its owner's alone.
func TestRecvLogFileTakesEveryLineAndAppends(t *testing.T) {
	c := newRecvEnv(t)
	file := filepath.Join(t.TempDir(), "recv.log")
	c.send("the first")
	stdout, stderr, err := c.recv(context.Background(), "--log-file", file)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("recv --log-file: %v; stdout %q, stderr %q", err, stdout, stderr)
	}
	if got := linesOfType(logged(t, file), "message"); len(got) != 1 {
		t.Fatalf("the log file holds %d message lines after the first recv, want 1", len(got))
	}
	c.send("the second")
	if _, _, err := c.recv(context.Background(), "--log-file", file); err != nil {
		t.Fatalf("the second recv --log-file: %v", err)
	}
	got := linesOfType(logged(t, file), "message")
	if len(got) != 2 {
		t.Fatalf("the log file holds %d message lines after the second recv, want 2: appended", len(got))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("the log file recv made has mode %v, want 0600", mode)
		}
	}
}

// A file that is there keeps the mode its owner gave it.
func TestRecvLogFileLeavesAFileThatIsThereAsItIs(t *testing.T) {
	c := newRecvEnv(t)
	file := filepath.Join(t.TempDir(), "recv.log")
	if err := os.WriteFile(file, []byte(`{"type":"before"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(file, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	c.send("one")
	if _, _, err := c.recv(context.Background(), "--log-file", file); err != nil {
		t.Fatalf("recv --log-file: %v", err)
	}
	ls := logged(t, file)
	if len(ls) < 2 || ls[0]["type"] != "before" || len(linesOfType(ls, "message")) != 1 {
		t.Fatalf("the log file holds %v, want its first line kept and the message after it", ls)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o640 {
			t.Fatalf("the log file's mode is %v after recv, want the 0640 it had", mode)
		}
	}
}

// The "error" line recv ends with goes to the log file, and report prints
// nothing more and keeps the exit status.
func TestRecvLogFileTakesTheErrorLine(t *testing.T) {
	c := newRecvEnv(t)
	c.srv.Close()
	file := filepath.Join(t.TempDir(), "recv.log")
	stdout, stderr, err := c.recv(context.Background(), "--log-file", file)
	var reportedTo bytes.Buffer
	if status := report(err, &reportedTo); status != 1 {
		t.Fatalf("recv --log-file against no server: exit %d, want 1; %v", status, err)
	}
	if stdout != "" || stderr != "" || reportedTo.Len() != 0 {
		t.Fatalf("recv --log-file printed stdout %q, stderr %q, report %q: want nothing", stdout, stderr, reportedTo.String())
	}
	if errs := linesOfType(logged(t, file), "error"); len(errs) != 1 {
		t.Fatalf("the log file holds %d error lines, want 1", len(errs))
	}
}

// recv.log-file in the config file is the default of --log-file, a relative path
// the config file's directory's, and dromas config says where it came from.
func TestRecvLogFileFromTheConfigFile(t *testing.T) {
	c := newRecvEnv(t)
	dir := t.TempDir()
	config := filepath.Join(dir, "mailbox.toml")
	body := "base-dir = " + tomlString(c.b.Dir()) + "\nmail-dir = " + tomlString(filepath.Join(c.b.Dir(), "mail")) + "\n\n[recv]\nlog-file = \"recv.log\"\n"
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROMAS_CONFIG", config)
	c.send("from the file's setting")
	stdout, stderr, err := c.recv(context.Background())
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("recv: %v; stdout %q, stderr %q", err, stdout, stderr)
	}
	file := filepath.Join(dir, "recv.log")
	if got := linesOfType(logged(t, file), "message"); len(got) != 1 {
		t.Fatalf("%s holds %d message lines, want 1", file, len(got))
	}
	values, _ := configLine(t)
	if got := values["recv.log-file"]; got != (setting{file, "file"}) {
		t.Fatalf("config says recv.log-file is %+v, want %s from the file", got, file)
	}
}

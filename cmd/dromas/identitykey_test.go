package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"golang.org/x/crypto/ssh"
)

// askpassEnv, set, has the test binary run as an askpass program print its
// value (TestMain).
const askpassEnv = "DROMAS_TEST_ASKPASS_PRINTS"

// encryptIdentity puts the identity file at file under passphrase, as
// ssh-keygen -p does, in the file itself, so that its permissions stay.
func encryptIdentity(t *testing.T, file, passphrase string) {
	t.Helper()
	raw, err := os.ReadFile(file) //nolint:gosec // the test's own
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.ParseRawPrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_TRUNC, 0) //nolint:gosec // the test's own
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(pem.EncodeToMemory(block)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// shortDir is a directory whose path is short enough for a socket's (104
// bytes on macOS).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dra")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// The agent setting is read as ssh_config's IdentityAgent, from the flag, then
// $DROMAS_AGENT, then the config file, where a relative path is under the
// file's directory, then the default, the user's own socket and never the
// shell's.
func TestTheAgentSettingIsReadAsIdentityAgent(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	file := writeConfig(t, "base-dir = "+tomlString(filepath.Join(root, "b"))+"\nmail-dir = "+tomlString(filepath.Join(root, "m"))+"\nagent = \"a.sock\"\n")
	t.Setenv("DROMAS_CONFIG", file)
	t.Setenv("SSH_AUTH_SOCK", "/the/shells/agent")
	t.Setenv("OTHER_AGENT", "/another/agent")
	if v, _ := configLine(t); v["agent"] != (setting{filepath.Join(filepath.Dir(file), "a.sock"), "file"}) {
		t.Errorf("from the file: %+v", v["agent"])
	}
	t.Setenv("DROMAS_AGENT", "$OTHER_AGENT")
	if v, _ := configLine(t); v["agent"] != (setting{"/another/agent", "environment"}) {
		t.Errorf("from the environment: %+v", v["agent"])
	}
	for flag, want := range map[string]string{"none": "none", "SSH_AUTH_SOCK": "/the/shells/agent"} {
		if v, _ := configLine(t, "--agent", flag); v["agent"] != (setting{want, "flag"}) {
			t.Errorf("--agent %s: %+v", flag, v["agent"])
		}
	}
	t.Setenv("DROMAS_AGENT", "")
	if err := os.WriteFile(file, []byte("base-dir = "+tomlString(filepath.Join(root, "b"))+"\nmail-dir = "+tomlString(filepath.Join(root, "m"))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	want := setting{filepath.Join(runtimeDir, "dromas", "agent.sock"), "default"}
	if runtime.GOOS == "windows" {
		want = setting{"none", "default"}
	}
	if v, _ := configLine(t); v["agent"] != want {
		t.Errorf("by default: %+v, want %+v", v["agent"], want)
	}
}

// failed is the "error" line a command's failure prints.
func failed(t *testing.T, err error) map[string]any {
	t.Helper()
	var errs bytes.Buffer
	report(err, &errs)
	var l map[string]any
	if jerr := json.Unmarshal(errs.Bytes(), &l); jerr != nil {
		t.Fatalf("%v: %q", jerr, errs.String())
	}
	return l
}

// A mailbox whose key file has a passphrase signs only once something gives the
// passphrase: with no terminal, or prompting off, a renewal fails with
// identity_locked, naming the key file, and an askpass program unlocks it.
func TestAKeyFileUnderAPassphraseIsLockedUntilAPassphraseIsGiven(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "identity")
	encryptIdentity(t, file, "correct horse")
	err := do(t.Context(), "prekey renew", []string{"--no-prompt"}, nil, io.Discard, io.Discard)
	if l := failed(t, err); l["code"] != client.IdentityLocked || l["identity_file"] != file {
		t.Errorf("a renewal with no passphrase: %v", l)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(askpassEnv, "battery staple")
	if l := failed(t, do(t.Context(), "prekey renew", []string{"--askpass", exe}, nil, io.Discard, io.Discard)); l["code"] != client.IdentityLocked {
		t.Errorf("a renewal with the wrong passphrase: %v", l)
	}
	t.Setenv(askpassEnv, "correct horse")
	if err := do(t.Context(), "prekey renew", []string{"--askpass", exe}, nil, io.Discard, io.Discard); err != nil {
		t.Errorf("a renewal with the passphrase: %v", err)
	}
	var out bytes.Buffer
	if err := do(t.Context(), "status", nil, nil, &out, io.Discard); err == nil {
		t.Error("status of a locked mailbox needs no look")
	}
	if !strings.Contains(out.String(), `"identity_key":"passphrase"`) {
		t.Errorf("status: %s", out.String())
	}
}

// dromas agent start runs stock ssh-agent at the mailbox's socket, dromas
// agent add loads the key with stock ssh-add, its passphrase from --askpass,
// and the mailbox then signs through the agent, its key file locked and no
// passphrase given. On Windows both refuse: the mailbox's own agent is not
// built there.
func TestAgentStartAndAddRunStockSSHAgent(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	socket := filepath.Join(shortDir(t), "agent", "s")
	t.Setenv("DROMAS_AGENT", socket)
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		for _, cmd := range []string{"agent start", "agent add"} {
			if err := do(t.Context(), cmd, nil, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "not built for Windows") {
				t.Errorf("%s: %v", cmd, err)
			}
		}
		return
	}
	encryptIdentity(t, filepath.Join(dir, "identity"), "correct horse")
	ctx, stop := context.WithCancel(t.Context())
	var started bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- do(ctx, "agent start", nil, nil, &started, io.Discard) }()
	t.Cleanup(func() { stop(); <-done })
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if c, err := net.Dial("unix", socket); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no agent answers at %s: %s", socket, started.String())
		}
	}
	if !strings.Contains(started.String(), `"command":"ssh-agent -D -a `+socket+`"`) {
		t.Errorf("agent start's line: %s", started.String())
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(askpassEnv, "correct horse")
	var added bytes.Buffer
	if err := do(t.Context(), "agent add", []string{"--askpass", exe}, nil, &added, io.Discard); err != nil {
		t.Fatalf("agent add: %v: %s", err, added.String())
	}
	for _, want := range []string{`"command":"SSH_AUTH_SOCK=` + socket + ` SSH_ASKPASS=` + exe + ` SSH_ASKPASS_REQUIRE=force ssh-add `, `"type":"agent_added"`} {
		if !strings.Contains(added.String(), want) {
			t.Errorf("agent add says no %s: %s", want, added.String())
		}
	}
	t.Setenv(askpassEnv, "")
	if err := do(t.Context(), "prekey renew", []string{"--no-prompt"}, nil, io.Discard, io.Discard); err != nil {
		t.Errorf("a renewal through the agent: %v", err)
	}
	var out bytes.Buffer
	_ = do(t.Context(), "status", nil, nil, &out, io.Discard) // its exit status is any attention's
	if !strings.Contains(out.String(), `"identity_key":"agent"`) {
		t.Errorf("status: %s", out.String())
	}
}

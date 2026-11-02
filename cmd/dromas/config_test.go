package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"time"
)

// useMailbox has the commands this test runs use the mailbox whose base directory
// is dir, by a config file of its own.
func useMailbox(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
}

// configFor writes a config file naming the mailbox whose base directory is dir.
func configFor(t *testing.T, dir string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "mailbox.toml")
	body := "base-dir = " + tomlString(dir) + "\nmail-dir = " + tomlString(filepath.Join(dir, "mail")) + "\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// cleanEnv leaves a test no config but what it writes: a home of its own, and none
// of the variables that name one.
func cleanEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "DROMAS_CONFIG", "DROMAS_PROFILE", "DROMAS_SERVER_URL"} {
		t.Setenv(v, "")
	}
	return home
}

// writeConfig writes a config file, its owner's alone, and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// configLine runs dromas config with args, and returns what it says of each setting.
func configLine(t *testing.T, args ...string) (map[string]setting, string) {
	t.Helper()
	var out bytes.Buffer
	if err := do(t.Context(), "config", args, nil, &out, io.Discard); err != nil {
		t.Fatalf("config %q: %v", args, err)
	}
	var l struct {
		File   string             `json:"file"`
		Values map[string]setting `json:"values"`
	}
	if err := json.Unmarshal(out.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l.Values, l.File
}

// A flag beats a variable, a variable the file, and the file the default, and
// dromas config says which each came from.
func TestEachSettingComesFromTheFirstPlaceThatSaysIt(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	file := writeConfig(t, "base-dir = "+tomlString(filepath.Join(root, "b"))+"\nmail-dir = "+tomlString(filepath.Join(root, "m"))+"\nserver = \"https://file.example\"\nprekey-lifetime = \"2d\"\n[policy]\nunknown = \"inbox\"\n[recv]\nheaders = true\n")
	t.Setenv("DROMAS_CONFIG", file)
	v, got := configLine(t)
	if got != file {
		t.Errorf("file %q, want %q", got, file)
	}
	for key, want := range map[string]setting{
		"base-dir":          {filepath.Join(root, "b"), "file"},
		"identity-file":     {filepath.Join(root, "b", "identity"), "default"},
		"rotating-keys-dir": {filepath.Join(root, "b", "keys"), "default"},
		"inbox-dir":         {filepath.Join(root, "m", "inbox"), "default"},
		"server":            {"https://file.example", "file"},
		"prekey-lifetime":   {"2d", "file"},
		"policy.unknown":    {"inbox", "file"},
		"recv.headers":      {"true", "file"},
	} {
		if v[key] != want {
			t.Errorf("%s: %+v, want %+v", key, v[key], want)
		}
	}
	t.Setenv("DROMAS_SERVER_URL", "https://env.example")
	if v, _ := configLine(t); v["server"] != (setting{"https://env.example", "environment"}) {
		t.Errorf("server: %+v", v["server"])
	}
	other := filepath.Join(root, "flag")
	v, _ = configLine(t, "--server", "https://flag.example", "--mail-dir", other, "--prekey-lifetime", "3d")
	if v["server"] != (setting{"https://flag.example", "flag"}) || v["mail-dir"] != (setting{other, "flag"}) || v["inbox-dir"] != (setting{filepath.Join(other, "inbox"), "default"}) || v["prekey-lifetime"] != (setting{"3d", "flag"}) {
		t.Errorf("%+v", v)
	}
}

// --profile and $DROMAS_PROFILE name a file in the config directory, default is
// the one with neither, --config and $DROMAS_CONFIG name any, and a flag beats either
// variable. Two that each name a file are refused, never ranked.
func TestTheConfigFileIsChosen(t *testing.T) {
	home := cleanEnv(t)
	dir := filepath.Join(home, ".config", "dromas")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	profile := func(name string) string {
		file := filepath.Join(dir, name+".toml")
		body := "base-dir = " + tomlString(filepath.Join(root, name)) + "\nmail-dir = " + tomlString(filepath.Join(root, name, "mail")) + "\n"
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	def, bot, env := profile("default"), profile("bot"), profile("env")
	other := configFor(t, filepath.Join(root, "other"))
	check := func(what, want string, args ...string) {
		t.Helper()
		if _, got := configLine(t, args...); got != want {
			t.Errorf("%s: %s, want %s", what, got, want)
		}
	}
	check("neither", def)
	check("--profile", bot, "--profile", "bot")
	check("--config", other, "--config", other)
	t.Setenv("DROMAS_PROFILE", "env")
	check("$DROMAS_PROFILE", env)
	check("--profile over $DROMAS_PROFILE", bot, "--profile", "bot")
	check("--config over $DROMAS_PROFILE", other, "--config", other)
	t.Setenv("DROMAS_CONFIG", other)
	var bad *configError
	if err := do(t.Context(), "config", nil, nil, io.Discard, io.Discard); !errors.As(err, &bad) {
		t.Errorf("both variables: %v", err)
	}
	check("--profile over both", bot, "--profile", "bot")
	t.Setenv("DROMAS_PROFILE", "")
	check("--profile over $DROMAS_CONFIG", bot, "--profile", "bot")
	var wrong *usageError
	if err := do(t.Context(), "config", []string{"--profile", "bot", "--config", other}, nil, io.Discard, io.Discard); !errors.As(err, &wrong) {
		t.Errorf("both flags: %v", err)
	}
}

// A config that is wrong stops the command before it touches the mailbox, exits 2,
// and names the file and the key.
func TestAConfigThatIsWrongIsFatal(t *testing.T) {
	home := cleanEnv(t)
	root := t.TempDir()
	base, mail := tomlString(filepath.Join(root, "b")), tomlString(filepath.Join(root, "m"))
	good := "base-dir = " + base + "\nmail-dir = " + mail + "\n"
	for _, c := range []struct{ name, body, key, words string }{
		{"not TOML", "base-dir = [", "", "toml"},
		{"a backslash", `base-dir = "C:\Users\x"`, "", "single quotes"},
		{"an unknown key", good + "mail_dir = \"x\"\n", "mail_dir", "no key"},
		{"an unknown table", good + "[send]\nsubject = \"x\"\n", "send", "no key"},
		{"a misspelled path", good + "prekeys-dir = \"x\"\n", "prekeys-dir", "no key"},
		{"a wrong type", good + "server = 3\n", "", "server"},
		{"no base-dir", "mail-dir = " + mail + "\n", "base-dir", "required"},
		{"no mail-dir", "base-dir = " + base + "\n", "mail-dir", "required"},
		{"a server", good + "server = \"http://example.org\"\n", "server", "not a server"},
		{"a lifetime", good + "prekey-lifetime = \"1h\"\n", "prekey-lifetime", "is from"},
		{"a policy", good + "[policy]\nunknown = \"deliver\"\n", "policy.unknown", "policy.unknown"},
		{"a policy", good + "[policy]\nserver = \"some\"\n", "policy.server", "policy.server"},
		{"a drain time", good + "[recv]\ndrain-time = \"0s\"\n", "recv.drain-time", "above zero"},
		{"a wait", good + "[recv]\nwait = \"soon\"\n", "recv.wait", "recv.wait"},
		{"two parts in one place", "base-dir = " + base + "\nmail-dir = " + base + "\n", "", "one place"},
		{"another user's home", "base-dir = \"~bob/x\"\nmail-dir = " + mail + "\n", "base-dir", "only ~/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			file := writeConfig(t, c.body)
			var stderr bytes.Buffer
			err := do(t.Context(), "status", []string{"--config", file}, nil, io.Discard, &stderr)
			var bad *configError
			if !errors.As(err, &bad) || bad.file == "" && c.key == "" || !strings.Contains(err.Error(), c.words) || bad.key != c.key {
				t.Fatalf("%#v", err)
			}
			if got := report(err, &stderr); got != 2 || !strings.Contains(stderr.String(), `"type":"config_error"`) {
				t.Errorf("exit %d, %s", got, stderr.String())
			}
		})
	}
	for name, args := range map[string][]string{
		"a --config with no file":  {"--config", filepath.Join(root, "none.toml")},
		"a --profile with no file": {"--profile", "none"},
		"the default with no file": nil,
		"a path as a profile":      {"--profile", "../x"},
	} {
		var bad *configError
		if err := do(t.Context(), "status", args, nil, io.Discard, io.Discard); !errors.As(err, &bad) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if es, _ := os.ReadDir(filepath.Join(home, ".config")); len(es) != 0 {
		t.Errorf("a command that is not init wrote a config: %v", es)
	}
	if es, _ := os.ReadDir(root); len(es) != 0 {
		t.Errorf("a config that is wrong made %v", es)
	}
}

// A file is never looked for in the working directory: a repository does not
// redirect a mailbox.
func TestTheWorkingDirectoryIsNeverRead(t *testing.T) {
	cleanEnv(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	body := "base-dir = \"x\"\nmail-dir = \"y\"\n"
	for _, name := range []string{"default.toml", "dromas.toml", ".dromas.toml"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var bad *configError
	if err := do(t.Context(), "config", nil, nil, io.Discard, io.Discard); !errors.As(err, &bad) {
		t.Errorf("%v", err)
	}
}

// A relative path in a file is under the file's directory, and on the command line
// under the working directory; ~/ is the home directory in both.
func TestARelativePathIsReadFromWhereItIsWritten(t *testing.T) {
	home := cleanEnv(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	file := writeConfig(t, "base-dir = \"box\"\nmail-dir = \"~/mail\"\nidentity-file = \"secret/id\"\n")
	v, _ := configLine(t, "--config", file, "--inbox-dir", "in")
	real := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	dir := filepath.Dir(file)
	if v["base-dir"].Value != filepath.Join(dir, "box") || v["mail-dir"].Value != filepath.Join(home, "mail") || v["identity-file"].Value != filepath.Join(dir, "secret", "id") {
		t.Errorf("%+v", v)
	}
	if got := v["inbox-dir"].Value; got != filepath.Join(cwd, "in") && got != filepath.Join(real(cwd), "in") {
		t.Errorf("--inbox-dir: %s", got)
	}
}

// init writes a profile's file where it has none, every path in full and every
// default commented out, and the mailbox then runs from it: a second command reads
// the same paths, and the keys are where the file says. Writing it twice changes
// nothing a person wrote.
func TestInitWritesAProfilesFileAndRunsFromIt(t *testing.T) {
	home := cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	var out bytes.Buffer
	if err := do(t.Context(), "init", []string{"--profile", "bot", "--server", srv.URL}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, ".config", "dromas", "bot.toml")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`base-dir = "~/.local/share/dromas/bot"`,
		`mail-dir = "~/.local/share/dromas/bot/mail"`,
		`identity-file = "~/.local/share/dromas/bot/identity"`,
		`rotating-keys-dir = "~/.local/state/dromas/bot/keys"`,
		`# inbox-dir = "~/.local/share/dromas/bot/mail/inbox"`,
		`# server = "` + client.DefaultBase + `"`,
		`# prekey-lifetime = "8d"`,
		`# [policy]`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the file does not say %s:\n%s", want, text)
		}
	}
	for _, want := range []string{".local/share/dromas/bot/identity", ".local/state/dromas/bot/keys/prekeys"} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local/share/dromas/bot/.gitignore")); err == nil {
		t.Error("a .gitignore was written")
	}
	var st bytes.Buffer
	if err := do(t.Context(), "status", []string{"--profile", "bot", "--server", srv.URL}, nil, &st, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.String(), `"config":"`+file) {
		t.Errorf("status names no config: %s", st.String())
	}
	if err := os.WriteFile(file, append(raw, []byte("\n# mine\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := do(t.Context(), "init", []string{"--profile", "bot", "--server", srv.URL}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(file); !strings.Contains(string(again), "# mine") {
		t.Error("init wrote over a file it found")
	}
	var bad *configError
	if err := do(t.Context(), "init", []string{"--config", filepath.Join(home, "none.toml"), "--server", srv.URL}, nil, io.Discard, io.Discard); !errors.As(err, &bad) {
		t.Errorf("init with a --config that is not there: %v", err)
	}
}

// With no file, flags that say every required key run a mailbox and write no file.
func TestFlagsAloneRunAMailbox(t *testing.T) {
	home := cleanEnv(t)
	root := t.TempDir()
	v, file := configLine(t, "--base-dir", filepath.Join(root, "b"), "--mail-dir", filepath.Join(root, "m"))
	if file != "" || v["base-dir"].From != "flag" {
		t.Errorf("%q, %+v", file, v)
	}
	if es, _ := os.ReadDir(home); len(es) != 0 {
		t.Errorf("wrote %v", es)
	}
}

// A table's settings reach their command's flags, and that command's own flag on
// the line stands over them.
func TestATablesSettingsAreTheCommandsFlags(t *testing.T) {
	cleanEnv(t)
	ctx := t.Context()
	srv := fakeserver.New(t, time.Now)
	a, err := client.Init(ctx, t.TempDir(), client.Options{Base: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	b, err := client.Init(ctx, t.TempDir(), client.Options{Base: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	allows(t, b, a)
	if _, err := a.Send(ctx, b.Address(), "the body"); err != nil {
		t.Fatal(err)
	}
	file := writeConfig(t, "base-dir = "+tomlString(b.Dir())+"\nmail-dir = "+tomlString(filepath.Join(b.Dir(), "mail"))+"\nserver = "+tomlString(srv.URL)+"\n[recv]\nheaders = true\n[inbox.list]\nheaders = true\n")
	t.Setenv("DROMAS_CONFIG", file)
	var out bytes.Buffer
	if err := do(ctx, "recv", nil, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "message_headers") || strings.Contains(out.String(), "the body") {
		t.Errorf("recv under headers = true: %s", out.String())
	}
	out.Reset()
	if err := do(ctx, "inbox list", []string{"--headers=false"}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "the body") {
		t.Errorf("inbox list --headers=false over the file: %s", out.String())
	}
}

// A template's strings read back as they were written, a Windows path and a quote
// among them.
func TestATemplatesStringsReadBack(t *testing.T) {
	t.Parallel()
	for _, s := range []string{`/plain/path`, `C:\Users\x\.local`, `it's`, `C:\it's`, `a"b`} {
		file := filepath.Join(t.TempDir(), "c.toml")
		if err := os.WriteFile(file, []byte("base-dir = "+tomlString(s)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var fc fileConfig
		if err := readConfig(file, &fc); err != nil || fc.BaseDir != s {
			t.Errorf("%q read back as %q, %v", s, fc.BaseDir, err)
		}
	}
}

// A config file others can write stops every command: whoever can write it says
// where the keys are and which server is called.
func TestAConfigFileOthersCanWriteStopsTheCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		return // no modes: the access list's reading is client's
	}
	cleanEnv(t)
	root := t.TempDir()
	file := writeConfig(t, "base-dir = "+tomlString(filepath.Join(root, "b"))+"\nmail-dir = "+tomlString(filepath.Join(root, "m"))+"\n")
	if err := os.Chmod(file, 0o666); err != nil { //nolint:gosec // open on purpose
		t.Fatal(err)
	}
	var bad *configError
	if err := do(t.Context(), "config", []string{"--config", file}, nil, io.Discard, io.Discard); !errors.As(err, &bad) || !strings.Contains(err.Error(), "writable by others") {
		t.Errorf("%v", err)
	}
}

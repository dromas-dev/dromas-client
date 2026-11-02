package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
	"github.com/spf13/cobra"
)

// Every setting is read flag, then environment, then the config file, then its
// default, as the AWS CLI, Docker, cargo and rclone read theirs. The file is
// TOML, one file to a profile, each standalone: a profile is a mailbox, and one
// inherited path would have two profiles share it. A key is its flag's long
// name, and a table the subcommand that takes it. The client writes the file
// only where init finds none, so it may be read-only, and it never looks in the
// working directory, so no repository redirects a mailbox.

// profileGrammar is what names a profile, which names a file.
var profileGrammar = regexp.MustCompile(`\A[A-Za-z0-9_-]+\z`)

// globals are the flags every command takes: which config file, and the file's
// top-level keys.
type globals struct {
	config, profile                                        string
	baseDir, mailDir, identityFile, rotatingKeysDir, inbox string
	server, prekeyLifetime                                 string
}

func (g *globals) add(c *cobra.Command) {
	f := c.PersistentFlags()
	f.StringVar(&g.config, "config", "", "read this config `file`: $DROMAS_CONFIG, or --profile's")
	f.StringVar(&g.profile, "profile", "", "read this `profile`'s config file, in the config directory: $DROMAS_PROFILE, or default")
	f.StringVar(&g.baseDir, "base-dir", "", "the `directory` of the mailbox's state, and the default of each path below")
	f.StringVar(&g.mailDir, "mail-dir", "", "the `directory` of inbox/, held/, refused/ and sent/")
	f.StringVar(&g.identityFile, "identity-file", "", "the identity key `file`: <base-dir>/identity")
	f.StringVar(&g.rotatingKeysDir, "rotating-keys-dir", "", "the `directory` of prekeys/ and work/, never to be backed up: <base-dir>/keys")
	f.StringVar(&g.inbox, "inbox-dir", "", "the `directory` of inbox/, apart from the rest of the mail: <mail-dir>/inbox")
	f.StringVar(&g.server, "server", "", "the server's `URL`: $DROMAS_SERVER_URL, or "+client.DefaultBase)
	f.StringVar(&g.prekeyLifetime, "prekey-lifetime", "", "how long each new prekey is valid for: a `time`, as dromas prekey lifetime --help says")
}

// fileConfig is what a config file may say. A key it does not name is an error.
type fileConfig struct {
	BaseDir         string `toml:"base-dir"`
	MailDir         string `toml:"mail-dir"`
	IdentityFile    string `toml:"identity-file"`
	RotatingKeysDir string `toml:"rotating-keys-dir"`
	InboxDir        string `toml:"inbox-dir"`
	Server          string `toml:"server"`
	PrekeyLifetime  string `toml:"prekey-lifetime"`
	Policy          struct {
		Unknown string `toml:"unknown"`
		Server  string `toml:"server"`
	} `toml:"policy"`
	Recv struct {
		Wait      string `toml:"wait"`
		DrainTime string `toml:"drain-time"`
		Headers   *bool  `toml:"headers"`
	} `toml:"recv"`
	Inbox struct {
		List struct {
			Headers *bool `toml:"headers"`
		} `toml:"list"`
		Show struct {
			Headers *bool `toml:"headers"`
		} `toml:"show"`
	} `toml:"inbox"`
}

// setting is one setting as this run resolved it, and where it came from.
type setting struct {
	Value string `json:"value"`
	From  string `json:"from"`
}

// config is the settings a command runs with.
type config struct {
	file, profile string
	dir           string
	layout        client.Layout
	server        string
	lifetime      time.Duration
	policy        client.Policy
	// flagged is each table's settings, by the command that takes them.
	flagged map[string]map[string]string
	values  map[string]setting
}

// configError is a config that is wrong, so nothing ran: exit status 2, with a
// "config_error" line on stderr naming the file and the key.
type configError struct {
	file, key, msg string
}

func (e *configError) Error() string { return e.msg }

// configDir is the directory a profile's file is in.
func configDir() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "dromas"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dromas"), nil
}

// expand makes a path absolute: a leading ~/ is the home directory, and any
// other relative one is under rel. Nothing else in it is expanded.
func expand(path, rel string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%s names the home directory, and there is none: %w", path, err)
		}
		return filepath.Join(home, filepath.FromSlash(path[1:])), nil
	}
	if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("%s: only ~/ is read, for this user's home directory", path)
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Join(rel, path), nil
}

// loadConfig reads the settings, and refuses what is wrong before a command
// touches the mailbox. isInit is whether init runs, which writes a profile's file
// where it finds none.
func loadConfig(c *cobra.Command, g *globals, isInit bool) (*config, error) {
	envConfig, envProfile := os.Getenv("DROMAS_CONFIG"), os.Getenv("DROMAS_PROFILE")
	switch {
	case g.config != "" && g.profile != "":
		return nil, wrong(c, "--config and --profile each name a config file: give one")
	case g.config == "" && g.profile == "" && envConfig != "" && envProfile != "":
		return nil, &configError{msg: "$DROMAS_CONFIG and $DROMAS_PROFILE each name a config file: set one"}
	}
	cfg := &config{flagged: map[string]map[string]string{}, values: map[string]setting{}}
	named := g.config
	if named == "" && g.profile == "" {
		named = envConfig
	}
	if named == "" {
		cfg.profile = g.profile
		if cfg.profile == "" {
			cfg.profile = envProfile
		}
		if cfg.profile == "" {
			cfg.profile = "default"
		}
		if !profileGrammar.MatchString(cfg.profile) {
			return nil, &configError{key: "profile", msg: fmt.Sprintf("%.60q names no profile: one is letters, digits, - and _", cfg.profile)}
		}
	}
	file := named
	if named != "" {
		abs, err := filepath.Abs(named)
		if err != nil {
			return nil, &configError{file: named, msg: err.Error()}
		}
		file = abs
	} else if dir, err := configDir(); err == nil {
		file = filepath.Join(dir, cfg.profile+".toml")
	}
	var fc fileConfig
	found := false
	if file != "" {
		_, err := os.Stat(file) //nolint:gosec // the config file its owner named
		switch {
		case err == nil:
			found = true
		case !errors.Is(err, fs.ErrNotExist):
			return nil, &configError{file: file, msg: err.Error()}
		case named != "":
			return nil, &configError{file: file, msg: fmt.Sprintf("no config file at %s", file)}
		case isInit:
			if err := writeTemplate(file, cfg.profile, g); err != nil {
				return nil, &configError{file: file, msg: err.Error()}
			}
			found = true
		case g.baseDir == "" || g.mailDir == "":
			return nil, &configError{file: file, msg: fmt.Sprintf("no config file at %s: dromas init makes one", file)}
		}
	}
	rel := "."
	if found {
		if err := readConfig(file, &fc); err != nil {
			return nil, err
		}
		cfg.file = file
		rel = filepath.Dir(file)
	}
	if err := cfg.resolve(g, &fc, rel); err != nil {
		return nil, err
	}
	return cfg, nil
}

// readConfig reads a config file, refusing one others can write, one that is not
// TOML, and any key it does not know.
func readConfig(file string, fc *fileConfig) error {
	if err := client.CheckConfigFile(file); err != nil {
		return &configError{file: file, msg: err.Error()}
	}
	md, err := toml.DecodeFile(file, fc)
	if err != nil {
		msg := err.Error()
		if raw, rerr := os.ReadFile(file); rerr == nil && strings.Contains(string(raw), `\`) { //nolint:gosec // the config file its owner named
			msg += `; a backslash in a "..." string is an escape, so write a Windows path in single quotes`
		}
		return &configError{file: file, msg: msg}
	}
	if un := md.Undecoded(); len(un) > 0 {
		return &configError{file: file, key: un[0].String(), msg: fmt.Sprintf("%s: this client reads no key %s", file, un[0])}
	}
	return nil
}

// resolve takes each setting from the first place that says it, and refuses
// what no mailbox can run with.
func (cfg *config) resolve(g *globals, fc *fileConfig, rel string) error {
	bad := func(key, format string, a ...any) error {
		return &configError{file: cfg.file, key: key, msg: fmt.Sprintf(format, a...)}
	}
	path := func(key, flag, file, def string) (string, error) {
		var p, from string
		var err error
		switch {
		case flag != "":
			p, from = flag, "flag"
			p, err = expand(p, ".")
			if err == nil {
				p, err = filepath.Abs(p)
			}
		case file != "":
			from = "file"
			p, err = expand(file, rel)
			if err == nil {
				p, err = filepath.Abs(p)
			}
		case def != "":
			p, from = def, "default"
		default:
			return "", bad(key, "%s is required, by --%s or in the config file: where a mailbox's files go is never left to be guessed", key, key)
		}
		if err != nil {
			return "", bad(key, "%s: %v", key, err)
		}
		cfg.values[key] = setting{p, from}
		return p, nil
	}
	var err error
	if cfg.dir, err = path("base-dir", g.baseDir, fc.BaseDir, ""); err != nil {
		return err
	}
	if cfg.layout.Mail, err = path("mail-dir", g.mailDir, fc.MailDir, ""); err != nil {
		return err
	}
	if cfg.layout.IdentityFile, err = path("identity-file", g.identityFile, fc.IdentityFile, filepath.Join(cfg.dir, "identity")); err != nil {
		return err
	}
	if cfg.layout.RotatingKeys, err = path("rotating-keys-dir", g.rotatingKeysDir, fc.RotatingKeysDir, filepath.Join(cfg.dir, "keys")); err != nil {
		return err
	}
	if cfg.layout.Inbox, err = path("inbox-dir", g.inbox, fc.InboxDir, filepath.Join(cfg.layout.Mail, "inbox")); err != nil {
		return err
	}
	if err := client.CheckLayout(cfg.dir, cfg.layout); err != nil {
		return bad("", "%v", err)
	}

	server, from := client.DefaultBase, "default"
	switch env := os.Getenv("DROMAS_SERVER_URL"); {
	case g.server != "":
		server, from = g.server, "flag"
	case env != "":
		server, from = env, "environment"
	case fc.Server != "":
		server, from = fc.Server, "file"
	}
	if err := client.CheckServerURL(server); err != nil {
		return bad("server", "%v", err)
	}
	cfg.server = server
	cfg.values["server"] = setting{server, from}

	lifetime, from := g.prekeyLifetime, "flag"
	if lifetime == "" {
		lifetime, from = fc.PrekeyLifetime, "file"
	}
	if lifetime == "" {
		cfg.values["prekey-lifetime"] = setting{formatTime(protocol.PrekeyLifetime), "default"}
	} else {
		d, err := parseTime(lifetime)
		if err != nil {
			return bad("prekey-lifetime", "prekey-lifetime: %v", err)
		}
		if d < client.MinPrekeyLifetime || d > protocol.PrekeyLifetime {
			return bad("prekey-lifetime", "a prekey lifetime is from %s to %s, and %s is not", formatTime(client.MinPrekeyLifetime), formatTime(protocol.PrekeyLifetime), lifetime)
		}
		cfg.lifetime = d
		cfg.values["prekey-lifetime"] = setting{formatTime(d), from}
	}

	cfg.policy = client.Policy{Unknown: fc.Policy.Unknown, Server: fc.Policy.Server}
	if p := cfg.policy.Unknown; p != "" && p != client.LandHeld && p != client.LandInbox {
		return bad("policy.unknown", `policy.unknown is %q or %q, and %.40q is not`, client.LandHeld, client.LandInbox, p)
	}
	if p := cfg.policy.Server; p != "" && p != client.AdmitAll && p != client.AdmitAllowed {
		return bad("policy.server", `policy.server is %q or %q, and %.40q is not`, client.AdmitAll, client.AdmitAllowed, p)
	}
	for key, v := range map[string]string{"policy.unknown": cfg.policy.Unknown, "policy.server": cfg.policy.Server} {
		if v != "" {
			cfg.values[key] = setting{v, "file"}
		}
	}

	table := func(command, key, value string) {
		if cfg.flagged[command] == nil {
			cfg.flagged[command] = map[string]string{}
		}
		cfg.flagged[command][key] = value
		cfg.values[strings.ReplaceAll(command, " ", ".")+"."+key] = setting{value, "file"}
	}
	if v := fc.Recv.Wait; v != "" {
		if _, err := time.ParseDuration(v); err != nil {
			return bad("recv.wait", "recv.wait: %v", err)
		}
		table("recv", "wait", v)
	}
	if v := fc.Recv.DrainTime; v != "" {
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			return bad("recv.drain-time", "recv.drain-time takes a duration above zero, such as 15m, and %.40q is not", v)
		}
		table("recv", "drain-time", v)
	}
	headers := func(command string, v *bool) {
		if v != nil {
			table(command, "headers", fmt.Sprint(*v))
		}
	}
	headers("recv", fc.Recv.Headers)
	headers("inbox list", fc.Inbox.List.Headers)
	headers("inbox show", fc.Inbox.Show.Headers)
	return nil
}

// apply sets a command's flags from its table, where the command line did not.
func (cfg *config) apply(c *cobra.Command) error {
	name := strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
	for key, value := range cfg.flagged[name] {
		if c.Flags().Changed(key) {
			continue
		}
		if err := c.Flags().Set(key, value); err != nil {
			return &configError{file: cfg.file, key: name + "." + key, msg: err.Error()}
		}
	}
	return nil
}

// tomlString writes s as TOML: a literal string where it holds a backslash, as a
// Windows path does, which needs no escape there, and otherwise a basic one.
func tomlString(s string) string {
	if strings.Contains(s, `\`) && !strings.ContainsAny(s, "'\n\r\t") {
		return "'" + s + "'"
	}
	b, _ := json.Marshal(s) //nolint:errchkjson // a string marshals
	return string(b)
}

// homeBase is an XDG base directory for a template: its variable's value, or its
// default under ~/, written so.
func homeBase(env, def string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	return "~/" + def
}

// writeTemplate writes a profile's config file where it has none: every path in
// full, and every default commented out, as OpenSSH ships sshd_config, since an
// active default would hold its value through an upgrade. What the command line
// says goes in as it says it.
func writeTemplate(file, profile string, g *globals) error {
	join := func(a, b string) string {
		if strings.HasPrefix(a, "~/") {
			return a + "/" + b
		}
		return filepath.Join(a, b)
	}
	abs := func(p string) string {
		if p == "" || strings.HasPrefix(p, "~") {
			return p
		}
		a, err := filepath.Abs(p)
		if err != nil {
			return p
		}
		return a
	}
	first := func(a, b string) string {
		if a != "" {
			return abs(a)
		}
		return b
	}
	base := first(g.baseDir, join(homeBase("XDG_DATA_HOME", ".local/share"), filepath.Join("dromas", profile)))
	mail := first(g.mailDir, join(base, "mail"))
	identity := first(g.identityFile, join(base, "identity"))
	keys := first(g.rotatingKeysDir, join(homeBase("XDG_STATE_HOME", ".local/state"), filepath.Join("dromas", profile, "keys")))
	var b strings.Builder
	fmt.Fprintf(&b, "# dromas profile %q. Commented lines show the default: uncomment to change.\n", profile)
	b.WriteString("# `dromas config` prints every value as resolved.\n")
	fmt.Fprintf(&b, "base-dir = %s\nmail-dir = %s\nidentity-file = %s\nrotating-keys-dir = %s\n\n", tomlString(base), tomlString(mail), tomlString(identity), tomlString(keys))
	fmt.Fprintf(&b, "# inbox-dir = %s\n", tomlString(join(mail, "inbox")))
	fmt.Fprintf(&b, "# server = %s\n", tomlString(client.DefaultBase))
	fmt.Fprintf(&b, "# prekey-lifetime = %s\n", tomlString(formatTime(protocol.PrekeyLifetime)))
	b.WriteString("\n# [policy]\n")
	fmt.Fprintf(&b, "# unknown = %s\n# server = %s\n", tomlString(client.LandHeld), tomlString(client.AdmitAll))
	b.WriteString("\n# [recv]\n")
	fmt.Fprintf(&b, "# drain-time = %s\n# headers = false\n", tomlString(strings.TrimSuffix(client.DefaultDrainTime.String(), "0s")))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil { //nolint:gosec // the config directory of a profile whose name is held to profileGrammar
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*") //nolint:gosec // as above
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close() //nolint:errcheck,gosec // the write error wins
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file) //nolint:gosec // as above
}

// configCmd prints every setting as this run resolved it.
func (e *cli) configCmd() *cobra.Command {
	return command("config", "print every setting as resolved, and where each came from",
		`Print a "config" line: the config file, the profile, and each setting's value and
where it came from, "flag", "environment", "file" or "default". Each comes from the
first of those that says it. The policy this client takes from a restore is the
state file's, and status says the policy in force. Nothing here touches the mailbox.`,
		nargs(0, 0), func(*cobra.Command, []string) error {
			return e.out(line("config", "file", omit(e.cfg.file), "profile", omit(e.cfg.profile), "values", e.cfg.values))
		})
}

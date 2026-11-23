# Configuration reference

`dromas` reads its settings from command-line flags, environment variables, and a TOML config file. This page describes the config file and lists every key that it accepts. To set up a mailbox, see the [User guide](guide.md).

## Config file

`dromas init` creates a config file for the mailbox:

```toml
# dromas profile "default". Commented lines show the default: uncomment to change.
# `dromas config` prints the settings in effect.
base-dir = "~/.local/share/dromas/default"
mail-dir = "~/.local/share/dromas/default/mail"
identity-file = "~/.local/share/dromas/default/identity"
rotating-keys-dir = "~/.local/state/dromas/default/keys"

# inbox-dir = "~/.local/share/dromas/default/mail/inbox"
# server = "https://dromas.dev"
# prekey-lifetime = "8d"
# certificate-lifetime = "45d1h"

# [policy]
# unknown = "held"
# admit = "all"

# [recv]
# drain-time = "15m"
# headers = false
```

`dromas init` writes the file only if no file exists, so you can make the file read-only. `dromas init` always writes the four path keys `base-dir`, `mail-dir`, `identity-file`, and `rotating-keys-dir`: each is the path that you pass to `dromas init`, or else a path that `dromas init` chooses. That path is under `~/.local/share` for `base-dir`, under `base-dir` for `mail-dir` and `identity-file`, and under `~/.local/state` for `rotating-keys-dir`. The values in the file take the place of the defaults that this page gives for those keys, such as `<base-dir>/keys` for `rotating-keys-dir`. `dromas init` also writes the values that you pass to its `--inbox-dir`, `--server`, `--ca-file`, `--prekey-lifetime`, and `--certificate-lifetime` flags. When `XDG_DATA_HOME` or `XDG_STATE_HOME` is set to an absolute path, `dromas init` writes paths under that directory instead of under `~/.local/share` or `~/.local/state`. On Windows, if those variables aren't set, `dromas init` puts the mailbox in `%LocalAppData%\dromas\<profile>` and its keys in the mailbox's `keys` directory. Neither the file nor the mailbox is in `%AppData%`, because a roaming profile copies `%AppData%` to every machine that the user signs in to, and a mailbox runs on one machine only.

### Location

The config file for the default profile is `~/.config/dromas/default.toml`, or `$XDG_CONFIG_HOME/dromas/default.toml` when `XDG_CONFIG_HOME` is set to an absolute path. On Windows, if `XDG_CONFIG_HOME` isn't set, the file is `%LocalAppData%\dromas\default.toml`, next to the mailbox. Each profile is a separate mailbox with its own file:

- `--profile <name>` or `DROMAS_PROFILE` selects `<name>.toml` in the same directory. A profile name can contain only letters, digits, `-`, and `_`.
- `--config <file>` or `DROMAS_CONFIG` selects the file at that path.

A flag takes precedence over an environment variable. It's an error for two flags, or two environment variables, to name both a profile and a file. `dromas` never looks for a config file in the working directory.

`dromas` rejects a config file in any of these cases: other users can write to the file, a user other than you or root owns the file, the file isn't valid TOML, or the file contains a key that this page doesn't list. An invalid config file stops every command with exit status 2 and a `config_error` line.

### Precedence

Each setting takes its value from the first of these sources that sets it:

1. A command-line flag
2. An environment variable
3. The config file
4. The default

`dromas config` prints the value of each setting in effect and where the value came from. It leaves out a setting that nothing sets: `ca-file`, `askpass`, `certificate-lifetime`, and each key of the `[policy]`, `[recv]`, and `[inbox.*]` tables appear only when a source sets them.

### Paths

In the config file, a relative path is relative to the directory of the config file. On the command line, a relative path is relative to the working directory. A path that starts with `~/` is relative to your home directory. `dromas` doesn't expand anything else in a path, so `$VAR` stays as written, and `~user` is an error.

In TOML, a backslash in a double-quoted string starts an escape sequence, so write a Windows path in single quotes: `base-dir = 'C:\Users\me\dromas'`.

### Durations and times

A key of type **time** accepts one or more numbers, each followed by a unit (`s`, `m`, `h`, `d`, or `w`), and adds them together. This is the time format that sshd uses: for example, `90m` or `2d2h`. A number without a unit means seconds.

A key of type **duration** accepts a Go duration, with units up to `h`: for example, `90s`, `15m`, or `1h30m`.

A setting that only modifies another setting has no effect when the other setting isn't set. For example, `recv.exec-timeout` modifies `recv.exec`, and a webhook file modifies `recv.webhook`.

## Keys

### `base-dir`

- Type: path
- Default: none; the key is required
- Flag: `--base-dir`

The directory that holds the mailbox's state file and lists (`state`, `allowed`, `blocked`, `aliases`), and two other files of the mailbox: `partial`, the ciphertext of the message that a fetch was reading, so that a cut fetch can resume; and, on Windows only, `gate`, an empty file that the mailbox locks so that reads don't hold back a waiting writer. The defaults of `identity-file` and `rotating-keys-dir` are paths in this directory. The mailbox's lock is a `flock` on `state`, so the lock is only as reliable as the file system's locks. Don't open one base directory from two machines at the same time; SQLite and Dovecot give the same warning about their own files.

### `mail-dir`

- Type: path
- Default: none; the key is required
- Flag: `--mail-dir`

The directory that holds `inbox/`, `held/`, `refused/`, and `sent/`. The mailbox keeps every message in plaintext and never deletes a message.

### `identity-file`

- Type: path
- Default: `<base-dir>/identity`
- Flag: `--identity-file`

The identity's private key, as an OpenSSH key file. The identity is the mailbox's address, so back up this file. Like ssh, `dromas` rejects the file if other users can access it.

### `rotating-keys-dir`

- Type: path
- Default: `<base-dir>/keys`
- Flag: `--rotating-keys-dir`

The directory that holds `prekeys/`, `work/`, and `tls-ticket`. This directory doesn't need a backup, because a restored mailbox makes new keys. Forward secrecy depends on the deletion of retired prekeys: a copy of `prekeys/` in a backup still decrypts the messages sealed to its keys after the mailbox deletes them (`PROTOCOL.md` §15.3).

### `inbox-dir`

- Type: path
- Default: `<mail-dir>/inbox`
- Flag: `--inbox-dir`

The directory for delivered messages. To give an agent the inbox without `held/` or `refused/`, point the agent at this directory. If the inbox is on its own volume, set `inbox-dir` to a folder inside the volume, not to the mount point.

### `server`

- Type: string, a URL
- Default: `https://dromas.dev`
- Flag: `--server`
- Environment: `DROMAS_SERVER_URL`

The server that the mailbox uses. The URL is a scheme, a host and an optional port, with no user information, query or fragment, and no path but `/`. It must use `https`, or `http` with a loopback IP address (`127.0.0.0/8` or `::1`), never a name such as `localhost`.

### `ca-file`

- Type: path
- Default: the system's certificate authorities
- Flag: `--ca-file`
- Environment: `DROMAS_CA_FILE`

A PEM file of certificates. When this key is set, `dromas` trusts only these certificates to verify the server's TLS certificate.

### `prekey-lifetime`

- Type: time, from `1d` to `8d`
- Default: `8d`
- Flag: `--prekey-lifetime`

How long each new prekey is valid. A shorter lifetime limits how long a stolen prekey stays usable. `dromas recv` renews the prekey once a day, so a lifetime shorter than `2d2h` requires `recv` to run more often. To find out how often, run `dromas prekey lifetime`.

### `certificate-lifetime`

- Type: time, more than `7d1h` and at most `45d1h`
- Default: `45d1h`, or the longest lifetime that the server accepts, if that's shorter
- Flag: `--certificate-lifetime`
- Environment: `DROMAS_CERTIFICATE_LIFETIME`

For a mailbox that signs under a certificate (`dromas init --offline`), how long each certificate that the mailbox makes is valid.

### `agent`

- Type: socket path, `$VAR`, `SSH_AUTH_SOCK`, or `none`
- Default: `$XDG_RUNTIME_DIR/dromas/agent.sock`, or `dromas/agent.sock` in your cache directory; `none` on Windows
- Flag: `--agent`
- Environment: `DROMAS_AGENT`

The socket of the ssh-agent that holds the identity's key. `dromas` interprets the value the same way that ssh interprets `IdentityAgent` in ssh_config. `dromas agent start` starts an agent at this socket. See [Protect the identity key](guide.md#protect-the-identity-key).

### `askpass`

- Type: program
- Default: none
- Flag: `--askpass`
- Environment: `DROMAS_ASKPASS`

A program that prints the passphrase of the identity's key file, as `SSH_ASKPASS` does for ssh. If the name has no path separator, `dromas` looks up the program on `PATH` when it runs the program.

### `no-prompt`

- Type: boolean
- Default: `false`
- Flag: `--no-prompt`
- Environment: `DROMAS_NO_PROMPT`

When `true`, `dromas` never prompts for the passphrase at a terminal. The `askpass` program still runs.

### `add-keys-to-agent`

- Type: `no`, `yes`, or a time of at least `1s`
- Default: `no`
- Flag: `--add-keys-to-agent`
- Environment: `DROMAS_ADD_KEYS_TO_AGENT`

Whether to add the identity's key to the agent after a passphrase unlocks the key file, as `AddKeysToAgent` does in ssh_config. A time sets how long the agent keeps the key.

### `unsafe-inbox-permissions`

- Type: boolean
- Default: `false`
- Flag: `--unsafe-inbox-permissions`
- Environment: `DROMAS_UNSAFE_INBOX_PERMISSIONS`

When `true`, every command runs on the mailbox even when other users can write to one of its directories, an entry in `outbox/`, or a message file in `inbox-dir` or `held/`, or when another user owns one. Anyone who can write to these locations can add a file that `dromas` takes as yours, such as a list, a key, a message that `dromas` signs and sends, or a message that appears to be delivered.

When `false`, a `base-dir` or `mail-dir` that you made yourself with group write is refused, for example one made under a umask of `002`, a shared folder with group write and the `setgid` bit, or a Windows folder that inherits Modify for Authenticated Users. To fix it, run `chmod go-w` on the directory, or on Windows give the folder an access list that only you, SYSTEM, and Administrators can write.

### `unsafe-state-permissions`

- Type: boolean
- Default: `false`
- Flag: `--unsafe-state-permissions`
- Environment: `DROMAS_UNSAFE_STATE_PERMISSIONS`

When `true`, `dromas` opens `state`, `gate`, and `partial` even when other users can access them, and opens the lists even when other users can write to them. It also opens any of these files when another user owns it. No setting allows a private key that other users can access.

### `policy.unknown`

- Type: `"held"` or `"inbox"`
- Default: `"held"`

Where a message from an unknown sender goes. A sender is unknown until you write to them, name them with `dromas alias add`, or allow them with `dromas peer allow`. With `"held"`, the message waits in `held/`. With `"inbox"`, the message is delivered to `inbox/` like any other message.

### `policy.admit`

- Type: `"all"` or `"allowed"`
- Default: `"all"`

Which senders the server accepts messages from. With `"allowed"`, the server rejects messages from every sender that you haven't allowed, so the server sees your list of allowed senders.

### `recv.wait`

- Type: duration
- Default: none
- Flag: `dromas recv --wait`

The default value of `recv --wait`: how long `recv` waits for a message.

### `recv.drain-time`

- Type: duration, greater than zero
- Default: `15m`
- Flag: `dromas recv --drain-time`

How long one drain of the server runs before it stops and leaves the rest for the next `recv`. A drain is one pass in which `recv`, holding the mailbox's lock, lists the messages that wait on the server and fetches each one.

### `recv.headers`

- Type: boolean
- Default: `false`
- Flag: `dromas recv --headers`

When `true`, `recv` prints the checked fields and the body size of each message, but not the body or the subject.

### `recv.exec`

- Type: array of strings
- Default: none

The program to run for each message, followed by its arguments, for example `exec = ["/usr/local/bin/bot", "--quiet"]`. The program receives the message's `message` line on stdin. No shell runs, and `dromas` doesn't split or expand the arguments. On Windows, `cmd.exe` runs a `.bat` or `.cmd` program, and `cmd.exe` does split and expand its arguments. `recv` runs the program only when it waits: with `--wait` or `recv.wait`, or with `--follow`. It also runs the program only when the command line has no `--exec` flag, which replaces the program. See [Run a program for each message](guide.md#run-a-program-for-each-message).

### `recv.exec-timeout`

- Type: duration, greater than zero
- Default: `5m`
- Flag: `dromas recv --exec-timeout`

How long one run of the `recv.exec` program can take before `recv` kills the run.

### `recv.webhook`

- Type: string, an `http` or `https` URL
- Default: none
- Flag: `dromas recv --webhook`

A URL that `recv` posts the `message` line of each message to, whether or not `recv.exec` is set. `recv` uses this key only when it waits: with `--wait` or `recv.wait`, or with `--follow`. A `--webhook` flag on the command line replaces it. A URL that contains a user name or a password is an error; put a token in `recv.webhook-header-file` instead. See [Send each message to a webhook](guide.md#send-each-message-to-a-webhook).

### `recv.webhook-header-file`

- Type: path
- Default: none
- Flag: `dromas recv --webhook-header-file`

A file that contains exactly one line, `Name: Value`. A trailing line ending is optional. Each webhook post carries this header, for example `Authorization: Bearer <token>`. If other users can access the file, `recv` rejects the file and runs nothing, as `ssh` rejects a private key. `recv` also rejects a header that each post sets itself, in any letter case: `Content-Type`, `Content-Length`, `Host`, `Transfer-Encoding`, and any `webhook-*` header. Each post also sets `User-Agent` to `dromas/<version>`; a header file that sets `User-Agent` replaces that value.

### `recv.webhook-secret-file`

- Type: path
- Default: none
- Flag: `dromas recv --webhook-secret-file`

A file that contains a [Standard Webhooks](https://www.standardwebhooks.com/) secret: `whsec_` followed by the standard, padded base64 encoding of 24 to 64 random bytes. When this key is set, each post carries the `webhook-id`, `webhook-timestamp`, and `webhook-signature` headers of the Standard Webhooks `v1` scheme, so the endpoint can verify the post with any Standard Webhooks library. `recv` rejects this file in the same cases as `recv.webhook-header-file`.

### `recv.log-file`

- Type: path
- Default: none
- Flag: `dromas recv --log-file`

A file that `recv` appends its lines to: the lines that it would otherwise print on stdout and stderr. Use this key when the service manager doesn't keep a log of its own, such as Windows Task Scheduler. If the file doesn't exist, `recv` creates it, readable only by you. If the file exists, `recv` keeps its permissions and appends to it. Nothing rotates the file. See [Run as a service](guide.md#run-as-a-service).

### `inbox.list.headers`

- Type: boolean
- Default: `false`
- Flag: `dromas inbox list --headers`

When `true`, `inbox list` prints the checked fields and the body size of each message, but not the body or the subject.

### `inbox.show.headers`

- Type: boolean
- Default: `false`
- Flag: `dromas inbox show --headers`

When `true`, `inbox show` prints the checked fields and the body size of the message, but not the body or the subject.

## Environment variables

In addition to the variables listed with each key in the preceding sections, `dromas` reads these variables:

| Variable | Description |
|---|---|
| `DROMAS_CONFIG` | The config file to read, like `--config` |
| `DROMAS_PROFILE` | The profile to read, like `--profile` |
| `DROMAS_REVOCATION_FILE` | The file that `dromas init` writes the revocation record to, like `init --revocation-file` |
| `XDG_CONFIG_HOME` | The parent of the config directory, when set to an absolute path |
| `XDG_DATA_HOME` | The parent of the paths that `dromas init` writes for `base-dir`, `mail-dir`, and `identity-file`, when set to an absolute path |
| `XDG_STATE_HOME` | The parent of the path that `dromas init` writes for `rotating-keys-dir`, when set to an absolute path |
| `XDG_RUNTIME_DIR` | The parent of the default `agent` socket |
| `LOCALAPPDATA` | On Windows, the parent of the config directory when `XDG_CONFIG_HOME` isn't set, and of the default `base-dir` when `XDG_DATA_HOME` isn't set, when set to an absolute path |

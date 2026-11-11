# Configuration reference

`dromas` reads its settings from command-line flags, environment variables and a TOML config file. This page describes the config file and lists every key it accepts. For how to set up a mailbox, see the [User guide](guide.md).

## Config file

`dromas init` creates a config file for the mailbox:

```toml
# dromas profile "default". Commented lines show the default: uncomment to change.
# `dromas config` prints every value as resolved.
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

`dromas init` writes the file only when none exists, so you can make the file read-only. Flags you give `dromas init`, such as `--server`, are written into the file. When `XDG_DATA_HOME` or `XDG_STATE_HOME` is an absolute path, `dromas init` writes paths under that directory instead of `~/.local/share` or `~/.local/state`.

### Location

The config file for the default profile is `~/.config/dromas/default.toml`, or `$XDG_CONFIG_HOME/dromas/default.toml` when `XDG_CONFIG_HOME` is an absolute path. Each profile is a separate mailbox with its own file:

- `--profile <name>` or `DROMAS_PROFILE` selects `<name>.toml` in the same directory. A profile name contains only letters, digits, `-` and `_`.
- `--config <file>` or `DROMAS_CONFIG` selects the file at that path.

Give a profile or a file, not both. `dromas` never reads a config file from the working directory.

`dromas` rejects a config file that other users can write or that another user owns, a file that is not valid TOML, and any key this page does not list. An invalid config file stops every command with exit status 2 and a `config_error` line.

### Precedence

Each setting comes from the first of these that sets it:

1. A command-line flag
2. An environment variable
3. The config file
4. The default

`dromas config` prints each setting's value and where the value came from.

### Paths

In the config file, a relative path is relative to the config file's directory. On the command line, a relative path is relative to the working directory. A path that starts with `~/` is relative to your home directory. `dromas` expands nothing else in a path, so `$VAR` stays as written, and `~user` is an error.

In TOML, a backslash in a double-quoted string starts an escape, so write a Windows path in single quotes: `base-dir = 'C:\Users\me\dromas'`.

### Durations and times

A key that takes a **time** accepts numbers, each with a unit, `s`, `m`, `h`, `d` or `w`, added together, as sshd writes a time: `90m`, `2d2h`. A number with no unit is seconds.

A key that takes a **duration** accepts a Go duration, with units up to `h`: `90s`, `15m`, `1h30m`.

## Keys

### `base-dir`

- Type: path
- Default: none; the key is required
- Flag: `--base-dir`

The directory of the mailbox's state file and lists (`state`, `allowed`, `blocked`, `aliases`). `identity-file` and `rotating-keys-dir` default to paths under it. On Linux and macOS, `dromas` rejects a `base-dir` on a network filesystem, because the mailbox's lock is a `flock`.

### `mail-dir`

- Type: path
- Default: none; the key is required
- Flag: `--mail-dir`

The directory of `inbox/`, `held/`, `refused/` and `sent/`. The mailbox keeps every message in plaintext and never deletes one.

### `identity-file`

- Type: path
- Default: `<base-dir>/identity`
- Flag: `--identity-file`

The identity's private key, an OpenSSH key file. The identity is the mailbox's address: back up this file. `dromas` rejects the file when other users can access it, as ssh does.

### `rotating-keys-dir`

- Type: path
- Default: `<base-dir>/keys`
- Flag: `--rotating-keys-dir`

The directory of `prekeys/`, `work/` and `tls-ticket`. Never back up or sync this directory: a copy of `prekeys/` undoes forward secrecy.

### `inbox-dir`

- Type: path
- Default: `<mail-dir>/inbox`
- Flag: `--inbox-dir`

The directory of delivered messages. Point an agent at this directory to give it the inbox without `held/` or `refused/`. When the inbox is on its own volume, set `inbox-dir` to a folder inside the volume, not the mount point.

### `server`

- Type: URL
- Default: `https://dromas.dev`
- Flag: `--server`
- Environment: `DROMAS_SERVER_URL`

The server the mailbox uses. The URL is `https`, or `http` only to this machine by its address (`127.0.0.0/8` or `::1`).

### `ca-file`

- Type: path
- Default: the system's certificate authorities
- Flag: `--ca-file`
- Environment: `DROMAS_CA_FILE`

A PEM file of certificates. When set, `dromas` trusts only these certificates for the server's TLS certificate.

### `prekey-lifetime`

- Type: time, from `6h` to `8d`
- Default: `8d`
- Flag: `--prekey-lifetime`

How long each new prekey is valid. A shorter lifetime shortens how long a stolen prekey stays usable. `dromas recv` renews the prekey once a day, so a lifetime under `2d2h` needs `recv` to run more often: `dromas prekey lifetime` says how often.

### `certificate-lifetime`

- Type: time, more than `7d1h` and at most `45d1h`
- Default: the longest lifetime the server accepts
- Flag: `--certificate-lifetime`
- Environment: `DROMAS_CERTIFICATE_LIFETIME`

How long each certificate the mailbox makes is valid, for a mailbox that signs under a certificate (`dromas init --offline`).

### `agent`

- Type: socket path, `$VAR`, `SSH_AUTH_SOCK` or `none`
- Default: `$XDG_RUNTIME_DIR/dromas/agent.sock`, or `dromas/agent.sock` in your cache directory; `none` on Windows
- Flag: `--agent`
- Environment: `DROMAS_AGENT`

The socket of the ssh-agent that holds the identity's key, read as ssh_config's `IdentityAgent`. `dromas agent start` starts an agent at this socket. See [Protect the identity key](guide.md#protect-the-identity-key).

### `askpass`

- Type: program
- Default: none
- Flag: `--askpass`
- Environment: `DROMAS_ASKPASS`

A program that prints the passphrase of the identity's key file, as ssh's `SSH_ASKPASS` does. A name without a path separator is looked up on `PATH` when the program runs.

### `no-prompt`

- Type: boolean
- Default: `false`
- Flag: `--no-prompt`
- Environment: `DROMAS_NO_PROMPT`

When `true`, `dromas` never prompts for the passphrase at a terminal. `askpass` still runs.

### `add-keys-to-agent`

- Type: `no`, `yes`, or a time of at least `1s`
- Default: `no`
- Flag: `--add-keys-to-agent`
- Environment: `DROMAS_ADD_KEYS_TO_AGENT`

After a passphrase unlocks the identity's key file, add the key to the agent, as ssh_config's `AddKeysToAgent` does. A time sets how long the agent keeps the key.

### `unsafe-inbox-permissions`

- Type: boolean
- Default: `false`
- Flag: `--unsafe-inbox-permissions`
- Environment: `DROMAS_UNSAFE_INBOX_PERMISSIONS`

When `true`, `dromas` reads `inbox-dir` and `held/` even when other users can write to them, and reads message files that other users can access. Anyone who can write there can add a message that reads as delivered.

### `unsafe-state-permissions`

- Type: boolean
- Default: `false`
- Flag: `--unsafe-state-permissions`
- Environment: `DROMAS_UNSAFE_STATE_PERMISSIONS`

When `true`, `dromas` opens `state`, `gate`, `partial` and the lists even when other users can access them or another user owns them. No setting allows a private key that other users can access.

### `policy.unknown`

- Type: `"held"` or `"inbox"`
- Default: `"held"`

Where a message from an unknown sender goes. A sender is unknown until you write to them, name them with `dromas alias add` or allow them with `dromas peer allow`. With `"held"`, the message waits in `held/`. With `"inbox"`, the message is delivered to `inbox/` like any other.

### `policy.admit`

- Type: `"all"` or `"allowed"`
- Default: `"all"`

Which senders the server accepts messages from. With `"allowed"`, the server rejects every sender you have not allowed, so the server sees your list of allowed senders.

### `recv.wait`

- Type: duration
- Default: none
- Flag: `dromas recv --wait`

The default for `recv --wait`: how long `recv` waits for a message.

### `recv.drain-time`

- Type: duration, above zero
- Default: `15m`
- Flag: `dromas recv --drain-time`

How long one drain of the server runs before it stops for the next `recv`.

### `recv.headers`

- Type: boolean
- Default: `false`
- Flag: `dromas recv --headers`

When `true`, `recv` prints each message's checked fields and body size, without the body or subject.

### `recv.exec`

- Type: array of strings
- Default: none

A program to run for each message, then its arguments, for example `exec = ["/usr/local/bin/bot", "--quiet"]`. The program gets the message's `message` line on stdin. No shell runs, and `dromas` neither splits nor expands the arguments. On Windows, `cmd.exe` runs a `.bat` or `.cmd` program, and `cmd.exe` does split and expand its arguments. `recv` runs the program only with `--wait` or `--follow`, and only when the command line has no `--exec`, which replaces the program. See [Run a program for each message](guide.md#run-a-program-for-each-message).

### `recv.exec-timeout`

- Type: duration, above zero
- Default: `5m`
- Flag: `dromas recv --exec-timeout`

How long one run of the `recv.exec` program may take before `recv` kills the run. `recv.exec-timeout` without `recv.exec` is an error.

### `inbox.list.headers`

- Type: boolean
- Default: `false`
- Flag: `dromas inbox list --headers`

When `true`, `inbox list` prints each message's checked fields and body size, without the body or subject.

### `inbox.show.headers`

- Type: boolean
- Default: `false`
- Flag: `dromas inbox show --headers`

When `true`, `inbox show` prints the message's checked fields and body size, without the body or subject.

## Environment variables

Besides the variables listed with each key above, `dromas` reads these:

| Variable | Description |
|---|---|
| `DROMAS_CONFIG` | The config file to read, as `--config` |
| `DROMAS_PROFILE` | The profile to read, as `--profile` |
| `DROMAS_REVOCATION_FILE` | The file `dromas init` writes the revocation record to, as `init --revocation-file` |
| `XDG_CONFIG_HOME` | The parent of the config directory, when an absolute path |
| `XDG_DATA_HOME` | The parent of the paths `dromas init` writes for `base-dir`, `mail-dir` and `identity-file`, when an absolute path |
| `XDG_STATE_HOME` | The parent of the path `dromas init` writes for `rotating-keys-dir`, when an absolute path |
| `XDG_RUNTIME_DIR` | The parent of the default `agent` socket |

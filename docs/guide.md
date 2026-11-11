# User guide

This guide covers what to do after you [install](install/README.md) `dromas` and create a mailbox: where the mailbox keeps its files, how to back it up, how to protect its key, who can write to it, and how to run it as a service. Every key of the config file is in the [Configuration reference](configuration.md), and every command is in the [Command reference](commands/dromas.md).

## Mailbox files

`dromas init` creates a mailbox and writes its config file, `~/.config/dromas/default.toml`. The config file names the mailbox's directories:

- `base-dir`: the state file and the lists of senders
- `mail-dir`: the messages, in `inbox/`, `held/`, `refused/` and `sent/`
- `identity-file`: the identity's private key, which is the mailbox's address
- `rotating-keys-dir`: the prekeys, the working key and the TLS session ticket

`dromas status` reports what the mailbox holds. Each file is described at the top of [`client/doc.go`](../client/doc.go). Default paths for each system are planned in [#460](https://github.com/dromas-dev/dromas-client/issues/460).

To run a second mailbox, give it a profile: `dromas init --profile work` creates `~/.config/dromas/work.toml`, and `--profile work` selects that mailbox in every other command.

### Missing directories

When a directory under `mail-dir`, `inbox-dir` or `rotating-keys-dir` is missing, every command that writes the mailbox stops and acknowledges nothing. So an unmounted volume hides no message. If you remove one of these directories on purpose, create it again yourself.

An `inbox-dir` that is itself a mount point stays as an empty directory when the volume is unmounted, and `dromas` cannot tell the difference. Point `inbox-dir` at a folder inside the volume instead.

### File permissions

`dromas` creates each directory readable only by you, and leaves a directory that exists as it is. `dromas` rejects:

- A private key file that other users can access, as ssh does. A Kubernetes or Docker Compose secret needs mode `0400`. No setting turns this off.
- `state`, `gate`, `partial`, `allowed`, `blocked` or `aliases` when other users can access the file or another user owns it. The error gives the command that fixes the file. A list you write yourself needs mode `0600`.
- `inbox-dir` or `held/` when other users can write to it or another user owns it, and a message file in either that other users can access. Anyone who can write there can add a message that reads as delivered.

To turn off the last two, see [`unsafe-state-permissions`](configuration.md#unsafe-state-permissions) and [`unsafe-inbox-permissions`](configuration.md#unsafe-inbox-permissions).

`dromas` writes no `.gitignore`. `dromas` warns when a private key is inside a `.git` working tree: make sure git ignores the key.

## Back up and restore

Back up these files:

- `identity`, the identity's private key: the identity is the address, and a lost identity is a lost address
- your config file
- the lists `allowed`, `blocked` and `aliases`

Never back up or sync `prekeys/` or `tls-ticket`. A copy of `prekeys/` undoes forward secrecy. Anyone who reads `tls-ticket` and is on the network path can pose as your server to this mailbox. Deleting `tls-ticket` is safe. `state` needs no backup.

To restore a mailbox:

1. Put `identity` at the path that `identity-file` names, and the lists beside it if you kept them.
2. Run `dromas prekey renew`. If you also kept `prekeys/`, run `dromas init --restore` instead.

Without `allowed` and `blocked`, the first renewal that `recv` makes replaces the server's ACL with an open one. A sender you blocked then reaches `held/` again until you restore the lists.

### Restore required

The mailbox stops with `restore_required` when `state` does not match `prekeys/`. That happens when `state` is lost, or is restored from an older copy while `prekeys/` was kept. Until you run `dromas init --restore`, the mailbox receives, sends, delivers and renews nothing. `dromas init --restore` adopts the prekeys in `prekeys/` and renews.

A lost `state` loses its replay set, so the mailbox may deliver a message again that it accepted before (`PROTOCOL.md` §15.9). If your agent's work must not happen twice, key the work by `from` and `msg_id`.

## Protect the identity key

An identity is an Ed25519 key. To make a P-256 identity instead, run `dromas init --identity-type ecdsa-sha2-nistp256`. Every client verifies both (`PROTOCOL.md` §4.1).

The identity's key file has no passphrase by default. To add one, run `ssh-keygen -p -f <identity-file>`, without `-m PEM`, so the file stays in the OpenSSH format. A key file with a passphrase leaves the mailbox locked after a reboot, until you unlock the key in one of these ways:

- **The mailbox's own ssh-agent.** `dromas agent start` runs stock `ssh-agent` at the socket that [`agent`](configuration.md#agent) names, separate from your shell's agent, which ssh forwards. `dromas agent add` then loads the key with stock `ssh-add`. Each command prints the command it runs.
- **An askpass program.** [`askpass`](configuration.md#askpass) names a program that prints the passphrase, as `SSH_ASKPASS` does for ssh.
- **A terminal prompt.** When stdin is a terminal and [`no-prompt`](configuration.md#no-prompt) is not set, `dromas` asks for the passphrase.

A command that signs with the identity uses the agent when the agent lists the key, and otherwise the key file:

- When the agent lists the key, the agent signs. If the agent refuses, or does not answer within 2 minutes, the command fails with `identity_locked` and does not try the key file.
- For a key file with a passphrase, the command runs the `askpass` program when one is set, and otherwise prompts at the terminal. With neither, the command fails with `identity_locked`.
- A command keeps a key it unlocked until its process ends, so `recv --wait` asks once.

`dromas agent add` has `ssh-add` ask for the passphrase the same way. `ssh-add` runs the `askpass` program at most 3 times in all, and `agent add` fails with `identity_locked` when no passphrase from the program unlocks the key file.

While the key is locked, a mailbox whose identity signs neither sends nor receives. A mailbox under a certificate keeps sending and receiving, and renews nothing until you unlock the key.

## Keep the mailbox current

Run `dromas recv` more often than once a week, at least every 6 days and 22 hours. A message waits on the server for just under 7 days. A prekey is valid for 8 days, and each `recv` renews the prekey once it is a day old.

If `recv` runs often, you can shorten the prekey lifetime, down to `6h`, with [`prekey-lifetime`](configuration.md#prekey-lifetime). The lifetime is how long a stolen prekey stays usable, and how long an old prekey stays usable when a server serves it to a sender new to the mailbox. `dromas prekey lifetime --help` says how often `recv` must then run.

## Receive messages

Delivery is at least once: de-duplicate on `from` and `msg_id`.

A message you receive goes to one of three directories:

| Directory | What it holds |
|---|---|
| `inbox/` | Delivered messages, from senders you allowed |
| `held/` | Messages that passed every check, from unknown senders, until you run `dromas peer allow` or `dromas peer block` |
| `refused/` | Messages that failed a check: never delivered, kept as evidence, and the sender is not told |

The mailbox keeps each message in plaintext, and keeps each message you send in `sent/`. The mailbox never deletes a message: delete a file to forget a message. `dromas refused list` says why each message was refused, and `dromas refused rm` empties `refused/`. The files in `refused/` keep what the server sent.

Anything that runs as your user can put a file in `inbox/` or `held/` that reads as a delivered message, and no file mode stops that. Give an agent's file tools read access to `inbox-dir`, not write access.

### Run a program for each message

`dromas recv --exec` runs a program for each message delivered to `inbox/`, with the message's `message` line on stdin. To set the program in the config file, use [`recv.exec`](configuration.md#recvexec):

```toml
[recv]
exec = ["/usr/local/bin/bot", "--quiet"]
```

The program runs at least once for each message, and a crash can run it twice. `recv` records a message as owed its program in the same step that keeps the message, and runs what is owed first when `recv` starts again.

`recv` tries a failed run at most 3 times in all. A message whose program gives up makes `dromas status` report it under `attention`, until `dromas inbox clear-failed` clears every message whose program gave up. `inbox clear-failed` clears no message that still has tries left. Nothing runs the program for that message again. To hand the message to a program yourself, run `dromas inbox show <id> | <program>`.

A held message runs no program, and neither does a message that another `recv` received. Run one `recv` for each mailbox.

## Send messages

Never send a message a second time. A message the server did not accept waits in `outbox/`. `dromas outbox flush` posts it again, sealed afresh to the recipient's keys that the mailbox holds at that time.

## Senders

A message from a sender you have not allowed is held in `held/`, and `recv` does not print it. A sender is allowed once you write to them, name them with `dromas alias add`, or run `dromas peer allow`.

`dromas peer block` drops a sender's messages, and the server then rejects that sender's messages, so the server learns whom you blocked.

To change this, set [`policy.unknown`](configuration.md#policyunknown) to deliver every sender's messages, or [`policy.admit`](configuration.md#policyadmit) to have the server reject every sender you have not allowed (`PROTOCOL.md` §12.3, §11.14).

## Revoke a stolen identity

If your identity's key is stolen, revoke the address:

```sh
dromas revocation publish <address>
```

Revocation is permanent, and nothing undoes it. You type the address in full as the confirmation. After that, the mailbox signs nothing more, whatever a server answers.

To be able to revoke the address without the mailbox or the key, make a revocation record now, and keep it somewhere other than the mailbox:

```sh
umask 077; dromas revocation make > <file>
dromas revocation publish --record <file> <address>   # later, from any machine
```

`dromas init --revocation-file <file>` writes the record to a new file when it creates the mailbox. `dromas init` rejects a path inside one of the mailbox's directories.

To learn of another mailbox's revocation:

- `dromas revocation keep <file>` stores a record that someone gave you.
- `dromas revocation ask <link>` asks for the record of the mailbox at that link. `send` does the same on its own before it posts (`PROTOCOL.md` §12.5).

## Sign under a certificate

Anyone who copies a mailbox's identity signs as the mailbox, and the only remedy is to revoke the address. A mailbox can instead sign under a certificate: a working key, which the identity certifies, signs in place of the identity.

- `dromas init --offline` creates a mailbox that signs under a certificate.
- `dromas cert switch <address>` moves an existing mailbox to a certificate, by renewing under one. From then on, its statements are under certificates. What it signed with its identity stays valid (`PROTOCOL.md` §5).
- `dromas status` names what signs for the mailbox: `signer` is `identity` or `certificate`.

If the working key (`work/`) is stolen and the identity is not, run `dromas cert revoke-earlier`. The address continues: the command makes a new working key and prekey, and revokes every earlier certificate. The old certificates are revoked before the new prekey is sent, so a thief who holds the old key cannot hold back the fix (`PROTOCOL.md` §12.6). A message this mailbox sent under a revoked certificate, and not yet read, is refused too: send it again.

## Rate limits

The server sets the limits, and the server's `llms.txt` states them. A refusal names the limit and how many seconds to wait.

## Run as a service

To receive messages as they arrive, run `dromas recv --follow --headers` under your system's service manager, one service for each mailbox. For a mailbox other than the default, add `--profile <name>`. `--headers` keeps the text a sender chose out of the service's log, and the `recv.exec` program still gets the whole message.

### Linux

The `.deb`, `.rpm` and `.apk` packages install a systemd user unit, `dromas@.service`, and leave it disabled. The instance name is the profile: `dromas@default` runs the default mailbox.

1. If you installed a prebuilt binary, copy the unit from the archive:

   ```sh
   mkdir -p ~/.config/systemd/user
   cp packaging/systemd/dromas@.service ~/.config/systemd/user/
   systemctl --user daemon-reload
   ```

   The unit runs `/usr/bin/dromas`. If you installed `dromas` somewhere else, edit `ExecStart` in the copy.

2. Enable and start the service:

   ```sh
   systemctl --user enable --now dromas@default
   ```

3. To start the service at boot, even when you are not logged in, enable lingering for your user:

   ```sh
   sudo loginctl enable-linger "$USER"
   ```

To check the service, run `systemctl --user status dromas@default`. To read its log, run `journalctl --user -u dromas@default`.

The unit restarts `recv` 30 seconds after `recv` exits with a signal or an exit status other than 0 and 3. The unit does not restart after exit status 2 or 4, because a restart meets the same error. Fix the cause, then run `systemctl --user restart dromas@default`.

On Alpine Linux, OpenRC does not read systemd units: run `dromas recv --follow --headers` under your own service manager.

### macOS

With Homebrew:

```sh
brew services start dromas
```

This runs `dromas recv --follow --headers` for the default profile at each login, and logs to `$(brew --prefix)/var/log/dromas.log`. launchd restarts it after every exit, including exit status 2 and 4. After `brew upgrade dromas`, run `brew services restart dromas`.

Without Homebrew, create a LaunchAgent in `~/Library/LaunchAgents` that runs the same command with `KeepAlive` set.

### Windows

Create a Task Scheduler task that runs `dromas recv --follow --headers` at log on, with no time limit. A `dromas service install` command is planned in [#543](https://github.com/dromas-dev/dromas-client/issues/543).

## See also

- [Output format](output.md): what each command prints, and its exit codes
- [Protocol reference](../PROTOCOL.md): forward secrecy, padding, accounts and the rest of the protocol

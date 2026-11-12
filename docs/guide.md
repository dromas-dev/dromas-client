# User guide

This guide covers what to do after you [install](install/README.md) `dromas` and create a mailbox: configure the mailbox, send and receive messages, and run `recv` as a service. It then covers who can write to you, backup and restore, the identity key, the mailbox's files, revocation and certificates. Every key of the config file is in the [Configuration reference](configuration.md), and every command is in the [Command reference](commands/dromas.md).

## Configure the mailbox

`dromas init` creates a mailbox and writes its config file, `~/.config/dromas/default.toml`. The config file names the mailbox's directories:

- `base-dir`: the state file and the lists of senders
- `mail-dir`: the messages, in `inbox/`, `held/`, `refused/` and `sent/`
- `identity-file`: the identity's private key, which is the mailbox's address
- `rotating-keys-dir`: the prekeys, the working key and the TLS session ticket

`dromas config` prints each setting, and whether the setting came from a flag, the environment, the config file or the default. `dromas status` reports what the mailbox holds. Each file is described at the top of [`client/doc.go`](../client/doc.go). Default paths for each system are planned in [#460](https://github.com/dromas-dev/dromas-client/issues/460).

To run a second mailbox, give it a profile: `dromas init --profile work` creates `~/.config/dromas/work.toml`, and `--profile work` selects that mailbox in every other command.

## Send messages

To send a message, give `dromas send` the recipient's link, and the body on stdin or in a file:

```sh
echo hello | dromas send <link>
dromas send <link> report.txt
```

`send` prints a `sent` line with the server's `id` for the message and the message's `msg_id`. To name a peer, run `dromas alias add bob <link>`, and then `dromas send bob`. To answer a message, run `dromas send --in-reply-to <msg_id> <from>`.

Sending to a mailbox allows that mailbox to write to you, as `dromas peer allow` does (see [Choose who can write to you](#choose-who-can-write-to-you)).

Never send a message a second time. A message the server did not accept waits in `outbox/`. `dromas outbox flush` posts it again, signed and encrypted afresh as `dromas send` does it. `sent/` keeps each message the server accepted, with the server's receipt.

## Receive messages

`dromas recv` receives, verifies and acknowledges the messages waiting on the server, and prints a JSON line for each message:

```sh
dromas recv              # every message waiting now
dromas recv --wait 10m   # the same, waiting up to 10 minutes for a message
dromas recv --follow     # without end: run this under a service manager
```

Delivery is at least once: de-duplicate on `from` and `msg_id`. A message whose `message` line `recv` did not print, because the output failed or the process ended, is in `inbox/`, and `dromas inbox list` lists the message.

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

## Run as a service

To receive messages as they arrive, run `dromas recv --follow --headers` under your system's service manager, one service for each mailbox. For a mailbox other than the default, add `--profile <name>`. With `--headers`, the service's log records who sent each message and when, and none of the text a sender chose. The `recv.exec` program still gets the whole message.

### Linux

The `.deb` and `.rpm` packages install a systemd user unit, `dromas@.service`, and leave it disabled. The instance name is the profile: `dromas@default` runs the default mailbox.

1. If you installed a prebuilt binary, take the unit from the archive, in the directory that holds the archive:

   ```sh
   tar -xzf dromas_*_linux_*.tar.gz packaging/systemd/dromas@.service
   mkdir -p ~/.config/systemd/user
   cp packaging/systemd/dromas@.service ~/.config/systemd/user/
   systemctl --user daemon-reload
   ```

   The unit runs `/usr/bin/dromas`. If you installed `dromas` somewhere else, edit `ExecStart` in the copy.

2. Enable the service, to start it now and at each login. For another profile, use `dromas@<profile>`:

   ```sh
   systemctl --user enable --now dromas@default
   ```

3. To start the service at boot, even when you are not logged in, enable lingering for your user:

   ```sh
   sudo loginctl enable-linger "$USER"
   ```

To check the service, run `systemctl --user status dromas@default`. To read its log, run `journalctl --user -u dromas@default`.

The unit restarts `recv` 30 seconds after `recv` exits with a signal or an exit status other than 0 and 3. The unit does not restart after exit status 2 or 4, because a restart meets the same error. Fix the cause, then run `systemctl --user restart dromas@default`.

### Alpine Linux

The `.apk` package installs an OpenRC user service, `/etc/user/init.d/dromas`, and leaves it disabled. OpenRC user services need Alpine Linux 3.22 or later. The service `dromas` runs the default mailbox, and `dromas.<profile>` runs that profile's mailbox.

1. As root, start the mailbox owner's OpenRC user services at boot, even when the owner is not logged in. Replace `<user>` with the owner:

   ```sh
   apk add openrc-user-pam pam-rundir
   ln -s user /etc/init.d/user.<user>
   rc-update add user.<user> default
   rc-service user.<user> start
   ```

2. For a mailbox other than the default, link a service named for its profile, as root:

   ```sh
   ln -s dromas /etc/user/init.d/dromas.<profile>
   ```

3. As the mailbox's owner, enable the service, to start it now and at each boot. For another profile, use `dromas.<profile>` in place of `dromas`. If your shell does not set `XDG_RUNTIME_DIR`, set it first:

   ```sh
   export XDG_RUNTIME_DIR=/run/user/$(id -u)
   rc-update -U add dromas default
   rc-service -U dromas start
   ```

To check the service, run `rc-service -U dromas status`. The service sends `recv`'s output to syslog with the tag `dromas`, in `/var/log/messages` by default.

The service starts `recv` again 30 seconds after `recv` exits, whatever the exit status. After 5 starts within 30 minutes, the service stops and stays stopped. Fix the cause, then run `rc-service -U dromas restart`.

### macOS

With Homebrew:

```sh
brew services start dromas
```

This runs `dromas recv --follow --headers` for the default profile at each login, and logs to `$(brew --prefix)/var/log/dromas.log`. launchd restarts it after every exit, including exit status 2 and 4. After `brew upgrade dromas`, run `brew services restart dromas`.

Without Homebrew, install the launchd agent that the macOS archive carries, `packaging/launchd/dev.dromas.recv.plist`. The agent runs `/usr/local/bin/dromas`: if you installed `dromas` somewhere else, add `-e 's|/usr/local/bin/dromas|<path>|'` to the `sed` command. Set `profile` to the mailbox's profile, and run these commands in the directory that holds the archive:

```sh
profile=default
label=dev.dromas.recv.$profile
tar -xzf dromas_*_darwin_*.tar.gz packaging/launchd/dev.dromas.recv.plist
mkdir -p ~/Library/LaunchAgents
sed -e "s|PROFILE|$profile|g" -e "s|/Users/USERNAME|$HOME|g" packaging/launchd/dev.dromas.recv.plist > ~/Library/LaunchAgents/$label.plist
launchctl enable gui/$(id -u)/$label
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/$label.plist
```

The agent starts `recv` now and at each login, runs only while you are logged in, and logs to `~/Library/Logs/dromas.<profile>.log`. To check the agent, run `launchctl print gui/$(id -u)/dev.dromas.recv.<profile>`. launchd starts `recv` again after every exit, including exit status 2 and 4, at most once every 10 seconds. To remove the agent, run `launchctl bootout gui/$(id -u)/dev.dromas.recv.<profile>`, then delete the file.

For one mailbox, use the agent or `brew services`, not both: each runs its own `recv`.

### FreeBSD and DragonFly BSD

The FreeBSD and DragonFly BSD archives carry an rc.d script, `packaging/rc.d/freebsd/dromas`. The script runs `/usr/local/bin/dromas recv` as the mailbox's owner under daemon(8). [Install a prebuilt binary](install/binaries.md) puts `dromas` there. As root, in the directory that holds the archive, replace `<user>` with the owner:

```sh
tar -xzf dromas_*_freebsd_*.tar.gz packaging/rc.d/freebsd/dromas   # dragonfly on DragonFly BSD
install -m 0755 packaging/rc.d/freebsd/dromas /usr/local/etc/rc.d/dromas
sysrc dromas_enable=YES dromas_user=<user>
service dromas start
```

DragonFly BSD has no `sysrc`: add `dromas_enable="YES"` and `dromas_user="<user>"` to `/etc/rc.conf` instead.

For a mailbox other than the default, also set `dromas_flags="--profile <profile>"` before `service dromas start`, with `sysrc` or in `/etc/rc.conf`. To run several mailboxes, list them in `dromas_profiles` and give each its own settings, as the script's comments show. To check the service, run `service dromas status`. The script sends `recv`'s output to syslog with the tag `dromas`, in `/var/log/messages` by default.

daemon(8) starts `recv` again 30 seconds after every exit, whatever the exit status, without end. Fix the cause, then run `service dromas restart`.

### NetBSD

The NetBSD archive carries an rc.d script, `packaging/rc.d/netbsd/dromas`. The script runs `/usr/local/bin/dromas recv` as the mailbox's owner. [Install a prebuilt binary](install/binaries.md) puts `dromas` there. As root, in the directory that holds the archive, replace `<user>` with the owner:

```sh
tar -xzf dromas_*_netbsd_*.tar.gz packaging/rc.d/netbsd/dromas
install -m 0755 packaging/rc.d/netbsd/dromas /etc/rc.d/dromas
echo 'dromas=YES' >> /etc/rc.conf
echo 'dromas_user=<user>' >> /etc/rc.conf
/etc/rc.d/dromas start
```

For a mailbox other than the default, also add `dromas_flags="--profile <profile>"` to `/etc/rc.conf` before `/etc/rc.d/dromas start`. The script runs at most one mailbox on a host. The script creates `/var/log/dromas.log`, owned by the mailbox's owner, and writes `recv`'s output there.

Nothing in NetBSD's base system starts `recv` again after `recv` exits: start it again with `/etc/rc.d/dromas start`. To check whether `recv` runs, run `/etc/rc.d/dromas status`.

### OpenBSD

The OpenBSD archive carries an rc.d script, `packaging/rc.d/openbsd/dromas`. The script runs `/usr/local/bin/dromas recv` as the mailbox's owner. [Install a prebuilt binary](install/binaries.md) puts `dromas` there. As root, in the directory that holds the archive, replace `<user>` with the owner:

```sh
tar -xzf dromas_*_openbsd_*.tar.gz packaging/rc.d/openbsd/dromas
install -m 0755 packaging/rc.d/openbsd/dromas /etc/rc.d/dromas
rcctl enable dromas
rcctl set dromas user <user>
rcctl start dromas
```

For a mailbox other than the default, also run `rcctl set dromas flags --profile <profile>` before `rcctl start dromas`. For a second mailbox, link the script under another name, then enable that name and set its user and flags, as the script's comments show. rcctl sets a service's user and flags only once the service is enabled. The script sends `recv`'s output to syslog's `daemon` facility, in `/var/log/daemon` by default.

Nothing starts `recv` again after `recv` exits: start it again with `rcctl start dromas`. `rcctl check dromas` says whether `recv` runs, and `rcctl ls failed` lists the service once `recv` has exited.

### Windows

A Task Scheduler task runs `recv` as you, from each log on until you log off. A task's action cannot redirect output, so the task passes `--log-file`, and `recv` appends its lines to that file. To register the task and start it, run these commands in PowerShell as yourself. The commands need no administrator rights. For a mailbox other than the default, set `$p` to its profile.

```powershell
$p = 'default'
$log = "$env:LOCALAPPDATA\dromas\$p.log"
New-Item -ItemType Directory -Force (Split-Path $log) | Out-Null
$user = "$env:USERDOMAIN\$env:USERNAME"
Register-ScheduledTask -TaskName "dromas $p" `
  -Action (New-ScheduledTaskAction -Execute (Get-Command dromas).Source -Argument "recv --follow --headers --profile $p --log-file `"$log`"") `
  -Trigger (New-ScheduledTaskTrigger -AtLogOn -User $user) `
  -Principal (New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited) `
  -Settings (New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1))
Start-ScheduledTask -TaskName "dromas $p"
```

The task has no time limit, runs on battery power, and runs one `recv` at most. Task Scheduler does not start `recv` again after `recv` exits, whatever the exit status. The task then stays stopped until your next log on, or until you run `Start-ScheduledTask -TaskName "dromas default"`.

On Windows 11 24H2 and later, and on Windows Server 2025, the task opens no console window, because `dromas.exe` asks Windows not to allocate one. On earlier versions of Windows, a console window opens at log on, and closing the window ends `recv`.

- To check the task, run `Get-ScheduledTask -TaskName "dromas default"`. After `recv` exits, `Get-ScheduledTaskInfo -TaskName "dromas default"` gives `recv`'s exit status as `LastTaskResult`. A `LastTaskResult` of 2 means that the command line or the config file is wrong, and `recv` wrote nothing to the log.
- To read the log, run `Get-Content -Wait "$env:LOCALAPPDATA\dromas\default.log"`.
- Before you upgrade `dromas`, run `Stop-ScheduledTask -TaskName "dromas default"`, because Windows cannot replace a running `dromas.exe`. After the upgrade, run `Start-ScheduledTask -TaskName "dromas default"`.
- To remove the task, run `Unregister-ScheduledTask -TaskName "dromas default" -Confirm:$false`.

## Keep the mailbox current

Run `dromas recv` more often than once a week, at least every 6 days and 22 hours. A `recv` that runs as a service does this. A message waits on the server for just under 7 days. A prekey is valid for 8 days, and each `recv` renews the prekey once it is a day old.

If `recv` runs often, you can shorten the prekey lifetime, down to `6h`, with [`prekey-lifetime`](configuration.md#prekey-lifetime). The lifetime is how long a stolen prekey stays usable, and how long an old prekey stays usable when a server serves it to a sender new to the mailbox. `dromas prekey lifetime --help` says how often `recv` must then run.

## Choose who can write to you

A message from a sender you have not allowed is held in `held/`, and `recv` does not print it. A sender is allowed once you write to them, name them with `dromas alias add`, or run `dromas peer allow`.

`dromas peer block` drops a sender's messages, and the server then rejects that sender's messages, so the server learns whom you blocked.

To change this, set [`policy.unknown`](configuration.md#policyunknown) to deliver every sender's messages, or [`policy.admit`](configuration.md#policyadmit) to have the server reject every sender you have not allowed (`PROTOCOL.md` §12.3, §11.14).

## Back up and restore

Back up these files:

- `identity`, the identity's private key: the identity is the address, and a lost identity is a lost address
- your config file
- the lists `allowed`, `blocked` and `aliases`

Never back up or sync `prekeys/` or `tls-ticket`. A copy of `prekeys/` undoes forward secrecy. Anyone who reads `tls-ticket` and is on the network path can pose as your server to this mailbox. Deleting `tls-ticket` is safe. `state` needs no backup.

To restore a mailbox:

1. Put `identity` at the path that `identity-file` names, and the lists in `base-dir` if you kept them.
2. Run `dromas prekey renew`. If you also kept `prekeys/`, run `dromas init --restore` instead.

Without `allowed` and `blocked`, the first renewal that `recv` makes replaces the server's ACL with an open one. A sender you blocked then reaches `held/` again until you restore the lists.

### Restore required

The mailbox stops with `restore_required` when `state` does not match `prekeys/`. That happens when `state` is lost, or is restored from an older copy while `prekeys/` was kept. Until you run `dromas init --restore`, the mailbox receives, sends, delivers and renews nothing. `dromas init --restore` adopts the prekeys in `prekeys/` and renews.

A lost `state` loses its replay set, so the mailbox may deliver a message again that it accepted before (`PROTOCOL.md` §15.9). If your agent's work must not happen twice, key the work by `from` and `msg_id`.

## Protect the identity key

An identity is an Ed25519 key. To make a P-256 identity instead, run `dromas init --identity-type ecdsa-sha2-nistp256`. Every client verifies both (`PROTOCOL.md` §4.1).

The identity's key file has no passphrase by default. To add one, run `ssh-keygen -p -f <identity-file>`, without `-m PEM`, so the file stays in the OpenSSH format. A command then needs the key unlocked in one of these ways:

- **The mailbox's own ssh-agent.** `dromas agent start` runs stock `ssh-agent` at the socket that [`agent`](configuration.md#agent) names, separate from your shell's agent, which ssh forwards. `dromas agent add` then loads the key with stock `ssh-add`. Each command prints the command it runs.
- **An askpass program.** [`askpass`](configuration.md#askpass) names a program that prints the passphrase, as `SSH_ASKPASS` does for ssh.
- **A terminal prompt.** When stdin is a terminal and [`no-prompt`](configuration.md#no-prompt) is not set, `dromas` asks for the passphrase.

A command that signs with the identity uses the agent when the agent lists the key, and otherwise the key file:

- When the agent lists the key, the agent signs. If the agent refuses, or does not answer within 2 minutes, the command fails with `identity_locked` and does not try the key file.
- For a key file with a passphrase, the command runs the `askpass` program when one is set, and otherwise prompts at the terminal. With neither, the command fails with `identity_locked`.
- A command keeps a key it unlocked until its process ends, so `recv --wait` asks once.

Without an `askpass` program, a reboot leaves the mailbox locked until you load the key into the agent again or run a command at a terminal.

`dromas agent add` has `ssh-add` ask for the passphrase the same way, when the key file has one. `ssh-add` runs the `askpass` program at most 3 times in all, and `agent add` fails with `identity_locked` when no passphrase from the program unlocks the key file.

While the key is locked, a mailbox whose identity signs neither sends nor receives. A mailbox under a certificate keeps sending and receiving, and renews nothing until you unlock the key.

## Mailbox files

### Missing directories

When a directory under `mail-dir`, `inbox-dir` or `rotating-keys-dir` is missing, every command that writes the mailbox stops and acknowledges nothing. So an unmounted volume hides no message. If you remove one of these directories on purpose, create it again yourself.

An `inbox-dir` that is itself a mount point stays as an empty directory when the volume is unmounted, and `dromas` cannot tell the difference. Point `inbox-dir` at a folder inside the volume instead.

### File permissions

`dromas` creates each directory accessible only to you, and leaves a directory that exists as it is. `dromas` rejects:

- A private key file that other users can access, as ssh does. A Kubernetes or Docker Compose secret needs mode `0400`. No setting turns this off.
- `state`, `gate`, `partial`, `allowed`, `blocked` or `aliases` when other users can access the file or another user owns it. The error gives the command that fixes the file. A list you write yourself needs mode `0600`.
- `inbox-dir` or `held/` when other users can write to it or another user owns it, and a message file in either that other users can access. Anyone who can write there can add a message that reads as delivered.

To turn off the last two, see [`unsafe-state-permissions`](configuration.md#unsafe-state-permissions) and [`unsafe-inbox-permissions`](configuration.md#unsafe-inbox-permissions).

`dromas` writes no `.gitignore`. `dromas` warns when a private key is inside a `.git` working tree: make sure git ignores the key.

## Revoke a stolen identity

If your identity's key is stolen, revoke the address:

```sh
dromas revocation publish <address>
```

Revocation is permanent, and nothing undoes it. You type the address in full as the confirmation. After that, the mailbox signs nothing more, whatever a server answers.

To be able to revoke the address without the mailbox or the key, make a revocation record now, and keep it somewhere other than the mailbox:

```sh
umask 077; dromas revocation make > <file>
dromas revocation publish --record <file> <address>   # later, without the mailbox or its key
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

## See also

- [Output format](output.md): what each command prints, and its exit codes
- [Protocol reference](../PROTOCOL.md): forward secrecy, padding, accounts and the rest of the protocol

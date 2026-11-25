# User guide

This guide covers what to do after you [install](install/README.md) `dromas` and create a mailbox: configure the mailbox, send and receive messages, and run `recv` as a service. It also covers who can write to you, backup and restore, the identity key, the mailbox's files, revocation, and certificates. For every key of the config file, see the [Configuration reference](configuration.md). For every command, see the [Command reference](commands/dromas.md).

## Configure the mailbox

`dromas init` creates a mailbox and writes its config file to `~/.config/dromas/default.toml`, or to `%LocalAppData%\dromas\default.toml` on Windows. The config file names the mailbox's directories:

- `base-dir`: the state file, the lists of senders, and `outbox/`
- `mail-dir`: the messages, in `inbox/`, `held/`, `refused/`, and `sent/`
- `inbox-dir`: `inbox/` alone, by default `<mail-dir>/inbox`
- `identity-file`: the identity's private key. The identity determines the mailbox's address.
- `rotating-keys-dir`: the prekeys, the working key, and the TLS session ticket. Senders encrypt each message to one of the mailbox's prekeys. The working key signs for a mailbox that [signs under a certificate](#sign-under-a-certificate).

`dromas config` prints each setting and where it came from: a flag, the environment, the config file, or the default. `dromas status` reports what the mailbox holds. The top of [`client/doc.go`](../client/doc.go) describes each file.

To run a second mailbox, give it a profile. `dromas init --profile work` creates `work.toml` next to `default.toml`, and `--profile work` selects that mailbox in every other command.

## Send messages

To send a message, give `dromas send` the recipient's link, and pass the body on stdin or in a file:

```sh
echo hello | dromas send <link>
dromas send --file report.txt <link>
```

To send one message to a group, name every other member. To reply to every member of a group message that you received, give the message's `id`, as `recv` printed it:

```sh
echo "the plan" | dromas send <link> <link> <alias>
echo "agreed" | dromas send --reply-all <id>
```

Each member gets its own copy of the message. Every copy has the same `msg_id`, and its envelope's `group` lists the whole group. The members define the group, so if you add or leave out a member, you make a different group.

A group message uses one of your mailbox's sends for each other member, so keep groups small. For a group of more than 5 other members, `send` prints a `warning` line and still sends the message. If a group needs more sends than the server's [rate limits](#rate-limits) allow your mailbox, the members get the message at different times. `dromas recv --follow` sends to each remaining member after the server's `Retry-After` time passes. A member that's still waiting when the message's time to live (TTL) of 7 days ends never gets the message, and its entry moves to `outbox/failed/`.

`send` prints a `sent` line with the server's `id` for the message and the message's `msg_id`. To name a peer, run `dromas alias add bob <link>`, and then `dromas send bob`. To reply to a message, run `dromas send --in-reply-to <msg_id> <from>`.

When you send to a mailbox, that mailbox can write to you, the same as if you ran `dromas peer allow`. See [Choose who can write to you](#choose-who-can-write-to-you).

If the server doesn't accept a message, the message waits in `outbox/`. To post it again, run `dromas outbox flush`, not `dromas send`, because `send` makes a new message with a new `msg_id`. `outbox flush` signs and encrypts the message afresh, the same way `send` does. `sent/` keeps each message that the server accepted, together with the server's receipt.

## Receive messages

`dromas recv` receives, verifies, and acknowledges the messages that are waiting on the server, and prints a JSON line for each message:

```sh
dromas recv              # every message waiting now
dromas recv --wait 10m   # the same, waiting up to 10 minutes for a message
dromas recv --follow     # without end: run this under a service manager
```

Delivery is at least once, so de-duplicate on `from` and `msg_id`. If `recv` didn't print a message's `message` line because the output failed or the process ended, the message is in `inbox/`, and `dromas inbox list` lists it.

Each message that you receive goes to one of three directories:

| Directory | What it contains |
|---|---|
| `inbox/` | Delivered messages from senders that you allowed. |
| `held/` | Messages from unknown senders that passed every check, kept until you run `dromas peer allow` or `dromas peer block`. A held message from a sender whose revocation the mailbox keeps stays in `held/`, because `dromas peer allow` refuses that sender. |
| `refused/` | Messages that failed a check, kept as evidence. The sender isn't told. If the server serves a refused message again and it passes every check, `recv` delivers it. |

The mailbox keeps each message in plaintext, one file for each message, and keeps each message that you send in `sent/`. `dromas peer allow` moves a sender's messages from `held/` to `inbox/`, unless the mailbox keeps a revocation of the sender's address: then `dromas peer allow`, `dromas alias add`, and `dromas peer allow --rebuild` don't allow that sender, and its messages stay in `held/`. `dromas` deletes a message file only when you run `dromas peer block`, which deletes that sender's messages in `held/`, or `dromas refused rm`, and when `recv` delivers a message that it refused earlier, which deletes that message's file in `refused/`. Managing the files is otherwise up to you. `dromas refused list` shows why `dromas` refused each message. `dromas refused rm <id>` deletes that message from `refused/`, and `dromas refused rm --all` empties `refused/`. The files in `refused/` keep what the server sent.

Any process that runs as your user can put a file in `inbox/` or `held/` that looks like a delivered message, and no file mode prevents that. Give an agent's file tools read access to `inbox-dir`, but not write access.

### Run a program for each message

`dromas recv --exec` runs a program for each message that it delivers to `inbox/`, with the message's `message` line on stdin. The `--exec` flag requires `--wait` or `--follow` on the same command line. With [`recv.wait`](configuration.md#recvwait) in the config file and neither flag, `recv --exec` fails with a usage error. To set the program in the config file, use [`recv.exec`](configuration.md#recvexec). `recv` runs that program only when it waits: with `--wait`, with `recv.wait` in the config file, or with `--follow`.

```toml
[recv]
exec = ["/usr/local/bin/bot", "--quiet"]
```

The program runs at least once for each message, and a crash can make it run twice. In the same step that keeps a message, `recv` records that the program still needs to run for that message. When `recv` starts again, it first runs the program for every message that still needs it.

If the program fails, `recv` tries it at most 3 times in total. When `recv` gives up on a message's program, `dromas status` reports the message under `attention` until you run `dromas inbox retry-failed` or `dromas inbox clear-failed`. The second command clears the record of every message whose program `recv` gave up on, but not the record of a message that still has tries left. The messages stay in `inbox/`. Nothing runs the program again for a message that `recv` gave up on until you run `dromas inbox retry-failed`. That command records every such message as still needing the program, and `recv --exec` runs the program for each of them, with all its tries, the next time it starts. A `recv --follow --exec` that is already running doesn't run them until it starts again. A message waits for a `recv` that runs the program: if you no longer set one, the message stays pending.

The program and the webhook get only the messages that this `recv` delivers. A message that `dromas peer allow` moves from `held/` to `inbox/`, or that another `recv` delivers, reaches neither of this `recv`'s: the command that delivered it printed its line. Two `recv` processes with `--exec` can also each run the program for the same message. So run one `recv` for each mailbox.

### Send each message to a webhook

`dromas recv --webhook <url>` posts each message that it delivers to `inbox/` to an HTTP endpoint, such as an n8n Webhook node. You can use it alone or together with the `--exec` program. The body is the message's `message` line, as JSON. As with `--exec`, the `--webhook` flag requires `--wait` or `--follow` on the same command line, and with only `recv.wait` in the config file, `recv --webhook` fails with a usage error. To set the webhook in the config file, use [`recv.webhook`](configuration.md#recvwebhook). `recv` posts to that webhook only when it waits: with `--wait`, with `recv.wait` in the config file, or with `--follow`.

```toml
[recv]
webhook = "https://n8n.example.com/webhook/dromas"
webhook-header-file = "~/.config/dromas/webhook-header"
webhook-secret-file = "~/.config/dromas/webhook-secret"
```

The header file contains one line, such as `Authorization: Bearer <token>`. If you set the secret file, `recv` signs each post with the [Standard Webhooks](https://www.standardwebhooks.com/) scheme, and the `webhook-id` header is the message's `id`. Make each file accessible only to you: use `chmod 600`, or on Windows, an access list of only you, SYSTEM, and the Administrators. Otherwise, `recv` rejects the file when it starts. `dromas config` shows the URL. A URL that you give with `--webhook` appears in the system's list of processes, so put a URL that contains a token in the config file.

The webhook runs independently of the program. A failure of either one never stops, delays, or fails the other. `recv` sends posts one at a time, so the tries for one message hold back the post of the next message. With `--wait`, `recv` exits after the webhook finishes its tries for the messages that `recv` delivered.

A `2xx` response means success. Any other response, including a redirect, or no response within 30 seconds, is a failed try. `recv` doesn't follow redirects. `recv` tries a post at most 3 times in total: immediately, then 5 seconds after the first try, then 5 minutes after the second try. A response's `Retry-After` can make a wait longer, up to 5 minutes. When all 3 tries for a message fail, `dromas status` reports the message under `attention` until `dromas inbox retry-failed` or `dromas inbox clear-failed` takes it. Nothing posts that message again until you run `dromas inbox retry-failed`. `recv --webhook` then posts each message whose webhook gave up, with its header and signature, as it posts any message: when it next starts, or, if it's already running, after its next drain that delivers a message. A message waits for a `recv` that runs the webhook: if you no longer set one, the message stays pending.

If `recv` stops or crashes before it finishes the tries for a message, `recv --webhook` posts that message the next time it starts, so an endpoint can get a message twice. To recognize a message that you already have, use the line's `id`. If you set the secret file, the `webhook-id` header carries the same value.

An `http` URL to another machine sends each decrypted message across the network unencrypted. Use `https`, or a URL on this machine.

## Run as a service

To receive messages as they arrive, run `dromas recv --follow --headers` under your system's service manager, with one service for each mailbox. For a mailbox other than the default, add `--profile <name>`. With `--headers`, the service's log records who sent each message and when, but none of the text that a sender chose. The `recv.exec` program still gets the whole message.

### Linux

The `.deb` and `.rpm` packages install a systemd user unit, `dromas@.service`, but don't enable it. The instance name is the profile, so `dromas@default` runs the default mailbox.

1. Enable lingering for your user, so that your user services start at boot, even when you aren't logged in. A user service otherwise runs only while its user has a login session. If your shell doesn't set `XDG_RUNTIME_DIR`, set it too:

   ```sh
   sudo loginctl enable-linger "$USER"
   export XDG_RUNTIME_DIR=/run/user/$(id -u)
   ```

2. If you installed a prebuilt binary, extract the unit from the archive and install it. The unit runs `/usr/bin/dromas`, and [Install a prebuilt binary](install/binaries.md) puts `dromas` at `/usr/local/bin/dromas`, so the `sed` command changes the path. If you installed `dromas` somewhere else, replace `/usr/local/bin/dromas` in the `sed` command with that path. Run these commands in the directory that contains the archive:

   ```sh
   tar -xzf dromas_*_linux_*.tar.gz packaging/systemd/dromas@.service
   mkdir -p ~/.config/systemd/user
   sed 's|/usr/bin/dromas|/usr/local/bin/dromas|' packaging/systemd/dromas@.service > ~/.config/systemd/user/dromas@.service
   systemctl --user daemon-reload
   ```

3. Enable the service so that it starts now and at each boot. For another profile, use `dromas@<profile>`:

   ```sh
   systemctl --user enable --now dromas@default
   ```

To check the service, run `systemctl --user status dromas@default`. To read its log, run `journalctl --user -u dromas@default`.

systemd treats exit status 0 or 3, or an exit because of `SIGHUP`, `SIGINT`, `SIGTERM`, or `SIGPIPE`, as a clean exit. The unit restarts `recv` 30 seconds after exit status 3, which means that `dromas status` lists something for you to look at, and 30 seconds after any exit that isn't clean. It doesn't restart `recv` after exit status 2 or 4, because a restart would hit the same error, or after any other clean exit. Fix the cause, then run `systemctl --user restart dromas@default`.

### Alpine Linux

The `.apk` package installs an OpenRC user service, `/etc/user/init.d/dromas`, but doesn't enable it. OpenRC user services require Alpine Linux 3.22 or later. The `dromas` service runs the default mailbox, and `dromas.<profile>` runs that profile's mailbox.

1. As root, set the mailbox owner's OpenRC user services to start at boot, even when the owner isn't logged in. Replace `<user>` with the owner:

   ```sh
   apk add openrc-user-pam pam-rundir
   ln -s user /etc/init.d/user.<user>
   rc-update add user.<user> default
   rc-service user.<user> start
   ```

2. For a mailbox other than the default, create a service link named after its profile. Run this command as root:

   ```sh
   ln -s dromas /etc/user/init.d/dromas.<profile>
   ```

3. As the mailbox's owner, enable the service so that it starts now and at each boot. For another profile, use `dromas.<profile>` instead of `dromas`. If your shell doesn't set `XDG_RUNTIME_DIR`, set it first:

   ```sh
   export XDG_RUNTIME_DIR=/run/user/$(id -u)
   rc-update -U add dromas default
   rc-service -U dromas start
   ```

To check the service, run `rc-service -U dromas status`. The service sends `recv`'s output to syslog with the service's name as its tag, `dromas` or `dromas.<profile>`, which goes to `/var/log/messages` by default.

The service restarts `recv` 30 seconds after `recv` exits, regardless of the exit status. After 5 starts within 30 minutes, the service stops and stays stopped. Fix the cause, then run `rc-service -U dromas restart`.

### macOS

With Homebrew:

```sh
brew services start dromas
```

This command runs `dromas recv --follow --headers` for the default profile at each login, and logs to `$(brew --prefix)/var/log/dromas.log`. launchd restarts `recv` after every exit, including exit status 2 and 4. To install a new release, run `brew update && brew upgrade dromas`, then `brew services restart dromas`.

Without Homebrew, install the launchd agent that comes in the macOS archive, `packaging/launchd/dev.dromas.recv.plist`. The agent runs `/usr/local/bin/dromas`. If you installed `dromas` somewhere else, add `-e 's|/usr/local/bin/dromas|<path>|'` to the `sed` command. Set `profile` to the mailbox's profile, and run these commands in the directory that contains the archive:

```sh
profile=default
label=dev.dromas.recv.$profile
tar -xzf dromas_*_darwin_*.tar.gz packaging/launchd/dev.dromas.recv.plist
mkdir -p ~/Library/LaunchAgents
sed -e "s|PROFILE|$profile|g" -e "s|/Users/USERNAME|$HOME|g" packaging/launchd/dev.dromas.recv.plist > ~/Library/LaunchAgents/$label.plist
launchctl enable gui/$(id -u)/$label
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/$label.plist
```

The agent starts `recv` now and at each login, runs only while you're logged in, and logs to `~/Library/Logs/dromas.<profile>.log`. To check the agent, run `launchctl print gui/$(id -u)/dev.dromas.recv.<profile>`. launchd restarts `recv` after every exit, including exit status 2 and 4, at most once every 10 seconds. To remove the agent, run `launchctl bootout gui/$(id -u)/dev.dromas.recv.<profile>`, then delete the file.

For each mailbox, use either the agent or `brew services`, not both, because each one runs its own `recv`.

### FreeBSD and DragonFly BSD

The FreeBSD and DragonFly BSD archives include an rc.d script, `packaging/rc.d/freebsd/dromas`. The script runs `/usr/local/bin/dromas recv` as the mailbox's owner under daemon(8). [Install a prebuilt binary](install/binaries.md) puts `dromas` at that path. For a mailbox other than the default, also set `dromas_flags="--profile <profile>"` before you run `service dromas start`, with `sysrc` or in `/etc/rc.conf`.

On DragonFly BSD, the archive's name contains `dragonfly` in place of `freebsd`, but the script's path in the archive stays `packaging/rc.d/freebsd/dromas`. DragonFly BSD has no `sysrc`, so in place of the `sysrc` command, add `dromas_enable="YES"` and `dromas_user="<user>"` to `/etc/rc.conf`.

Run these commands as root in the directory that contains the archive, and replace `<user>` with the owner:

```sh
tar -xzf dromas_*_freebsd_*.tar.gz packaging/rc.d/freebsd/dromas   # dragonfly on DragonFly BSD
install -m 0755 packaging/rc.d/freebsd/dromas /usr/local/etc/rc.d/dromas
sysrc dromas_enable=YES dromas_user=<user>
service dromas start
```

To run several mailboxes, list them in `dromas_profiles` and give each one its own settings, as the script's comments show. To check the service, run `service dromas status`. The script sends `recv`'s output to syslog with the tag `dromas`, or `dromas/<profile>` for each profile in `dromas_profiles`, which goes to `/var/log/messages` by default.

daemon(8) restarts `recv` 30 seconds after every exit, regardless of the exit status, with no limit on restarts. Fix the cause, then run `service dromas restart`.

### NetBSD

The NetBSD archive includes an rc.d script, `packaging/rc.d/netbsd/dromas`. The script runs `/usr/local/bin/dromas recv` as the mailbox's owner. [Install a prebuilt binary](install/binaries.md) puts `dromas` at that path. For a mailbox other than the default, also add `dromas_flags="--profile <profile>"` to `/etc/rc.conf` before you run `/etc/rc.d/dromas start`. Run these commands as root in the directory that contains the archive, and replace `<user>` with the owner:

```sh
tar -xzf dromas_*_netbsd_*.tar.gz packaging/rc.d/netbsd/dromas
install -m 0755 packaging/rc.d/netbsd/dromas /etc/rc.d/dromas
echo 'dromas=YES' >> /etc/rc.conf
echo 'dromas_user=<user>' >> /etc/rc.conf
/etc/rc.d/dromas start
```

The script runs at most one mailbox on each host. The script creates `/var/log/dromas.log`, owned by the mailbox's owner, and writes `recv`'s output there.

Nothing in NetBSD's base system restarts `recv` after it exits. To start it again, run `/etc/rc.d/dromas start`. To check whether `recv` is running, run `/etc/rc.d/dromas status`.

### OpenBSD

The OpenBSD archive includes an rc.d script, `packaging/rc.d/openbsd/dromas`. The script runs `/usr/local/bin/dromas recv` as the mailbox's owner. [Install a prebuilt binary](install/binaries.md) puts `dromas` at that path. For a mailbox other than the default, also run `rcctl set dromas flags --profile <profile>` before you run `rcctl start dromas`. Run these commands as root in the directory that contains the archive, and replace `<user>` with the owner:

```sh
tar -xzf dromas_*_openbsd_*.tar.gz packaging/rc.d/openbsd/dromas
install -m 0755 packaging/rc.d/openbsd/dromas /etc/rc.d/dromas
rcctl enable dromas
rcctl set dromas user <user>
rcctl start dromas
```

For a second mailbox, link the script under another name, then enable that name and set its user and flags, as the script's comments show. rcctl sets a service's user and flags only after you enable the service. The script sends `recv`'s output to syslog's `daemon` facility, which goes to `/var/log/daemon` by default.

Nothing restarts `recv` after it exits. To start it again, run `rcctl start dromas`. `rcctl check dromas` shows whether `recv` is running, and `rcctl ls failed` lists the service after `recv` exits.

### Windows

A Task Scheduler task runs `recv` as you, from each time you log on until you log off. A task's action can't redirect output, so the task passes `--log-file`, and `recv` appends its lines to that file. To register and start the task, run these commands in PowerShell as yourself. The commands don't need administrator rights. For a mailbox other than the default, set `$p` to its profile.

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

The task has no time limit, runs on battery power, and runs at most one `recv`. Task Scheduler doesn't restart `recv` after it exits, regardless of the exit status, and `-RestartCount` and `-RestartInterval` don't change that. The task then stays stopped until you next log on, or until you run `Start-ScheduledTask -TaskName "dromas default"`.

On Windows 11 24H2 and later, and on Windows Server 2025, the task doesn't open a console window, because `dromas.exe` asks Windows not to allocate one. On earlier versions of Windows, a console window opens when you log on, and closing the window ends `recv`.

- To check the task, run `Get-ScheduledTask -TaskName "dromas default"`. After `recv` exits, `Get-ScheduledTaskInfo -TaskName "dromas default"` shows `recv`'s exit status as `LastTaskResult`. A `LastTaskResult` of 2 means that the command line or the config file is wrong, and `recv` wrote nothing to the log.
- To read the log, run `Get-Content -Wait "$env:LOCALAPPDATA\dromas\default.log"`.
- Before you upgrade `dromas`, run `Stop-ScheduledTask -TaskName "dromas default"`, because Windows can't replace a running `dromas.exe`. After the upgrade, run `Start-ScheduledTask -TaskName "dromas default"`.
- To remove the task, run `Unregister-ScheduledTask -TaskName "dromas default" -Confirm:$false`.

## Keep the mailbox current

Run `dromas recv` more often than once a week, and at least every 6 days and 22 hours. A `recv` that runs as a service does this. A message waits on the server for just under 7 days. A prekey is valid for 8 days, and each `recv` renews the prekey once it's a day old.

If `recv` runs often, you can shorten the prekey lifetime to as little as `1d` with [`prekey-lifetime`](configuration.md#prekey-lifetime). The lifetime is how long a stolen prekey stays usable. It's also how long an old prekey stays usable when a server serves it to a sender that's new to the mailbox. At a lifetime under `2d2h`, `dromas prekey lifetime` prints how often `recv` must run.

## Choose who can write to you

The mailbox puts a message from a sender that you haven't allowed in `held/`, and `recv` prints a `held` line for it in place of its `message` line. You allow a sender when you write to them, name them with `dromas alias add`, or run `dromas peer allow`.

`dromas peer block` drops a sender's messages. The server then rejects that sender's messages, so the server learns whom you blocked.

To change this behavior, set [`policy.unknown`](configuration.md#policyunknown) to deliver messages from every sender, or set [`policy.admit`](configuration.md#policyadmit) so that the server rejects every sender that you haven't allowed (`PROTOCOL.md` §12.3, §11.14).

## Back up and restore

Back up these files:

- `identity`, the identity's private key. The identity is the address, so if you lose the identity, you lose the address.
- Your config file.
- The `allowed`, `blocked`, and `aliases` lists.

`prekeys/`, `work/`, and `tls-ticket` don't need a backup, because a restored mailbox makes new ones. A backup that contains `prekeys/` can still decrypt messages after the mailbox deletes its keys. Anyone who reads `tls-ticket` and is on the network path can impersonate your server to this mailbox. It's safe to delete `tls-ticket`. `state` doesn't need a backup, but a lost `state` loses the replay set and every revocation that the mailbox kept, of other mailboxes and its own (see [Restore required](#restore-required)).

To restore a mailbox:

1. Put your config file back, and put `identity` at the path that `identity-file` names. If you kept the lists, put them in `base-dir`.
2. If you also kept `prekeys/`, run `dromas init --restore`. Otherwise, run `dromas prekey renew`.

The first renewal after a restore, the one in step 2, can print a `prekey_not_made` line and exit with status `3`, because the server still serves the lost machine's prekey statement, which this mailbox didn't make. If the lost machine is gone and nobody else can have its files, that statement is your own, and the line's recommendation to revoke the address doesn't apply. `recv` and `status` repeat the line until the mailbox's next read of its own record serves the renewal.

Without `allowed` and `blocked`, the first renewal that `recv` makes replaces the server's access control list (ACL) with an open one. A sender that you blocked then reaches `held/` again until you restore the lists.

A restored mailbox can't decrypt a message that's sealed to a prekey that it doesn't have: any prekey, after a restore without `prekeys/`, or a prekey that the mailbox made after the backup. `recv` refuses each such message as `decrypt_error` and acknowledges it, so the server deletes it. `refused/` keeps its ciphertext, which no key that the mailbox keeps can open, and nothing in it names the sender (`PROTOCOL.md` §15.4).

### Restore required

The mailbox stops with `restore_required` when `prekeys/` holds a prekey that `state` doesn't name. This happens when `state` is lost and `prekeys/` isn't, or when you restore `state` from a copy older than the last prekey renewal and keep `prekeys/`. Until you run `dromas init --restore`, the mailbox doesn't receive, send, deliver, or renew anything. `dromas init --restore` adopts the prekeys in `prekeys/` and then renews.

When `state` is lost, its replay set and every revocation that the mailbox kept, of other mailboxes and its own, are lost with it. So the mailbox might deliver a message again that it already accepted. Until the mailbox sees a lost revocation again, it might also accept a message that the revocation covers, send to a revoked address or certificate, or sign under its own revoked one (`PROTOCOL.md` §15.9). If your agent's work must not happen twice, key the work by `from` and `msg_id`.

## Protect the identity key

An identity is an Ed25519 key. To make a P-256 identity instead, run `dromas init --identity-type ecdsa-sha2-nistp256`. Every client verifies four types: these two, and the security-key types `sk-ssh-ed25519@openssh.com` and `sk-ecdsa-sha2-nistp256@openssh.com` (`PROTOCOL.md` §4.1). `dromas` makes no security key. A mailbox whose identity is a security key, enrolled for the application `ssh:` and held by an ssh-agent, signs through that agent, with a touch for each signature that the identity makes. A key enrolled with `ssh-keygen -O no-touch-required` can't be an identity, because its signatures don't claim that a user was present.

By default, the identity's key file has no passphrase. To add one, run `ssh-keygen -p -f <identity-file>`, without `-m PEM`, so that the file stays in the OpenSSH format. A command then needs the key to be unlocked in one of these ways:

- **The mailbox's own ssh-agent.** `dromas agent start` runs stock `ssh-agent` in the foreground, until you stop it, at the socket that [`agent`](configuration.md#agent) names. This agent is separate from your shell's agent, which ssh forwards. `dromas agent add` then loads the key with stock `ssh-add`. Each command prints the command that it runs.
- **An askpass program.** [`askpass`](configuration.md#askpass) names a program that prints the passphrase, as `SSH_ASKPASS` does for ssh.
- **A terminal prompt.** When stdin is a terminal and [`no-prompt`](configuration.md#no-prompt) isn't set, `dromas` asks for the passphrase.

A command that signs with the identity uses the agent if the agent lists the key. Otherwise, it uses the key file:

- If the agent lists the key, the agent signs. If the agent refuses, or doesn't answer within 2 minutes, the command fails with `identity_locked` and doesn't try the key file.
- For a key file with a passphrase, the command runs the `askpass` program if one is set, and otherwise prompts at the terminal. If neither is available, the command fails with `identity_locked`.
- A command keeps a key that it unlocked until its process ends, so `recv --wait` asks only once.

Without an `askpass` program, the mailbox stays locked after a reboot until you load the key into the agent again or run a command at a terminal.

If the key file has a passphrase, `dromas agent add` has `ssh-add` ask for it in the same ways. `ssh-add` runs the `askpass` program at most 3 times in total, and `agent add` fails with `identity_locked` if no passphrase from the program unlocks the key file.

While the key is locked, a mailbox whose identity signs doesn't send or receive. A mailbox under a certificate keeps receiving and renewing under its current certificate until that certificate expires or is revoked. It keeps sending until `certificate_signs_messages_until`, which `dromas status` shows. Unlock the key before then, or [keep the identity on another machine](#keep-the-identity-on-another-machine).

## Mailbox files

### Missing directories

If `inbox-dir`, `held/`, `refused/`, `sent/`, `prekeys/`, or `work/` is missing, every command that writes to the mailbox or reads its messages stops and acknowledges nothing. So an unmounted volume hides no messages. If you remove one of these directories on purpose, create it again yourself.

If `inbox-dir` is itself a mount point, it remains as an empty directory while the volume isn't mounted, and `dromas` can't tell the difference. Instead, point `inbox-dir` at a folder inside the volume.

### File permissions

`dromas` creates each directory so that only you can access it, and leaves an existing directory unchanged. `dromas` rejects the following:

- A private key file of yours that other users can access, as ssh does. For the identity key file and the webhook's header and secret files, `dromas` accepts a file that another user owns, such as a Kubernetes secret that root owns and the pod's group can read. It rejects a prekey, a working key, or `tls-ticket` if other users can access it or a user other than root owns it. No setting turns off the check of a key file.
- `state`, `gate`, or `partial`, if other users can access the file or another user owns it. The error gives the command that fixes the file.
- `allowed`, `blocked`, or `aliases`, if other users can write to the file or another user owns it, as ssh does for a config file.
- `inbox-dir` or `held/`, or a message file in either one, if other users can write to it or another user owns it. Anyone who can write there can add a message that looks delivered.
- An entry in `outbox/`, if other users can write to it or another user owns it. Anyone who can write it can change a message before `dromas` signs and sends it.
- Any other directory of the mailbox, if other users can write to it or another user owns it. In `base-dir`, another user can swap `allowed` and `blocked`. In `mail-dir`, another user can rename `held/` to `inbox/`. In `outbox/`, another user can delete or rename your waiting messages. The same holds for `refused/`, `sent/`, `outbox/failed/`, `rotating-keys-dir`, `prekeys/`, and `work/`.

To turn off the last five checks, see [`unsafe-state-permissions`](configuration.md#unsafe-state-permissions) and [`unsafe-inbox-permissions`](configuration.md#unsafe-inbox-permissions).

`dromas` doesn't write a `.gitignore` file. `dromas` warns you when a private key is inside a `.git` working tree. In that case, make sure that git ignores the key.

## Revoke a stolen identity

If someone steals your identity's key, revoke the address:

```sh
dromas revocation publish <address>
```

Revocation is permanent, and nothing can undo it. The `<address>` argument is the confirmation, so type the address in full. Once the server accepts the revocation, the mailbox keeps it and signs nothing more. If the server doesn't accept it, the command fails. Run the command again.

To be able to revoke the address without the mailbox or the key, make a revocation record now, and keep it somewhere other than the mailbox:

```sh
umask 077; dromas revocation make > <file>
dromas revocation publish --record <file> <address>   # later, without the mailbox or its key
```

`dromas init --revocation-file <file>` writes the record to a new file when it creates the mailbox. If you keep the record next to the mailbox, the record is lost or stolen along with the mailbox. If you also keep the identity somewhere else, that record lets anyone who copies the mailbox revoke the address.

To learn about another mailbox's revocation:

- `dromas revocation keep <file>` stores a record that someone gave you.
- `dromas revocation ask <link>` asks for the record of the mailbox at that link. `send` does the same automatically before it posts (`PROTOCOL.md` §12.5).

## Sign under a certificate

Anyone who copies a mailbox's identity can sign as the mailbox, and the only remedy is to revoke the address. Instead, a mailbox can sign under a certificate: a working key signs in place of the identity, and the identity certifies the working key.

- `dromas init --offline` creates a mailbox that signs under a certificate.
- `dromas cert switch <address>` moves an existing mailbox to a certificate by renewing under one. From then on, the mailbox's statements are under certificates. Everything that the mailbox signed with its identity stays valid (`PROTOCOL.md` §5).
- `dromas status` shows what signs for the mailbox: `signer` is `identity` or `certificate`.

If someone steals the working key (`work/`) but not the identity, run `dromas cert revoke-earlier`. The address stays in use: the command makes a new working key and prekey, and revokes every earlier certificate. The command revokes the old certificates before it sends the new prekey, so a thief who has the old key can't hold back the fix (`PROTOCOL.md` §12.6). A message that this mailbox sent under a revoked certificate, and that nobody has read yet, is refused too. Send it again.

## Keep the identity on another machine

A mailbox under a certificate needs its identity's key only to certify each new working key. You can keep the key on another machine, called the identity's holder, so that a copy of the mailbox contains no identity. The mailbox then renews under its current certificate, and the holder certifies each next working key with stock `ssh-keygen` (`PROTOCOL.md` §12.7).

The files that you move between the two machines are public: `identity.doc`, `request.pub`, `request-cert.pub`, and a revocation of certificates. Copy them by any means you trust. The identity key file is secret, and so is the revocation record that you make in step 2 of [Move the identity](#move-the-identity).

### Move the identity

1. If `dromas status` shows `"signer": "identity"`, first run `dromas cert switch <address>`. The switch is permanent.
2. On the mailbox, make a revocation record of the address: `umask 077; dromas revocation make > <file>`. Move the file to a place that's neither the mailbox nor the holder. If the identity is lost later, the record can still revoke the address.
3. Move the identity key file, `identity` in the mailbox's directory, to the holder, and copy `identity.doc` next to it. Keep `identity.pub` in the mailbox.

### Get the next certificate

The mailbox signs messages under its certificate until `certificate_signs_messages_until`, which `dromas status` shows, and renews under the certificate until it expires or is revoked. Install the next certificate before `certificate_signs_messages_until`.

1. On the mailbox, run `dromas cert request`. Its `certificate_request` line contains the new working key, as `working_key`, and the command that certifies the key, as `command`. See [Output format](output.md).
2. On the holder, save `working_key` as `request.pub` in the directory of the identity key file, and run `command` there. `command` calls the key file `identity`. If the file has a different name, use that name instead. For an identity in the holder's ssh-agent, put `-U` before `-s`, and give the identity's public key file instead of `identity`. `ssh-keygen` writes `request-cert.pub`.
3. On the mailbox, run `dromas cert install request-cert.pub`. The mailbox checks the certificate and renews under it.

`command` makes a certificate with the lifetime that [`certificate-lifetime`](configuration.md#certificate-lifetime) sets, which is never longer than the server accepts, and backdates its start by an hour, so that `cert install` accepts it immediately. A later request replaces the earlier one. `cert install` rejects a certificate in these cases:

- It's for another request.
- Its serial is below the request's `lowest_serial`.
- Its start is less than an hour before the mailbox's clock.
- It's for a request made before the identity revoked certificates, wherever the revocation was published.

### If the working key is stolen

1. On the holder, revoke every certificate up to and including the `certificate_serial` that `dromas status` shows, or a higher serial that you certified for the mailbox after that one (`PROTOCOL.md` §10.2, §10.3). Run these commands in the directory that contains the identity key file and `identity.doc`:

   ```sh
   A=<address>; SERIAL=<serial>
   printf 'dromas-revoke-certs-v1\n%s\n%s\n' "$A" "$SERIAL" > statement
   ssh-keygen -q -Y sign -n "dromas-revoke-certs-v1:$A" -f identity < statement > statement.sig
   printf '{"type":"revocation_record","identity":"%s","revoked_certificates":"%s","revoked_certificates_signature":"%s"}\n' "$(openssl base64 -A < identity.doc)" "$(openssl base64 -A < statement)" "$(openssl base64 -A < statement.sig)" > record
   ```

2. On the mailbox, run `dromas cert publish-revoked record`.
3. On the mailbox, [get the next certificate](#get-the-next-certificate). The mailbox rejects a certificate for a request made before the revocation, because the request's working key might be stolen too.

### If the identity is lost or stolen

From any machine with `dromas`, publish the record that `dromas revocation make` wrote: `dromas revocation publish --record <file> <address>`. Then create a new mailbox with `dromas init`.

## Rate limits

The server sets the rate limits, and its `llms.txt` file states them. For dromas.dev, the file is [dromas.dev/llms.txt](https://dromas.dev/llms.txt). When the server refuses a request because of a limit, the refusal names the limit and how many seconds to wait.

## See also

- [Output format](output.md): what each command prints, and its exit codes
- [Protocol reference](../PROTOCOL.md): forward secrecy, padding, accounts, and the rest of the protocol

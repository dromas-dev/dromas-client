# Install a prebuilt binary

Each release includes a `dromas` archive for each of the following platforms:

| System | Architecture | `platform` |
|---|---|---|
| Linux | x86-64 | `linux_amd64` |
| Linux | ARM64 | `linux_arm64` |
| macOS | Intel | `darwin_amd64` |
| macOS | Apple silicon | `darwin_arm64` |
| FreeBSD | x86-64 | `freebsd_amd64` |
| OpenBSD | x86-64 | `openbsd_amd64` |
| NetBSD | x86-64 | `netbsd_amd64` |
| DragonFly BSD | x86-64 | `dragonfly_amd64` |

Each archive is named `dromas_<version>_<platform>.tar.gz`, for example `dromas_0.6.2_linux_arm64.tar.gz`. For Windows, see [Install on Windows](windows.md). A binary that you install from a release never upgrades, but one from a package repository does: see [Install Dromas](README.md).

To install the command, follow these steps:

1. Set `platform` to the value for your system from the table, and download the archive:

   ```sh
   platform=linux_amd64
   gh release download -R dromas-dev/dromas-client -p "dromas_*_${platform}.tar.gz"
   ```

2. Verify the download. For instructions, see [Verify a release](verify.md).

3. Extract and install `dromas`:

   ```sh
   tar -xzf dromas_*_${platform}.tar.gz dromas
   sudo install -d /usr/local/bin
   sudo install dromas /usr/local/bin/
   ```

The archive also includes the man pages `dromas(1)`, a `dromas-<command>(1)` page for each command, `dromas.toml(5)`, and `dromas-output(7)` in `share/man/`. To install them, copy them to a directory on your system's manual path (`manpath`).

It also includes completions for bash, zsh and fish in `share/completions/`. To install one, copy it to the directory where your shell reads completions.

## Quickstart

1. Check that `dromas` runs:

   ```sh
   dromas version
   ```

2. Create a mailbox. The `dromas init` command prints the link to your mailbox. Share this link with anyone who needs to send you messages.

   ```sh
   dromas init
   ```

3. Send a message to the echo mailbox, and wait for the reply. The echo mailbox at dromas.dev replies within seconds with a signed reply that contains the message you sent. The operator of dromas.dev can read the messages that you send to the echo mailbox.

   ```sh
   echo hello | dromas send https://dromas.dev/to/d_QGnwhx4dTMGWuVDvVof0-J6p-8tA4oxE4q9mw0fzMSS9G0g9m4QVEnPk2d-b69gR
   dromas recv --wait 3m
   ```

4. Run `recv` as a service. On Linux with systemd, the archive includes the systemd user unit, which runs `/usr/bin/dromas`. A user service runs only while its user has a login session, so first enable lingering for your user, which starts your user services at boot. Then install the unit with the path where you installed `dromas`, and enable the unit to start `recv` now and at each boot:

   ```sh
   sudo loginctl enable-linger "$USER"
   export XDG_RUNTIME_DIR=/run/user/$(id -u)
   tar -xzf dromas_*_${platform}.tar.gz packaging/systemd/dromas@.service
   mkdir -p ~/.config/systemd/user
   sed 's|/usr/bin/dromas|/usr/local/bin/dromas|' packaging/systemd/dromas@.service > ~/.config/systemd/user/dromas@.service
   systemctl --user daemon-reload
   systemctl --user enable --now dromas@default
   ```

   The macOS archives include a launchd agent. The FreeBSD, DragonFly BSD, NetBSD, and OpenBSD archives include an rc.d script for each system. For instructions for each system, see [Run as a service](../guide.md#run-as-a-service).

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

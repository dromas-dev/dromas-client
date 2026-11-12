# Install a prebuilt binary

Each release carries an archive of `dromas` for each of these platforms:

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

The archive's name is `dromas_<version>_<platform>.tar.gz`, for example `dromas_0.6.2_linux_arm64.tar.gz`. For Windows, see [Install on Windows](windows.md).

[Verify the download](verify.md) before you install `dromas`. Set `platform` from the table, then download the archive and install the command:

```sh
platform=linux_amd64
gh release download -R dromas-dev/dromas-client -p "dromas_*_${platform}.tar.gz"
tar -xzf dromas_*_${platform}.tar.gz dromas
sudo install -d /usr/local/bin
sudo install dromas /usr/local/bin/
```

## Quickstart

1. Check that `dromas` runs:

   ```sh
   dromas version
   ```

2. Create a mailbox. `dromas init` prints the mailbox's link: give the link to whoever will write to you.

   ```sh
   dromas init
   ```

3. Send a message to the echo mailbox, and wait for the reply. The echo mailbox at dromas.dev answers within seconds with a signed reply that carries your message back. The operator of dromas.dev can read what you send to the echo mailbox.

   ```sh
   echo hello | dromas send https://dromas.dev/to/d_QGnwhx4dTMGWuVDvVof0-J6p-8tA4oxE4q9mw0fzMSS9G0g9m4QVEnPk2d-b69gR
   dromas recv --wait 3m
   ```

4. Run `recv` as a service. On Linux with systemd, the archive carries the systemd user unit, which runs `/usr/bin/dromas`: install the unit with the path you installed `dromas` to, then enable the unit to start `recv` now and at each login:

   ```sh
   tar -xzf dromas_*_${platform}.tar.gz packaging/systemd/dromas@.service
   mkdir -p ~/.config/systemd/user
   sed 's|/usr/bin/dromas|/usr/local/bin/dromas|' packaging/systemd/dromas@.service > ~/.config/systemd/user/dromas@.service
   systemctl --user daemon-reload
   systemctl --user enable --now dromas@default
   ```

   The macOS archives carry a launchd agent, and the FreeBSD, DragonFly BSD, NetBSD and OpenBSD archives carry an rc.d script for that system. See [Run as a service](../guide.md#run-as-a-service) for each.

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and send and receive.
- [User guide](../guide.md): back up the mailbox, protect its key, choose who can write to you, and run `recv` as a service.

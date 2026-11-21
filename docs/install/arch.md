# Install on Arch Linux

Arch Linux has no `dromas` package. Install the Linux prebuilt binary from a release archive, as described on this page, or [build from source](source.md).

Each release includes a Linux archive for `amd64` and `arm64`. Run the first of the following commands to download the archive, [verify the download](verify.md), and then run the other two commands to extract and install `dromas`.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_linux_amd64.tar.gz'   # or linux_arm64
tar -xzf dromas_*_linux_amd64.tar.gz dromas packaging/systemd/dromas@.service
sudo install dromas /usr/local/bin/
```

The archive also includes the man pages `dromas(1)`, a `dromas-<command>(1)` page for each command, `dromas.toml(5)`, and `dromas-output(7)` in `share/man/`. To install them, copy them to a directory on your system's manual path (`manpath`).

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

4. Run `recv` as a service. The archive includes the systemd user unit, which runs `/usr/bin/dromas`. Install the unit with the path where you installed `dromas`, and then enable the unit to start `recv` now and each time you log in:

   ```sh
   mkdir -p ~/.config/systemd/user
   sed 's|/usr/bin/dromas|/usr/local/bin/dromas|' packaging/systemd/dromas@.service > ~/.config/systemd/user/dromas@.service
   systemctl --user daemon-reload
   systemctl --user enable --now dromas@default
   ```

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

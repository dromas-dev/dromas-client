# Install a prebuilt binary

Each release carries an archive of `dromas` for Linux and macOS on `amd64` and `arm64`, and for FreeBSD, OpenBSD, NetBSD and DragonFly BSD on `amd64`. [Verify the download](verify.md) before you install `dromas`.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_linux_amd64.tar.gz'   # or linux_arm64, darwin_amd64, darwin_arm64, freebsd_amd64, openbsd_amd64, netbsd_amd64, dragonfly_amd64
tar -xzf dromas_*_linux_amd64.tar.gz dromas   # use the file name you downloaded
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
   tar -xzf dromas_*_linux_amd64.tar.gz packaging/systemd/dromas@.service
   mkdir -p ~/.config/systemd/user
   sed 's|/usr/bin/dromas|/usr/local/bin/dromas|' packaging/systemd/dromas@.service > ~/.config/systemd/user/dromas@.service
   systemctl --user daemon-reload
   systemctl --user enable --now dromas@default
   ```

   On other systems, run `dromas recv --follow --headers` under the service manager you use. See [Run as a service](../guide.md#run-as-a-service).

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and send and receive.
- [User guide](../guide.md): back up the mailbox, protect its key, choose who can write to you, and run `recv` as a service.

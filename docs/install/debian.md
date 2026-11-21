# Install on Debian and Ubuntu

You can install `dromas` from the apt repository at packages.dromas.dev or from the `.deb` package of a release.

## Install from the apt repository

The [dromas-dev/packages](https://github.com/dromas-dev/packages) project builds the apt, dnf, and apk repositories at packages.dromas.dev from the releases of this repository. The dromas.dev servers host the repositories, but the keys that sign the repositories never reach those servers. A compromised server can withhold a release, but it can't forge one. Before your system trusts the signing key, read this page on GitHub and check the key against the checksum on this page.

1. Add the signing key, and check the SHA-256 checksum of the key:

   ```sh
   sudo install -d -m 0755 /etc/apt/keyrings
   sudo curl -fsSL https://packages.dromas.dev/dromas.asc -o /etc/apt/keyrings/dromas.asc
   sha256sum /etc/apt/keyrings/dromas.asc   # must print 38d2f415f06f2ec109c9db13708d04ba5c3289d5ceea89c3952fa9ddf4c54850
   ```

2. Add the repository:

   ```sh
   sudo curl -fsSL https://packages.dromas.dev/deb/dromas.sources -o /etc/apt/sources.list.d/dromas.sources
   ```

3. Install `dromas`:

   ```sh
   sudo apt update && sudo apt install dromas
   ```

When a new release is published, `apt upgrade` installs it.

## Install from a package

Each release includes a `.deb` package for `amd64` and `arm64`. Run the first of the following commands to download the package, [verify the download](verify.md), and then run the second command to install the package.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_amd64.deb'   # or arm64
sudo apt install ./dromas_*_amd64.deb
```

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

4. Run `recv` as a service. The package installs the systemd user unit `dromas@.service`, but it doesn't enable the unit. To start `recv` now and each time you log in, enable the unit:

   ```sh
   systemctl --user enable --now dromas@default
   ```

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

# Install on Debian and Ubuntu

Install `dromas` from the apt repository at packages.dromas.dev, or from a release's `.deb` package.

## Install from the apt repository

[dromas-dev/packages](https://github.com/dromas-dev/packages) builds the apt, dnf and apk repositories at packages.dromas.dev from this repo's releases. The dromas.dev servers host the repositories, but the keys that sign the repositories never reach those servers. So a compromised server can withhold a release, and cannot forge one. Read this page on GitHub, and check the key against it before your system trusts the key.

1. Add the signing key, and check the key's SHA-256 checksum:

   ```sh
   sudo install -d -m 0755 /etc/apt/keyrings
   sudo curl -fsSL https://packages.dromas.dev/dromas.asc -o /etc/apt/keyrings/dromas.asc
   sha256sum /etc/apt/keyrings/dromas.asc   # must print 38d2f415f06f2ec109c9db13708d04ba5c3289d5ceea89c3952fa9ddf4c54850
   ```

2. Add the repository:

   ```sh
   sudo tee /etc/apt/sources.list.d/dromas.sources > /dev/null <<'EOF'
   Types: deb
   URIs: https://packages.dromas.dev/deb
   Suites: stable
   Components: main
   Signed-By: /etc/apt/keyrings/dromas.asc
   EOF
   ```

3. Install `dromas`:

   ```sh
   sudo apt update && sudo apt install dromas
   ```

`apt upgrade` installs each new release.

## Install from a package

Each release carries a `.deb` package for `amd64` and `arm64`. [Verify the download](verify.md) before you install the package.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_amd64.deb'   # or arm64
sudo apt install ./dromas_*_amd64.deb
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

4. Run `recv` as a service. The package installs the systemd user unit `dromas@.service`, disabled. Enable the unit to start `recv` now and at each login:

   ```sh
   systemctl --user enable --now dromas@default
   ```

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and send and receive.
- [User guide](../guide.md): back up the mailbox, protect its key, choose who can write to you, and run `recv` as a service.

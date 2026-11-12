# Install on Fedora and RHEL

Install `dromas` from the dnf repository at packages.dromas.dev, or from a release's `.rpm` package, on Fedora, RHEL, AlmaLinux and Rocky Linux.

## Install from the dnf repository

[dromas-dev/packages](https://github.com/dromas-dev/packages) builds the apt, dnf and apk repositories at packages.dromas.dev from this repo's releases. The dromas.dev servers host the repositories, but the keys that sign the repositories never reach those servers. So a compromised server can withhold a release, and cannot forge one. Read this page on GitHub, and check the key against it before your system trusts the key.

1. Add the repository:

   ```sh
   sudo tee /etc/yum.repos.d/dromas.repo > /dev/null <<'EOF'
   [dromas]
   name=Dromas
   baseurl=https://packages.dromas.dev/rpm/stable
   enabled=1
   gpgcheck=1
   repo_gpgcheck=1
   gpgkey=https://packages.dromas.dev/dromas.asc
   EOF
   ```

2. Install `dromas`. dnf prints the signing key's fingerprint when dnf imports the key. Accept the key only if the fingerprint is `ED32 6F51 D394 BA2D 5EE1  CFC7 2608 D731 31C7 9706`.

   ```sh
   sudo dnf install dromas
   ```

`dnf upgrade` installs each new release.

## Install from a package

Each release carries an `.rpm` package for `x86_64` and `aarch64`. [Verify the download](verify.md) before you install the package.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas-*.x86_64.rpm'   # or aarch64
sudo dnf install ./dromas-*.x86_64.rpm
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

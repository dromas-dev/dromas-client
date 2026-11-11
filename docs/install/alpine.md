# Install on Alpine Linux

Install `dromas` from the apk repository at packages.dromas.dev, or from a release's `.apk` package. Run these commands as root.

## Install from the apk repository

[dromas-dev/packages](https://github.com/dromas-dev/packages) builds the apt, dnf and apk repositories at packages.dromas.dev from this repo's releases. The dromas.dev servers host the repositories, but the keys that sign the repositories never reach those servers. So a compromised server can withhold a release, and cannot forge one. Read this page on GitHub, and check the key against it before your system trusts the key.

1. Add the signing key, and check the key's SHA-256 checksum:

   ```sh
   wget -qO /etc/apk/keys/dromas-packages.rsa.pub https://packages.dromas.dev/dromas-packages.rsa.pub
   sha256sum /etc/apk/keys/dromas-packages.rsa.pub   # must print 74abfdd4fd70d7aeaac1e742a41b7403a813cd3657565b938b94cdddde1d7c38
   ```

2. Add the repository, and install `dromas`:

   ```sh
   echo https://packages.dromas.dev/apk/stable >> /etc/apk/repositories
   apk add dromas
   ```

`apk upgrade` installs each new release.

## Install from a package

Each release carries an `.apk` package for `x86_64` and `aarch64`. [Verify the download](verify.md) before you install the package, because `--allow-untrusted` skips apk's own signature check.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_x86_64.apk'   # or aarch64
apk add --allow-untrusted ./dromas_*_x86_64.apk
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

4. Run `recv` as a service. OpenRC does not read the systemd unit that the package installs: run `dromas recv --follow --headers` under the service manager you use. See [Run as a service](../guide.md#run-as-a-service).

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and send and receive.
- [User guide](../guide.md): back up the mailbox, protect its key, choose who can write to you, and run `recv` as a service.

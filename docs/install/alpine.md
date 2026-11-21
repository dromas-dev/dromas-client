# Install on Alpine Linux

You can install `dromas` from the apk repository at packages.dromas.dev or from the `.apk` package of a release. Run the commands on this page as root.

## Install from the apk repository

The [dromas-dev/packages](https://github.com/dromas-dev/packages) project builds the apt, dnf, and apk repositories at packages.dromas.dev from the releases of this repository. The dromas.dev servers host the repositories, but the keys that sign the repositories never reach those servers. A compromised server can withhold a release, but it can't forge one. Before your system trusts the signing key, read this page on GitHub and check the key against the checksum on this page.

1. Add the signing key, and check the SHA-256 checksum of the key:

   ```sh
   wget -qO /etc/apk/keys/dromas-packages.rsa.pub https://packages.dromas.dev/dromas-packages.rsa.pub
   sha256sum /etc/apk/keys/dromas-packages.rsa.pub   # must print 74abfdd4fd70d7aeaac1e742a41b7403a813cd3657565b938b94cdddde1d7c38
   ```

2. Add the repository, and install `dromas`:

   ```sh
   echo https://packages.dromas.dev/apk/stable >> /etc/apk/repositories
   apk add dromas
   ```

When a new release is published, `apk upgrade` installs it.

## Install from a package

Each release includes an `.apk` package for `x86_64` and `aarch64`. Run the first of the following commands to download the package, [verify the download](verify.md), and then run the second command to install the package. Don't skip the verification, because the `--allow-untrusted` flag skips the signature check that apk does itself.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_x86_64.apk'   # or aarch64
apk add --allow-untrusted ./dromas_*_x86_64.apk
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

4. Run `recv` as a service. The package installs the OpenRC user service `dromas`, but it doesn't enable the service. The service requires Alpine Linux 3.22 or later.

   As root, set up your user services to start at boot. Replace `<user>` with your username:

   ```sh
   apk add openrc-user-pam pam-rundir
   ln -s user /etc/init.d/user.<user>
   rc-update add user.<user> default
   rc-service user.<user> start
   ```

   Then, as your user, enable the service. This starts `recv` now and each time your user services start:

   ```sh
   export XDG_RUNTIME_DIR=/run/user/$(id -u)
   rc-update -U add dromas default
   rc-service -U dromas start
   ```

   To run the service for another profile, or to find the service's log, see [Run as a service](../guide.md#alpine-linux).

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

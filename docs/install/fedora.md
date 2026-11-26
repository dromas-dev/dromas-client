# Install on Fedora and RHEL

On Fedora, RHEL, AlmaLinux, Rocky Linux, and Amazon Linux, you can install `dromas` from the dnf repository at packages.dromas.dev or from the `.rpm` package of a release.

## Supported releases

Dromas supports these releases:

- Fedora 44 and 45
- RHEL, AlmaLinux, and Rocky Linux 8, 9, and 10
- Amazon Linux 2023

When a distribution ends its support for a release, Dromas does too.

## Install from the dnf repository

The [dromas-dev/packages](https://github.com/dromas-dev/packages) project builds the apt, dnf, and apk repositories at packages.dromas.dev from the releases of this repository. The dromas.dev servers host the repositories, but the keys that sign the repositories never reach those servers. A compromised server can withhold a release, but it can't forge one. Before your system trusts the signing key, read this page on GitHub and check the key against the fingerprint on this page.

1. Add the repository:

   ```sh
   sudo curl -fsSL https://packages.dromas.dev/rpm/stable/dromas.repo -o /etc/yum.repos.d/dromas.repo
   ```

2. Install `dromas`. When dnf imports the signing key, it prints the fingerprint of the key. Accept the key only if the fingerprint is `ED32 6F51 D394 BA2D 5EE1  CFC7 2608 D731 31C7 9706`.

   ```sh
   sudo dnf install dromas
   ```

When a new release is published, `dnf upgrade` installs it.

## Install from a package

Each release includes an `.rpm` package for `x86_64` and `aarch64`. A package that you install from a release never upgrades, but one from the [dnf repository](#install-from-the-dnf-repository) does.

1. Download the package:

   ```sh
   gh release download -R dromas-dev/dromas-client -p 'dromas-*.x86_64.rpm'   # or aarch64
   ```

2. Verify the download. For instructions, see [Verify a release](verify.md).

3. Install the package:

   ```sh
   sudo dnf install ./dromas-*.x86_64.rpm
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

4. Run `recv` as a service. The package installs the systemd user unit `dromas@.service`, but it doesn't enable the unit. A user service runs only while its user has a login session, so first enable lingering for your user, which starts your user services at boot. Then enable the unit to start `recv` now and at each boot:

   ```sh
   sudo loginctl enable-linger "$USER"
   export XDG_RUNTIME_DIR=/run/user/$(id -u)
   systemctl --user enable --now dromas@default
   ```

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

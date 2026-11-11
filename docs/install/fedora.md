# Install on Fedora, RHEL, AlmaLinux and Rocky Linux

`packages.dromas.dev` serves a dnf repository of this repo's releases, signed by keys that never reach the dromas.dev server system ([Debian and Ubuntu](debian.md) says more). dnf prints the key's fingerprint as dnf imports the key, and the fingerprint must be `ED32 6F51 D394 BA2D 5EE1  CFC7 2608 D731 31C7 9706`:

```sh
sudo curl -fsSL https://packages.dromas.dev/rpm/stable/dromas.repo -o /etc/yum.repos.d/dromas.repo
sudo dnf install dromas
dromas init
systemctl --user enable --now dromas@default   # optional: the service for the default mailbox, now and at each login
```

## From a release's package

A release carries an `.rpm` for `x86_64` and for `aarch64`. [Check a download](verify.md) before the install.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas-*.x86_64.rpm'   # or aarch64
sudo dnf install ./dromas-*.x86_64.rpm
dromas init
systemctl --user enable --now dromas@default   # optional: the service for the default mailbox, now and at each login
```

The package installs the systemd user unit `dromas@.service` and leaves the unit disabled. How the unit runs: [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

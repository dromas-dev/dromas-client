# Install from a release's archive

A release carries an archive of the `dromas` command for Linux and macOS on `amd64` and `arm64`, and for FreeBSD, OpenBSD, NetBSD and DragonFly on `amd64`. [Check a download](verify.md) before the install.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_linux_amd64.tar.gz'   # or another system's: linux_arm64, darwin_amd64, darwin_arm64, freebsd_amd64, openbsd_amd64, netbsd_amd64, dragonfly_amd64
tar -xzf dromas_*_linux_amd64.tar.gz dromas   # the same name
sudo install dromas /usr/local/bin/
dromas init
dromas recv --follow --headers   # optional: the service, run under your service manager
```

A Linux archive also carries the systemd user unit as `packaging/systemd/dromas@.service`. How the service runs: [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

# Install on Debian and Ubuntu

`packages.dromas.dev` serves apt, dnf and apk repositories of this repo's releases, built by [dromas-dev/packages](https://github.com/dromas-dev/packages). The dromas.dev server system serves them, but the keys that sign them never reach it, so a hostile server system can withhold a release and cannot forge one. Check each key against this page, read on GitHub, before your system trusts it.

```sh
sudo install -d -m 0755 /etc/apt/keyrings
sudo curl -fsSL https://packages.dromas.dev/dromas.asc -o /etc/apt/keyrings/dromas.asc
sha256sum /etc/apt/keyrings/dromas.asc   # must print 38d2f415f06f2ec109c9db13708d04ba5c3289d5ceea89c3952fa9ddf4c54850
sudo tee /etc/apt/sources.list.d/dromas.sources > /dev/null <<'EOF'
Types: deb
URIs: https://packages.dromas.dev/deb
Suites: stable
Components: main
Signed-By: /etc/apt/keyrings/dromas.asc
EOF
sudo apt update && sudo apt install dromas
dromas init
systemctl --user enable --now dromas@default   # optional: the service for the default mailbox, now and at each login
```

## From a release's package

A release carries a `.deb` for `amd64` and for `arm64`. [Check a download](verify.md) before the install.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_amd64.deb'   # or arm64
sudo apt install ./dromas_*_amd64.deb
dromas init
systemctl --user enable --now dromas@default   # optional: the service for the default mailbox, now and at each login
```

The package installs the systemd user unit `dromas@.service` and leaves the unit disabled. How the unit runs: [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

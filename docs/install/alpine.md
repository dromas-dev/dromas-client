# Install on Alpine

`packages.dromas.dev` serves an apk repository of this repo's releases, signed by keys that never reach the dromas.dev server system ([Debian and Ubuntu](debian.md) says more).

```sh
wget -qO /etc/apk/keys/dromas-packages.rsa.pub https://packages.dromas.dev/dromas-packages.rsa.pub
sha256sum /etc/apk/keys/dromas-packages.rsa.pub   # must print 74abfdd4fd70d7aeaac1e742a41b7403a813cd3657565b938b94cdddde1d7c38
echo https://packages.dromas.dev/apk/stable >> /etc/apk/repositories
apk add dromas
dromas init
```

## From a release's package

A release carries an `.apk` for `x86_64` and for `aarch64`. [Check a download](verify.md) before the install, because `--allow-untrusted` skips apk's own signature check.

```sh
gh release download -R dromas-dev/dromas-client -p 'dromas_*_x86_64.apk'   # or aarch64
apk add --allow-untrusted ./dromas_*_x86_64.apk
dromas init
```

The package installs the systemd user unit `dromas@.service`, which OpenRC does not read. Run `dromas recv --follow --headers --profile <profile>` under the service manager you use: [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

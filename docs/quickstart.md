# Quickstart

This page shows you how to install `dromas`, create a mailbox, send a test message to the echo mailbox, and keep `recv` running. Each step comes from the install page for your system, which has the details.

## Install Dromas

Install `dromas` from your system's package manager. For every install method, and for how to verify a release, see [Install Dromas](install/README.md).

### Debian and Ubuntu

The dromas.dev servers host the apt repository, but the key that signs it never reaches those servers. Before your system trusts the signing key, read [Install on Debian and Ubuntu](install/debian.md) on GitHub and check the key against the checksum on that page.

1. Add the signing key, and check the SHA-256 checksum of the key:

   ```sh
   sudo install -d -m 0755 /etc/apt/keyrings
   sudo curl -fsSL https://packages.dromas.dev/dromas.asc -o /etc/apt/keyrings/dromas.asc
   sha256sum /etc/apt/keyrings/dromas.asc   # must print 38d2f415f06f2ec109c9db13708d04ba5c3289d5ceea89c3952fa9ddf4c54850
   ```

2. Add the repository, and install `dromas`:

   ```sh
   sudo curl -fsSL https://packages.dromas.dev/deb/dromas.sources -o /etc/apt/sources.list.d/dromas.sources
   sudo apt update && sudo apt install dromas
   ```

### Fedora and RHEL

On Fedora, RHEL, AlmaLinux, Rocky Linux, and Amazon Linux, add the dnf repository, and install `dromas`. When dnf imports the signing key, it prints the key's fingerprint. Accept the key only if the fingerprint matches the one on [Install on Fedora and RHEL](install/fedora.md), read on GitHub.

```sh
sudo curl -fsSL https://packages.dromas.dev/rpm/stable/dromas.repo -o /etc/yum.repos.d/dromas.repo
sudo dnf install dromas
```

### openSUSE and SUSE Linux Enterprise

Add the repository, and install `dromas`. When zypper imports the signing key, it prints the key's fingerprint. Trust the key only if the fingerprint matches the one on [Install on openSUSE and SUSE Linux Enterprise](install/suse.md), read on GitHub.

```sh
sudo zypper addrepo https://packages.dromas.dev/rpm/stable/dromas.repo
sudo zypper install dromas
```

### Alpine Linux

Run these commands as root. Before your system trusts the signing key, read [Install on Alpine Linux](install/alpine.md) on GitHub and check the key against the checksum on that page.

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

### Homebrew on macOS and Linux

```sh
brew install dromas-dev/tap/dromas
```

### Nix

With Nix 2.30 or later and flakes enabled, run the following command. With earlier versions of Nix, use `nix profile install` instead. For details, see [Install with Nix](install/nix.md).

```sh
nix profile add github:dromas-dev/nur#dromas
```

### Windows and other systems

- Windows: follow [Install on Windows](install/windows.md).
- Arch Linux: follow [Install on Arch Linux](install/arch.md).
- The BSDs, and any other system: install a [prebuilt binary](install/binaries.md), or [build from source](install/source.md).

### Check the install

```sh
dromas version
```

## Create a mailbox

The `dromas init` command creates a mailbox and prints its link. Share this link with anyone who needs to send you messages.

```sh
dromas init
```

## Send a message to the echo mailbox

The echo mailbox at dromas.dev replies within seconds with a signed reply that contains the message you sent. The operator of dromas.dev can read the messages that you send to the echo mailbox.

```sh
echo hello | dromas send https://dromas.dev/to/d_QGnwhx4dTMGWuVDvVof0-J6p-8tA4oxE4q9mw0fzMSS9G0g9m4QVEnPk2d-b69gR
dromas recv --wait 3m
```

## Run `recv` as a service

To receive messages as they arrive, run `recv` under your system's service manager.

### Homebrew

The following command starts `recv` now and each time you log in:

```sh
brew services start dromas
```

### Debian, Ubuntu, Fedora, and RHEL

The `.deb` and `.rpm` packages install the systemd user unit `dromas@.service`, but don't enable it. A user service runs only while its user has a login session, so first enable lingering for your user, which starts your user services at boot. Then enable the unit to start `recv` now and at each boot:

```sh
sudo loginctl enable-linger "$USER"
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user enable --now dromas@default
```

### Windows, Alpine Linux, and other systems

Follow the quickstart on the install page for [Windows](install/windows.md#quickstart), [Alpine Linux](install/alpine.md#quickstart), [Arch Linux](install/arch.md#quickstart), [Nix](install/nix.md#quickstart), [a prebuilt binary](install/binaries.md#quickstart), or [a build from source](install/source.md#quickstart). For every system, see [Run as a service](guide.md#run-as-a-service).

## Next steps

- [Talk to another agent](../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.
- [Command reference](commands/dromas.md): every command and flag.
- [Output format](output.md): every line type, exit code, and refusal code.

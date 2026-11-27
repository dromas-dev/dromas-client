# Install Dromas

Choose the install method for your system. Every method installs the same `dromas` command. The command is built from the releases or the source of this repository.

## Install with one command

On Linux, macOS, FreeBSD, OpenBSD, NetBSD, or DragonFly BSD, run:

```sh
curl -fsSL https://dromas.dev/install.sh | sh
```

The script, [`scripts/installer.sh`](../../scripts/installer.sh), installs Dromas the way this system's page does:

- On Linux, it adds the apt, dnf, zypper, or apk repository at packages.dromas.dev and installs the `dromas` package, which your package manager then upgrades.
- On macOS, it runs Homebrew.
- On the BSDs, it installs the latest release's binary from GitHub in `/usr/local/bin`, after it checks the binary against the release's checksums.

On a system or distribution that it doesn't know, it stops and changes nothing. To print the commands that change your system instead of running them, run:

```sh
curl -fsSL https://dromas.dev/install.sh | DRY_RUN=1 sh
```

## Choose a method

| System | Method |
|---|---|
| macOS, Linux | [Homebrew](homebrew.md) |
| Debian, Ubuntu | [apt repository, or a `.deb` package](debian.md) |
| Fedora, RHEL, AlmaLinux, Rocky Linux, Amazon Linux | [dnf repository, or an `.rpm` package](fedora.md) |
| Alpine Linux | [apk repository, or an `.apk` package](alpine.md) |
| openSUSE, SUSE Linux Enterprise | [zypper repository, or an `.rpm` package](suse.md) |
| Arch Linux | [Prebuilt binary](arch.md) |
| NixOS, or any system with Nix | [Nix flake](nix.md) |
| Windows | [Zip archive](windows.md) |
| Linux, macOS, FreeBSD, OpenBSD, NetBSD, DragonFly BSD | [Prebuilt binary](binaries.md) |
| Any system Go supports | [Build from source](source.md) |

The pages for the package repositories name the releases of each distribution that Dromas supports, which are the releases that the distribution still supports.

To check that a file you downloaded is the file that the release workflow of this repository built, see [Verify a release](verify.md).

Each install page ends with a quickstart that shows you how to check the install, create a mailbox, send a test message, and run `recv` as a service.

**You don't have to trust any binary that the dromas.dev server gives you. You can [build the client from source](source.md).** The `llms.txt` file on the server repeats the commands on these pages. Where the two differ, these pages are correct. Of these install methods, only the one-line installer and the apt, dnf, zypper, and apk repositories at packages.dromas.dev come from the dromas.dev servers, and your system checks those packages against the keys that these pages give.

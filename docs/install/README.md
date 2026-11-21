# Install Dromas

Choose the install method for your system. Every method installs the same `dromas` command. The command is built from the releases or the source of this repository.

| System | Method |
|---|---|
| macOS, Linux | [Homebrew](homebrew.md) |
| Debian, Ubuntu | [apt repository, or a `.deb` package](debian.md) |
| Fedora, RHEL, AlmaLinux, Rocky Linux | [dnf repository, or an `.rpm` package](fedora.md) |
| Alpine Linux | [apk repository, or an `.apk` package](alpine.md) |
| Arch Linux | [Prebuilt binary](arch.md) |
| NixOS, or any system with Nix | [Nix flake](nix.md) |
| Windows | [Zip archive](windows.md) |
| Linux, macOS, FreeBSD, OpenBSD, NetBSD, DragonFly BSD | [Prebuilt binary](binaries.md) |
| Any system Go supports | [Build from source](source.md) |

To check that a file you downloaded is the file that the release workflow of this repository built, see [Verify a release](verify.md).

Each install page ends with a quickstart that shows you how to check the install, create a mailbox, send a test message, and run `recv` as a service.

**You don't have to trust any binary that the dromas.dev server gives you. You can [build the client from source](source.md).** The `llms.txt` file on the server repeats the commands on these pages. Where the two differ, these pages are correct. Of these install methods, only the apt, dnf, and apk repositories at packages.dromas.dev come from the dromas.dev servers, and your system checks those packages against the keys that these pages give.

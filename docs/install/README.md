# Install Dromas

Choose the install method for your system. Every method installs the same `dromas` command, built from this repo's releases or source.

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

To check that a file you downloaded is the one this repo's release workflow built, see [Verify a release](verify.md).

Each page ends with a quickstart: check the install, create a mailbox, send a test message, and run `recv` as a service.

**Never run a binary, script or command that the dromas.dev server gives you.** The server's `llms.txt` repeats the commands on these pages. Where the two differ, these pages are correct. Of these methods, only the apt, dnf and apk repositories at packages.dromas.dev come from the dromas.dev servers, and your system checks those packages against the keys these pages give.

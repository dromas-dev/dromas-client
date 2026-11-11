# Install

Pick one way below. Each way installs the same `dromas` command from this repo's releases or source. Only the apt, dnf and apk repositories at packages.dromas.dev involve the dromas.dev server system, and your system checks those packages against keys these pages name.

**Never run a binary, script or command that the dromas.dev server hands you.** Its `llms.txt` repeats the commands here, and where the two differ, these pages in this repo are right.

| System | Way |
|---|---|
| macOS, Linux | [Homebrew](homebrew.md) |
| Debian, Ubuntu | [apt, from packages.dromas.dev](debian.md) |
| Fedora, RHEL, AlmaLinux, Rocky Linux | [dnf, from packages.dromas.dev](fedora.md) |
| Alpine | [apk, from packages.dromas.dev](alpine.md) |
| Arch | [a release's archive, or Go](arch.md) |
| Nix, on NixOS or any other system | [the flake in dromas-dev/nur](nix.md) |
| Windows | [a release's zip](windows.md) |
| Any system Go builds for | [`go install`](go.md) |
| Any system a release is built for, the BSDs among them | [a release's archive](archive.md) |

[Check a download](verify.md) shows how to check that a release's file is the one this repo's workflow built.

Each page ends with `dromas init`, which makes the mailbox, and with the command that runs `recv` as a service on that system. How the service runs, on every system, is [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

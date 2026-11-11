# Install on Arch Linux

Arch has no package of `dromas`. Install the Linux build from [a release's archive](archive.md), or build the command with [Go](go.md).

The Linux archive carries the systemd user unit as `packaging/systemd/dromas@.service`. Copy the unit to `~/.config/systemd/user/`, and point its `ExecStart` at the path where you installed `dromas`. How the unit runs: [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

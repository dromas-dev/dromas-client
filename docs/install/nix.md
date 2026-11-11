# Install with Nix

[dromas-dev/nur](https://github.com/dromas-dev/nur) is a flake whose `dromas` package is this repo's release. This repo's release workflow writes the package at each release tag, from that release's archives. The flake needs Nix with flakes enabled.

```sh
nix run github:dromas-dev/nur#dromas -- version   # run the command once, with nothing installed
nix profile add github:dromas-dev/nur#dromas      # install it: Nix 2.30 or later, or nix profile install before 2.30
dromas init
```

The package installs the `dromas` command alone. Run `dromas recv --follow --headers --profile <profile>` under the service manager you use: [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

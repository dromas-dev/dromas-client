# Build from source

On any system that Go supports, `go install` builds `dromas` from the source of this repository through the Go module proxy and checksum database. You need Go 1.27 or later. `go install` puts `dromas` in the directory that `GOBIN` names, which is `$(go env GOPATH)/bin` by default. That directory must be on your `PATH`.

```sh
go install github.com/dromas-dev/dromas-client/cmd/dromas@latest
go version -m "$(command -v dromas)"
```

`@latest` builds the newest release. To build a specific commit, use `@<commit>` instead. `go version -m` prints the version of the module that the binary was built from, and the module's checksum. For a commit, the version is a pseudo-version, which ends with the first 12 characters of the commit hash.

## Quickstart

1. Check that `dromas` runs:

   ```sh
   dromas version
   ```

2. Create a mailbox. The `dromas init` command prints the link to your mailbox. Share this link with anyone who needs to send you messages.

   ```sh
   dromas init
   ```

3. Send a message to the echo mailbox, and wait for the reply. The echo mailbox at dromas.dev replies within seconds with a signed reply that contains the message you sent. The operator of dromas.dev can read the messages that you send to the echo mailbox.

   ```sh
   echo hello | dromas send https://dromas.dev/to/d_QGnwhx4dTMGWuVDvVof0-J6p-8tA4oxE4q9mw0fzMSS9G0g9m4QVEnPk2d-b69gR
   dromas recv --wait 3m
   ```

4. Run `recv` as a service. To do this, run `dromas recv --follow --headers` under your service manager. For details, see [Run as a service](../guide.md#run-as-a-service).

`go install` installs no shell completions. To write one, run `dromas completion bash`, `dromas completion zsh`, `dromas completion fish` or `dromas completion powershell`, and save its output where your shell reads completions.

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

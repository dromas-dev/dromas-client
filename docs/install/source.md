# Build from source

On any system that Go supports, `go install` builds `dromas` from the source of this repository through the Go module proxy and checksum database. You need Go 1.27 or later.

```sh
go install github.com/dromas-dev/dromas-client/cmd/dromas@latest
go version -m "$(command -v dromas)"
```

`@latest` builds the newest release. To build a specific commit, use `@<commit>` instead. `go version -m` prints the module and commit that the binary was built from.

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

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

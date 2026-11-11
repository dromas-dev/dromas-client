# Build from source

`go install` builds `dromas` from this repo's source, through Go's module proxy and checksum database, on any system Go supports. You need Go 1.27 or later.

```sh
go install github.com/dromas-dev/dromas-client/cmd/dromas@latest
go version -m "$(command -v dromas)"
```

`@latest` builds the newest release, and `@<commit>` builds that commit. `go version -m` prints the module and commit that a binary was built from.

## Quickstart

1. Check that `dromas` runs:

   ```sh
   dromas version
   ```

2. Create a mailbox. `dromas init` prints the mailbox's link: give the link to whoever will write to you.

   ```sh
   dromas init
   ```

3. Send a message to the echo mailbox, and wait for the reply. The echo mailbox at dromas.dev answers within seconds with a signed reply that carries your message back. The operator of dromas.dev can read what you send to the echo mailbox.

   ```sh
   echo hello | dromas send https://dromas.dev/to/d_QGnwhx4dTMGWuVDvVof0-J6p-8tA4oxE4q9mw0fzMSS9G0g9m4QVEnPk2d-b69gR
   dromas recv --wait 3m
   ```

4. Run `recv` as a service: run `dromas recv --follow --headers` under the service manager you use. See [Run as a service](../guide.md#run-as-a-service).

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and send and receive.
- [User guide](../guide.md): back up the mailbox, protect its key, choose who can write to you, and run `recv` as a service.

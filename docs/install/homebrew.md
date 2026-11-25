# Install with Homebrew

On macOS and Linux, the Homebrew tap `dromas-dev/tap` builds `dromas` from the source of a release. To install `dromas`, run the following command:

```sh
brew install dromas-dev/tap/dromas
```

To install a new release, run the following commands. `brew upgrade` installs a new release of the tap only after `brew update` fetches it, and the running service keeps the old binary until it restarts:

```sh
brew update && brew upgrade dromas
brew services restart dromas
```

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

4. Run `recv` as a service. The following command starts `recv` now and each time you log in:

   ```sh
   brew services start dromas
   ```

   The service runs `recv` with `--headers`, so the log at `$(brew --prefix)/var/log/dromas.log` records who sent each message and when. Other users can read the log, as they can read the log of every Homebrew service.

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

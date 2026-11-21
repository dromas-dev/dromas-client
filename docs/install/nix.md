# Install with Nix

[dromas-dev/nur](https://github.com/dromas-dev/nur) is a flake whose `dromas` package is the release from this repository. At each release tag, the release workflow of this repository writes the package from the archives of that release. You need Nix with flakes enabled.

To run `dromas` once without installing it, run the following command:

```sh
nix run github:dromas-dev/nur#dromas -- version
```

To install `dromas` in your profile with Nix 2.30 or later, run the following command:

```sh
nix profile add github:dromas-dev/nur#dromas
```

With Nix versions earlier than 2.30, use `nix profile install` instead.

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

4. Run `recv` as a service. The package installs no service file, so run `dromas recv --follow --headers` under your service manager. For details, see [Run as a service](../guide.md#run-as-a-service).

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

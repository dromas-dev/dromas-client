# Install on Windows

Each release carries a zip archive for `amd64` and `arm64`. [Verify the download](verify.md) before you install `dromas`.

1. Download the zip archive:

   ```powershell
   gh release download -R dromas-dev/dromas-client -p 'dromas_*_windows_amd64.zip'   # or arm64
   ```

2. Extract the archive. Replace `<version>` with the version in the file's name:

   ```powershell
   Expand-Archive dromas_<version>_windows_amd64.zip "$env:LOCALAPPDATA\Programs\dromas"
   ```

3. Add the folder to your `Path`, then open a new shell:

   ```powershell
   [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ";$env:LOCALAPPDATA\Programs\dromas", 'User')
   ```

## Quickstart

1. Check that `dromas` runs:

   ```powershell
   dromas version
   ```

2. Create a mailbox. `dromas init` prints the mailbox's link: give the link to whoever will write to you.

   ```powershell
   dromas init
   ```

3. Send a message to the echo mailbox, and wait for the reply. The echo mailbox at dromas.dev answers within seconds with a signed reply that carries your message back. The operator of dromas.dev can read what you send to the echo mailbox.

   ```powershell
   echo hello | dromas send https://dromas.dev/to/d_QGnwhx4dTMGWuVDvVof0-J6p-8tA4oxE4q9mw0fzMSS9G0g9m4QVEnPk2d-b69gR
   dromas recv --wait 3m
   ```

4. Run `recv` as a service: create a Task Scheduler task that runs `dromas recv --follow --headers` at log on, with no time limit. See [Run as a service](../guide.md#run-as-a-service).

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and send and receive.
- [User guide](../guide.md): back up the mailbox, protect its key, choose who can write to you, and run `recv` as a service.

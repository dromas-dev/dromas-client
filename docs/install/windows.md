# Install on Windows

Each release includes a zip archive for `amd64` and `arm64`.

1. Download the zip archive:

   ```powershell
   gh release download -R dromas-dev/dromas-client -p 'dromas_*_windows_amd64.zip'   # or arm64
   ```

2. Verify the download. For instructions, see [Verify a release](verify.md).

3. Extract the archive. Replace `<version>` with the version in the filename:

   ```powershell
   Expand-Archive dromas_<version>_windows_amd64.zip "$env:LOCALAPPDATA\Programs\dromas"
   ```

4. Add the folder to your `Path`, and then open a new shell:

   ```powershell
   [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ";$env:LOCALAPPDATA\Programs\dromas", 'User')   # then open a new shell
   ```

## Quickstart

1. Check that `dromas` runs:

   ```powershell
   dromas version
   ```

2. Create a mailbox. The `dromas init` command prints the link to your mailbox. Share this link with anyone who needs to send you messages.

   ```powershell
   dromas init
   ```

3. Send a message to the echo mailbox, and wait for the reply. The echo mailbox at dromas.dev replies within seconds with a signed reply that contains the message you sent. The operator of dromas.dev can read the messages that you send to the echo mailbox.

   ```powershell
   echo hello | dromas send https://dromas.dev/to/d_QGnwhx4dTMGWuVDvVof0-J6p-8tA4oxE4q9mw0fzMSS9G0g9m4QVEnPk2d-b69gR
   dromas recv --wait 3m
   ```

4. Run `recv` as a service. The following PowerShell commands register a Task Scheduler task and start it. The task runs `recv` each time you sign in, and writes the lines that `recv` prints to a log file:

   ```powershell
   $p = 'default'
   $log = "$env:LOCALAPPDATA\dromas\$p.log"
   New-Item -ItemType Directory -Force (Split-Path $log) | Out-Null
   $user = "$env:USERDOMAIN\$env:USERNAME"
   Register-ScheduledTask -TaskName "dromas $p" `
     -Action (New-ScheduledTaskAction -Execute (Get-Command dromas).Source -Argument "recv --follow --headers --profile $p --log-file `"$log`"") `
     -Trigger (New-ScheduledTaskTrigger -AtLogOn -User $user) `
     -Principal (New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited) `
     -Settings (New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1))
   Start-ScheduledTask -TaskName "dromas $p"
   ```

   To check, upgrade, or remove the task, see [Run as a service](../guide.md#windows).

## Next steps

- [Talk to another agent](../../README.md#talk-to-another-agent): exchange links with another agent, and then send and receive messages.
- [User guide](../guide.md): back up your mailbox, protect its key, choose who can write to you, and run `recv` as a service.

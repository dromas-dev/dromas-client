# Install on Windows

A release carries a zip for `amd64` and for `arm64`. [Check a download](verify.md) before the install.

```powershell
gh release download -R dromas-dev/dromas-client -p 'dromas_*_windows_amd64.zip'   # or arm64
Expand-Archive dromas_<version>_windows_amd64.zip "$env:LOCALAPPDATA\Programs\dromas"   # <version> as in the file's name
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ";$env:LOCALAPPDATA\Programs\dromas", 'User')   # then open a new shell
dromas init
# optional: the service, as a Task Scheduler task at log on that runs dromas recv --follow --headers (Running a mailbox § Run it as a service)
```

How the service runs: [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

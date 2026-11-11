# Install with Homebrew

The tap `dromas-dev/tap` builds `dromas` from a release's source, on macOS and on Linux.

```sh
brew install dromas-dev/tap/dromas
dromas init
brew services start dromas       # optional: the service, now and at each login
```

`brew upgrade dromas` installs a new release once the tap has the release, and `brew services restart dromas` then restarts the service.

How the service runs: [Running a mailbox § Run it as a service](../mailbox.md#run-it-as-a-service).

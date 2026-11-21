# Documentation

These pages describe the `dromas` command and the protocol behind it. The dromas.dev/docs/ site shows each page as it is at the commit of this repository that the site names, and links to the page's source. Where dromas.dev and this repository differ, this repository is correct.

- [Quickstart](../README.md): what Dromas is, a test with the echo mailbox, and two agents that talk to each other
- [Install](install/README.md): a page for each install method, and how to verify a release
- [User guide](guide.md): the config file, sending and receiving, running `recv` as a service, senders, backup and restore, key protection, and revocation
- [Command reference](commands/dromas.md): every command and flag, generated from the command help, and installed by each package as the man pages `dromas(1)` and `dromas-<command>(1)`
- [Configuration reference](configuration.md): every config file key and the environment variables, installed as the man page `dromas.toml(5)`
- [Output format](output.md): every line type, exit code, and refusal code, installed as the man page `dromas-output(7)`
- [Security](../SECURITY.md): how to report a vulnerability, and each security claim with the code and tests that hold it
- [Protocol reference](../PROTOCOL.md): wire formats, client checks, server rules, and security considerations

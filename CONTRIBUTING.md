# Contributing

## Sign off every commit

This project uses the [Developer Certificate of Origin](https://developercertificate.org/) (DCO), version 1.1. Every commit must have a `Signed-off-by:` line with the name and email address of the commit's author. To add the line, commit with the `-s` flag:

```sh
git commit -s
```

When you add that line, you certify the statement in [The certificate](#the-certificate) for that commit. If a commit in a pull request doesn't have the line, the pull request fails the DCO check and isn't merged. To add the line to commits that you already made, run the first of the following commands for the last commit, or the second for every commit on your branch, where `main` is the branch that you based it on. Then run the third to push:

```sh
git commit --amend -s --no-edit     # the last commit
git rebase --signoff main           # every commit on your branch
git push --force-with-lease         # then update the pull request
```

**If an AI agent wrote the change, you still sign it off.** You certify the change and you're responsible for it, however it was written. An agent can run `git commit -s` for you only after you tell it to. The command writes the line from your Git identity. An agent never signs off as itself, and never writes the line by hand. Name the tool in a `Co-Authored-By:` or `Assisted-by:` trailer. The maintainer's own commits name Custos, the maintainer's AI assistant, in this way, and have the maintainer's sign-off.

## License of contributions

You make each contribution to this repository under the license of this repository, Apache-2.0 (`LICENSE`). There is no contributor license agreement.

The server, [dromas-server](https://github.com/dromas-dev/dromas-server), is also licensed under Apache-2.0.

## Where a change goes

- **A change to the wire protocol, or to a check or refusal of what the wire carries:** change the specification, `PROTOCOL.md`, in the same commit as the code that implements the change, and include in that commit a test vector or a test that simulates a hostile server.
- **A change to a client's own behavior (what a command prints, its retries, its files, its defaults, and how it refuses its owner's own input):** change the code, and its doc comment or the command's help, and include a test. The specification doesn't restate a client's own behavior.
- **A change to how the server does something:** make the change in the server's repository.
- **A question about the protocol:** open an issue in this repository, where the open questions are.
- **A vulnerability:** report it privately, never in an issue, as `SECURITY.md` describes.

## The certificate

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

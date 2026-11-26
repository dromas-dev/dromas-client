# AGENTS.md — dromas-client

**The verifiable client side of Dromas: the protocol's spec, the Go client and `dromas` command, a client in plain shell on stock `age` and `ssh-keygen`, test vectors, and tests that run the client against a server that forges.** Dromas carries end-to-end encrypted, sender-authenticated messages between bots, built so that nobody has to trust its server: everything a mailbox relies on is checked here, in code you can read and by tests you can run.

🛑 **This repo is public: every push publishes.** Never commit a secret, a private key (the vectors' keys are published test keys: § Tests), a token or an internal hostname, and never copy text from a private note without a decision to publish it.

- **Is Dromas secure?** → `SECURITY.md`: each security claim, the spec section with the whole rule, the code and test that hold it, and how to report a vulnerability privately.
- **The rules a client follows** → `PROTOCOL.md`, then the doc comment of the code. This file does not restate them. § Rules by area lists only the traps: what a change in each area is likely to break.
- **Changing something** → § Contributing, then the area you touch in § Rules by area.

## Contributing

### Sign off every commit

Each commit carries your human's sign-off, made with `git commit -s` (the Developer Certificate of Origin: `CONTRIBUTING.md`).
- **The sign-off is your human's certification, not yours.** Tell them the repo requires it. With their say-so you may run `git commit -s` for them: it writes the line from their git identity.
- 🛑 **Never sign as yourself, never write a `Signed-off-by:` line by hand, and never put anyone else's name on one.**
- **Name yourself** in an `Assisted-by:` or `Co-Authored-By:` trailer.
- A pull request whose commits lack the line fails the DCO check and is not merged. `CONTRIBUTING.md` has the commands that add it to commits already made.

### Where things are written

Each fact has one home. Put it there, and point to it from anywhere else.

| What | Where |
|---|---|
| The wire: formats, checks, refusals, what a server promises a client | `PROTOCOL.md`, an RFC-style spec. Cite a rule by section: `PROTOCOL.md §11.10`. |
| The HTTP shapes: routes, members and their types, statuses, codes, headers | `api/openapi.yaml` (OpenAPI 3.1). No server serves it; `api/` and `api/apitest` hold code and servers to it. |
| A comparison with another system | `PRECEDENTS.md`, and nowhere else. |
| Each security claim and what holds it; how to report a vulnerability | `SECURITY.md`. |
| A behaviour of the client: what a command prints, its steps, retries, files, defaults | The code's doc comment or the command's help, held by a test. |
| The manual: install, user guide, command, configuration and output references | `docs/`, shown on dromas.dev/docs/ as of the commit the site builds from. |
| The quickstart | `docs/quickstart.md`, its commands copied from `docs/install/`. |
| The repo's layout; a mailbox's files | `README.md` § What is here; the top of `client/doc.go`. |
| The direction for the next year: what is planned, later and not planned | `ROADMAP.md`, one line each, pointing at the issue where one exists. A pull request that builds, drops or changes a planned item edits its line. |
| The releases of each distribution that Dromas supports | The page of `docs/install/` for its package repository: the releases its distribution still supports, the same list as the test matrix in `dromas-dev/packages` `publish.yml`. A release leaves both when its distribution ends its support for it. |
| Anything undecided or not built | An issue. Every open issue is wanted for v1; one whose outcome could change the wire is settled before v1. |

- **`PROTOCOL.md` states the wire and nothing else.** It gives each rule once, a procedure's BCP 14 keyword at its head, and the reasoning in §15 (the threat model, what a hostile server can and cannot do, the residual risks). Elsewhere a rule gives at most a clause of reason, pointing at §15. It cites a standard rather than restating it. It never prescribes the client's own behaviour (the table above), what an owner does with the machine (backups, permissions), how a server stores things, a setting's value, history, issue numbers or a comparison. Where one of these costs a security property, §15 states the cost. A change to the wire changes the spec in the same commit as the code.
- **`PRECEDENTS.md`:** Part 1 sets Dromas beside whole systems, Part 2 each decision beside the systems that make the same one, Part 3 the reference server's own build choices. Add a block when a big decision on the wire or the security model is made; a client's own behaviour gets none, and carries its precedent in its doc comment. A block has the file's one shape (its header): the decision, its prior art, where ours differs and why, and what was rejected, one line each, defending the decision and never arguing it out. Every claim about another system says how it is known: `read <date>` with its source, or `measured <date>`; a claim not read at a primary source is not written.
- **Pages and comments describe what is true now.** No story of a change, no "was", "since" or "used to" of this project's past, and no date except when a source was `read`, a measurement `measured`, a review made (`review`) or an outside document `dated`. Why a rule holds goes in the present tense; how it came about goes in its pull request. `TestEveryPageSaysWhatHoldsNow` fails on any other date written as `YYYY-MM-DD` in a page, a comment or a workflow, outside `PRECEDENTS.md` (its own rule, above) and `testdata/`.
- **No judgement is written as a fact,** on a page, in a comment or in an issue: write what was measured, read at a source, or decided. Nothing ranks one cipher, key type or design over another without the source that says so.
- **The README is the project's front page:** what a reader needs to run a mailbox safely, each command once, and pointers into `docs/`.
  - Two lines are the maintainer's: the status line, and the line that sends a reader to the open issues. Neither gives a date for v1.
  - A limit in the README that an open issue plans to change has that issue's number beside it.
  - Who terminates TLS for dromas.dev is said in the README's § No server trust and in `SECURITY.md`, each marked as the operator's statement, and never in the status line.
  - 🛑 **Command lines are held by tests in three repos:** here, `TestHelpAndREADMENameTheEchoMailbox` holds the echo mailbox's link in the help and the README, `TestEachInstallPageEndsWithTheQuickstart` the install pages' quickstarts, and `TestTheQuickstartsCommandsAreTheInstallPages` the lines of `docs/quickstart.md` to the install pages; a test in dromas-server holds its link page's command; the site's tests require every command of `llms.txt` and the front page's install terminal to be a line of the README or of a page in `docs/`, comment included. Change a command line everywhere it appears.
- **`docs/`:**
  - Each line of a code block in `docs/quickstart.md` is a line of a page in `docs/install/` (`TestTheQuickstartsCommandsAreTheInstallPages`).
  - Each page of `docs/install/` except `README.md` and `verify.md` ends with a quickstart (check the install, create a mailbox, send to the echo mailbox, run `recv` as a service) and then next steps (`TestEachInstallPageEndsWithTheQuickstart`).
  - `docs/configuration.md` has an entry for every config key and every environment variable `cmd/dromas` or `client/` reads, added in the same commit as the key (`TestTheConfigurationReferenceNamesEveryKey`).
  - `docs/commands/` is generated from the command tree: `go test ./cmd/dromas -run TestTheCommandPagesAreTheHelp -update`.
  - The man pages are generated at each release by the hidden `dromas gen-docs` and never committed (`TestTheManPagesAreTheCommandsAndTheDocs`).
  - A link between two files of this repo is a relative path, and a `#fragment` is the id GitHub gives the heading (`TestTheDocsLinksResolve`).
- **`SECURITY.md`:** a claim goes there only with the test or code that holds it, and what is not tested is said there too. A new claim, or a change to what holds one, edits its row in the same commit.
  - A claim is one sentence of at most 35 words. Its whole rule is in the `PROTOCOL.md` section its row cites, or in the doc comments of the code that keeps it; a condition cut from a claim is written there first (`TestEachSecurityClaimIsOneSentence`).
  - A security question closed with the design kept gets the `decided` label and an entry in § Decided questions, in the pull request that closes it, or when the issue closes where no pull request does: one sentence naming the question, ending with the §15 section and the issue, as `(§15.4; #29)`, and at most one sub-bullet on what limits the risk. Where §15 lacks the reasoning, the same change adds it. Every closed `decided` issue is named by an entry; one entry may name several issues.

### How a page is written

Each page follows the published standard for its kind. A rule of this file says what the project does, never how its prose reads.

- **Pages for people follow the [Google developer documentation style guide](https://developers.google.com/style):** the README, `SECURITY.md`, `CONTRIBUTING.md`, `docs/` and the command help. Write for a person who knows the field and has never seen this project. dromas.dev's menu labels follow the same guide.
- **`PROTOCOL.md` follows RFC conventions:** the [RFC Style Guide](https://www.rfc-editor.org/rfc/rfc7322) for its layout, and BCP 14 ([RFC 2119](https://www.rfc-editor.org/rfc/rfc2119), [RFC 8174](https://www.rfc-editor.org/rfc/rfc8174)) for its requirement keywords.
- **Doc comments follow [Go Doc Comments](https://go.dev/doc/comment).**
- **A term of this project's own is only for a concept with no standard name.** Define it once, in `PROTOCOL.md` §2.3 (the spec's terminology section), and use it only in that sense; use the standard word everywhere else.
- **[Vale](https://vale.sh) checks the Markdown pages for people and `PROTOCOL.md` against the Google guide** (`.vale.ini`), and the help as `docs/commands/` renders it, where a flag, a code, a duration and a TOML list are skipped as tokens, since the help is plain text. CI's `vale` job fails on an error; a warning or a suggestion is advice. Run `vale sync` once, then `vale .`. A term of this project that Vale does not know goes in `.github/styles/config/vocabularies/Dromas/accept.txt`.
- **A rewrite keeps every rule exact:** "only", "at most", "before" and "whenever" stay where they are, and a shorter sentence that drops a condition is a change to the rule.
- **A page rewritten whole, or a `PROTOCOL.md` rule reworded without a change of meaning, is read by a stranger before it merges:** a fresh session with no project context compares the new text with the old, and each difference of meaning it finds is restored or declined with a reason. Any other change to a page gets ordinary review.

### How a design is chosen

- **Priorities, in this order:** correct encryption; then a server that is feasible and fast, not as cheap as possible; then simple things simple for the user.
- 🛑 **Follow industry practice, read at its source** (an RFC, a major system's documentation, then its code), never this project's own earlier choice. Where a standard has a reference implementation, its behaviour is the practice: OpenSSH for SSHSIG and certificates, age for age. For the concerns of a free public API (limits, renewal, revocation, abuse), Let's Encrypt is the precedent (`PRECEDENTS.md` § Limits, § Revocation). Challenge a statement of the design that differs, whoever made it. A deviation is justified in `PRECEDENTS.md` beside the systems it differs from; one with no reason there is a defect.
- 🛑 **Where the words overpromise, fix the words, never the code.** The spec, the docs and this file are the project's own. When the code does not do what they say, first ask whether the words are right by industry practice. A promise stronger than the norm is a defect in the words: change them to the simple correct behaviour, and delete any code that served the overpromise.
  - A finding is a defect only where the code fails a user, the cryptography, or a norm read at its source.
  - Judge a fix by what peers doing the same job do. A fix that adds code, state, a guard or hedging words is suspect.
- **Before v1, choose the correct fix.** Backwards compatibility and an unchanged wire carry no weight.
  - 🛑 **Nor does a rule, format or behaviour of our own** (the server's, the spec's, a client's files, the script's). One that forces a workaround is a design question: put it to the maintainer with the best design for every side and its cost, and change every side together. It hides as a command changing shape, a workaround for the other side's rule, a known variance, or a reason that names what is already written.
  - **Nor does an old tool.** Assume a current client and a modern library; what the oldest `ssh-keygen` or `age` can do is never a reason for or against a design. The script's minimum versions follow the design.
- 🛑 **A failure case gets the plainest recovery; write down its cost, never guard it.** A failure case is a lost or rolled-back state, a crash between two writes, a missing volume, a clock far off, or an owner's misconfiguration, even where a hostile server acts during one.
  - The client detects the failure, tells its owner once, and recovers the simplest way. The docs state the cost in one place.
  - Add no counter, floor, marker, set or refusal to make a failure look like the normal case.
  - Grade a finding that shows only a failure's edge by this rule: write down its cost, or decline it.
  - Keep writes crash-safe the standard way: write beside, sync, then rename into place.
- 🛑 **A format uses a layout others already use, and its overhead is a fixed number of bytes, never a share of the message.** Read what the standards do, and what went wrong with them, and give each option's cost. Every format that cannot change after v1 gets that look once (#39).
- **Bound a list of entries of like size by a count, never by bytes** (`protocol.MaxACLEntries`): one bound, set well past any real use.
- **Make speed calls among standard tools.** Never replace a standard function with hand-written code to save CPU; leave deeper optimisation until real load can be measured.
- 🛑 **Clocks: add no allowance to a check, and never make a time-limited thing that asks for exactly what its verifier accepts** (`protocol.MaxSkew`; `PROTOCOL.md` §3.4, §12.1; `PRECEDENTS.md` § Clocks). Exactness to the second is for what is cryptographic, not for how long a server keeps a message.
  - 🛑 Never delete a key a message may be waiting for to recover from a wrong clock.

### Tests

- **Every client passes `testdata/vectors/`.** The shell client is tested the way agents run shell: a fresh shell per command.
- **`client/`, `cmd/dromas` and `sh/` are tested against `internal/fakeserver`,** an honest in-memory server that a test turns hostile with a middleware (the common forgeries are `internal/forge`'s). The fake checks little by design: a test of what the real server enforces belongs in dromas-server.
- **`client/testdata/statetable.golden` records what each command does in each state of a mailbox** (`TestStateTable`). Regenerate it with `go test ./client -run TestStateTable -update`, and read each changed line before you commit it.
- **On macOS, `./sh` and `generate.sh` need bash 4 or later and GNU coreutils first in `PATH`** (the header of `scripts/check.sh`).
- 🛑 **After an edit to `sh/dromas.sh`, run `go test ./sh` with `-count=1`:** a `-run` that leaves out `TestTheScriptsBoundIsTheProtocols`, the one test that reads the script as a file, prints a cached `ok` from before the edit.
  - Where the local age makes no post-quantum keys, the tests build the age `go.mod` names (`internal/agetool`), so they run on any machine with Go.
  - To test the script against another OpenSSH, build that release (`./configure [--without-openssl] && make ssh ssh-keygen`) and put it first in `PATH`; `go test -count=1 ./sh -v` names the build and each known variance it met (`PROTOCOL.md` Appendix A).
- **Vectors:** `testdata/vectors/generate.sh` uses stock tools only. A re-run must change only what you meant to change: diff the tree. 🛑 **Its keys are published test keys** (RFC 8032's for Ed25519, mailbox E's security-key identity among them, RFC 6979 §A.2.5's for mailbox C's P-256 identity, RFC 5903 §8.1's for mailbox A's P-256 working key, RFC 9500's for mailbox D's security-key identity, C2SP's age testkit's for the prekeys of B and C): never add any other private key to this repo. A certificate is made once and kept.
- **A command killed at each step at which it writes the mailbox leaves the mailbox whole** (`client/crash_test.go`). A new write that something follows gets a `step` call and its place in `steps` (`TestStepsAreTheCode`).
- **Code that reads bytes before a signature verifies has a fuzz target, seeded from the vectors** (the `Fuzz*` functions in `protocol/` and `sshsig/`). A new parser of unsigned bytes gets one. Run each target alone in its own package: `go test -run '^$' -fuzz '^FuzzX$' -fuzztime 2m -parallel 1 ./protocol` (in the wrong package a target fuzzes nothing and still passes). Keep a crash's input as a seed.
- 🛑 **A claim about every member of a set is tested against the set built from the code, never against a list typed by hand,** which holds only the members its writer thought of (`TestEveryLockingCommandIsTriedFinished`, `TestStepsAreTheCode`, `TestEveryEntryThatWaitsIsTriedWithItsContext`).
- 🛑 **A test asserts only what the code promises.** Never assert an order, a time or a count the code does not give; one green run of a timing-dependent check proves nothing. Where two outcomes are both right, accept either.
- 🛑 **Test a check against a clock with a mailbox on a clock of its own** (`client/clock_test.go`), and run a test that races a bound in time inside a `testing/synctest` bubble, against `fakeserver.NewInMemory` (`client.Bounded` gives the default bounds): on real time, a busy runner's stall reads as the code's.
- **Tests run in parallel** (`t.Parallel()`), except one that sets a package's state (any `client.Set*` of `client/export_test.go`), the process's (`t.Setenv`), or needs a port to stay free. **A test of 5 s or more skips under `-short`,** saying so; CI never passes `-short`. **Tests build with `-tags dromas_nofsync`** (`client/fsync.go`): a test kills a process, never the machine.
- **On Windows, CI's test job runs as the built-in Administrator.** The `windows-accounts` job (daily run and hand-runs) runs the tests as other accounts (`scripts/windows-as-account.ps1`, `scripts/windows-accounts.ps1`).
- **No mutation testing:** don't add it unasked.

### Before and after every push

- **Before: `scripts/check.sh push`:** gofmt, vet, every test with the skip check and the race test, then `golangci-lint run ./...` (`.golangci.yml`, shared with dromas-server) and govulncheck. While working, `scripts/check.sh` runs vet and the `-short` tests in under a minute. On macOS it leaves `./sh` to CI. `vale .` checks the pages (§ How a page is written).
- **CI** (`.github/workflows/ci.yml`) runs `scripts/check.sh full` on every push to `main`, every pull request and every merge group, on each system and architecture a release is built for; NetBSD, DragonFly and the Intel Mac run in the daily run only, and the BSDs skip the race test but in the daily run. The header of `ci.yml` and each job's comment say which runs where. Each fuzz target runs for an hour once a week, never on a push. dromas-server's `scripts/check.sh full` also runs against the commit, through a `go.work`.
- **After: open a `needs-audit` issue in this repo, whichever repo changed, for a change that needs a second reader, never by default:** a change to what a client or the server checks, signs, keys, times or deletes, that its tests do not show right by themselves, and that nobody else read as it shipped (or whose code written after its review nobody has read). Changes to docs, wording, tests, or the shell client alone get none. It is not a gate.
  - 🛑 **The issue is the auditor's whole brief:** the commits, the claims to try to break, where to look first, what is known and open, how to run it, and what closes it. #45 is the pattern.
  - The auditor closes it saying what was read, run and tried, and files one issue, `findings: the audit of #N`: each finding numbered with its severity (Low, Medium, High), kind, expected behaviour and reproduction; then the tests to add, each one line with the control that every test passed, counted apart from the findings. Stale words are a finding, and a finding that shows only a failure case's edge is graded by § How a design is chosen before it is filed. A fix names its item (`#M item 2`, `#M tests`), and the findings issue closes once each item is fixed or declined. A finding of Medium or above, or one that breaks a `SECURITY.md` claim or the wire, also gets an issue of its own in the repo of its code.
  - 🛑 **A fix that changes behaviour is read cold before it merges,** by a reader given its diff and the findings issue, never its author's conclusions: a fix's own controls show that its tests catch what it meant, not that it is complete.

### Releases

- **A release is a signed tag `v0.<minor>.<patch>`, made by `task release`** (`Taskfile.yml`). The task checks the checkout, asks, signs with `git tag -s` and pushes; `.github/workflows/release.yml` verifies the tag, runs `ci.yml`'s jobs for a push on the tagged commit, builds and publishes with GoReleaser, then checks the published release the way `docs/install/verify.md` tells a user to and rebuilds it from its tag to compare. Its comments name whose documented practice each step is. 🛑 **Nothing in the chain is hand-written where a standard path exists.**
- 🛑 **A release is a tag the maintainer signs.**
- A dependency moves in a commit before the tag, never in the release. A platform joins `.goreleaser.yaml` once CI runs the client on it.

### Merging

`main` accepts changes only through a pull request and the merge queue, which tests each one on `main` with those queued ahead of it and squash-merges it once every required check passes (`gh pr merge <n>` queues one). Nobody bypasses it, an org admin included. `gh api repos/dromas-dev/dromas-client/rules/branches/main` lists the required checks; a CI job that is not among them can fail without blocking a merge. 🛑 **The ruleset is a setting of the repository, not a file, and only the maintainer changes it:** a pull request that renames, adds or removes a required job says so, and he changes the ruleset as it merges, or every pull request waits on a check that never reports.

## Rules by area

The rules are in `PROTOCOL.md` and in the doc comments of the code named. Each line here is a trap: a change that looks harmless and breaks a guarantee.

### Every client

- **Verification is always on, with no flag to skip it. Fail closed. Message bodies are untrusted data** (`client/doc.go`). Nothing is found from the working directory (`cmd/dromas/config.go`), and the client changes nothing it did not make (`makeDir`).
- **File permissions follow ssh's conventions** (`checkKeyFile`, `CheckConfigFile`, `judgeFiles`, `checkKeptDir`). `--unsafe-*-permissions` never covers a key.
- 🛑 **The client writes no `.gitignore` and runs no git** (`gitAbove`; `KeysInGit` only warns).
- 🛑 **The client writes the identity's key file only for a new identity** (`newIdentityKey`), **and its default agent is never `SSH_AUTH_SOCK`,** which ssh forwards (`cmd/dromas/identitykey.go`).
- 🛑 **Prekeys are never derived from a seed and never reused** (`PROTOCOL.md` §12.1, §12.4): either voids forward secrecy.
- **Nothing depends on a file before it is on disk** (`writeFile`, `dr_put`; a renewal's files: `makePrekey`).
- **The client's records about the mailbox are in its `state` file; keys, messages and the owner's files stay files** (`client/store.go`). 🛑 **No server call inside a transaction** (`update`).
- 🛑 **From v1, a change to what a mailbox's files hold, or where, is a new format** (`client/doc.go`, `client/format.go`).
- **The mailbox's lock is its `state` file** (`client/lock.go`; `locked`, `reading`). A new entry that waits ends with its context (`TestEveryEntryThatWaitsIsTriedWithItsContext`). 🛑 **The lock is not re-entrant** (`claim`): take it at the exported entry only, and call unexported twins below it (`syncACL`, `prune`, `resolve`).
- 🛑 **Lowercase a protocol token with `protocol.LowerASCII` only** (`forbidigo` in `.golangci.yml` refuses `strings`/`bytes` case functions).
- **Dependencies are minimums, moved by committed updates** (`go get -u ./... && go mod tidy`), never inside a build. Dependabot opens them weekly.

### The shell client

- **`sh/dromas.sh` proves that a mailbox made of stock `age`, `ssh-keygen`, `curl` and `jq` exchanges messages with a Go mailbox,** and nothing more: not a client to run, not a second implementation. It runs a mailbox of its own (`DR_HOME`, `~/.config/dromas-sh/`) and refuses the Go client's directory.
- **It answers to the vectors that are the proof:** every crypto and container vector, each envelope it must accept, each envelope refusal that guards the five below, and each server answer the Go client accepts, refusing each of `answerRead` (`TestVectorsAnswer`). The rest of `json/`, `answer/` and `envelope/` are the Go client's.
- 🛑 **Outside the vectors, the script is held to the Go client only where a difference would let through one of five things:** a forgery, a replay, a message its keys did not decrypt, a signature it should not make, or a pass sent where a network can read it. Such a difference becomes a vector, then a fix. Any other difference is not a defect: file nothing, add no workaround, and remove any workaround that guards none of the five.
- **The Go client is the reference** and never changes to match the script.
- 🛑 **A shell variable drops trailing line feeds and every NUL:** test a value in jq or compare bytes, never through `$(…)`; compare a document with itself rebuilt (`dr_idcheck`).
- 🛑 **The script runs in the C locale** (`LC_ALL=C`): under other locales `[0-9]` and `[A-Z]` match the wrong characters (`TestTheScriptReadsBytesInEveryLocale`).
- 🛑 **A line printed inside a function whose stdout is captured is lost:** such a function writes to `$DR/said`, which its caller prints.
- The script's minimum tool versions, and why, are in its header.

### Sending

- **The encrypted payload is git's signed-tag layout with three reader rules** (`protocol/signed.go`; `PROTOCOL.md` §9.2; `PRECEDENTS.md` § Signed payload). 🛑 A change to how a reader splits the parts breaks one of them: `OpenSigned` and `dr_open` agree on every `plain/` vector (`TestVectorsPlain`). 🛑 Never base64 the envelope inside JSON.
- **Padding** (`protocol/pad.go`; §9.3): change the arithmetic in both clients together (`TestPaddingIsTheGoClients`, `TestSealedSizeIsWhatAgeWrites`).
- **Every post seals to what its own read of the recipient returns** (`readMailbox`, `resolve`; §11.8, §12.2). 🛑 **A failed read is never an answer about the message:** it never reaches `undeliverable` and never moves an entry to `outbox/failed/` (`lookupError`).
- 🛑 **A seal is made for one post and never kept** (`seal`; `TestAPostIsDatedAtThePost`). Each check a kept seal would need grows with every finding.
- **A mailbox signs no message by a signer it holds revoked, nor under a working key that a statement it did not make shows another holds** (`seal`, `NotMade.OwnCertificate`).
- **A sender checks what it writes against the reader's rules before keeping it** (`NewJSONEnvelope`, `SendJSON`, `dr_envcheck`).
- **A prekey serial is signed once** (`Rekey`; §12.4).

### Reading what others wrote

- 🛑 **A mailbox never asks the server who a sender is, and pins one only once its signature verified** (`judge`, `pin`; §12.3).
- 🛑 **Count the age recipient stanzas before age is given any prekey** (`oneNative`, `decrypt`, `dr_judge`; §12.3 check 1).
- **Fetches, and a cut fetch's resume by range, follow §12.3** (`client/partial.go`). 🛑 **The partial file is trusted only as far as the state records it synced.**
- 🛑 **A new command that receives, sends, delivers or renews calls `keysMatch` first and joins `mailCommands` in `client/restore_test.go`:** a lost state stops each until the owner runs `dromas init --restore` (`PROTOCOL.md` §15.9 states the cost).
- 🛑 **Every prekey, current or retired, goes at the first `recv` at or past its deletion time by the client's own clock, answered or not** (`prune`, `dr_prune`), the current one's key alone: its statement signs passes and its renewal, and `lostPrekeys` reads a key past that time as deleted, not lost; **one goes earlier, and the replay set is pruned, only after a call the server answered, by the clock read then** (`dropRetired`, `pruneSeen`; §12.4). A wrong clock is a failure case (§15.7), never guarded.
- 🛑 **A `304` answers only for the tag its poll sent** (`sameTag`; §12.3). Read `If-None-Match` with `protocol.IfNoneMatch`; never split it at commas.
- 🛑 **A signature has one form** (`protocol.Dearmor`; §3.2). `ssh-keygen -Y verify` is a test of a signature, never its definition: it accepts forms the spec refuses (Appendix A).
- **Every JSON text is read by `protocol.Unmarshal` or `protocol.UnmarshalRequest`** (§3.3). 🛑 **Never `encoding/json`'s `Unmarshal` on text a server, a sender or a file brought:** it matches names in any case and keeps the last duplicate.
- 🛑 **A JSON number member is `int64` or `uint64`, never `int`, `uint`, a float or a named type** (`TestWireIntegersHaveAFixedWidth`; a serial's bound: `protocol.MaxSerial`).
- **Identity types are those of `PROTOCOL.md` §4.1** (`identityTypes`). Add a type in both clients together, with a vector for each document a client must refuse. Where the Go library and `ssh-keygen` differ on a key, the stricter rule goes into the spec and the other client makes it explicit, so one document gets one verdict. A type a client lacks is `unsupported_key_type`, never `invalid_identity` (§12.3 check 4; a prekey's: §6). 🛑 The script reads a prekey's type before it runs `age -r`, which encrypts to more types than the protocol defines.
- 🛑 **Nothing may rest on a certificate having one set of bytes:** an ECDSA signature has a second valid form.
- 🛑 **An identity key's checks are in `protocol.ParseIdentity`, not in `sshsig`.**

### Signers and the working key

- **A mailbox signs through `client/internal/sign`** (`protocol.Signer`; §5). 🛑 **A new way to sign goes in `sign`, never around it.**
- 🛑 **Compare ranks as numbers, and statements by `(c, p)`** (`protocol.Order`). **Never compare prekey serials across signers.**
- 🛑 **Find a working key by its certificate's serial, never its statement's** (`signerOf`, `rankOf`).
- 🛑 **A verifier never reads a serial as a time,** and "one above the highest" stays (`protocol.SerialAt`; `TestALostMachinesFastClockDoesNotDelayTheRemedy`).
- 🛑 **A number an attempt took is kept until its statement is published or spent** (`highestBegun`, `begin`).
- **A renewal the server accepted is recorded before anything else** (`publish`, `recordTaken`).
- 🛑 **A renewal the mailbox makes on its own never renews above a statement it did not make** once it has published one of its own (`renew`, `readOwn`): only the owner judges whose it is.
- 🛑 **`ssh-keygen -Y verify` accepts certificates the spec refuses,** so the script reads a certificate's fields from its bytes (`TestVectorsCertificate`).
- **A mailbox proves itself with a pass it signs; there is no token** (`protocol/pass.go`; §8). 🛑 **Nothing a server sends is decrypted with a key that reads messages** (§12.1).
- 🛑 **A test reads a mailbox's number and never assumes one** (`numberOf`).

### Revocation

- 🛑 **A server's `mailbox_revoked` or `sender_revoked` is checked, never believed** (`proved`; §10.1, §12.5); no code `proved` checks goes back into `undeliverable` (`TestNoAnswerProvedIsUndeliverable`).
- 🛑 **A revocation that verified is kept and never removed** (§12.5).
- 🛑 **A mailbox that holds its own revocation signs nothing more** (`finished`, `client/internal/sign`, `locked`): a new command or path gets its case in `partwayCases` (`client/partway_test.go`), which `TestEveryCommandShownItsOwnRevocationAtEachCall` runs and `TestEveryLockingCommandIsShownItsOwnRevocationPartway` holds to `lockingCommands`.
- 🛑 **A working key's signature is never a revocation** (`VerifyRevocation` verifies against the identity's bare key).
- 🛑 **No answer is not a negative answer** (`readRevocations`; §12.3 check 7).
- 🛑 **The revocation statement is fixed text and never gains a field;** add a second statement under a new tag instead.
- 🛑 **There is no way to revoke fewer certificates:** the highest verified serial stands (§10.2).
- 🛑 **`cert revoke-earlier` publishes the revocation first, whatever the renewal meets** (`RenewAndRevoke`).
- 🛑 **A mailbox reads its own state at its server through `known` alone.**
- 🛑 **One function, `renewalMends`, says whether a renewal mends an error;** call it, and never decide that anywhere else.
- 🛑 **What ends an address goes out only with that address written in full** (`publishCmd`, `publishRevokedCmd`).

### Who may write to a mailbox

The client holds messages; the server's ACL refuses them (`protocol/acl.go`, `client/senders.go`; §12.3, §11.14).
- 🛑 **`held/` is not `refused/`:** a message from a sender not yet allowed passed every check, and is held, never refused (`client/senders.go`, `client/doc.go`).
- 🛑 **A policy, `allowed` or `blocked` file that cannot be read is never read as the default** (`readSenders`).
- 🛑 **An ACL always states whom it lets in:** never drop an empty `allow`, which would read as everyone.
- 🛑 **Compare ACL documents, never tags** (`checkACL`); a drain or a send reads no ACL.
- **Tests:** `env.mailbox` lands every sender's messages; a test of where a message lands uses `heldMailbox`.

### What a mailbox prints

- **Every command prints JSON lines** (`cmd/dromas/output.go`; `docs/output.md`). 🛑 **Build a line with `line` or `typed`, never by formatting text into JSON.** A new line type goes in `docs/output.md`'s table in the same commit.
- 🛑 **The envelope's members appear only inside `envelope`** (`TestNoLineCarriesAnEnvelopeMemberBesideTheEnvelope`).
- **Exit statuses are `report`'s and the help's** (`needsALook`, `cmd/dromas/main.go`): a refused message never changes `recv`'s.
- 🛑 **Nothing someone else wrote is printed as this client's words** (`cmd/dromas/output.go`, `client/errors.go`; `TestAnAPIErrorPrintsNoneOfTheServersWords`, `TestATransportErrorPrintsNoneOfTheServersWords`). A new code gets its sentence (`APIError.words`, `dr_api`) in the same commit. A new error path that formats outside text with `%s`, `%q` or `%v` breaks this rule.
- **Both clients escape the same characters nobody sees** (`escapeUnseen`, `dr_line`).
- 🛑 **`refused/` keeps none of the server's strings in the clear** (`servedRefusal`), and the state never records the identity a failed signature claims as its signer (`keptFindings`).
- 🛑 **Never hold a message on the server to wait for a clock:** a refusal for a time is a verdict like any other (§12.3).
- 🛑 **A secret is never an argument** (`account join` reads stdin; `recv --webhook` reads files), and no line carries one (`TestNoWebhookLineCarriesItsSecrets`).

### The command line

`dromas <command>`, or `dromas <group> <command>` (`cmd/dromas`, on cobra). A renamed command keeps no old name before v1. `sh/dromas.sh` uses the same words.
- **Help works as in git-like tools** (clig.dev). 🛑 **`--help` prints help and runs nothing,** wherever it appears (`TestHelpRunsNothing`, `TestHelpIsReadAsAnyFlag`).
- **A wrong command line runs nothing and exits 2** (`usageError`).
- 🛑 **No flag has a shorthand of its own:** `-h` stays the deprecated alias of `--help`, in no help, and never gets another meaning; flags are written with two dashes everywhere (`TestFlagsAreWrittenWithTwoDashes`).

### Names and modules

- **The name** is Dromas in the prose of the README and `docs/`, and `dromas` in every command, label, path and format.
- **The words:** a *message* is sent, with its *envelope* and *body*; it lands in a *mailbox*, whose folders are `inbox/`, `outbox/`, `held/`, `refused/` and `sent/`. Never "mail" or "box" in prose, help, a line, a code or a name (`TestTheClientSaysMessageAndMailbox`, `TestTheWireSaysMessageAndMailbox`).
- **Module path:** `github.com/dromas-dev/dromas-client`. Installing never depends on the dromas.dev server.
- **`sshsig/` implements exactly the API accepted for `x/crypto/ssh`** (golang/go#68197). When `x/crypto` ships it, check that its `Verify` behaves the same, then switch the import and delete ours.

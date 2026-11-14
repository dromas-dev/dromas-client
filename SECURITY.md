# Security

Dromas is built so that nobody has to trust its server. Everything that protects a message runs in a client, in this repo, and is held by tests that play a server that forges. This file says how to check that, and how to tell us when it is wrong.

## Supported versions

**Dromas is pre-v1, and only the latest release and the latest commit on `main` are supported.** A fix is a commit on `main`, released as the next patch. The protocol may still change until it is frozen, which we expect by 30 November 2026; what must be settled first is every open issue on both trackers.

## Reporting a vulnerability

**Report it privately, with GitHub's private vulnerability reporting:** the **Report a vulnerability** button under this repo's Security tab, or from a terminal:

```sh
gh api repos/dromas-dev/dromas-client/security-advisories/reports \
  -f summary='…' -f description='…'
```

- **Here:** the protocol and its specification (`PROTOCOL.md`), both clients and the test vectors. **The server, and dromas.dev as it is run:** [dromas-server](https://github.com/dromas-dev/dromas-server/security), by the same route. Not sure which? Either one.
- **Give what it takes to reproduce it:** what you did, what happened, what should have, the commit you ran, and the impact. GitHub's form asks for a summary, details, a proof of concept and the impact.
- **AI-assisted reports are welcome**; the form has a mailbox to say so.
- 🛑 **Never put a real mailbox's identity key, a pass or a message in a report,** private or public.
- If private reporting is unavailable, open an issue asking for a security contact, with no detail.

**Not a vulnerability, though a better design is welcome as an issue:**
- A question already decided: § Decided questions, below.
- A weakness already public: an open issue, or one of dromas-server's `LIMITS.md` § Known weaknesses.

**What happens next.** Reports reach the maintainer, who works them with Custos, his AI assistant. The fix lands in this repo together with the published advisory, which credits the reporter unless they ask otherwise. Reports are answered on a best-effort basis, and a fix ships as the next patch.

## Checking the claims yourself

Each row is a claim in one sentence, the section of `PROTOCOL.md` that gives the whole rule, and what holds the claim: the code that keeps it, then the tests that try to break it, grouped by file. A row whose claim, or part of it, no test holds says so. For a claim that is not the protocol's, the doc comments of the code that keeps it give the whole rule. What the claims leave open is under [Decided questions](#decided-questions).

| Claim | `PROTOCOL.md` | Held by |
|---|---|---|
| **Standard cryptography:** age with ML-KEM-768 and X25519 encrypts, Ed25519 or ECDSA P-256 signs in SSHSIG, SHA-384 names each mailbox, and stock `age`, `ssh-keygen` and `openssl` check the crypto vectors | §3.1, §16 | `protocol/`, `sshsig/`, `testdata/vectors/`; `protocol/stocktools_test.go`: `TestVectorsByToolsNotOurs`; `protocol/vectors_test.go`: `TestVectorMessage`, `TestVectorSignatures`, `TestVectorEnvelopes`; `sshsig/sshsig_test.go`: `TestSSHKeygen`; `protocol/certificate_test.go`: `TestCertificateAgainstSSHKeygen`; `sh/sh_test.go`: `TestWithGoClient` (the stock-tools script and a Go mailbox), `TestVectorsMessage` |
| **Self-certifying addresses:** an address is the SHA-384 of its identity document, so a sender that has the right address trusts no server for the mailbox's keys | §4, §15.1 | `protocol/identity.go`; `protocol/protocol_test.go`: `TestParseAddress`; `protocol/stocktools_test.go`: `TestVectorsByToolsNotOurs` (openssl's SHA-384 of each document); `client/forgery_test.go`: `TestForgeries` (an identity that does not hash to the address); `sh/messages_test.go`: `TestScriptChecksTheMailboxItIsServed` |
| **Confidentiality:** only the recipient can decrypt a message, because the sender's machine encrypts it to the recipient's prekey, and the server never holds plaintext or a private key | §9.4, §15 | `protocol/signed.go`, `client/send.go`, `sh/dromas.sh` (`dr_seal`); `protocol/vectors_test.go`: `TestVectorMessage` (the sealed layout); `client/revokedcerts_test.go`: `TestASendNamesItsStatement` (the body is an age file); `client/forgery_test.go`: `TestForgeries` (sealed to the recipient alone); `sh/messages_test.go`: `TestScriptSendsAndReads` (the script posts no envelope) |
| **Post-quantum confidentiality:** a message stays confidential against a quantum computer built later, because every prekey is ML-KEM-768 with X25519, while signatures stay Ed25519 or P-256 | §9.4, §15.10 | `protocol/prekey.go`, `testdata/vectors/message/`; `protocol/vectors_test.go`: `TestVectorMessage` (one `mlkem768x25519` stanza), `TestAPrekeyOfATypeThisClientLacks`; `protocol/stocktools_test.go`: `TestVectorsByToolsNotOurs` (stock age decrypts with the hybrid key) |
| **No substituted prekey:** a sender encrypts only to a prekey that the recipient's identity, or a working key that identity certified, signed, so a server cannot put in a prekey of its own | §6, §12.2, §15 | `client/send.go`, `client/signer.go`, `sh/dromas.sh`; `client/forgery_test.go`: `TestForgeries` (a swapped prekey, an earlier prekey, a stolen working key); `client/ask_test.go`: `TestASenderAsksBeforeItSends`, `TestNoAnswerAboutARecipientPostsNothing`; `client/signer_test.go`: `TestAnIdentityStatementAfterACertificatesIsRefused`; `sh/revoke_test.go`: `TestScriptAsksBeforeItPosts`, `TestScriptAfterAWorkingKeyIsTaken` |
| **Sender authentication:** a reader accepts a message only under its sender's own signature, never on a server's word, and no option skips any of the reader's checks | §12.3, §15.4 | `client/recv.go`; `client/forgery_test.go`: `TestAFromTheServerListsIsNotRead`, `TestAFromTheServerListsDecidesNothing`, `TestForgeries`; `client/hostile_test.go`: `TestMadeUpSendersLeaveNothingInPeers`, `TestARefusalNeverQuotesTheMessage` (each malformed envelope refused); `client/crash_test.go`: `TestADrainKilledAtEachStepOfARefusalKeepsItOnce` |
| **Recipient binding:** the signature covers the recipient, so a recipient cannot pass a message on to a third mailbox as one that the sender wrote to that mailbox | §9.2, §12.3 check 6, §15 | `protocol/envelope.go`; `protocol/vectors_test.go`: `TestVectorEnvelopes` (`refuse-to-another-mailbox`); `sh/sh_test.go`: `TestVectorsEnvelope` |
| **Key-compromise impersonation resistance:** a party that steals a mailbox's prekey can decrypt what was sealed to it, but cannot forge a message to that mailbox, because the sender signs with another key | §15 | `protocol/signed.go`; `client/forgery_test.go`: `TestForgeries` (a message in another mailbox's name) |
| **Forward secrecy:** the Go client deletes a retired prekey at the first emptying drain an hour after its replacement is taken, and the shell client 7 days past that prekey's `not_after` | §12.1, §12.4, §15.3 | `client/rekey.go`, `sh/dromas.sh` (`dr_prune`); `client/renew_test.go`: `TestAnEmptyingDrainDeletesEveryRetiredPrekeyAnHourAfterTheTake`, `TestTheOlderRetiredPrekeysGoByTheNewestRetiredOnesTake`, `TestAMailboxReceivedAtItsCadenceDeletesAtTheRenewingCheckIn`; `client/clock_test.go`: `TestADrainDeletesARetiredKeyPastItsDeletionTime`, `TestAPrekeyPastItsDeletionTimeWithNoServerConfirmingItIsSaid` (kept until a server answers); `sh/renew_test.go`: `TestScriptDeletesARetiredPrekey` |
| **Replay protection:** while its state file is intact, a reader delivers no message twice, so a replay or a retry within 14 days and an hour of its `sent_at` is acked and not delivered | §12.2, §12.3 check 8, §15.9 | `client/recv.go` (`seenMsgID`), `sh/dromas.sh`; `client/failure_test.go`: `TestDuplicateRetryIsAckedNotDelivered`; `client/replay_retry_test.go`: `TestARetryAfterAStepOfTheHour`, `TestAStretchedRetryAtClocksAnHonestServerTakes`; `client/replay_test.go`: `TestAReplayIsRefusedAfterTheClockStepsBack`; `client/dating_test.go`: `TestARetryDatedAfreshIsDeliveredOnce`, `TestAPostIsDatedAtThePost`; `sh/clock_test.go`: `TestScriptKeepsItsReplaySetByItsClock` |
| **Revocation:** a client takes an address or a certificate as revoked only on the identity's own signature, and then refuses for good every message, statement and pass that the revocation covers | §10, §12.5, §15.5, §15.6 | `client/revocation.go`, `protocol/revokedcerts.go`, `sh/dromas.sh`; `client/revocation_test.go`: `TestAServersWordRevokesNothing`, `TestWhatIsNotARevocationRevokesNothing`, `TestAMessageFromARevokedSenderIsRefused` (after the server hides it), `TestAKeptRevocationRefusesAMessageWithNoLookup`; `client/revokedcerts_test.go`: `TestRevokeEarlierEndsWhatAnOldWorkingKeySigns`; `client/signer_test.go`: `TestARevocationOfCertificatesLeavesTheIdentity`; `client/ownrevocation_test.go`: `TestAMailboxThatKeepsItsOwnRevocationSignsNothingMore`; `sh/revoke_test.go`: `TestScriptTakesARevocationOnItsProofAlone`, `TestScriptUnderItsOwnRevocations` |
| **Size hiding:** every send is padded, so the server learns a message's size only to within a padding block, whose size grows with the message's | §9.3 | `protocol/pad.go`, `client/send.go` (`seal`), `sh/dromas.sh` (`dr_padding`); `client/pad_test.go`: `TestSendPadsEveryMessage`; `sh/sh_test.go`: `TestPaddingIsTheGoClients` |
| **Bounded cost:** a hostile server can cost a mailbox only a bounded amount in each drain, in messages, list pages, bytes and time, whatever the server sends | §11.10–§11.12, §12.3; the bounds are the client's own | `client/recv.go`, `client/api.go`; `client/hostile_test.go`: `TestADrainIsBounded` (messages, pages, bytes), `TestAFetchIsTheSizeItsListGives`; `client/resume_test.go`: `TestNoSizeAServerGivesIsTakenPastTheLargestCiphertext`, `TestAFetchIsCutWhenItStallsAndNotWhileItMoves`; `client/draintime_test.go`: `TestADrainHoldsTheLockForItsTimeAndTheNextGoesOnFromIt`; `client/stall_internal_test.go`: `TestAnyOtherCallIsBoundedWhole` |
| **Output safety:** nothing a sender or a server wrote is printed as the client's own words, and a message body reaches output only inside its envelope, with control and invisible characters escaped | §11.3, §11.17; the rest is the client's own | `client/errors.go`, `client/recv.go` (`escapeUnseen`), `cmd/dromas/output.go`; `client/hostile_test.go`: `TestARefusalNeverQuotesTheMessage`, `TestAnAPIErrorPrintsNoneOfTheServersWords`; `client/escape_internal_test.go`: `TestNoControlCharacterReachesALineRaw`; `cmd/dromas/lines_test.go`: `TestNoLineCarriesAnEnvelopeMemberBesideTheEnvelope`; `cmd/dromas/transport_test.go`: `TestTheServersWordsReachNoStdoutLine`; `cmd/dromas/versions_test.go`: `TestTheVersionHeadersPrintNoneOfTheServersWords`; `cmd/dromas/exec_test.go`: `TestExecGetsTheLineAndItsArgumentsAsGiven` (`recv --exec` gets the line on stdin, through no shell) |
| **Transport:** a pass, and the address a lookup asks about, never cross a network in the clear, because both clients use https, or http only to a loopback IP address, and follow no redirect | §11.1 | `client/mailbox.go` (`checkServerURL`, `followNone`), `sh/dromas.sh` (`dr_api`); `client/transport_test.go`: `TestARedirectIsNeverFollowed`, `TestNoCallOfAMailboxFollowsARedirect`, `TestAServerIsCalledOverHTTPSOrOnThisMachine`; `client/transport_internal_test.go`: `TestEveryRequestIsHeldToTheServerRule`; `sh/sh_test.go`: `TestBothClientsCallTheSameServers`, `TestNoCallOfTheScriptFollowsARedirect`, `TestTheScriptReadsNoCurlrc` |
| **Private files:** the client creates every file owner-only, refuses a private key that other users can access, and by default refuses such a state file or kept message | the client's own | `client/private_*.go`, `client/keyfile_*.go`, `client/layout.go` (`judgeFiles`); `client/private_test.go`: `TestEveryFileOfAMailboxIsItsOwnersAlone`; `client/keyfile_unix_test.go`: `TestAPrivateKeyOthersCanReachIsRefused`, `TestAWorkingKeyOthersCanReachIsRefused`; `client/judged_test.go`: `TestEveryPrivateFileIsJudgedBeforeEveryOpen`; `client/kept_judged_test.go`: `TestAKeptMessageOthersCanReachIsRefused`, `TestTheUnsafeOptionsLeaveTheirFilesUnjudged` (the key is refused under both options); `sh/files_test.go`: `TestTheScriptRefusesAMailboxOthersCanReach`; `sh/messages_test.go`: `TestScriptInit`; on Windows, CI's `windows-accounts` job (a Guest account is not tested) |
| **Release provenance:** a release is built by this repo's workflow from a tag its maintainer signed, after its tests pass, with every file attested and its checksums signed ([Verify a release](docs/install/verify.md)) | — | `.github/workflows/release.yml`; `gh` and `cosign`, which check the attestations and the signature; the tag ruleset and immutable releases, settings of the repo that no test reads |

**Run them:** `README.md` § Tests, which says what each needs and what an `ok` does not tell you.

**What is not tested:** that a release rebuilds byte for byte anywhere but in this repo's own workflow (its `rebuild` job does it for each release from v0.1.1 on); `age-plugin-pq` against the server's check of a send's stanzas; a restore whose clock reads behind what a server's older record hides, and two machines that make a certificate in one second (`PROTOCOL.md` §5.2); OpenSSH releases other than the two that CI's Ubuntu jobs run the script and the cross-tests on, 9.6p1 and 10.2p1; age 1.3.0 and 1.3.1; macOS and BusyBox; what a callback's own processes do once `--exec-timeout` kills it, since only its one process is killed; a forgery by a party that holds the recipient's prekey; that a theft after a prekey's deletion opens none of its messages; the padding of a group send and of a flush.

## Decided questions

Each entry is a security question that is already decided, and under it, where one applies, what limits the risk or why the design accepts it. The `PROTOCOL.md` section that the entry names gives the reasoning. The issue that the entry names decided the question, and carries the `decided` label: `gh issue list -R dromas-dev/dromas-client --label decided --state closed` lists every such issue. A report of a decided question is not a vulnerability. A better design for one is welcome as an issue.

**What the server, and anyone who has an address, sees and can do**
- The operator sees who writes to whom, when, how much, and from which IP address (§15.2, §15.8; #507).
  - The operator never sees a message's content or signature, and padding hides a message's size to within a block. An honest server keeps no sender beside a stored message. `dromas-server` logs no client IP address, and writes an address or message id only as a keyed hash, under a key that `dromas-server` holds in memory and replaces each day. dromas.dev's operator states that its load balancer and TLS terminator keep no access log.
- A server can drop, delay or delete a message, and no receipt shows the loss (§15.2, §15.9; #516).
  - A server cannot read, alter or forge a message, and the sender keeps each message in `outbox/` until a server accepts it. A receipt from one client to another fits inside version 1, as an optional member of the envelope.
- The party that terminates TLS for a server is part of that server, and sees each pass in transit (§15.8; #586).
  - At dromas.dev, nginx terminates TLS on the host that runs `dromas-server`, behind a load balancer that passes TCP through, with no third party in front, as dromas.dev's operator states. A pass is valid at one server for at most an hour, and a pass decrypts nothing and signs nothing.
- The server holds each mailbox's ACL to enforce it, so an allow list shows the server senders that may never have written to the mailbox (§15.2; #576).
  - By default a mailbox's ACL allows every sender and denies the senders that the owner blocked, and the client holds a message from a sender that the owner has not allowed.

**Passes, keys and revocation**
- A pass is a bearer credential at one server for at most an hour, and is not bound to the request that carries the pass (§15.5; #570, #724).
  - Binding a pass to each request would leave the server a signed record of who writes to whom. A stolen pass gives a thief only what a hostile server can already do.
- A server can withhold a revocation from a reader that has never seen the revocation, and the protocol has no key transparency (§15.6; #572, #311).
  - A reader keeps every revocation it was shown, and a record that the owner hands over out of band settles the question. Key transparency fits inside version 1.
- A stolen working key signs messages that every reader accepts, through any server, until the key's certificate ends, at most 45 days and an hour after the certificate began (§15.5; #716).
  - Only a revocation of certificates ends the key at a reader, once the reader sees the revocation, and `dromas cert revoke-earlier` publishes one. A renewal ends the stolen key's passes and prekey statements, at the server and at each sender that sees the new certificate, and ends nothing at a reader.
- A party that copies a mailbox's identity key signs as the mailbox until the owner revokes the address (§15.5; #571, #594).
  - Revoking the address is the one remedy, and is permanent. The owner can keep the identity key in the mailbox's agent or behind a passphrase, and a mailbox whose identity is held on another machine is #459, open.

**Forward secrecy and the signature**
- Forward secrecy is per prekey, as in X3DH without a one-time prekey, and bounded by how often a mailbox checks in (§15.3; #575).
  - A mailbox that renews daily and polls deletes a message's prekey about a day after the message arrives, and a prekey lifetime under 27 hours renews sooner and shortens that. Per-prekey forward secrecy keeps no state per peer, so each mailbox renews and deletes on its own.
- A server can serve a stale prekey to a sender that has not seen a later prekey statement, until that prekey's `not_after` (§15.2).
  - The server can serve only a prekey that the mailbox's signer signed, and a sender rejects a prekey statement earlier than one the sender has seen.
- A recipient can show anyone what a sender signed, so a message is not deniable toward its recipient (§15.4; #29).
  - This is the trade-off that lets a program verify who sent each message it acts on. The server keeps no signature, and can prove nothing about who sent what.
- A verified signature proves who sent a message, and does not prove that the message is safe to obey (§15.9).
- Signatures are classical, Ed25519 or P-256, so an attacker with a quantum computer at signing time can sign as a mailbox, and senders then encrypt to the prekeys that attacker signed (§15.10).
  - A message sent before then stays confidential against a quantum computer, and a post-quantum signature type fits inside version 1 as a new identity key type.

**The mailbox's own machine**
- A mailbox that loses its state, or puts back an older copy of the state, can be served again a message that the mailbox accepted before (§15.9; #784).
  - A replay works only within that message's TTL, and keying work by `from` and `msg_id` makes a repeat harmless.
- A client never sets its clock by a server, so a clock that steps or jumps is the mailbox's own fault, and its cost is accepted (§15.7; #831).
  - A clock further off than the skew allowance is stopped at its pass before it fetches, prunes or deletes.
- A reader acks a message that the reader rejects for a time, so a message whose certificate has not begun, or whose `sent_at` is more than an hour ahead, by the reader's clock is lost (§15.7; #44).
  - A certificate's maker dates the certificate an hour back, so only a clock that is already wrong makes such a message. The reader keeps the rejected message in `refused/`, with the clock that the reader judged by.

**Senders**
- One verified sender, or many cheap identities, can fill an open mailbox to the mailbox's caps and spend the mailbox's intake budget (§15.9; dromas-server's `LIMITS.md` § Known weaknesses).
  - An allow list is the full defence for a mailbox that knows its senders.
- `in_reply_to` is the one link between two messages that a sender signs, and `sent_at` does not order a group's messages (§15.4).

**Answered in `PROTOCOL.md` §15 alone**
- What a lookup shows anyone who has an address, what a sender learns by posting a ciphertext again, what a revocation lookup and a lookup's length tell, the certificate a signature carries, ECDSA's two s values, decryption before verification, and the owner's own code (§15.1, §15.4, §15.5, §15.8; #49, #148, #352, #580, #654, #717, #719, #752).

## Where the rest is

- **The threat model, what a server sees and can and cannot do, and the residual risks:** `PROTOCOL.md` §15. For whoever runs a mailbox, in short: `README.md` § No server trust.
- **What is not built, or not decided:** the open issues, here and in [dromas-server](https://github.com/dromas-dev/dromas-server/issues); every one is wanted for v1.
- **Every design decision beside the standard or system that does the same:** `PRECEDENTS.md`.
- **The server's limits, and what each protects:** dromas-server's [`LIMITS.md`](https://github.com/dromas-dev/dromas-server/blob/main/LIMITS.md).
- **Code awaiting an outside audit:** the issues labelled `needs-audit`. Each is a brief an outside agent can work from: the commits, the claims to break, where to look first, how to run it. Auditing one is the most useful review this project can get.

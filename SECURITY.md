# Security

Dromas is designed so that nobody has to trust its server. Everything that protects a message runs in a client, in this repository, and tests that act as a forging server check most of these protections. The table in [Checking the claims yourself](#checking-the-claims-yourself) says where no test does. This page explains how to check that for yourself, and how to report a case where it fails.

## Supported versions

**Dromas is pre-v1. Only the latest release and the latest commit on `main` are supported.** A fix is a commit on `main`, released as the next patch. The protocol can still change until v1. The open issues in this repository and in [dromas-server](https://github.com/dromas-dev/dromas-server/issues) are the work planned before it.

## Reporting a vulnerability

**Report vulnerabilities privately, using GitHub's private vulnerability reporting.** On this repository's **Security** tab, click **Report a vulnerability**, or run the following command from a terminal:

```sh
gh api repos/dromas-dev/dromas-client/security-advisories/reports \
  -f summary='…' -f description='…'
```

- **Report here for** the protocol and its specification (`PROTOCOL.md`), both clients (the Go client and the shell client, `sh/dromas.sh`), and the test vectors. **For the server, and for dromas.dev as it's operated,** report to [dromas-server](https://github.com/dromas-dev/dromas-server/security) the same way. If you aren't sure which repository applies, use either one.
- **Include what's needed to reproduce the problem:** what you did, what happened, what should have happened, the commit you ran, and the impact. GitHub's form asks for a summary, details, a proof of concept, and the impact.
- **AI-assisted reports are welcome.** The form has a place to say so.
- 🛑 **Never include a real mailbox's identity key, a pass (the statement that a mailbox signs to prove itself to its server), or a message in a report,** whether the report is private or public.
- If private reporting is unavailable, open an issue that asks for a security contact, and include no details.

**The following aren't vulnerabilities, but you're welcome to propose a better design in an issue:**
- A question that's already decided. See [Decided questions](#decided-questions).
- A weakness that's already public: an open issue, or an entry in dromas-server's `LIMITS.md` § Known weaknesses.

**What happens next.** Reports go to the maintainer, who handles them with Custos, his AI assistant. The fix lands in this repository together with the published advisory, which credits the reporter unless the reporter asks otherwise. Answers to reports are best effort, and a fix ships as the next patch.

## Checking the claims yourself

Each row of the following table gives a claim in one sentence, the section of `PROTOCOL.md` that states the whole rule, and, in the **Held by** column, the code that enforces the claim and then the tests that try to break it, grouped by file. If no test covers a claim, or part of a claim, the row says so. For a claim that isn't part of the protocol, the doc comments of the code that enforces it give the whole rule. For what the claims leave open, see [Decided questions](#decided-questions). The claims use the terms of [`PROTOCOL.md` §2.3](PROTOCOL.md#23-terms), such as *working key*, *statement*, and *drain*, and an *emptying drain* is a drain that leaves the mailbox empty.


| Claim | `PROTOCOL.md` | Held by |
|---|---|---|
| **Standard cryptography:** encryption uses age with ML-KEM-768 and X25519, signatures use Ed25519 or ECDSA P-256 in SSHSIG format, SHA-384 names each mailbox, and the stock `age`, `ssh-keygen`, and `openssl` tools check the crypto vectors | §3.1, §16 | `protocol/`, `sshsig/`, `testdata/vectors/`; `protocol/stocktools_test.go`: `TestVectorsByToolsNotOurs`; `protocol/vectors_test.go`: `TestVectorMessage`, `TestVectorSignatures`, `TestVectorEnvelopes`; `sshsig/sshsig_test.go`: `TestSSHKeygen`; `protocol/certificate_test.go`: `TestCertificateAgainstSSHKeygen`; `sh/sh_test.go`: `TestWithGoClient` (the stock-tools script and a Go mailbox), `TestVectorsMessage` |
| **Self-certifying addresses:** an address is `d_` and the base64url SHA-384 hash of its identity document, so a sender that has the correct address doesn't trust any server for the mailbox's keys | §4, §15.1 | `protocol/identity.go`; `protocol/protocol_test.go`: `TestParseAddress`; `protocol/stocktools_test.go`: `TestVectorsByToolsNotOurs` (openssl's SHA-384 of each document); `client/forgery_test.go`: `TestForgeries` (an identity that does not hash to the address); `sh/messages_test.go`: `TestScriptChecksTheMailboxItIsServed` |
| **Confidentiality:** only the recipient can decrypt a message, because the sender's machine encrypts it to the recipient's prekey, and the server never has the plaintext or a private key | §9.4, §15 | `protocol/signed.go`, `client/send.go`, `sh/dromas.sh` (`dr_seal`); `protocol/vectors_test.go`: `TestVectorMessage` (the sealed layout); `client/revokedcerts_test.go`: `TestASendNamesItsStatement` (the body is an age file); `client/forgery_test.go`: `TestForgeries` (sealed to the recipient alone); `sh/messages_test.go`: `TestScriptSendsAndReads` (the script posts no envelope) |
| **Post-quantum confidentiality:** a message stays confidential against a quantum computer built later, because every prekey uses ML-KEM-768 with X25519, while signatures remain Ed25519 or P-256 | §9.4, §15.10 | `protocol/prekey.go`, `testdata/vectors/message/`; `protocol/vectors_test.go`: `TestVectorMessage` (one `mlkem768x25519` stanza), `TestAPrekeyOfATypeThisClientLacks`; `protocol/stocktools_test.go`: `TestVectorsByToolsNotOurs` (stock age decrypts with the hybrid key) |
| **No substituted prekey:** a sender encrypts only to a prekey signed by the recipient's identity, or by a working key that the identity certified, so a server can't substitute a prekey of its own | §6, §12.2, §15 | `client/send.go`, `client/signer.go`, `sh/dromas.sh`; `client/forgery_test.go`: `TestForgeries` (a swapped prekey, an earlier prekey, a stolen working key); `client/ask_test.go`: `TestASenderAsksBeforeItSends`, `TestNoAnswerAboutARecipientPostsNothing`; `client/signer_test.go`: `TestAnIdentityStatementAfterACertificatesIsRefused`; `sh/revoke_test.go`: `TestScriptAsksBeforeItPosts`, `TestScriptAfterAWorkingKeyIsTaken` |
| **Sender authentication:** a reader accepts a message only with its sender's own valid signature, never because a server vouches for it, and no option skips any of the reader's checks | §12.3, §15.4 | `client/recv.go`; `client/forgery_test.go`: `TestAFromTheServerListsIsNotRead`, `TestAFromTheServerListsDecidesNothing`, `TestForgeries`; `client/hostile_test.go`: `TestMadeUpSendersLeaveNothingInPeers`, `TestARefusalNeverQuotesTheMessage` (each malformed envelope refused); `client/crash_test.go`: `TestADrainKilledAtEachStepOfARefusalKeepsItOnce` |
| **Recipient binding:** the signature covers the recipient, so a recipient that passes a message on to a third mailbox can't present it as one that the sender wrote to that mailbox | §9.2, §12.3 check 6, §15 | `protocol/envelope.go`; `protocol/vectors_test.go`: `TestVectorEnvelopes` (`refuse-to-another-mailbox`); `sh/sh_test.go`: `TestVectorsEnvelope` |
| **Key-compromise impersonation resistance:** anyone who steals a mailbox's prekey can decrypt the messages encrypted to that prekey, but can't forge a message to that mailbox, because the sender signs with a different key | §15 | `protocol/signed.go`; `client/forgery_test.go`: `TestForgeries` (a message in another mailbox's name) |
| **Forward secrecy:** the Go client deletes a retired prekey at the first emptying drain an hour after the server accepts its replacement, and the shell client deletes it 7 days after the prekey's `not_after` | §12.1, §12.4, §15.3 | `client/rekey.go`, `sh/dromas.sh` (`dr_prune`); `client/renew_test.go`: `TestAnEmptyingDrainDeletesEveryRetiredPrekeyAnHourAfterTheTake`, `TestTheOlderRetiredPrekeysGoByTheNewestRetiredOnesTake`, `TestAMailboxReceivedAtItsCadenceDeletesAtTheRenewingCheckIn`; `client/clock_test.go`: `TestADrainDeletesARetiredKeyPastItsDeletionTime`, `TestAPrekeyPastItsDeletionTimeWithNoServerConfirmingItIsSaid` (kept until a server answers); `sh/renew_test.go`: `TestScriptDeletesARetiredPrekey` |
| **Replay protection:** while its state file is intact, a reader never accepts a message twice, so a replay or retry within 14 days and an hour of its `sent_at` is acknowledged, not delivered | §12.2, §12.3 checks 8 and 9, §15.9 | `client/recv.go` (`seenMsgID`), `sh/dromas.sh`; `client/failure_test.go`: `TestDuplicateRetryIsAckedNotDelivered`; `client/replay_retry_test.go`: `TestARetryAfterAStepOfTheHour`, `TestAStretchedRetryAtClocksAnHonestServerTakes`; `client/replay_test.go`: `TestAReplayIsRefusedAfterTheClockStepsBack`; `client/dating_test.go`: `TestARetryDatedAfreshIsDeliveredOnce`, `TestAPostIsDatedAtThePost`; `sh/clock_test.go`: `TestScriptKeepsItsReplaySetByItsClock` |
| **Revocation:** a client treats an address or a certificate as revoked only on the identity's own signature, and then permanently rejects every message, statement, and pass that the revocation covers | §10, §12.5, §15.5, §15.6 | `client/revocation.go`, `protocol/revokedcerts.go`, `sh/dromas.sh`; `client/revocation_test.go`: `TestAServersWordRevokesNothing`, `TestWhatIsNotARevocationRevokesNothing`, `TestAMessageFromARevokedSenderIsRefused` (after the server hides it), `TestAKeptRevocationRefusesAMessageWithNoLookup`; `client/revokedcerts_test.go`: `TestRevokeEarlierEndsWhatAnOldWorkingKeySigns`; `client/signer_test.go`: `TestARevocationOfCertificatesLeavesTheIdentity`; `client/ownrevocation_test.go`: `TestAMailboxThatKeepsItsOwnRevocationSignsNothingMore`; `sh/revoke_test.go`: `TestScriptTakesARevocationOnItsProofAlone`, `TestScriptUnderItsOwnRevocations` |
| **Size hiding:** the client pads every message that it sends, so the server learns a message's size only to within a padding block, and the block size grows with the message size | §9.3 | `protocol/pad.go`, `client/send.go` (`seal`), `sh/dromas.sh` (`dr_padding`); `client/pad_test.go`: `TestSendPadsEveryMessage`; `sh/sh_test.go`: `TestPaddingIsTheGoClients` |
| **Bounded cost:** whatever a hostile server sends, it can cost a mailbox only a bounded amount in each drain, measured in messages, list pages, bytes, and time | §11.10–§11.12, §12.3; the bounds are the client's own | `client/recv.go`, `client/api.go`; `client/hostile_test.go`: `TestADrainIsBounded` (messages, pages, bytes), `TestAFetchIsTheSizeItsListGives`; `client/resume_test.go`: `TestNoSizeAServerGivesIsTakenPastTheLargestCiphertext`, `TestAFetchIsCutWhenItStallsAndNotWhileItMoves`; `client/draintime_test.go`: `TestADrainHoldsTheLockForItsTimeAndTheNextGoesOnFromIt`; `client/stall_internal_test.go`: `TestAnyOtherCallIsBoundedWhole` |
| **Output safety:** the client never prints what a sender or a server wrote as its own words, and outputs a message body only inside its envelope, in a line with control and invisible characters escaped | §11.3, §11.17; the rest is the client's own | `client/errors.go`, `client/recv.go` (`escapeUnseen`), `cmd/dromas/output.go`; `client/hostile_test.go`: `TestARefusalNeverQuotesTheMessage`, `TestAnAPIErrorPrintsNoneOfTheServersWords`; `client/escape_internal_test.go`: `TestNoControlCharacterReachesALineRaw`; `cmd/dromas/lines_test.go`: `TestNoLineCarriesAnEnvelopeMemberBesideTheEnvelope`; `cmd/dromas/transport_test.go`: `TestTheServersWordsReachNoStdoutLine`; `cmd/dromas/versions_test.go`: `TestTheVersionHeadersPrintNoneOfTheServersWords`; `cmd/dromas/exec_test.go`: `TestExecGetsTheLineAndItsArgumentsAsGiven` (`recv --exec` gets the line on stdin, through no shell); `cmd/dromas/webhook_test.go`: `TestNoWebhookLineCarriesItsSecrets`, `TestTheWebhookPostsTheLineWithTheHeaderAndTheSignature` (`recv --webhook` posts the line, and no line or error of `recv`'s, and no `dromas status` output, quotes its URL, header or secret; `dromas config` shows the URL, as it shows every setting) |
| **Transport:** a pass, and the address that a lookup asks about, never cross a network unencrypted, because both clients use HTTPS, or HTTP only to a loopback IP address, and never follow a redirect | §11.1 | `client/mailbox.go` (`checkServerURL`, `followNone`), `sh/dromas.sh` (`dr_api`); `client/transport_test.go`: `TestARedirectIsNeverFollowed`, `TestNoCallOfAMailboxFollowsARedirect`, `TestAServerIsCalledOverHTTPSOrOnThisMachine`; `client/transport_internal_test.go`: `TestEveryRequestIsHeldToTheServerRule`; `sh/sh_test.go`: `TestBothClientsCallTheSameServers`, `TestNoCallOfTheScriptFollowsARedirect`, `TestTheScriptReadsNoCurlrc` |
| **Private files:** the client creates every file owner-only, rejects a user's own private key that others can access, and by default rejects such a state file, and a list or kept message others can write | the client's own | `client/private_*.go`, `client/keyfile_*.go`, `client/layout.go` (`judgeFiles`, `judgeKept`); `client/private_test.go`: `TestEveryFileOfAMailboxIsItsOwnersAlone`; `client/keyfile_unix_test.go`: `TestAPrivateKeyOthersCanReachIsRefused`, `TestAKeyFileAnotherUserOwnsIsTheirsToGuard`, `TestAWorkingKeyOthersCanReachIsRefused`; `client/judged_test.go`: `TestEveryPrivateFileIsJudgedBeforeEveryOpen`; `client/kept_judged_test.go`: `TestAKeptMessageOthersCanWriteIsRefused`, `TestTheUnsafeOptionsLeaveTheirFilesUnjudged` (the key is refused under both options); `sh/files_test.go`: `TestTheScriptRefusesAMailboxOthersCanReach`; `sh/messages_test.go`: `TestScriptInit`; on Windows, CI's `windows-accounts` job (a Guest account is not tested) |
| **Release provenance:** this repository's workflow builds each release from a tag that the maintainer signed, after the tests pass, attests every file, and signs the checksums ([Verify a release](docs/install/verify.md)) | — | `.github/workflows/release.yml`; `gh` and `cosign`, which check the attestations and the signature; the tag ruleset and immutable releases, settings of the repository that no test reads |


**To run the tests,** see `README.md` § Tests, which gives the commands, what the shell client's tests need, and what an `ok` result for them doesn't tell you.

**What isn't tested:**
- That a release rebuilds byte for byte anywhere other than in this repository's own workflow. The workflow's `rebuild` job does this for each release from v0.1.1 on.
- A message sealed through `age-plugin-pq`, the plugin that gives post-quantum recipients to an age older than 1.3.0 and to other age implementations: whether the server's check of a sent message's stanzas accepts it, or refuses it as `invalid_recipients` (`PROTOCOL.md` §11.10, step 7).
- A mailbox restored while its clock reads behind the lost machine's clock, from a server that serves it an older record than the lost machine's last one. The restored mailbox can give its certificate a serial below a certificate the lost machine made, and the higher serial ranks higher (`PROTOCOL.md` §5.2, §5.3).
- Two machines that each make a certificate for one mailbox in the same second, so that the two certificates have the same certificate serial. A verifier accepts either one, and a server ranks neither one higher than the other (`PROTOCOL.md` §5.2, §5.3).
- OpenSSH releases other than 9.6p1 and 10.2p1, the two releases that CI's Ubuntu jobs run the script and the cross-tests on.
- age 1.3.0 and 1.3.1.
- The shell client on macOS and on BusyBox.
- What the processes that the `recv --exec` program starts do after `--exec-timeout` kills the program. Only the program's own process is killed.
- A forgery by a party that has the recipient's prekey.
- That a theft after a prekey is deleted exposes none of the messages encrypted to that prekey.
- The padding of a group send and of `dromas outbox flush`.

## Decided questions

Each entry is a security question that's already decided. Under the entry, where one applies, is what limits the risk or why the design accepts it. The `PROTOCOL.md` section that the entry names gives the reasoning. The issue that the entry names is where the question was decided, and that issue has the `decided` label. To list every such issue, run `gh issue list -R dromas-dev/dromas-client --label decided --state closed`. A report of a decided question isn't a vulnerability, but you're welcome to propose a better design in an issue.

**What the server, and anyone who has an address, can see and do**
- The server operator can see who writes to whom, when, how much, and from which IP address (§15.2, §15.8; #507).
  - The operator never sees a message's content or signature, and padding hides a message's size to within one block. An honest server doesn't keep the sender alongside a stored message. `dromas-server` doesn't log client IP addresses, and writes an address or message ID only as a keyed hash, using a key that `dromas-server` keeps in memory and replaces every day. The operator of dromas.dev states that its load balancer and TLS terminator keep no access log.
- A server can drop, delay, or delete a message, and no receipt reveals the loss (§15.2, §15.9; #516).
  - A server can't read, alter, or forge a message, and the sender keeps each message in `outbox/` until a server accepts it. A receipt from one client to another can be added within version 1, as an optional member of the envelope.
- Whoever terminates TLS for a server is part of that server, and sees each pass in transit (§15.8; #586).
  - According to the operator of dromas.dev, nginx terminates TLS on the host that runs `dromas-server`, behind a load balancer that passes TCP through, with no third party in front. A pass is valid at one server for at most an hour, and a pass can't decrypt or sign anything.
- The server must store each mailbox's ACL to enforce it, so an allow list reveals to the server senders that might never have written to the mailbox (§15.2; #576).
  - By default, a mailbox's ACL allows every sender except the senders that the owner blocked, and the client holds back a message from a sender that the owner hasn't allowed.

**Passes, keys, and revocation**
- A pass is a bearer credential, valid at one server for at most an hour, and it isn't bound to the request that carries it (§15.5; #570, #724).
  - Binding a pass to each request would give the server a signed record of who writes to whom. A stolen pass lets a thief do only what a hostile server can already do.
- A server can withhold a revocation from a reader that hasn't seen the revocation, and the protocol has no key transparency (§15.6; #572, #311).
  - A reader keeps every revocation that it has seen, and a record that the owner hands over out of band settles the question. Key transparency can be added within version 1.
- A thief can use a stolen working key to sign messages that every reader accepts, through any server, until the key's certificate expires, at most 45 days and an hour after the certificate starts (§15.5; #716).
  - Only a revocation of certificates stops a reader from accepting the key, once the reader sees the revocation. `dromas cert revoke-earlier` publishes one, and so does `dromas cert publish-revoked` with a record that the identity's holder made. A renewal invalidates the stolen key's passes and prekey statements at the server and at each sender that sees the new certificate, but changes nothing at a reader.
- Anyone who copies a mailbox's identity key can sign as the mailbox until the owner revokes the address (§15.5; #571, #594).
  - Revoking the address is the only remedy, and it's permanent. The owner can keep the identity key in the mailbox's agent, behind a passphrase, or on another machine that certifies each working key (`docs/guide.md`; #459).

**Forward secrecy and signatures**
- Forward secrecy is per prekey, as in X3DH without a one-time prekey, and limited by how often a mailbox checks in with its server (§15.3; #575).
  - A mailbox that renews daily and polls deletes a message's prekey about a day after the message arrives. A prekey lifetime under 27 hours makes the mailbox renew sooner, which shortens that time. Per-prekey forward secrecy keeps no per-peer state, so each mailbox renews and deletes independently.
- A server can serve a stale prekey to a sender that hasn't seen a later prekey statement, until that prekey's `not_after` (§15.2).
  - The server can serve only a prekey that the mailbox's signer signed, and a sender rejects a prekey statement that's earlier than one that the sender has seen.
- A recipient can show anyone what a sender signed, so a message isn't deniable to its recipient (§15.4; #29).
  - This is the trade-off that lets a program verify who sent each message that it acts on. The server keeps no signatures, and can prove nothing about who sent what.
- A verified signature proves who sent a message, but doesn't prove that the message is safe to obey (§15.9).
- Signatures are classical (Ed25519 or P-256), so an attacker who has a quantum computer at signing time can sign as a mailbox, and senders then encrypt to the prekeys that the attacker signed (§15.10).
  - A message sent before that time stays confidential against a quantum computer, and a post-quantum signature type can be added within version 1 as a new identity key type.

**The mailbox's own machine**
- If a mailbox loses its state, or restores an older copy of its state, a server can deliver a message again that the mailbox already accepted (§15.9; #784).
  - A replay works only within the message's TTL, and keying work by `from` and `msg_id` makes a repeat harmless.
- A client never sets its clock from a server, so a clock that steps or jumps is the mailbox's own fault, and the design accepts the cost (§15.7; #831).
  - If a mailbox's clock is off by more than the hour of clock skew that `PROTOCOL.md` §3.4 allows, the server rejects its pass before the mailbox fetches, prunes, or deletes anything.
- A reader acknowledges a message that it rejects on a time check, so a message is refused and never delivered if, by the reader's clock, its certificate hasn't started or its `sent_at` is more than an hour ahead (§15.7; #44).
  - Whoever makes a certificate dates it an hour back, so only a clock that's already wrong produces such a message. The reader keeps the rejected message in `refused/`, together with the clock reading that the reader used to judge it.

**Senders**
- One verified sender, or many cheap identities, can fill an open mailbox to its caps and use up its intake budget (§15.9; dromas-server's `LIMITS.md` § Known weaknesses).
  - For a mailbox that knows its senders, an allow list is a complete defense.
- `in_reply_to` is the only link between two messages that a sender signs, and `sent_at` doesn't order a group's messages (§15.4).

**Answered only in `PROTOCOL.md` §15**
- What a lookup shows anyone who has an address, what a sender learns by posting a ciphertext again, what a revocation lookup and the length of a lookup reveal, the certificate that a signature carries, ECDSA's two s values, decryption before verification, and the owner's own code (§15.1, §15.4, §15.5, §15.8; #49, #148, #352, #580, #654, #717, #719, #752).

## Where the rest is

- **The threat model, what a server sees and can and can't do, and the residual risks:** `PROTOCOL.md` §15. For a short version for people who run a mailbox, see `README.md` § No server trust.
- **What isn't built or isn't decided:** the open issues, here and in [dromas-server](https://github.com/dromas-dev/dromas-server/issues). Every open issue is wanted for v1.
- **Each design decision compared with the standard or system that does the same thing:** `PRECEDENTS.md`.
- **The server's limits, and what each limit protects:** dromas-server's [`LIMITS.md`](https://github.com/dromas-dev/dromas-server/blob/main/LIMITS.md).
- **Code awaiting an outside audit:** the issues labeled `needs-audit`. Each one is a brief that an outside agent can work from: the commits, the claims to try to break, where to look first, and how to run the code. Auditing one of these issues is the most useful review this project can get.

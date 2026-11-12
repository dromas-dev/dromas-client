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

**What happens next.** Reports reach the maintainer, who works them with Custos, his AI assistant. The fix lands in this repo together with the published advisory, which credits the reporter unless they ask otherwise. No response time is promised yet: the project is young and has one human maintainer.

## Checking the claims yourself

Each row is a claim in one sentence, the section of `PROTOCOL.md` that gives the whole rule, and the files that hold the claim: the code that keeps it, then the tests that try to break it. For a claim that is not the protocol's, the doc comments of the code that keeps it give the whole rule. What the claims leave open is under [Decided questions](#decided-questions).

| Claim | `PROTOCOL.md` | Held by |
|---|---|---|
| **Standard cryptography only, nothing home-rolled:** age (ML-KEM-768 + X25519) encrypts, Ed25519 or ECDSA P-256 signs in SSHSIG and OpenSSH certificates, and SHA-384 makes addresses | §3.1 | `protocol/`, `sshsig/`; the vectors (`testdata/vectors/`), checked by `ssh-keygen` and `age` |
| **An address is the SHA-384 of its identity document,** so only the identity's key, or a working key the identity certified, speaks for the address, and a server can forge neither | §4, §5 | `protocol/identity.go`, `protocol/certificate.go`, `protocol/signer.go`; `client/forgery_test.go`, `client/signer_test.go`, `protocol/signer_test.go`; dromas-server's tests of the pass and the statement |
| **Every document is accepted or refused exactly as its test vector says:** identities, certificates, prekey statements, passes, revocations, envelopes and messages | §4–§10, §16 | `protocol/prekey.go`, `sh/dromas.sh`; `protocol/vectors_test.go`, `testdata/vectors/` |
| **A signature has one encoding, the bytes `ssh-keygen -Y sign` writes,** and each client refuses any other encoding | §3.2, §8 | `protocol/signature.go`, `sh/dromas.sh`; `protocol/`'s tests, `protocol/fuzz_test.go`, `sh/sh_test.go`, `testdata/vectors/sig/` |
| **The server never sees plaintext or a private key:** a message is signed, then encrypted, on the sender's machine | §9.2, §9.4 | `protocol/signed.go`, `client/send.go` |
| **The reader decides who sent a message,** from the sender's own signature, never from the server's word | §12.3 | `client/recv.go`; `client/forgery_test.go`, `client/hostile_test.go` |
| **Verification is always on and fails closed:** a failed check stops, and no flag skips one | §12.3 | `client/forgery_test.go`, `client/hostile_test.go` |
| **A hostile server can cost a mailbox at most a bounded amount in each run,** in messages, bytes, list pages and time, whatever the server sends | §11.10–§11.12, §11.14, §12.3, §15.9 | `client/hostile_test.go`, `client/draintime_test.go`, `client/stall_internal_test.go`, `client/resume_test.go`, `client/etag_test.go` |
| **Nothing a sender or a server wrote is printed as the client's words;** a message body is data | §11.3, §11.17; the rest is the client's own | `client/hostile_test.go`, `cmd/dromas/transport_test.go`, `cmd/dromas/versions_test.go` |
| **`recv --exec` runs a program only on a verified message already kept, passed on stdin with no shell,** and a hung or crashed program loses no message | not the protocol's | `cmd/dromas/commands.go` (`recv`'s help); `cmd/dromas/exec_test.go`, `client/crash_test.go` |
| **Forward secrecy per prekey, bounded by how often a mailbox checks in:** retired prekeys are deleted, so a later theft opens no message sealed to a deleted prekey | §12.1, §12.4, §15.3, §15.7 | `client/rekey.go`; `client/renew_test.go`, `client/lifetime_test.go`, `client/taken_test.go`, `client/clock_test.go`, `sh/renew_test.go` |
| **The client refuses a private key that other users can access, as ssh does,** creates its own files owner-only, and by default refuses state and inbox files that others can access | §12.1 | `client/private_*.go`, `client/keyfile_*.go`; `client/judged_test.go`, `client/kept_judged_test.go`, `client/private_test.go`, `sh/files_test.go`; on Windows, CI's `windows-accounts` job (a Guest account is not tested) |
| **Only a config file its owner chose can redirect a mailbox:** the working directory is never read, and a file that others can write is refused | not the protocol's | `cmd/dromas/config.go`; `cmd/dromas/config_test.go` |
| **The Go client signs as the identity only with the identity's own key, from the mailbox's agent or key file,** and fails as `identity_locked` without one | not the protocol's | `client/identitykey.go`, `client/internal/sign/`; `client/identitykey_test.go`, `cmd/dromas/identitykey_test.go` |
| **A state file serves one identity,** and `recv` acks nothing while a prekey the state file records is missing | not the protocol's | `client/format.go`, `client/recv.go`; `client/layout_test.go`, `client/lostkey_test.go`, `client/crash_test.go` |
| **A missing folder hides no message:** once the state file records the mailbox's folders, every command that writes the mailbox stops while one is missing, acking nothing | not the protocol's | `client/mailbox.go`, `client/follow.go`; `client/folders_test.go`, `client/follow_test.go`, `cmd/dromas/recv_test.go` |
| **A kept TLS session ticket is a secret, as a prekey is,** private to its owner, and resumes no session more than 24 hours after a full handshake | not the protocol's | `client/ticket.go`; `client/ticket_test.go`, `client/ticket_internal_test.go` |
| **A pass, and the address a lookup asks about, never cross a network in the clear:** clients use https, or http only on the same machine, and follow no redirect | §11.1 | `client/mailbox.go`, `sh/dromas.sh`; `client/transport_test.go`, `client/transport_internal_test.go`, `sh/sh_test.go` |
| **With a CA file, the Go client sends requests only to a server whose certificate chains to that file,** so a TLS-inspecting proxy the system trusts sees no pass | not the protocol's | `client/cafile.go`, `client/mailbox.go`; `client/cafile_test.go`, `client/cafile_internal_test.go`, `cmd/dromas/cafile_test.go` |
| **Every send is padded,** so the server sees a message's size only to within a block | §9.3 | `client/pad_test.go` |
| **"Revoked" is taken only on the identity's own signature, never a server's word, and is permanent:** a reader refuses a verified-revoked mailbox's messages, and a mailbox holding its own revocation signs nothing | §10.1, §12.5, §15.5, §15.6 | `client/revocation.go`, `client/known.go`, `client/lock.go`, `client/recv.go`; `client/revocation_test.go`, `client/ownrevocation_test.go`, `sh/revoke_test.go` |
| **A revoked certificate loses everything signed under it,** while the address and the identity's own signatures stand, and every verifier refuses a certificate longer than 45 days and an hour | §10.2, §5, §5.2, §12.3, §12.4, §12.5, §15.5, §15.6 | `protocol/certificate.go`, `client/signer.go`, `sh/dromas.sh`; `client/signer_test.go`, `client/revokedcerts_test.go`, `client/notmade_test.go`, `sh/revoke_test.go` |
| **A sender reads and verifies its recipient's mailbox before every post,** refuses a revoked prekey statement or one older than it has seen, and posts nothing without an answer | §6, §12.2 | `client/ask_test.go`, `client/signer_test.go`, `sh/clock_test.go` |
| **A failure never loses a message, and a retry is never delivered twice** | §12.2, §12.3 | `client/failure_test.go` |
| **A replayed message is refused** for 14 days and an hour after its `sent_at`, and a mailbox whose state no longer names its prekeys stops until `dromas init --restore` | §12.3 check 8, §12.2, §15.7, §15.9 | `client/restore.go`; `client/restore_test.go`, `client/replay_test.go`, `client/replay_retry_test.go`, `client/adopted_test.go`, `sh/restore_test.go` |
| **A message a server still serves is one its reader accepts:** each post is dated afresh, and a server keeps a message for less time than a reader accepts it | §12.2, §13 | `client/dating_test.go`, `sh/dating_test.go`; dromas-server's tests of a message's life on the server |
| **Ciphertexts and signatures are standard formats, read by code this project did not write:** stock `age` and `ssh-keygen` check the crypto and container vectors, and the stock-tools script exchanges messages with a Go mailbox | §3.1, §16 | `protocol/stocktools_test.go`, `protocol/vectors_test.go`, `sh/` |
| **A release is built by this repo's workflow from a tag its maintainer signed, after its tests pass,** with every file attested and its checksums signed ([Verify a release](docs/install/verify.md)) | — | `.github/workflows/release.yml`; `gh` and `cosign`, which check the attestations and the signature; the tag ruleset and immutable releases, settings of the repo that no test reads |

**Run them:** `README.md` § Tests, which says what each needs and what an `ok` does not tell you.

**What is not tested:** that a release rebuilds byte for byte anywhere but in this repo's own workflow (its `rebuild` job does it for each release from v0.1.1 on); `age-plugin-pq` against the server's check of a send's stanzas; a restore whose clock reads behind what a server's older record hides, and two machines that make a certificate in one second (`PROTOCOL.md` §5.2); OpenSSH releases other than the two that CI's Ubuntu jobs run the script and the cross-tests on, 9.6p1 and 10.2p1; age 1.3.0 and 1.3.1; macOS and BusyBox; what a callback's own processes do once `--exec-timeout` kills it, since only its one process is killed.

## Decided questions

Each entry is a security question that is already decided, and under it, where one applies, what limits the risk or why the design accepts it. The `PROTOCOL.md` section that the entry names gives the reasoning. The issue that the entry names decided the question, and carries the `decided` label: `gh issue list -R dromas-dev/dromas-client --label decided --state closed` lists every such issue. A report of a decided question is not a vulnerability. A better design for one is welcome as an issue.

**What the server, and anyone who has an address, sees and can do**
- The operator sees who writes to whom, when, how much, and from which IP address (§15.2, §15.8; #507).
  - The operator never sees a message's content or signature, and padding hides a message's size to within a block. An honest server keeps no sender beside a stored message. `dromas-server` logs no client IP address, and writes an address or message id only as a keyed hash, under a key that `dromas-server` holds in memory and replaces each day. dromas.dev's operator states that its load balancer and TLS terminator keep no access log.
- A server can drop, delay or delete a message, and no receipt shows the loss (§15.2, §15.9; #516).
  - A server cannot read, alter or forge a message, and the sender keeps each message in `outbox/` until a server accepts it. A receipt from one client to another fits inside version 1, as an optional member of the envelope.
- The party that terminates TLS for a server is part of that server, and sees each pass in transit (§15.8; #586).
  - At dromas.dev, nginx terminates TLS on the host that runs `dromas-server`, behind a load balancer that passes TCP through, with no third party in front, as dromas.dev's operator states. A pass is valid at one server for at most an hour, and a pass decrypts nothing and signs nothing.
- Anyone who has a mailbox's address can look up the mailbox with no pass, and learns whether a server keeps the mailbox, the mailbox's public documents, and when the mailbox renews (§15.8; #352, #654).
  - A lookup returns only the documents that a sender verifies before a post, and a server counts a lookup against the IP address that the lookup comes from.
- The server sees each mailbox's ACL, and an allow list names senders that may never have written to the mailbox (§15.2; #576).
  - By default a mailbox's ACL allows every sender and denies the senders that the owner blocked, and the client holds a message from a sender that the owner has not allowed.
- A sender that posts the same ciphertext again learns, on the server's word, whether the recipient's mailbox still holds the message (§15.8; #719).
  - Removing the signal would need the server to keep a record of each acked message. A conforming sender posts the same ciphertext a second time only after the server rejected the first post's pass.
- A reader's lookup of a sender's revocations tells the server which senders the reader has just verified (§15.8; #752).
  - The lookup is how a reader catches a revoked sender at read time, and any call that names the sender gives the server the same link.
- A lookup's compressed length can help a network observer guess which mailbox a sender is about to write to (§15.8; #148).
  - The response holds only the mailbox's public documents, so the length can point at a mailbox but reveals no secret.
- An address in a URL is seen wherever URLs are logged (§15.8).
  - A leaked address is answered by a new mailbox and by revoking the old address.
- The address is the root of trust, so the party that delivers an address to a sender is trusted to deliver the right address (§15.1).
  - An address is the SHA-384 of the mailbox's identity document, so a sender that has the right address needs to trust nothing else, as with any key fingerprint.

**Passes, keys and revocation**
- A pass is a bearer credential at one server for at most an hour, and is not bound to the request that carries the pass (§15.5; #570, #724).
  - Binding a pass to each request would leave the server a signed record of who writes to whom. A stolen pass gives a thief only what a hostile server can already do.
- A server can withhold a revocation from a reader that has never seen the revocation, and the protocol has no key transparency (§15.6; #572, #311).
  - A reader keeps every revocation it was shown, and a record that the owner hands over out of band settles the question. Key transparency fits inside version 1.
- A stolen working key signs messages that every reader accepts, through any server, until the key's certificate ends, at most 45 days and an hour after the certificate began (§15.5; #716).
  - Only a revocation of certificates ends the key at a reader, once the reader sees the revocation, and `dromas cert revoke-earlier` publishes one. A renewal ends the stolen key's passes and prekey statements, at the server and at each sender that sees the new certificate, and ends nothing at a reader.
- A party that copies a mailbox's identity key signs as the mailbox until the owner revokes the address, and `dromas init` writes the identity key as a key file with no passphrase by default (§15.5; #571, #594).
  - Revoking the address is the one remedy, and is permanent. The owner can keep the identity key in the mailbox's agent or behind a passphrase, and a mailbox whose identity is held on another machine is #459, open.
- A pass and a message do not sign the certificate that carries the signature, so the signature verifies under any certificate of the same working key (§15.5; #654).
  - A certificate's maker certifies a new working key with every certificate, and a prekey statement signs the rank of the statement's signer.
- Anyone who has a revocation record of an address can revoke the address (§15.5).
  - A revocation record is made in advance for a lost identity key, so the owner keeps the record apart from the identity key.

**Forward secrecy and the signature**
- Forward secrecy is per prekey, and version 1 has no ratchet, so it gives no post-compromise security (§15.3; #575).
  - A mailbox that renews daily and polls deletes a message's prekey about a day after the message arrives, and a prekey lifetime under 27 hours renews sooner and shortens that. Per-prekey forward secrecy keeps no state per peer, so each mailbox renews and deletes on its own.
- A server that rejects every pass from a mailbox keeps the mailbox's retired prekeys on the mailbox's disk for as long as the server rejects the passes (§15.2, §15.3; #654).
  - A mailbox deletes a prekey only by a clock reading that a server confirmed, because the mailbox cannot tell a server that rejects its passes from a clock that reads far ahead.
- A server can serve a stale prekey to a sender that has not seen a later prekey statement, until that prekey's `not_after` (§15.2).
  - The server can serve only a prekey that the mailbox's signer signed, and a sender rejects a prekey statement earlier than one the sender has seen.
- A recipient can show anyone what a sender signed, so a message is not deniable toward its recipient (§15.4; #29).
  - This is the trade-off that lets a program verify who sent each message it acts on. The server keeps no signature, and can prove nothing about who sent what.
- A verifier accepts both (r, s) and (r, n − s) as a P-256 identity's signature, so anyone who has seen a P-256 signature or certificate can write a second one that verifies (§15.4; #580).
  - Nothing depends on a signature having one set of bytes: a message's id is the hash of the message's ciphertext, a send names a prekey statement by the hash of the statement alone, and a server that remembers passes compares the lower s.
- A reader decrypts a message before the reader verifies the message's signature (§15.4; #49).
  - Every system whose encryption wraps the signature does the same, OpenPGP and Nostr's NIP-17 among them (`PRECEDENTS.md`), and the wrapping keeps the signature from the server.
- A verified signature proves who sent a message, and does not prove that the message is safe to obey (§15.9).

**The mailbox's own machine**
- The client trusts code that runs as the mailbox's owner (§15.1; #717).
- A mailbox that loses its state, or puts back an older copy of the state, can be served again a message that the mailbox accepted before (§15.9; #784).
  - A replay works only within that message's TTL, and keying work by `from` and `msg_id` makes a repeat harmless.
- A clock that steps or jumps is a failure whose cost is accepted (§15.7; #831).
  - A clock further off than the skew allowance is stopped at its pass before it fetches, prunes or deletes, and a client never sets its clock by a server.
- A reader acks a message that the reader rejects for a time, so a message whose certificate has not begun, or whose `sent_at` is more than an hour ahead, by the reader's clock is lost (§15.7; #44).
  - A certificate's maker dates the certificate an hour back, so only a clock that is already wrong makes such a message. The reader keeps the rejected message in `refused/`, with the clock that the reader judged by.

**Senders**
- One verified sender, or many cheap identities, can fill an open mailbox to the mailbox's caps and spend the mailbox's intake budget (§15.9; dromas-server's `LIMITS.md` § Known weaknesses).
  - An allow list is the full defence for a mailbox that knows its senders.

## Where the rest is

- **The threat model, what a server sees and can and cannot do, and the residual risks:** `PROTOCOL.md` §15. For whoever runs a mailbox, in short: `README.md` § No server trust.
- **What is not built, or not decided:** the open issues, here and in [dromas-server](https://github.com/dromas-dev/dromas-server/issues); every one is wanted for v1.
- **Every design decision beside the standard or system that does the same:** `PRECEDENTS.md`.
- **The server's limits, and where each is weak:** dromas-server's [`LIMITS.md`](https://github.com/dromas-dev/dromas-server/blob/main/LIMITS.md).
- **Shipped code that no one but its author has read:** the issues labelled `needs-audit`. Each is a brief an outside agent can work from: the commits, the claims to break, where to look first, how to run it. Auditing one is the most useful review this project can get.

# Precedents

Each design decision of Dromas beside the standards and deployed systems that make the same one, and where Dromas differs and why. `§` is a section of `PROTOCOL.md`, which states each rule whole; §15 holds the threat-model reasoning.

**How this file is written:**
- **Part 1** sets Dromas beside whole systems. **Part 2** sets each decision on the wire or in the security model beside its precedents. **Part 3** does the same for the reference server's own build choices, which are no part of the protocol. A client's own behaviour (its command line, files, output, lock) carries its precedent in the doc comment of its code.
- **Each decision is one block of one shape,** after the Rust RFC template's *Prior art* and *Rationale and alternatives*, PEP 1's *Rejected Ideas* and MADR's *Considered Options* (read 2026-11-25: [Rust RFC template](https://github.com/rust-lang/rfcs/blob/master/0000-template.md), [PEP 1](https://peps.python.org/pep-0001/), [MADR](https://adr.github.io/madr/)): **Ours**, one sentence and its section; **Prior art**, the systems that make the decision; **Where ours differs**, one line each, with its reason; **Rejected**, one line each. The heading names the decision, and other files cite it by name: `PRECEDENTS.md` § Clocks.
- **A block defends a decision and does nothing else:** no account of how it was reached, no restatement of the spec, no explanation of a standard.
- **Every claim about another system says how it is known:** `read <date>` with its source, `measured <date>`, or `review 2026-10-10`, the design reviews of that day, whose sources are in § Sources. A claim not read at a primary source is not written. A quotation is its source's words.
- **A precedent is industry practice:** a major public API, a published standard, or a widely deployed protocol. This is the one place a comparison is kept. If a row is wrong, an issue is welcome.

---

## Part 1: Dromas beside whole systems

| System | Identity and keys | Encryption | Forward secrecy, post-compromise, post-quantum | Sender authentication | Hidden from the server |
|---|---|---|---|---|---|
| **Signal** | Phone number or username; identity key from Signal's directory | PQXDH, Double Ratchet, SPQR's ML-KEM-768 ratchet | yes, yes, yes | Implicit; deniable | Contents; the sender (sealed sender) |
| **WhatsApp** | Signal's, plus key transparency (AKD) | Signal protocol | yes, yes, not documented | Implicit; deniable | Contents |
| **iMessage** | Device keys under a Contact Key Verification account key | PQ3: Kyber-1024, then a Kyber-768 ratchet | yes, yes, yes | Every message signed by a P-256 device key | Contents |
| **Proton Mail** | Email address; Proton serves keys and stores private keys under the password | OpenPGP | no, no, opt-in | OpenPGP signatures, which others can check | Body and attachments, not subject or addresses |
| **MLS** (RFC 9420) | Credentials sign one-time KeyPackages, but for a reusable "last resort" one | TreeKEM groups | yes, yes, by suite | Signatures inside the group | Contents; the sender within the group |
| **SimpleX** | No user identifiers: one-way queues whose addresses are capabilities | Double Ratchet + sntrup761, NaCl per queue | yes, yes, yes | Implicit; deniable | Contents, sender, recipient, their link |
| **Nostr** (NIP-17) | A secp256k1 key is the identity | NIP-44; a signed seal in an ephemeral-key gift wrap | no, no, no | A signature inside the encryption | Contents; the sender |
| **Noise one-way X** (rev 34) | None: "up to the application" (Noise §14) | `e, es, s, ss`, then AEAD | sender compromise only, no, no | Static-static DH: recipient-only, open to key-compromise impersonation, deniable | Contents; the sender's static key |
| **Dromas** | The address is the identity's hash; no directory | age: ML-KEM-768 + X25519 | bounded by how often a mailbox checks in, or, where `prekeys/` is backed up, by the backup's retention; no; yes | Ed25519 or P-256 inside the encryption, naming the recipient; others can check it | Contents only |

*(review 2026-10-10; § Sources.)*

| Where Dromas stands | Beside whom |
|---|---|
| Getting the right key | Level with key-as-identity systems (Nostr, did:key, Tor onion addresses); directory messengers serve keys and add safety numbers or key transparency. A renewed prekey or working key never moves the address (§15.10) |
| Sender authentication | Provable to others, as iMessage's; Signal, WhatsApp, SimpleX and Noise's one-way patterns are deniable. The server holds no signature (§ Signed payload) |
| Forward secrecy | Behind the ratchets, and behind X3DH's one-time prekeys for a first message; level with X3DH without one, which depends "on the signed prekey's lifetime" ([X3DH](https://signal.org/docs/specifications/x3dh/) §4.7, read 2026-11-12); ahead of PGP and Noise's one-way patterns |
| Metadata | Behind Signal, SimpleX and Nostr: the server sees who sends, at the send, and keeps no sender once a message is stored |

---

## Part 2: Each decision beside its precedent

### Pass

**Ours** (§8): the mailbox's signer signs a short-lived pass for each server, sent in `Authorization`; the server issues and keeps nothing.

| System | What it does | Source |
|---|---|---|
| GitHub Apps | The app signs a JWT; `exp` at most 10 minutes ahead, `iat` 60 seconds back | [GitHub Docs](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app), read 2026-10-12 |
| Google service accounts | The account's key signs a JWT sent straight to the API; an hour | [Google](https://developers.google.com/identity/protocols/oauth2/service-account), read 2026-10-12 |
| VAPID | `Authorization: vapid t=…, k=…`, the push service's origin as `aud`; at most 24 hours | [RFC 8292](https://www.rfc-editor.org/rfc/rfc8292), read 2026-10-12 |

**Where ours differs:**
- SSHSIG, not a JWT: stock `ssh-keygen` makes and checks a pass, and a working key's certificate rides in the signature.
- The key is the address's, not one the server holds on file.
- A server name never carries port 80 or 443, where an origin drops only its own scheme's default ([RFC 6454](https://www.rfc-editor.org/rfc/rfc6454) §6.2, read 2026-11-02): a server behind a proxy may not know the scheme.
- An unknown parameter is refused, where VAPID ignores one: an addition is a new scheme (§14). So is a quoted-pair, which no value needs ([RFC 9110](https://www.rfc-editor.org/rfc/rfc9110) §5.6.4, read 2026-10-20).

**Rejected:**
- A server-issued bearer token: issued, stored, rotated, and good until replaced when it leaks (review 2026-10-10).
- A signature over each request (SigV4, RFC 9421, DPoP; read 2026-10-18): on a send, a proof for the server of who sent what to whom (§15.5).

### Working keys

**Ours** (§5): the identity signs for its mailbox itself, or certifies a working key in an OpenSSH certificate that signs in its place; a verifier takes both; a certificate runs up to the lesser of its server's maximum and the protocol's, 45 days and the hour, every verifier enforces the protocol's, and validity is judged at the verifier's clock.

| System | What it does | Source |
|---|---|---|
| Signal | The identity key signs the signed prekey itself | [X3DH](https://signal.org/docs/specifications/x3dh/) §3.2, read 2026-10-31 |
| OpenPGP | Takes signatures by the primary key and by a signing subkey; "A revoked subkey is not to be used" | [RFC 9580](https://www.rfc-editor.org/rfc/rfc9580) §5.2.1.12, §5.2.3.29, read 2026-10-31 |
| Tor relays | The master identity key signs only a medium-term signing key, so that it can be kept offline | [Tor spec](https://spec.torproject.org/tor-spec/relay-keys.html), read 2026-10-31 |
| Let's Encrypt | 90-day certificates "will be cut in half to 45 days by 2028" | [Let's Encrypt](https://letsencrypt.org/2025/12/02/from-90-to-45), read 2026-10-31 |
| Apple | "Connections to TLS servers violating these new requirements will fail": no certificate over 398 days | [Apple](https://support.apple.com/en-us/102028), read 2026-11-25 |

**Where ours differs:**
- Both forms, as OpenPGP, where Signal and Tor fix one: refusing the identity's signature stops no thief, who certifies a key of its own as easily (§15.5).
- A prekey statement signs its signer's rank alone (§6), and passes and messages sign no rank, where SSH, TLS 1.3 and S/MIME's ESS sign the whole key or certificate (RFC 4252 §7, RFC 8446 §4.4.3, RFC 5035 §7; read 2026-11-02): SSHSIG signs no signing key, and the order reads the rank alone (§5.3).
- The maximum is Let's Encrypt's 45-day end state, enforced at every verifier as Apple's: a hostile server cannot stretch a stolen key's life.
- Judged at the verifier's clock, as X.509 path validation, where MLS only RECOMMENDS the lifetime check on a LeafNode it receives (RFC 9420 §7.3) and Sigstore judges the signing time (read 2026-11-24): no timestamp authority is in the protocol, and `sent_at` is the signer's word (§12.3 check 6).
- A working key is Ed25519 or P-256, where the format certifies more (`draft-ietf-sshm-cert-03` §2.1.3, read 2026-11-23): the types every verifier already checks.

### Certificate serials

**Ours** (§5.2): a serial is its maker's clock in seconds, or, where that is not above the highest the maker knows of for the mailbox, one above that; a verifier compares serials and never reads one as a time.

| System | What it does | Source |
|---|---|---|
| X.509 | A serial "MUST be unique" per CA, kept so by the issuer's records; nothing orders two | [RFC 5280](https://www.rfc-editor.org/rfc/rfc5280) §4.1.2.2, read 2026-10-14 |
| BIND 9 | `serial-update-method unixtime`: epoch seconds, or one more where the serial is already that high | `doc/arm/reference.rst`, read 2026-10-14 |
| WireGuard | A TAI64N timestamp; the receiver drops anything not above the greatest it holds | [WireGuard](https://www.wireguard.com/protocol/), read 2026-10-14 |

**Where ours differs:**
- BIND's rule, not X.509's records: an identity is a key, restored alone (§12.4), and its only other record is its untrusted server's.
- The serial orders, which a random one cannot, so revocation runs through it as an OpenSSH KRL's range does.
- It rests on the holder's clock; §12.4 and §15.7 state where that gives way.

### Signed payload

**Ours** (§9.2): sign, then encrypt: the envelope's line, its signature as `ssh-keygen` wrote it, then padding, inside the ciphertext, with the recipient named in what is signed.

| System | What it does | Source |
|---|---|---|
| JOSE | "normally producers should sign the message and then encrypt the result" | [RFC 7519](https://www.rfc-editor.org/rfc/rfc7519) §11.2, read 2026-10-13 |
| OpenPGP | A signed message inside the encrypted one | [RFC 9580](https://www.rfc-editor.org/rfc/rfc9580) §10.3, read 2026-10-13 |
| S/MIME | Either order; enveloping first lets the signature be checked without decrypting | [RFC 8551](https://www.rfc-editor.org/rfc/rfc8551) §3.7, read 2026-10-13 |
| git | A detached signature, SSH's among them, after a tag's unencoded payload | [gitformat-signature](https://git-scm.com/docs/gitformat-signature), read 2026-10-13 |
| JWS, DSSE | Payload and signature as base64 members of a JSON object | RFC 7515 §7.2.1, DSSE `envelope.md`, read 2026-10-13 |

**Where ours differs:**
- git's layout, a fixed overhead, where base64 members make the envelope a third larger.
- The recipient is signed, which git does not need: it stops a recipient passing a message on, the flaw of this order that Davis (review 2026-10-10) and age's author ([words.filippo.io](https://words.filippo.io/dispatches/age-authentication/), read 2026-10-13) name. The namespace names no domain, against `ssh-keygen(1)`'s advice of `NAMESPACE@YOUR.DOMAIN` (`ssh-keygen.1` at openssh-portable `325d965d`, read 2026-10-17): an identity belongs to no server (§11.15).
- One signature, verified on the line exactly as decrypted: the rules gitsign (GHSA-7rmh-48mx-2vwc), GitLab (issue 407252) and git before 2.20 broke (read 2026-10-13).
- No sender is kept at rest (§13, §15.8), as Signal's subpoena answers hold only "the date and time a user registered" and the last connection ([signal.org/bigbrother](https://signal.org/bigbrother/), read 2026-11-25); but no sealed sender, since the limits and the ACL count the sender's pass.

**Rejected:**
- Deniable, shared-key authentication, as Signal's (review 2026-10-10): a bot acting on a message needs a sender others can check.
- HPKE's Auth mode and Noise's one-way X and K: a leaked recipient key forges any sender, and no stock tool runs them (review 2026-10-10).
- A signature over re-serialized JSON: JWS's and XML-DSig's canonicalization lesson (review 2026-10-10).

### Clocks

**Ours** (§3.4, §8, §12.4): every check is exact at the checker's clock, and the maker of a time-limited thing leaves the room: an hour, or 5 minutes for a pass.

| System | What it does | Source |
|---|---|---|
| X.509, OpenSSH | Exact at the verifier; `ssh-keygen` backdates 59 seconds | [RFC 5280](https://www.rfc-editor.org/rfc/rfc5280) §6.1.3, `ssh-keygen.c` `V_10_2_P1`, read 2026-10-13 |
| Let's Encrypt, OpenMLS, BIND | Backdate about an hour: 58 min 30 s; a KeyPackage "to adapt to skewed clocks"; a DNSSEC signature | measured 2026-10-13; `lifetime.rs`, `dnssec-signzone(8)`, read 2026-10-13 |
| Kerberos | Leeway at the verifier: MIT's default "is 300 seconds, or five minutes" | [MIT krb5.conf](https://web.mit.edu/kerberos/krb5-latest/doc/admin/conf_files/krb5_conf.html), read 2026-11-24 |
| Mastodon | Leeway of an hour on HTTP signatures (`CLOCK_SKEW_MARGIN`) | `app/lib/signed_request.rb`, read 2026-10-13 |

**Where ours differs:**
- No leeway at any verifier, where Kerberos and Mastodon put theirs: one comparison, with no number in it to disagree on; the room is in what is made.
- A pass's 5 minutes is MIT Kerberos's default; the hour is what Let's Encrypt, OpenMLS and BIND leave for things strangers verify.
- A refusal for a time is final, where SMTP retries a 4yz ([RFC 5321](https://www.rfc-editor.org/rfc/rfc5321) §4.2.1, read 2026-10-19): a client with right clocks makes no such message (§15.7).

### Revocation

**Ours** (§10, §12.5): the identity revokes its address by signing a fixed statement, with no date, reason or number; certificates are revoked the same way through one serial, everything up to it; a client takes "revoked" only on a record that verifies, and keeps it.

| System | What it does | Source |
|---|---|---|
| OpenPGP | A key revokes itself or a subkey, with a reason | [RFC 9580](https://www.rfc-editor.org/rfc/rfc9580) §5.2.3.31, read 2026-10-13 |
| X.509 | A signed, rising list of serials, carried "via untrusted servers"; an entry stays until past its certificate's end | [RFC 5280](https://www.rfc-editor.org/rfc/rfc5280) §3.3, read 2026-11-08 |
| OpenSSH | A KRL revokes a serial range, unsigned; an unreadable `RevokedKeys` refuses everyone | `PROTOCOL.krl`, `sshd_config(5)`, read 2026-10-21 |
| AWS IAM, Entra ID | Revoke what was issued before a time | [AWS](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_use_revoke-sessions.html), [Microsoft](https://learn.microsoft.com/en-us/graph/api/user-revokesigninsessions?view=graph-rest-1.0), read 2026-10-14 |
| Let's Encrypt | Ended OCSP: the responder sees which site is visited | [Let's Encrypt](https://letsencrypt.org/2024/12/05/ending-ocsp/), read 2026-10-13 |

**Where ours differs:**
- No reason and no date: a fixed text is rebuilt by its verifier, so nothing is parsed before it is trusted; its one kind is OpenPGP's compromised.
- No slot for a later field, the unused extension point [RFC 9170](https://www.rfc-editor.org/rfc/rfc9170) §2.1 says fails (read 2026-10-13); an addition is a new statement.
- A revocation record names its own `type` (§10.3), as a Kubernetes object its `kind` (`api-conventions.md`, read 2026-10-16): every writer writes it, and a reader takes a record without it and refuses one of another type.
- SSHSIG, not a KRL: Go reads no KRL ([golang/go#22741](https://github.com/golang/go/issues/22741), read 2026-10-14), so each client would write a binary reader to say one number; `ssh-keygen(1)` recommends SSHSIG for a KRL that travels.
- One serial, not a list a later one can shrink, which gives two holders two verdicts: one serial cannot cover less than a lower one, and sparing an earlier certificate while revoking a later is nothing a mailbox needs, its next being numbered above every one revoked (§5.2).
- The serial is kept for good, where a CRL drops expired entries: it covers certificates a verifier never saw (§15.6).
- No answer stops a drain and holds a send, OpenSSH's rule, where the web's soft fail is "completely useless" ([Langley](https://www.imperialviolet.org/2014/04/19/revchecking.html), read 2026-10-13): a browser could not afford to refuse without an answer, and a mailbox can, since the server it waits on is the one its messages pass through.
- A reader asks its own server about each sender it verified, a server that already sees every message from that sender, where Let's Encrypt ended OCSP for what a per-subject query tells the responder; shipping every record to every reader would list every address (§15.8).
- One source, a mailbox's own server, where Sequoia publishes to five keyservers and GnuPG ships none (read 2026-10-14): a second guards only where owner and reader both chose it, none could be a default since a lookup tells it whom its asker writes to, and the mailbox's server already fails closed, as `RevokedKeys` and OpenSSL's `-crl_check` do (read 2026-10-21); the norm against one that withholds is key transparency (WhatsApp's AKD, Apple's Contact Key Verification, CONIKS, read 2026-11-03), which fits inside version 1 (§15.6).
- A revoked address is `410` with its record, where ACME answers a deactivated account `401` bare ([RFC 8555](https://www.rfc-editor.org/rfc/rfc8555) §7.3.6, read 2026-10-18): ACME's server is the authority, and here no server is, so the caller is shown the proof; a `401` here is a pass a new signature cures.
- No cache may keep an API answer: a `410` is cacheable by default ([RFC 9110](https://www.rfc-editor.org/rfc/rfc9110) §15.5.11, read 2026-10-13), and a kept answer would hide a revocation or show one stale.
- A revocation of certificates below the one held is `409 serial_too_low`, naming it, where ACME answers `400 alreadyRevoked` (RFC 8555 §7.6, read 2026-10-22): RFC 9110 §9.3.4 (read 2026-10-31) suggests `409` for a `PUT` inconsistent with the target's state.
- Neither client writes a revocation unasked, where GnuPG and Sequoia do (read 2026-10-13): a copy beside an identity is stolen with it.
- Revocation is the one, final way out of a server, as ACME's account deactivation: there is no delete of a mailbox, which a leaked pass, a credential of minutes, could use to end an address.
- Nobody signs "no revocation shown", where an OCSP responder signs "good" with a time ([RFC 6960](https://www.rfc-editor.org/rfc/rfc6960), read 2026-10-13): only the identity could, and a working key's word for its own certificate is a thief's word too, so it has no lifetime and a client asks before every post.

### Senders and the ACL

**Ours** (§11.14, §12.3): a server takes messages from every registered sender that its mailbox's ACL lets in, and the Go client holds a sender its mailbox has not allowed in `held/`.

| System | What it does | Source |
|---|---|---|
| Signal | A stranger's message waits for accept, delete or block; a block is silent | [message requests](https://signal.org/blog/message-requests/), read 2026-10-15; [Block numbers, usernames or groups](https://support.signal.org/hc/en-us/articles/360007060072-Block-numbers-usernames-or-groups), read 2026-10-21 |
| Matrix | A server ACL: `deny`, then `allow`, globs; a denied server gets `403 M_FORBIDDEN` | [`m.room.server_acl`](https://github.com/matrix-org/matrix-spec/blob/main/data/event-schemas/schema/m.room.server_acl.yaml); `content/server-server-api.md` at `cc56ef90`; read 2026-10-21 |
| Google Cloud Storage | Replaces a whole list under a precondition, `412` when it fails | `request-preconditions`, read 2026-10-21 |
| AWS WAF | An IP set holds at most 10,000 entries | [AWS WAF](https://docs.aws.amazon.com/waf/latest/developerguide/limits.html), read 2026-11-12 |

**Where ours differs:**
- Signal's default, not email's: a reader is often an agent, and a verified sender is not a wanted one.
- A refused sender is told, as Matrix tells, where Signal's block is silent: silence would need the server to take and drop the message.
- An entry is an address or `*`, never a glob, which would let in an address made to match; a missing `allow` is refused, not read as nobody as Matrix reads it, so a writer that drops an empty list fails loudly.
- At most 10,000 entries, AWS WAF's bound; too many is `413`, where S3 answers `400` (S3 API Reference, `Error`, read 2026-10-21): the entries bound the record's size.
- `If-Match` on the server's `ETag`, never a number a writer picks, which a leaked pass could raise to freeze a deny-all ACL.
- A `PUT` answers no `ETag`, as RFC 9110 §9.3.4 requires (read 2026-10-22): the server sorts and dedupes the entries, so what it holds may not be what was sent.
- No `DELETE`: no ACL means `{"allow":["*"],"deny":[]}`, as an S3 bucket always has an ACL (S3 User Guide, `acl-overview`, read 2026-10-21).
- The ACL sheds load and protects no reader: the client's hold is what a reader relies on.

### Errors

**Ours** (§11.3): RFC 9457 bodies in AEP-193's form, a `code` from a closed list with typed members, and a client shows its own sentence for the code, never the server's words.

| System | What it does | Source |
|---|---|---|
| RFC 9457 | "Consumers SHOULD NOT parse the "detail" member"; a `type` URI SHOULD resolve to documentation | [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) §3.1.4, §4, read 2026-10-17 |
| AEP-193, AIP-193 | Every dynamic value also a property; two errors are one if a client acts the same on both | `aep-dev/aeps`, `googleapis/aip`, read 2026-10-16 |
| OWASP LLM01 | "Separate and clearly denote untrusted content" | `LLM01_PromptInjection.md`, read 2026-10-13 |

**Where ours differs:**
- No `reason` beside the code: AIP-193 needs one for a code list every Google API shares.
- No client of ours shows the words, where the standards stop at "do not parse": the reader is often a model, and the server is in the threat model.
- `type` is a `tag:` URI that resolves to nothing: no client dereferences it, and a URL would tie every server's errors to one site.
- `Retry-After` is delay-seconds only, where RFC 9110 §10.2.3 allows an HTTP-date too, which a client reads as no wait: a date is read on the client's clock, which §3.4 expects may be off (read 2026-10-17).
- A code counts only under its own `type`, RFC 9457's identifier: a body that names its problem twice and disagrees names none; an unknown code, or one under another status, is no code, and the client prints the status alone.

### Address

**Ours** (§4.2): the address is `d_`, then the unpadded base64url SHA-384 of the identity document.

| System | What it does | Source |
|---|---|---|
| NIST PQC | Collision search on a 384-bit hash is category 4, on a 256-bit one category 2 | NIST CSRC, evaluation criteria, read 2026-10-30 |
| JOSE | A key's hash in base64url | [RFC 7638](https://www.rfc-editor.org/rfc/rfc7638) §3.1, read 2026-10-30 |
| multibase, Bech32 | A prefix names the form; Bech32's separator is the last `1` | `multiformats/multibase`, [BIP 173](https://github.com/bitcoin/bips/blob/master/bip-0173.mediawiki), read 2026-11-01 |
| Tor v3 | The key in the name, base32, lowercase, case-insensitive | `tpo/core/torspec` `dd82a2aa`, read 2026-10-21 |
| OpenSSH, SSHFP, age | Hash the key's wire blob, as parsed | `sshkey.c` `V_10_6_P1`, RFC 4255, age v1.3.2 `agessh.go`, read 2026-10-21 |

**Where ours differs:**
- SHA-384, where JOSE and SSHFP ([RFC 4255](https://www.rfc-editor.org/rfc/rfc4255) §3.1.3, RFC 6594, read 2026-10-21) use SHA-256: the address is the one value of a mailbox never renewed.
- The document's text is hashed, not the key's blob: it is refused unless it is the key's one encoding, so the two match one to one, and a client that lacks the key's type can still hash it.
- The delimiter is the first `_`, since a form's name never holds one, where Bech32's alphabet has no `1`.
- The link puts the address in the path, `/to/<address>` (§4.3), as `wa.me` and `t.me` do, where `signal.me` and matrix.to put it after `#`: the page is the mailbox's own server's, which sees every send anyway, served with `Referrer-Policy: no-referrer` (read 2026-10-17).
- The prefix keeps a command line from reading an address as an option, which bare base64url does one time in 64.
- Compared case and all: at 66 characters it fits no DNS label (63), so case-insensitivity, which IPFS's base32 serves for a DNS label (`docs.ipfs.tech`, content addressing, read 2026-10-30), buys nothing.

### Identity keys

**Ours** (§4.1): an identity key is in its one encoding, as OpenSSH writes it back, with explicit checks, Ed25519 small order and a security key's application among them.

| System | What it does | Source |
|---|---|---|
| OpenSSH | Names a key by the blob it rebuilds; P-256 points uncompressed only; small-order Ed25519 refused from 10.6p1; `ssh-keygen` enrolls a security key for an application beginning `ssh:` | openssh-portable `V_10_6_P1`, read 2026-10-21; `master`, read 2026-11-23 |
| libsodium, Tor | libsodium refuses small-order Ed25519 keys; Tor any torsion component | libsodium `open.c`, read 2026-10-15; torspec, read 2026-10-21 |
| BIP 66 | Strict DER "to make the consensus rules not depend on OpenSSL's signature parsing" | [BIP 66](https://github.com/bitcoin/bips/blob/master/bip-0066.mediawiki), read 2026-10-19 |

**Where ours differs:**
- Small order only, as libsodium, not Tor's torsion check: a torsion component gives no address other keys of its secret would not (2·A, −A; measured in the audit of #219, #277).
- One encoding, where git's allowed signers and age take re-encodings (read 2026-10-20): two readers must judge one key alike, BIP 66's reason.
- The application is `ssh:` exactly, stricter than OpenSSH: one length per type bounds a signature (§3.2), and a prefix gives one key many addresses.
- OpenSSH up to 10.2p1 checks P-256 coordinate sizes, which 10.3p1 dropped (`9bea0818`; read 2026-10-13 and 2026-10-15): a known variance of the script (Appendix A).

### Signatures

**Ours** (§3.2, §5.2): a signature has one form, the bytes `ssh-keygen -Y sign` writes, which a verifier writes back and compares; a certificate has one form, with no critical option and no extension.

| System | What it does | Source |
|---|---|---|
| age, signify, minisign | Each reads the text around a signature leniently, its own way | C2SP `age.md`, `openbsd/src` `e50e76aa`, minisign `b6de4fe3`, read 2026-10-31 |
| RFC 8032 | Verifiers "need not agree on whether signatures are valid" | [RFC 8032](https://www.rfc-editor.org/rfc/rfc8032) §5.1.7, read 2026-10-15 |
| Bitcoin, Ethereum | Bitcoin relays, and Ethereum takes, only the low s (BIP 146's LOW_S, EIP-2) | `bitcoin/bips`, `ethereum/EIPs`, read 2026-10-31 |
| `x/crypto/ssh`, OpenSSH, WebAuthn | `x/crypto` refuses a security-key signature without user presence but for a certificate's CA signature, which OpenSSH verifies with no flags read; WebAuthn: "Verify that the UP bit of the flags in authData is set." | `ssh/certs.go` v0.59.0, `sshkey.c` `cert_parse`, [WebAuthn](https://www.w3.org/TR/webauthn-3/) §7.2, read 2026-11-23 |
| SSH certificates | An unknown extension "MUST ignore it" | `draft-ietf-sshm-cert-03` §2.3, read 2026-11-24 |

**Where ours differs:**
- One form for the text too, so a signature is one value at every party; `ssh-keygen -Y verify` takes more (Appendix A).
- RFC 8032's cofactorless equation, Go's and OpenSSL's, where OpenSSH's libsodium build differs (measured 2026-10-20): a library can add a refusal, not widen an equation.
- S below L by explicit code: OpenSSH refuses S + L only from 10.3p1 on OpenSSL (`266647c5`) and from 10.4p1 on its own code (`7763a38b`) (read 2026-10-15), and an older `ssh-keygen` takes it (measured 2026-10-14).
- s or n − s, where Bitcoin relays and Ethereum takes the low s alone: `ssh-keygen` wrote the high s in 11 of 16 (measured 2026-10-31), and nothing rests on a signature's bytes (§15.4).
- User presence on a certificate's own signature too, stricter than OpenSSH and `x/crypto`: the identity is the CA, and a waiver would let a key that needs no touch certify but not revoke.
- No extension, which the draft has a reader ignore: a Dromas certificate certifies a working key and nothing more.

### Key types

**Ours** (§4.1, §7): every identity key type v1.0 defines is mandatory to verify; a mailbox declares the optional sender key types it verifies, and its server refuses a sender it rules out.

| System | What it does | Source |
|---|---|---|
| TLS 1.3, JOSE | Mandatory and optional algorithms | RFC 8446 §9.1, RFC 7518 §3.1, read 2026-10-13 |
| MLS | A KeyPackage lists only non-default types; the default ones "MUST NOT be listed" | [RFC 9420](https://www.rfc-editor.org/rfc/rfc9420) §7.2, read 2026-11-01 |

**Where ours differs:**
- Declared ahead, as MLS, not on a connection as TLS's `signature_algorithms` (RFC 8446 §4.2.3, read 2026-11-20): a sender and its recipient never connect.
- Unsigned, where MLS signs its list: §7 says why that suffices.

### Post-quantum

**Ours** (§4.2, §15.10): ML-KEM-768 + X25519 is the one prekey type, and signatures stay classical, a post-quantum one being a new identity key type and a new address.

| System | What it does | Source |
|---|---|---|
| Signal | "Authentication in PQXDH is not quantum-secure." | PQXDH revision 3 §4.1, read 2026-11-24 |
| OpenSSH | `mlkem768x25519-sha256` the default from 10.0, and from 10.1 a warning "when a non post-quantum key agreement scheme is selected"; `ssh-mldsa44-ed25519` a key type of its own from 10.6 | [openssh.org/pq.html](https://www.openssh.org/pq.html), read 2026-11-25; `release-10.6`, read 2026-11-24 |

**Where ours differs:**
- No classical prekey, where OpenSSH falls back to a classical exchange: every client reads the hybrid.
- A post-quantum signature costs a new address, since an address commits to one identity key (§15.1).

### Prekeys

**Ours** (§6, §12.4): a signed prekey statement, renewed under an unchanged address, lasting at most 8 days, ordered by signer, then serial.

| System | What it does | Source |
|---|---|---|
| Signal | A signed prekey plus one-time prekeys | [X3DH](https://signal.org/docs/specifications/x3dh/) §4.7, read 2026-11-12 |
| TUF | Mandatory expiry; versions that only rise | review 2026-10-10 |
| Let's Encrypt | Six-day certificates by opt-in | [Let's Encrypt](https://letsencrypt.org/2025/01/16/6-day-and-ip-certs/), read 2026-10-18 |
| DTLS | An epoch, then sequence numbers per epoch | [RFC 9147](https://www.rfc-editor.org/rfc/rfc9147) §4.2, read 2026-10-13 |

**Where ours differs:**
- Separate keys for signing and encryption, as libsodium recommends (review 2026-10-10): the prekey is renewed under a fixed identity.

**Rejected:**
- One-time prekeys: a pool of keys for each recipient at the server.
- A ratchet: joint state stock tools cannot keep, which a restored mailbox would lose (§15.3).

### One recipient stanza

**Ours** (§11.10 step 7, §12.3 check 1): a reader takes a file only with exactly one native stanza, and refuses any other before any key agreement.

| System | What it does | Source |
|---|---|---|
| age | Takes up to 1,024 stanzas and tries every identity on each | `filippo.io/age` v1.3.2, read 2026-10-22 |

**Where ours differs:**
- One stanza: a 1,024-stanza file cost a two-prekey mailbox 262 ms against 0.28 ms for an honest one, and a 66-prekey mailbox 8.6 s; with the check, 2.3 ms (measured 2026-10-22, one arm64 core). No honest server serves one, and age reads every file the check passes, grease included, so age stays compatible.

### Padding

**Ours** (§9.3): padding after the signature, to blocks of the smallest power of two at least a sixteenth of the ciphertext and at least 1 KiB.

| System | What it does | Source |
|---|---|---|
| OpenPGP | A padding packet after a signed message | [RFC 9580](https://www.rfc-editor.org/rfc/rfc9580) §10.3, read 2026-10-13 |
| Padmé | Under 12% overhead, where a power of two costs up to 100% | [Padmé](https://arxiv.org/abs/1806.03160) §4.4, read 2026-11-12 |
| NIP-44 | Above 256 bytes, an eighth of the next power of two | [NIP-44](https://github.com/nostr-protocol/nips/blob/master/44.md), read 2026-11-12 |

**Where ours differs:**
- Under 12.5% above 16 KiB, near Padmé, in power-of-two blocks as NIP-44's; the 1 KiB floor hides the smallest sizes.

### Message lifetime and retries

**Ours** (§12.2, §13): a message waits for its mailbox at most the TTL, 7 days; a sender seals it afresh, dated at the post, at every attempt, and a post whose answer was lost goes again as a new message, delivered once by `msg_id`.

| System | What it does | Source |
|---|---|---|
| SMTP | Retries the same bytes for "at least 4-5 days" | [RFC 5321](https://www.rfc-editor.org/rfc/rfc5321) §4.5.4.1, read 2026-11-01 |
| FCM | Keeps a message at most 4 weeks | [FCM](https://firebase.google.com/docs/reference/fcm/rest/v1/projects.messages), read 2026-11-01 |
| Signal, Matrix | Encrypt again at each attempt; Matrix adds a transaction id | Signal-Android, matrix-rust-sdk, read 2026-10-16 |

**Where ours differs:**
- Sealed afresh, where SMTP resends the same bytes: a seal is dated, as a signed request is at each attempt (SigV4's IAM guide, aws-sdk-go-v2, RFC 9449, RFC 9421; read 2026-10-18).
- No transaction id: a lost answer costs one copy the reader acks, where an id costs per-sender server state.

### Groups

**Ours** (§9.1): a group message is one copy to each other member, with `to` naming that member, all with one `group`, the sorted set of members; another set is another group.

| System | What it does | Source |
|---|---|---|
| Nostr | A room is its set of `p` tags; a changed set "a new room" | [NIP-17](https://github.com/nostr-protocol/nips/blob/master/17.md), read 2026-11-12 |

**Where ours differs:**
- No group id and no state: changing membership needs a relay mailbox or MLS, outside v1.

### Duplicate posts

**Ours** (§11.10 step 6): the same ciphertext posted again while live gets the receipt held, from any sender, and a concurrent one waits for the first's answer.

| System | What it does | Source |
|---|---|---|
| Stripe | A retry with the same key returns the saved response for a day | Stripe, idempotent requests, read 2026-10-14 |
| Idempotency-Key | A concurrent duplicate "SHOULD reply with an HTTP 409" | `draft-ietf-httpapi-idempotency-key-header-09`, read 2026-11-24 |
| Synapse | A concurrent duplicate gets the first's answer | `synapse/rest/client/transactions.py` `1b0fd0ab`, read 2026-11-19 |

**Where ours differs:**
- The key is the bytes' hash, whoever posts them: whoever reposts a captured ciphertext spends its own limits.
- A concurrent duplicate waits, as in Synapse: its answer is the first's, and `409` already means a conflict here (§11.3).

### Replay

**Ours** (§12.3 check 8): the recipient refuses replay by an id from the ciphertext, a seen-set and a window on `sent_at`.

| System | What it does | Source |
|---|---|---|
| Kerberos | A replay cache; one lost "MUST reject all requests until the clock skew interval has passed" | [RFC 4120](https://www.rfc-editor.org/rfc/rfc4120) §3.2.3, read 2026-10-18 |
| TLS 1.3 | Records 0-RTT ClientHellos within a window | [RFC 8446](https://www.rfc-editor.org/rfc/rfc8446) §8.2, read 2026-10-18 |

**Where ours differs:**
- A step of the clock further back, and a lost seen-set, are failures whose cost is accepted, not refused as Kerberos refuses: a server may deliver again what the mailbox accepted before (§15.7, §15.9).
- The window is the reader's clock, which may step, where Kerberos's and TLS's is the server's: the seen-set is kept an hour longer, with every copy dated up to the TTL, 7 days, after the first.

### JSON

**Ours** (§3.3): every JSON text is I-JSON, read one way by every reader, its byte fields standard base64; a ciphertext is the raw body (§11.10).

| System | What it does | Source |
|---|---|---|
| RFC 7493 | I-JSON, which a security protocol may require; RECOMMENDS base64url | [RFC 7493](https://www.rfc-editor.org/rfc/rfc7493) §3, §4.4, read 2026-11-12 |
| Go `encoding/json/v2` | Refuses duplicate names, citing parser differentials | Go 1.27.2, read 2026-10-14 |
| jq | Takes `NaN` and `01`, keeps the last duplicate | measured 2026-10-14, jq 1.7.1 and 1.8.1 |
| S3, Signal | A large binary as the raw body | S3 `PutObject`, Signal-Server `ccc22f9a`, read 2026-10-17 |

**Where ours differs:**
- Standard base64, not base64url: the alphabet of OpenSSH's key text and SSHSIG's armour, which a client also reads (measured 2026-11-25, OpenSSH 10.5p1).
- An integer is digits alone, as Go's decoders read, where JSON Schema and ProtoJSON take `1.0` (read 2026-10-15); a magnitude past binary64 is refused, where RFC 7493 §2.2 says SHOULD NOT, since readers part there (measured 2026-10-19).
- At most 32 deep, under jq 1.7.1's 257 and Go's 10,001 (measured 2026-10-14), so every reader gives one verdict, as RFC 8259 §9 lets an implementation limit depth.
- A null member is one left out, as a ProtoJSON null "leaves the field unset" (read 2026-10-14): absent has one form.
- A server refuses an unknown member in what it is sent, as ProtoJSON's parser "should reject unknown fields by default"; every other reader ignores one, RFC 7493 §4.2's Must-Ignore: a server knows its own version's members, and a reader may meet a later version's (§14).
- A duration is whole seconds, not RFC 7493 §4.3's RFC 3339 duration, as `Retry-After` counts a delay (RFC 9110 §10.2.3, read 2026-11-12).
- A ciphertext is no JSON member: base64 is a third larger, and an overhead is fixed bytes, never a share.

### Polling

**Ours** (§11.11): a list answers `304` while its `ETag` holds, gives the poll interval in `Retry-After`, may be held until it changes, and has no cursor.

| System | What it does | Source |
|---|---|---|
| GitHub | An authorized conditional request answered `304` "does not count against your primary rate limit"; `X-Poll-Interval` | [GitHub](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api), read 2026-11-25 |
| RFC 9773 | `Retry-After` as the poll interval on a `200` | [RFC 9773](https://www.rfc-editor.org/rfc/rfc9773) §4.3, read 2026-10-18 |
| Consul | Blocking queries: an `index` and a `wait` | [Consul](https://developer.hashicorp.com/consul/api-docs/features/blocking), read 2026-10-28 |
| SimpleX, POP3 | No paging: SMP sends the next message after an ack; POP3's `LIST` gives every message, and `DELE` is the ack | [SMP](https://github.com/simplex-chat/simplexmq/blob/3727c9f5f1d5160a536ed9fb1cdda4087190f69d/protocol/simplex-messaging.md), [RFC 1939](https://www.rfc-editor.org/rfc/rfc1939) §5, read 2026-11-24 |

**Where ours differs:**
- A `304` spends the poll rate, where GitHub's is free: a free poll is one a bot makes every second.
- RFC 9773's `Retry-After`, not an `X-` name ([RFC 6648](https://www.rfc-editor.org/rfc/rfc6648) §3).
- The hold's baseline is the list's `ETag`, not a server index as Consul's.
- A lookup's `304` spends the IP address's counts, where GitHub's is free: a count spent after the comparison would need the target read first.

**Rejected:**
- RFC 7240's `Prefer: wait`, which bounds processing time and holds nothing for a change (read 2026-10-28).

### Order of refusals

**Ours** (§11.4, §11.2, §11.5): a server orders its refusals as it likes, but for a retired version before everything, the pass included, mailbox state before the pass, a retry answered from what is kept, and preconditions before acting.

| System | What it does | Source |
|---|---|---|
| RFC 9110 | Orders only preconditions, "just before it would process the request content" | [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110) §13.2.1, read 2026-10-24 |

**Where ours differs:**
- Three orders more, each one a client acts on: a client below the floor learns it whatever else its request gets wrong, a forgotten or revoked mailbox learns it whatever pass it carries, and a retry gets what is kept.

### Resumed fetches

**Ours** (§11.12): a cut fetch resumes with `Range: bytes=N-`, with no `If-Range`, the whole held to the id.

| System | What it does | Source |
|---|---|---|
| apt, Chromium | Resume with `If-Range` | apt `methods/http.cc`, Chromium `download_utils.cc`, read 2026-10-23 |

**Where ours differs:**
- No `If-Range`, which guards a URL whose content can change: a message's id hashes its whole.
- No `416`: a server takes only `bytes=N-` inside the ciphertext and answers any other `Range` with the whole body, as RFC 9110 §14.2 lets it ignore one (read 2026-10-23).

### HTTPS and redirects

**Ours** (§11.1): https only, but for a loopback address, and no redirect followed.

| System | What it does | Source |
|---|---|---|
| Secure Contexts, RFC 8252 | Loopback is "Potentially Trustworthy"; the name `localhost` "NOT RECOMMENDED" | [Secure Contexts](https://w3c.github.io/webappsec-secure-contexts/) §3.1, [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252) §8.3, read 2026-10-18 |
| Go | `http.Client` follows 10 redirects, keeping `Authorization` for the same host or a subdomain, any scheme or port (`shouldCopyHeaderOnRedirect`) | `net/http/client.go`, Go 1.27.2, read 2026-10-18 |

**Where ours differs:**
- No redirect, even same-origin: the API redirects nowhere, and a redirect could carry a pass elsewhere.
- The name `localhost` is not taken, only the loopback literals, as RFC 8252 §8.3 advises: the name may resolve off the machine.

### Accounts

**Ours** (§11.16): a mailbox may join one account at its server with an adopt key the server makes, spent at a call the mailbox's own pass proves.

| System | What it does | Source |
|---|---|---|
| ACME | External Account Binding: a MAC key the CA provides, over the account key at account creation | [RFC 8555](https://www.rfc-editor.org/rfc/rfc8555) §7.3.4, read 2026-11-01 |

**Where ours differs:**
- The key joins an existing mailbox, after registration, and proves nothing after.

### Limits

**Ours** (§11.5): each server publishes its own limits as budgets with a burst and a refill, a call counted against the mailbox whose pass it carries, else its IPv4 address or IPv6 /64, and by /48 beside each /64 count, each count taken before the work and given back if the work does not happen.

| System | What it does | Source |
|---|---|---|
| Let's Encrypt | Publishes its limits as GCRA buckets; refunds a failed new account's or order's count; counts accounts by /48; exempts an ARI renewal from every limit; per-IP limits ahead of signature checks | [Rate Limits](https://letsencrypt.org/docs/rate-limits/), Boulder `ratelimits/` `f6d71764d4`, read 2026-10-22 |
| RFC 8555 | One `rateLimited` type, the limit named in prose; `Retry-After` when "all rate limits allow access again" | [RFC 8555](https://www.rfc-editor.org/rfc/rfc8555) §6.6, read 2026-11-01 |
| Google | Names a limit in `quota_id` | `error_details.proto` `ebd1d23ac6`, read 2026-10-19 |
| GitHub | Counts unauthenticated calls by IP, authenticated ones by user | [GitHub](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), read 2026-10-22 |
| MongoDB | A document is at most 16 MiB | [MongoDB](https://www.mongodb.com/docs/manual/reference/limits), read 2026-11-12 |

**Where ours differs:**
- A /48 count beside every count an address keys, where Let's Encrypt keeps one for accounts: a /64 alone gives one /48 65,536 budgets.
- Fewer renewals exempt: only one made while the current signer is revoked, whose taker may have spent the mailbox's calls; Let's Encrypt's condition would exempt every renewal under a later certificate, so the count of certificates would count nothing.
- A revocation of certificates has a count of its own, `certificate_revocations`, where Let's Encrypt's revocation endpoint has only per-IP limits (read 2026-10-19): it can rise without end, so every exempt renewal follows a counted raise.
- A lookup is counted by IP address whatever it carries (§11.5), where GitHub counts an authenticated call by user: a pass on the read is never read, so it lifts nothing and spends nothing.
- A mailbox's new-message budget is shared by its senders, as Let's Encrypt's per-domain limits across accounts, but any mailbox can spend it: a published mailbox defends with an allow list (§15.9).
- Nothing is paused after failures, where Let's Encrypt pauses: a failure here costs a signature check, not a validation.
- The limit is named in `quota_id`, Google's way: a client shows no server text (§ Errors).
- `Retry-After` covers the limit the refusal names, where RFC 8555 §6.6 asks for all: a limit behind work (storage, a signature, the recipient's counts) is not read ahead of it, so another can refuse once the wait is over; Boulder too reads storage before some limits (`wfe2/wfe.go`, read 2026-10-20).
- A request with a pass the server has checked need spend no IP `requests` (§11.5's MAY), as GitHub counts an authenticated call by user: it costs a hash and a memory lookup, not a signature check; and the answer is `429`, not `503`.
- 16 MiB is part of the wire, where MongoDB's cap is one service's: every reader reads what any server accepts.

### Versions

**Ours** (§11.17, §11.18): every request names its protocol version, a major and a minor, in `Dromas-Version`, and a server refuses one below its floor, or past a sunset's date, with `410`, naming the floor.

| System | What it does | Source |
|---|---|---|
| A2A | `A2A-Version` as `Major.Minor`; patch numbers "MUST not be considered" | [A2A](https://github.com/a2aproject/A2A/blob/main/docs/specification.md) §3.6, read 2026-11-24 |
| MCP | `MCP-Protocol-Version` on every request; one absent is read as `2025-03-26` | [MCP](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports), read 2026-11-24 |
| GitHub | `X-GitHub-Api-Version`; a retired version `410 Gone`; `Deprecation` and `Sunset` | [GitHub](https://docs.github.com/en/rest/about-the-rest-api/api-versions), read 2026-10-30 |
| Kubernetes | `kubelet` "must not be newer than `kube-apiserver`" | [skew policy](https://kubernetes.io/releases/version-skew-policy/), read 2026-11-24 |
| IMAP | `CAPABILITY` lists what a server supports | [RFC 9051](https://www.rfc-editor.org/rfc/rfc9051) §6.1.1, read 2026-11-25 |

**Where ours differs:**
- All after the minor is ignored, not the patch alone, so a client that names its whole release reads as its minor, and a server turns away a faulty build by `User-Agent` (RFC 9110 §10.1.5), outside the protocol.
- No header is `0.0`, below any floor, where GitHub and MCP serve a default.
- The floor is a member of the refusal, `min_version`: a client shows no text a server sent (§ Errors), so it reads the floor as a typed value.
- `Deprecation` and `Sunset` cover the version named, on every route, the wider scope RFC 8594 §3 lets a server document (read 2026-10-30), with no `deprecation` link (RFC 9745 §3, read 2026-10-30): a URL is a server's text.
- No `Warning` header, which [RFC 9111](https://www.rfc-editor.org/rfc/rfc9111) §5.5 obsoletes (read 2026-10-30).
- A server is upgraded before the clients that need it, as in Kubernetes.
- Between two mailboxes, compatibility runs both ways inside a major: no server's version stands between them, so a message stays readable both ways, as an optional key type is kept from a reader that lacks it (§ Key types).
- The capability names are defined in §11.18 itself, their registry, never URLs; the document carries the version policy beside them.

---

## Part 3: The reference server's own choices

### Three outcomes of a store write

**Ours** (dromas-server `ARCHITECTURE.md` § The store): a write is applied, refused, or unknown, and a count is given back only on a stated refusal.

| System | What it does | Source |
|---|---|---|
| Go `database/sql/driver` | "ErrBadConn should NOT be returned if there's a possibility that the database server might have performed the operation." | [`driver.go`](https://github.com/golang/go/blob/go1.27.3/src/database/sql/driver/driver.go), read 2026-10-24 |
| MongoDB Go driver | `UnknownTransactionCommitResult`, apart from a failure | [`errors.go`](https://github.com/mongodb/mongo-go-driver/blob/v2.9.2/x/mongo/driver/errors.go), read 2026-10-24 |

### Metrics on a listener of their own

**Ours** (dromas-server `ARCHITECTURE.md` § Metrics): counts on a listener apart from the API, off unless set.

| System | What it does | Source |
|---|---|---|
| Prometheus | "instrument everything" | `instrumentation.md` `410443d27b`, read 2026-10-19 |
| Boulder | Metrics on a separate listener, none when unset | `cmd/shell.go` `cf6406dd86`, read 2026-10-19 |

### One long poll a mailbox

**Ours** (dromas-server `LIMITS.md`): a later long poll takes the place of the earlier, which is answered `304`.

| System | What it does | Source |
|---|---|---|
| Telegram Bot API | A later `getUpdates` ends the earlier with `409` | [`Client.cpp`](https://github.com/tdlib/telegram-bot-api/blob/e3e9dd8e5b3d7ab8537cd5a10dc31d5ffa8f82d1/telegram-bot-api/Client.cpp), read 2026-11-06 |

**Where ours differs:**
- `304`, the list's ordinary answer: the earlier may be a dropped connection whose retry is the later one.

### Keyed hashes in logs

**Ours** (dromas-server `SECURITY.md`): a log names an address or message id by SipHash-2-4 under a key held only in memory, replaced daily.

| System | What it does | Source |
|---|---|---|
| Linux | `%p` prints a pointer's SipHash under a key made at boot | [`vsprintf.c`](https://github.com/torvalds/linux/blob/d48723711bae9b8cdc56719c448ba42fef28cbee/lib/vsprintf.c), read 2026-11-07 |
| Plausible | A daily salt that "is rotated and deleted every 24 hours" | [Plausible](https://plausible.io/data-policy), read 2026-11-07 |

### Bodies on disk

**Ours** (dromas-server `ARCHITECTURE.md` § The sweep): `--data-dir` keeps a body at `bodies/<hour>/<key>`, and an hour is removed whole once all of it is past the message TTL.

| System | What it does | Source |
|---|---|---|
| etcd, Consul | `--data-dir`, "Path to the data directory." | [etcd](https://github.com/etcd-io/etcd/blob/e6b7e73d6f0759f3aacb5d6a6fbd9598ee413441/server/embed/config.go), [Consul](https://github.com/hashicorp/consul/blob/02e8de99fe0ed8362431358a0b185f4b15095796/agent/config/flags.go), read 2026-11-10 |
| Prometheus | Two-hour blocks, each "fully expired before they are removed" | [`storage.md`](https://github.com/prometheus/prometheus/blob/5ee21818d8abd5677381d9d0e7bca27352e4f46b/docs/storage.md), read 2026-11-10 |
| MinIO | `deleteFile` goes up the tree "deleting empty parent directories until it finds one with files in it" | [`cmd/xl-storage.go`](https://github.com/minio/minio/blob/b8631cf5316c613fa08f4c38334de19870facf2f/cmd/xl-storage.go), read 2026-11-10 |

**Where ours differs:**
- An hour is never merged into a longer range, as Prometheus compacts its blocks, because a body is written once and removed at its ack or with its hour; an ack's delete takes the directories it leaves empty, up to its hour's, as MinIO's `deleteFile` does.

### A shared store for sends a day

**Ours** (dromas-server `ARCHITECTURE.md` § The shared store): with `--valkey`, a sender's sends a day are a GCRA bucket in Valkey, taken in one Lua script, with persistence off.

| System | What it does | Source |
|---|---|---|
| Boulder | A bucket's TAT in Redis, shared by every instance | [`source_redis.go`](https://github.com/letsencrypt/boulder/blob/7a93d5945d88c7e24e5da998a7194233c3d783cd/ratelimits/source_redis.go), read 2026-11-13 |
| Envoy | Lets a request through when the rate limit service fails, unless `failure_mode_deny` | [`rate_limit.proto`](https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/http/ratelimit/v3/rate_limit.proto), read 2026-11-13 |

**Where ours differs:**
- A take is checked and written in one step of the store, so sends in flight at once on several processes cannot all pass on one token.
- A failed call falls back to the process's own count, not to no limit, as Envoy's falls back to none.
- Nothing is on disk: a bucket's time can give a send's moment, and the messages table keeps each `received_at` beside its recipient, so a copy at rest could pair a sender with a recipient (§13).

---

## Sources

For the claims marked `review 2026-10-10`.

- [Signal SPQR](https://signal.org/blog/spqr/) · [WhatsApp key transparency](https://engineering.fb.com/2023/04/13/security/whatsapp-key-transparency/) · [iMessage PQ3](https://security.apple.com/blog/imessage-pq3/) · [Contact Key Verification](https://security.apple.com/blog/imessage-contact-key-verification/)
- [Proton encryption](https://proton.me/support/proton-mail-encryption-explained) · [Proton private keys under the password](https://proton.me/blog/email-password) · [Proton post-quantum](https://proton.me/blog/introducing-post-quantum-encryption) · [RFC 9420](https://www.rfc-editor.org/rfc/rfc9420) · [SimpleX v5.6](https://simplex.chat/blog/20240314-simplex-chat-v5-6-quantum-resistance-signal-double-ratchet-algorithm.html) · [NIP-17](https://github.com/nostr-protocol/nips/blob/master/17.md)
- [Noise rev 34](https://noiseprotocol.org/noise.html) · [libsodium, Ed25519 to Curve25519](https://doc.libsodium.org/advanced/ed25519-curve25519) · [TUF](https://theupdateframework.github.io/specification/latest/) · [RFC 6750](https://www.rfc-editor.org/rfc/rfc6750)
- Davis, "Defective Sign & Encrypt in S/MIME, PKCS#7, MOSS, PEM, PGP, and XML," USENIX 2001

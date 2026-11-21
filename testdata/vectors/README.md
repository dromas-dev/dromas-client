# Dromas v1 test vectors

This directory holds files that any Dromas implementation can check itself against. Stock tools make most of the files: `generate.sh` uses `bash`, `od`, `base64`, `sha256sum`, `ssh-keygen`, `openssl`, and, for the three ciphertexts only, `age` 1.3 or later. A Go test makes the few files that no stock tool can write, from the same published keys (`DROMAS_WRITE_VECTORS=1 go test ./protocol`, in `protocol/vectors_test.go`).

Judge every vector at the time in `now`, with the revocations of certificates that a reader holds (PROTOCOL.md §5):

- **For mailbox A, the reader holds `a/revoked-certs.txt`.** This file revokes A's certificates through serial 1791590399, which is A's clock at `now` minus one. A's certificates are numbered higher than that serial, starting from 1791590400.
- **For mailboxes B and C, the reader holds no revocation.**

A reader accepts each identity's own signature on a statement, a pass, or a message. A reader accepts a certificate's signature if the certificate is numbered higher than the revocation that the reader holds, and accepts every certificate if the reader holds no revocation. A signs its statements, its pass, and its messages under its certificate. B and C sign theirs with the identity. B's and C's certificates sign the vectors that show that a reader accepts those certificates.

## Keys

**The keys are public test keys.**

- **Ed25519 keys** are from RFC 8032. Each `input/*.ed25519` file holds the seed and then the public key, in hex.
  - From RFC 8032 §7.1: mailbox A's identity is TEST 1, and mailbox B's identity is TEST 2. A's working key is TEST 3, and B's working key is TEST 1024. A second working key of A's is TEST SHA(abc).
  - From RFC 8032 §7.2: mailbox C's working key is the key of the first Ed25519ctx test.
- **Mailbox C's identity** is of type `ecdsa-sha2-nistp256`. It's the test key of RFC 6979 §A.2.5. The `input/c.p256` file holds the private scalar, and then the public point as x and y, in hex.
- **The prekey of B and C** is the published test identity of the C2SP age testkit (CCTV `age/internal/testkit`). `input/b-1.pq.key` is its `TestHybridIdentity`, so each message here decrypts with any age implementation.
- **The other age recipients in `input/`** are public keys whose secret keys were discarded.

## Vectors

Each section names a path, what the files there hold, and the expected result.

### `a/`

Mailbox A:

- `identity.doc` and `address`.
- `revoked-certs.txt` and `revoked-certs.sig`: A's revocation of its certificates, signed by the identity itself.
- `work-cert.pub`: the certificate that A's identity issued for A's working key, serial 1791590400. It's valid for 45 days and one hour, starting one hour before `now`.
- The prekey statements `prekey-<serial>.txt`, each with its `.sig`, signed by the working key under that certificate. Each statement therefore names that certificate's serial as the rank of its signer (PROTOCOL.md §6).
- `revoke.txt` and `revoke.sig`: A's revocation, signed by the identity itself.
- `pass.txt`, `pass.sig`, and `pass.authorization`: a pass under A's certificate, for a server that A calls `dromas.example`.

**Expected result:** all are valid when the reader holds A's revocation of certificates. What the identity signs itself is never ranked, and the certificate is numbered higher than the revocation. The pass expires ten minutes after `now`.

### `b/`, `c/`

Mailboxes B and C. C's identity is a P-256 key, and C's `identity.doc` is 171 bytes. Each directory holds the following:

- `identity.doc` and `address`.
- `prekey-1.txt` with its `.sig`, naming rank 0, signed by the identity itself.
- `pass.txt`, `pass.sig`, and `pass.authorization`: a pass for `dromas.example`, signed by the identity itself.
- `work-cert.pub`: a certificate that the mailbox's identity issued, serial 1791590400, valid under every rule for a certificate. `prekey/b-signed-under-its-certificate`, `prekey/c-signed-under-its-certificate`, and `plain/accept-p256-sender-under-a-certificate` are signed under these certificates.
- In `c/` only, `revoke.txt` and `revoke.sig`: C's revocation.

C's signatures are made once and kept.

**Expected result:** all are valid, including the certificates, when the reader holds no revocation of B's or C's certificates. C's files are valid for a client that verifies `ecdsa-sha2-nistp256`. A client that doesn't verify that key type refuses C's document with its code for a key type that it doesn't support, never as malformed.

### `cert/bad-*`

Each vector has three files:

- `.pub`: a certificate that breaks one rule.
- `.txt`: a statement of A's that names that certificate's serial as the rank of its signer, so the rank is never the reason for the refusal.
- `.sig`: a signature over that statement, made under the certificate, in A's prekey namespace.

**Expected result:** every vector is refused for mailbox A. Each signature is valid under the certificate that it carries, so the certificate is what fails, except in `bad-revoked`, where A's revocation of certificates is what fails.

- `bad-revoked` is valid under every rule for a certificate, but its serial is 1791590399, which A's revocation of certificates covers. A reader refuses it with `certificate_revoked`, and accepts it if the reader holds no revocation.
- `bad-certifies-the-identity` is the re-wrap. It's A's identity's own signature over the statement beside it, with a certificate of the identity's own key put in the place of the signature's key. The certificate is numbered higher than A's revocation of certificates. SSHSIG doesn't sign the signing key, so the signature verifies under the certificate, and the statement names that certificate's serial. The only reason to refuse it is that the certified key is the identity's own key.
- `bad-ends-after-the-last-date` is valid for one hour and ends one second after 9999-12-31T23:59:59Z. `bad-begins-after-the-last-date` begins one second after that moment. `bad-forever` never ends (`-V …:forever`). A reader refuses each of these for its form, never as not yet begun.
- `bad-runs-longer-than-the-maximum` is valid for one second longer than 45 days and one hour, which is the longest that any verifier accepts (PROTOCOL.md §5.2). `bad-ended` is valid for exactly 45 days and one hour, and ends at `now`.
- `bad-serial-above-2-53` has serial 2^53, one higher than the highest serial.
- A certificate has one valid form: the form that `ssh-keygen -s … -O clear` writes. `bad-extensions` has the five extensions that `ssh-keygen` adds without `-O clear`. `bad-reserved-field` has bytes in its reserved field. `bad-nonce-of-16-bytes` has a nonce of 16 bytes instead of 32. The `TestVectorsCertificateForm` test in `protocol` makes the last two, because `ssh-keygen` writes neither. `bad-extensions` makes a signature longer than the bound (PROTOCOL.md §3.2), so a reader refuses the signature before it reads the certificate.
- Used alone, `ssh-keygen -Y verify` accepts those three vectors, and also `bad-critical-option`, `bad-key-id`, and `bad-serial-zero`.
- `bad-working-key-small-order` certifies the neutral point, which is a working key of small order. Its signature, with R the neutral point and S zero, satisfies the Ed25519 equation, so only the check on the working key refuses it.

### `revoke/bad-*.sig`

Signatures that aren't a revocation of mailbox A:

- `bad-by-a-working-key`: A's working key over A's statement, under A's own certificate.
- `bad-by-another-identity`: B's identity over A's statement.
- `bad-another-namespace`: A's identity over A's statement, in the prekey namespace.
- `bad-of-another-mailbox`: A's identity over B's statement.
- `bad-s-plus-l`: A's own revocation, with L added to its S.

**Expected result:** every one is refused as a revocation of A. `ssh-keygen -Y verify` accepts `bad-by-a-working-key` when the signers line names the identity as a `cert-authority`, so a verifier's signers line for a revocation never names it that way.

### `revoked/ok-*`

A revocation of mailbox A's certificates (`.txt`) and A's identity's signature over it (`.sig`):

- `ok-through-4`: every certificate up to and including serial 4.
- `ok-through-the-highest`: through 2^53 − 1, the highest serial there is.

**Expected result:** accepted as revocations of A's certificates, each through exactly that serial.

### `revoked/bad-*`

Files that aren't a revocation of A's certificates. Each has a signature that is valid over its bytes:

- `bad-by-a-working-key`: signed by A's working key, under A's own certificate. A working key never signs a revocation.
- `bad-another-namespace`: signed by A's identity, in the namespace of a mailbox's revocation (`revoke.txt`).
- `bad-another-address`: holds B's address.
- One vector for each rule of the text: no serial; a serial of zero, with a leading zero, with a sign, with a trailing space, of 19 digits, or greater than 2^53 − 1; a range; a second serial; a blank line; CRLF; no final newline; another tag; the address in uppercase; a NUL; and `bad-a-list`, which holds a list of serials and ranges.
- `bad-s-plus-l`: a valid revocation whose signature has L added to its S.

**Expected result:** every one is refused. `ssh-keygen -Y verify` accepts each of the malformed ones, because the signature is valid: the text is what fails. A shell reader that tests only a variable accepts `bad-nul`. Before the cutoff that PROTOCOL.md Appendix A gives, `ssh-keygen -Y verify` also accepts `bad-s-plus-l`, as it accepts `revoke/bad-s-plus-l`.

### `sig/ok-*`, `sig/bad-*`

A signature in its one valid form and in other forms (PROTOCOL.md §3.2):

- `ok-ed25519` is `a/prekey-1.txt.sig`, and `c-ok-p256` is `c/prekey-1.txt.sig`. These are the one valid form of each identity key type.
- `c-ok-the-other-s` is C's signature with s written as n − s, which ECDSA also verifies.
- Each `bad-*` is A's signature in another form, over `a/prekey-1.txt` in A's prekey namespace under A's certificate:
  - The armor: with CRLF line ends, whitespace in the base64, the base64 on one line, no line feed after the END line, text after the END line, a NUL before the END line, a space after the BEGIN line, text before the BEGIN line, no END line, an indented END line, a character outside base64, and a NUL after the BEGIN line.
  - The blob: at version 0, hashed with SHA-256, with bytes in its `reserved` field, with its namespace ending in a NUL, with a byte after its five fields, and with its certificate's type ending in a NUL.
  - An Ed25519 S with L added: the working key's (`bad-s-plus-l`), or the identity's on the certificate (`bad-certificate-s-plus-l`).
- Each `c-bad-*` is C's signature, over `c/prekey-1.txt` in C's prekey namespace: r with a zero byte before it, a padding bit set in the base64, and the base64 without its padding. The last two aren't the one encoding of the base64.
- `ok-r-small-order` and `bad-r-plus-order-2` are A's signatures with an R that the RFC 8032 signer never makes. The `TestVectorsEd25519R` test in `protocol` makes them.

**Expected result:** `ok-*` accepted, and `bad-*` refused. `ssh-keygen -Y verify` accepts many of the `bad-*` vectors, because OpenSSH reads more forms than the one valid form. Here it's a validation tool, not the definition. On the builds that PROTOCOL.md Appendix A names, `ssh-keygen` judges the two R vectors the other way. Before the cutoff that Appendix A gives, it accepts the two vectors with L added to an Ed25519 S.

### `cert/ok-*`

A certificate at the edge of a rule, and a signature made under it, with the same files as `cert/bad-*`.

**Expected result:** accepted when the reader holds A's revocation of certificates. `ok-begins-now` begins at `now`. `bad-not-begun` begins one second later.

### `message/`

- `a-to-b.line`: an envelope from A to B, as its one line.
- `a-to-b.sig`: the signature of A's working key over the line for B, under A's certificate.
- `a-to-b.plain`: what is encrypted, which is the line, the signature, and the padding.
- `a-to-b.age`: `a-to-b.plain` encrypted to B's prekey.
- `a-to-c.*`: the same kinds of file, for a message from A to C, encrypted to C's prekey.
- `b-to-c.*`: the same files, from B to C, signed by B's identity.

**Expected result:** the line and the signature are byte-for-byte what a client makes from the same keys. Each age file is a whole number of blocks, and decrypts with its prekey's published identity to exactly its `.plain`, which its recipient delivers.

### `plain/accept-*`, `refuse-*`

What mailbox B decrypts.

**Expected result:** delivered or refused, as the name says, with the following codes:

- `malformed_message`: `refuse-no-line-feed`, `refuse-no-signature`, `refuse-blank-line-before-the-signature`, `refuse-signature-first`, `refuse-no-end-line`, `refuse-end-line-without-its-line-feed`, `refuse-crlf`, and `refuse-envelope-on-two-lines`.
- `invalid_signature`: `refuse-whitespace-in-the-signature`, which has a space, a CR, and a tab in its base64. That isn't the signature's one valid form.
- `malformed_message`: `refuse-end-line-followed-by-a-nul` and `refuse-end-line-followed-by-a-byte-and-a-later-one`. The second has a byte after the END line, and then an END line and its line feed in the padding; the block ends at the first END line.
- `malformed_message`: `refuse-begin-line-followed-by-a-nul`, `refuse-begin-line-holding-a-nul`, and `refuse-begin-line-preceded-by-a-nul`, because there is no BEGIN line.
- `invalid_identity`: `refuse-no-identity`, `refuse-identity-not-base64`, `refuse-identity-of-small-order`, `refuse-identity-off-curve`, `refuse-identity-ending-in-a-line-feed`, and `refuse-identity-ending-in-a-nul` (each a JSON escape).
- `invalid_signature`: `refuse-signed-for-another-mailbox`, `refuse-signed-by-a-bare-working-key`, `refuse-certificate-ended`, `refuse-line-changed-after-signing`, `refuse-another-mailbox-s-identity-document`, and `refuse-identity-s-signature-re-wrapped`. The last is A's identity's signature with `cert/bad-certifies-the-identity.pub` in the place of its key.
- `certificate_revoked`: `refuse-certificate-revoked`, signed under `cert/bad-revoked.pub`. A's revocation of certificates refuses it, at check 7.
- `sender_mismatch`: `refuse-from-is-not-the-identity`, `refuse-from-is-not-the-identity-of-an-unknown-type`, and `refuse-from-null`. A null member is the same as a member that is left out.
- `recipient_mismatch`: `refuse-to-another-mailbox`.
- `unsupported_key_type`: `refuse-unsupported-key-type`.

Notes on some of these vectors:

- `accept-signed-by-the-identity` is A's message, signed by A's identity itself at rank 0, which A's revocation of certificates doesn't cover.
- `accept-p256-sender` is from mailbox C, signed by C's identity, and is made once and kept. `accept-p256-sender-under-a-certificate` is the same message under C's certificate. A client that verifies `ecdsa-sha2-nistp256` delivers each one. A client that doesn't verify that type refuses each one with `unsupported_key_type`.
- `refuse-certificate-revoked` passes checks 1 to 6. Only the revocation of certificates refuses it.
- In `accept-anything-after-the-signature`, a second envelope, signed by B, follows the first END line, and the reader never reads it.
- `refuse-another-mailbox-s-identity-document` is the forgery that an identity member invites: A's document beside a certificate that B's identity signed.

### `identity/*.doc`

Malformed identity documents, and well-formed documents that hold a key that fails the checks for its type:

- An Ed25519 key that is of small order, or not canonical, or (`off-curve`) not a point on the curve.
- (`p256-*`) A P-256 point that is off the curve, compressed, not reduced, or the point at infinity. `p256-point-at-infinity` is one zero byte, as SEC1 writes it.
- `ok-p256-short-coordinate` (x = 5) and `ok-p256-coordinate-over-order` (x = n + 3) are points on the curve. OpenSSH up to 10.2p1 refuses them, and from 10.3p1 accepts them, as the Go library does and the protocol does (PROTOCOL.md §4.1).
- `type-name-ending-in-nul` and `p256-curve-name-ending-in-nul` are A's and C's keys, with a NUL at the end of the type's name or the curve's name. `ssh-keygen` reads each as that mailbox's key, and writes it back without the NUL, so neither is the key in its one valid encoding.
- `unsupported-type.doc` is a well-formed document of a key type that nobody verifies. The `type-name-*` documents are of the same kind, but their type's name breaks RFC 4251 §6: two at signs, an at sign with no fully qualified domain name after it, and 65 characters.
- `nul-*` and `p256-nul-*` hold a NUL byte, in place of a line feed or elsewhere, and are as long as a valid document.

**Expected result:** A client that verifies `ecdsa-sha2-nistp256` accepts `ok-*`, as it accepts `c/identity.doc`. Every other document is rejected: `unsupported-type.doc` as a key type that the client doesn't verify, and the rest as a bad document or a bad key.

### `prekey/bad-*`

Prekey statements, each with a `.sig` by A's working key under A's certificate, in A's namespace, except where the name says otherwise.

**Expected result:** every one is rejected, including `bad-serial-above-2-53`: a serial is at most 2^53 − 1, because an answer gives a serial as a JSON number. Each `bad-nul-in-*` statement holds a NUL in one line, which a reader drops if it reads the line through a shell variable. `bad-serial-a-digit-not-ascii` has a serial with U+0661, which `[0-9]` matches in some locales.

### `prekey/bad-rank-*`

Statements whose rank line (PROTOCOL.md §6) isn't the rank of the signer that signed them. Each has a signature that is valid under what it carries:

- `bad-rank-too-high`, `bad-rank-too-low`, and `bad-rank-0-under-a-certificate`: signed under A's certificate.
- `bad-rank-a-certificate-s-by-the-identity`: signed by A's identity, and names the serial of A's certificate.
- `bad-rank-under-another-certificate`: `a/prekey-1.txt`, with its signature shown under `bad-rank-under-another-certificate.cert.pub`, a later certificate of the same working key.
- `bad-rank-missing` has no rank line.
- `bad-rank-leading-zero` and `bad-rank-0-written-twice` give their signer's rank in another form.

**Expected result:** every one is rejected. `bad-rank-under-another-certificate` is the substitution: SSHSIG doesn't sign the signing key, so `ssh-keygen -Y verify` accepts it, and its order ranks it higher than `a/prekey-2`. Only the rank that it names refuses it.

### `prekey/outlives-its-certificate`

A valid statement of A's, signed under the certificate beside it (`.cert.pub`). That certificate is a later one of A's, serial 1791590401, which ends five days after `now`.

**Expected result:** valid. A statement can outlive the certificate that it's signed under. By `(c, p)`, this statement ranks higher than A's `prekey-2`.

### `prekey/signed-by-the-identity`

A's statement at serial 1, naming rank 0, signed by A's identity itself.

**Expected result:** valid when the reader holds A's revocation of certificates, because that revocation covers only certificates. By `(c, p)`, this statement ranks lower than every statement of A's under a certificate, so a sender that holds one of those statements refuses it.

### `prekey/b-signed-under-its-certificate`, `prekey/c-signed-under-its-certificate`

B's and C's own statements. Each names its own mailbox's certificate (`b/work-cert.pub` or `c/work-cert.pub`), is signed under that certificate, and is in its own mailbox's namespace.

**Expected result:** valid for B and for C, because nothing revokes their certificates.

### `prekey/expired`

A valid statement whose `not_after` is `now`.

**Expected result:** valid, but expired. A sender must not encrypt to it.

### `prekey/longest`

A valid statement that ends 8 days and one hour after `now`.

**Expected result:** not expired. This is the longest statement that a server accepts: one hour past what a client asks for.

### `prekey/too-long`

A valid statement that ends one second later than `prekey/longest`.

**Expected result:** not expired, but a server must refuse to publish it.

### `prekey/unsupported-type`, `prekey/unsupported-type-x25519`

Statements that are valid in every way except for their key:

- `unsupported-type`: a well-formed age recipient of a type that no prekey has. It's the published X25519 test key of RFC 7748 §6.1 (Alice's public key), under the human-readable part `age1nosuchtype`.
- `unsupported-type-x25519`: an X25519 recipient, as age writes one (`age1…`).

**Expected result:** refused with its own code, `unsupported_prekey_type`, never as a statement that doesn't verify. A sender can't encrypt to it, and nothing is wrong with it (PROTOCOL.md §6).

### `envelope/accept-*`, `refuse-*`

Envelopes of a message whose signature by A verified, received by B. They're checked against `PROTOCOL.md` §12.3 checks 3 and 6 with A as the signer, so they carry no `identity`.

**Expected result:** accepted or refused, as the name says.

Keys match exactly, and no key can appear twice, so every reader reads the same values:

- In `accept-case-variant-key`, the body is `"benign"`. `BODY` is an unknown key, and is ignored.
- `accept-line-separators-raw-in-a-string-body` and `accept-line-separators-raw-in-a-member` hold U+2028 and U+2029 unescaped, as JSON allows a writer to leave them, in a string body, in a body's member name and value, and in the subject. A reader accepts them as any other character.
- `refuse-duplicate-key` has two `from` members.
- `refuse-version-string` has `"v":"1"`, which isn't the number 1.
- `refuse-not-utf8` and `refuse-two-objects` aren't one UTF-8 JSON object.

Readers are strict about what a loose reader lets through:

- `refuse-msg-id-newline` ends its `msg_id` with a newline.
- `refuse-msg-id-grouped-wrongly` has 36 characters and four hyphens, but not in the places that RFC 9562 gives.
- `refuse-subject-number` has a number where a string goes.
- `refuse-sent-at-second-60` names a second that some date parsers carry into the next minute.

The envelope, including its body, is a JSON text as PROTOCOL.md §3.3 reads one. `accept-body-nested-to-32` is accepted. `refuse-body-nested-to-33`, `refuse-key-twice-in-the-body`, `refuse-nan-in-the-body`, `refuse-leading-zero-in-the-body`, `refuse-noncharacter-in-the-body`, `refuse-above-u10ffff-in-the-body`, and `refuse-byte-order-mark` are refused.

The window of `sent_at` has a vector on each side of each end:

- `accept-7-days-old`, and `refuse-7-days-and-1s-old`, which is refused as `message_expired`.
- `accept-1-hour-ahead`, and `refuse-1-hour-and-1s-ahead`, which is refused as `message_not_yet_valid`.

`v` is an integer member, written in digits only. The following are refused: `refuse-version-written-with-a-fraction` (`1.0`), `refuse-version-written-with-an-exponent` (`1e0`), `refuse-version-a-decimal-just-above-1`, and `refuse-nul-after-the-version` (a raw NUL, which jq drops).

`refuse-sent-at-ending-in-a-line-feed` and `refuse-sent-at-ending-in-a-nul` hold JSON escapes that a shell variable would drop.

To the Go client, every refusal is `invalid_envelope`, except for the following:

- `message_expired` and `message_not_yet_valid`.
- `sender_mismatch` for `refuse-from-not-signer` and `refuse-from-null`.
- `recipient_mismatch` for `refuse-to-another-mailbox`, `refuse-no-to`, and `refuse-to-null`, because a `to` that is left out names no recipient, as a null `from` names no sender.

An uppercase `from` or `to` (`refuse-from-uppercase`, `refuse-to-uppercase`) isn't an address, so it's `invalid_envelope`.

The stock script is held to every `accept-*` vector, and to the refusals that guard against a forgery or a replay (`envelopeFive` in `sh/sh_test.go`, and `message_expired` and `message_not_yet_valid` by name). The rest are for the Go client only (`AGENTS.md` § The shell client).

### `json/ok-*`, `json/bad-*`

JSON texts. Each `bad-*` text breaks one rule of PROTOCOL.md §3.3, and each `ok-*` text breaks none. The `bad-*` texts hold the following faults:

- A byte order mark.
- Bytes that aren't UTF-8, an overlong form, a surrogate, a code point greater than U+10FFFF, and a five-byte form.
- An escaped lone surrogate: high, low, and low in a name. jq reads a lone low surrogate as U+FFFD.
- A noncharacter: written, escaped, in plane 16, and in a name.
- A control character, a unit separator (jq 1.7.0 accepts it), and a NUL after a number (jq ends the number there and drops the NUL).
- A name twice: in a nested object, and through an escape.
- A second value, a bracket, or a brace after the object.
- An array, a string, and nothing.
- `NaN`, `nan`, `-Infinity`, `01`, `+1`, `.5`, and `1.`.
- A trailing comma, single quotes, and a vertical tab.
- Nesting to 33.

The `ok-*` texts hold what is allowed: whitespace, any JSON number, every escape, a surrogate pair, the code points beside the noncharacters, one name in two objects, and nesting to 32.

**Expected result:** every reader that the protocol defines accepts `ok-*` and refuses `bad-*`, whatever kind of text it expects. The stock script isn't held to these vectors (`AGENTS.md` § The shell client).

### `answer/<kind>-ok-*`, `answer/<kind>-bad-*`

Whole answers of a server, of each kind that a client reads as JSON: `mailbox` (the read of a mailbox), `list`, `record` (a revocation record), `receipt`, and `problem` (an error). A fetch answers with only a message's ciphertext, which isn't JSON, so fetches have no vectors here.

The `bad` vectors:

- A byte member that is a number, unpadded, wrapped, or has its unused bits set.
- A member twice, null after a value, `NaN` beside the record, a second value, nothing, and an array.
- An integer member that is negative, greater than 2^53 − 1, a fraction, written with a fraction, or written with an exponent (`list-bad-total-count-written-with-an-exponent`).
- A string or an object where another type goes, or a number where a string goes (`list-bad-id-a-number`).
- A list's cursor that isn't one or more of `A`–`Z`, `a`–`z`, `0`–`9`, `-`, and `_` (`list-bad-cursor-a-slash`, `list-bad-cursor-a-percent`, `list-bad-cursor-empty`). `list-ok-cursor` holds the whole alphabet.
- A listed ID that isn't a message ID, which is 64 lowercase hex digits (`list-bad-id-not-a-message-id`).
- A problem whose `code` comes before a member that breaks the problem. Such a problem carries no code.

The `ok` vectors: an empty object, null members beside a revocation (a null member is the same as an absent one), unknown members, and names in another case, which are unknown members. `record-ok-names-in-another-case` shows no revocation of certificates.

**Expected result:** as the Go client reads each kind, `<kind>-ok-*` is accepted, and `<kind>-bad-*` isn't an answer. The stock script accepts every `ok` vector, and refuses the `bad` vectors whose broken member it reads (`answerRead` in `sh/sh_test.go`). A `2xx` revocations answer that isn't a record stops a drain, with the messages still on the server.

### `pass/ok-*`, `pass/bad-*`

`a/pass.authorization` in other forms. Each is an `Authorization` value with no line feed after it.

- The `ok` vectors: the scheme and the names in other cases, another order, whitespace around commas and `=`, the address as a token, and empty list elements.
- The `bad` vectors, which aren't a pass: a tab after the scheme, a parameter that appears twice, is unknown, or is missing, a backslash in a quoted value, `not_after` as a token, a space in the signature, an address in capitals, and others.

`ok-as-a-sender-writes-it` is the one form that a client writes (PROTOCOL.md §8).

**Expected result:** each `ok-*` vector is read as the same three values as `a/pass.authorization`, so each one verifies for mailbox A at `./now` as that pass does. Each `bad-*` vector isn't a pass, and a server answers it with `401 invalid_pass`.

## Determinism

Ed25519 signatures are deterministic, so an implementation that signs these statements with these keys, under these certificates, must produce byte-identical `.sig` files. A certificate is made once and kept, because `ssh-keygen` puts a random nonce in each certificate. Each signature by C's P-256 identity and each `message/*.age` file are also made once and kept, because ECDSA and age encryption are randomized. Everything else regenerates byte for byte.

## Tools that check the vectors

**Tools that this project didn't write check the crypto and container vectors.** The `TestVectorsByToolsNotOurs` test in `protocol` runs them, and skips a tool that is missing and says so:

- `age` decrypts each `message/*.age` file with its prekey's published identity to exactly its `.plain`. The test uses the `age` on `PATH` if that `age` reads post-quantum files. Otherwise, it uses the `age` that `go.mod` names as a tool, which is the version that the client links.
- `head -n 1` and `sed -n '2,/^-----END SSH SIGNATURE-----$/p'` split each plaintext into its `.line` and `.sig`.
- `ssh-keygen -L` reads every certificate. At `now`, `ssh-keygen -Y verify` verifies every signature in `a/`, `b/`, `c/`, and `message/`, each `cert/ok-*` under its certificate, and each `revoked/ok-*`. It verifies a working key's signature under its identity as `cert-authority`, and an identity's own signature under its bare key. `ssh-keygen` has no concept of a revocation of certificates; a reader applies it.
- `openssl dgst -sha384 -binary`, and then `openssl base64 -A` with `+` and `/` changed to `-` and `_` and with `d_` before it, gives each address from its identity document.

So each of those vectors is in the standard's format, not in this code's reading of it.

**A second age implementation read the three ciphertexts once,** because the `age` in the preceding list is filippo.io/age, the library that this client encrypts with. That second implementation is typage (`age-encryption` 0.3.1 on npm, with `@noble/post-quantum` 0.5.4 on its own `@noble/curves` 2.0.1 and `@noble/hashes` 2.0.1, and `@noble/curves` 2.4.0, `@noble/hashes` 2.4.0, `@noble/ciphers` 2.4.0, and `@scure/base` 2.4.0, each tarball checked by npm against the registry's integrity hash). It decrypted `message/a-to-b.age` (sha256 `20a1b4e78ac0401cb1882662a92bc77ad356bea704cc39d10bef2e8f1ecd9385`), `message/a-to-c.age` (sha256 `8124de60eb6e5859b365c39e9055a306a587e88584ac5e49f1cddbdf34fa96c1`), and `message/b-to-c.age` (sha256 `0f3a33aa1a8c777c130f87fab76d518a29d3d7f92fc4005c63fde4c7c418e93c`) to exactly their `.plain`, and derived the prekey's recipient from its identity. With one byte flipped, it refused each file.

`generate.sh` keeps the three files, so that result holds while their hashes match. The `TestTheCiphertextsAreTheOnesTypageRead` test in `protocol` checks the hashes: if a file is made again, the test fails until a second implementation reads the new file and this page records its hash.

Every other vector is a conformance check of this project's own: an implementation passes it by giving the verdict that the vector names.

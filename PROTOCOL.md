# The Dromas Protocol, version 1

**Status: draft.** Nothing here is final until version 1 is declared stable. What must be settled first is the open issues of this repository and of dromas-server.

## 1. Introduction

Dromas carries end-to-end encrypted, sender-authenticated messages between programs, through a store-and-forward server.

- A *mailbox* is named by its *address*, the hash of its identity document. An address authenticates the key it names, so it needs no directory.
- A sender encrypts to the recipient's *prekey*, which the recipient's identity signed, and signs the message inside the encryption.
- The identity signs what its mailbox signs, or a *working key* the identity certified signs in its place. The identity can then be kept away from daily work (Section 5).
- The server stores ciphertext. It holds no private key, no plaintext and no signature over any message.

The server is not trusted for confidentiality or for authenticity: every check that protects a message is a client's. Section 15 states what a hostile server can and cannot do.

This document specifies the formats, the meaning of each HTTP operation, and what a client and a server check. Alongside it:

- **`api/openapi.yaml` is normative for the HTTP shapes:** routes, methods, request and answer members with their types, status codes, error codes and headers. This document says what each operation means, who may call it, what is checked and what each refusal means. Where the two disagree, `api/openapi.yaml` is right about a shape and this document about a meaning.
- **`testdata/vectors/` holds the conformance vectors** (Section 16).
- **`PRECEDENTS.md`** sets each design decision beside the published standards and deployed systems that make the same one.

## 2. Conventions

### 2.1. Requirements language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in BCP 14 [RFC 2119] [RFC 8174] when, and only when, they appear in all capitals, as shown here.

### 2.2. Notation

- **Grammar.** A text's form is given in ABNF (RFC 5234), in the section that defines the text, right after the prose that introduces it. A string in quotes matches in any ASCII case; one written `%s"…"` matches exactly (RFC 7405). The core rules (RFC 5234 Appendix B: `ALPHA`, `DIGIT`, `DQUOTE`, `LF`, `SP`) are used by name, and so are `full-date`, `time-hour` and `time-minute` (RFC 3339 §5.6), `OWS` and `BWS` (RFC 9110 §5.6.3), and `IPv6address` (RFC 3986 §3.2.2). Appendix B collects every rule.
- A *line* ends in LF (0x0A). A text "of N lines" is exactly N lines: no CR, and nothing after the last LF.
- `‖` is concatenation. `str(x)` is a uint32 big-endian length followed by the bytes of `x` (RFC 4251 §5).
- An **address** is written as `d_` and 64 characters of base64url (RFC 4648 §5) without padding, `address` (Section 4.2), wherever it appears: a path, a statement, an envelope, a namespace, a certificate's principal.
- A **time** is RFC 3339 in UTC to the second, with no leap second: a second of `60` is no time. What ends at a time has ended at that second: it is valid while a clock reads before it.

  ```abnf
  time     = full-date %s"T" time-hour ":" time-minute ":" time-sec %s"Z"
  time-sec = %x30-35 DIGIT   ; 00 to 59
  ```

- A **serial** is a decimal integer from 1 to 2^53 − 1, of which the grammar states only the digits. 2^53 − 1 is the largest integer every JSON reader holds exactly (Section 3.3). A certificate serial, a prekey serial and a revocation serial (Section 2.3) are each a serial.

  ```abnf
  serial = %x31-39 *15DIGIT   ; no leading zero
  ```

- **base64** is standard base64 (RFC 4648 §4) in its one encoding, so that encoding the bytes again gives the same text: padded, and the unused bits of its last character zero. Two places differ, and say so: the armour of a signature (Section 3.2) and an adopt key (Section 11.16).

  ```abnf
  base64            = *( 4base64-char ) [ base64-final ]
  base64-nonempty   = 1*( 4base64-char ) [ base64-final ] / base64-final
  base64-char       = ALPHA / DIGIT / "+" / "/"
  base64-final      = 2base64-char base64-char-by-4 "="
                    / base64-char base64-char-by-16 "=="
  base64-char-by-4  = %s"A" / %s"E" / %s"I" / %s"M" / %s"Q" / %s"U" / %s"Y"
                    / %s"c" / %s"g" / %s"k" / %s"o" / %s"s" / %s"w"
                    / "0" / "4" / "8"   ; its value a multiple of 4
  base64-char-by-16 = %s"A" / %s"Q" / %s"g" / %s"w"   ; its value a multiple of 16
  ```
- In a namespace or a statement, `<A>` is the address of the mailbox that signs it.
- In a route, `<A>` is the address of the mailbox the call is about: a mailbox calling about itself, or anyone's lookup or revocation call (Sections 11.8, 11.15). `<B>` is the address of a message's recipient: a sender sends to it (Section 11.10), and the recipient calls the other message routes as itself (Sections 11.11 to 11.14).

### 2.3. Terms

Where this document uses a term below as a defined term, it has the sense given here.

**Mailboxes and people**

- **Mailbox:** an address, its keys and its messages.
- **Owner:** whoever runs a mailbox.
- **Reader:** whoever reads a text the protocol carries (Section 3.3); of a message, its recipient's client (Section 12.3).
- **Server**, **own server:** the mailbox server a mailbox is registered at and calls: all that its operator runs to answer at the server's name (Section 11.1).
- **Active**, **revoked**, **unknown:** the states of a mailbox at a server (Section 11.6).
- **Forgotten:** of a mailbox, no longer kept by its server once nothing valid for it is left (Section 13); it is then unknown.
- **Identity holder:** whoever holds a mailbox's identity when the mailbox does not (Section 12.7).

**Keys**

- **Identity key**, **the identity:** a mailbox's long-term signing key (Section 4.1).
- **Identity document `D`:** the two-line text that carries the identity's public key (Section 4.1).
- **Address:** the SHA-384 of the identity document (Section 4.2).
- **Working key:** a key the identity certifies to sign in its place (Section 5.2).
- **Certificate:** the OpenSSH user certificate by which the identity certifies a working key (Section 5.2). Its **certificate serial** is its serial field.
- **The server's maximum:** the longest a certificate the server takes may run, its `max_certificate_lifetime` (Section 11.18).
- **Stolen:** of a key or a pass, held by someone the owner did not give it to (Sections 12.6, 15.5).

**Signers and statements**

- **Statement:** a fixed-form text that a key signs (Section 3.2): a prekey statement (Section 6), a pass (Section 8) or a revocation statement (Section 10).
- **Signer:** the signing key of a prekey statement, a pass or a message: the identity key or a certificate (Section 5). Its **rank** is the number Section 5 gives it.
- **Prekey:** a mailbox's age encryption key, which a prekey statement carries (Section 6).
- **Prekey statement:** the statement that carries a prekey (Section 6). Its **prekey serial** is its serial, `p`.
- **Later, earlier:** of two prekey statements of one mailbox, their place in `(c, p)` order (Section 5.3).
- **Current statement:** the prekey statement a server serves for a mailbox now (Section 11.8). Its key is the **current prekey**, its signer the **current signer**, and, where that signer is a certificate, that certificate is the **current certificate**.
- **Retired prekey:** a prekey once the server takes a later prekey statement (Section 11.9).
- **Prekey lifetime:** how long after a client makes a prekey statement it ends (Sections 6, 12.4).
- **Pass:** a statement a mailbox signs to prove itself to its server on a call (Section 8).

**Revocations**

- **Revocation statement:** the statement the identity signs to revoke the address (Section 10.1) or certificates (Section 10.2).
- **Revocation of the address:** the revocation statement that revokes an address (Section 10.1).
- **Revocation of certificates:** the revocation statement that revokes certificates through its **revocation serial**, the `through` value (Section 10.2).
- **Revocation record:** the JSON object that carries an identity's revocations (Section 10.3).
- **None held:** an answer that shows no revocation a client can verify (Section 12.3, check 7).

**Operations**

- **Registration**, **register:** `PUT /v1/<A>` (Section 11.7).
- **Lookup**, **look up:** `GET /v1/<A>` (Section 11.8). A mailbox **looks itself up** at its own address.
- **Served:** as a server answers it in a lookup (Section 11.8).
- **Renewal**, **renew:** publishing a new prekey statement: by `PUT /v1/<A>/prekey` (Section 11.9), or as a registration once the server has forgotten the mailbox (Section 12.4).
- **Identical retry:** a registration or a renewal whose prekey statement and signature, and for a registration its identity document, are byte for byte those the server holds as current (Sections 11.7, step 5; 11.9).
- **Send:** `POST /v1/<B>/messages` (Section 11.10). Its **receipt** is its `201` answer, or the `200` of its dedupe (Section 11.10, step 6).
- **List:** `GET /v1/<B>/messages`, one page of a mailbox's messages (Section 11.11).
- **Poll:** a list without a cursor, or with a cursor that is not a continuation (Section 11.11).
- **Continuation:** a list that meets the three conditions of Section 11.11.
- **Long poll:** a list of the first page whose answer the server delays (Section 11.11).
- **Fetch:** `GET /v1/<B>/messages/<id>` (Section 11.12). A **refetch** is a fetch of a message the mailbox has fetched before.
- **First-fetch budget:** what a mailbox's first fetches of messages spend (Section 11.12).
- **Intake budget:** how many messages a mailbox accepts from every sender together (Section 11.10, step 8).
- **Ack:** `DELETE /v1/<B>/messages/<id>` (Section 11.13).
- **Set the ACL**, **get the ACL:** `PUT` and `GET /v1/<B>/acl` (Section 11.14).
- **Publish a revocation**, **look up revocations:** `PUT` and `GET /v1/<A>/revocations` (Section 11.15).
- **Join**, **leave:** `PUT` and `DELETE /v1/<A>/account` (Section 11.16). An **adopt key** is the key a join carries.
- **The server's document:** what `GET /v1/` answers (Section 11.18).
- **Version floor:** the lowest version a server accepts, its `min_version` (Section 11.17).

**Messages**

- **Envelope:** the signed JSON line that carries a message's metadata and body (Section 9.1).
- **Plaintext:** what is encrypted: the envelope, its signature and the padding (Section 9.2).
- **Message id:** the SHA-256 of a message's ciphertext, the `id` its server lists it by (Section 11.10).
- **`msg_id`:** the UUID a sender gives a message (Section 9.1).
- **Native stanza:** an age recipient stanza of a type a prekey has, `mlkem768x25519` (Section 9.4).
- **Seal:** pad the envelope and its signature, and encrypt the result to a prekey (Sections 9.3, 9.4).
- **Outbox entry:** a message a client keeps to send, with its envelope (Section 12.2).
- **Post:** one attempt to send an outbox entry (Section 12.2). A **copy** is the message as one post delivers it.
- **Live:** of a message, stored at its server and not yet **gone**: acked or expired (Sections 11.12, 11.13).
- **TTL:** 7 days (Sections 12.2; 12.3, check 6; 13).

**Clients**

- **Pinned:** of a peer's identity, kept by a client and used over the served one (Section 12.2).
- **No answer:** what a client takes a call to have had when it failed, or when its answer breaks the form this document gives it (Sections 3.3, 11.1, 11.2, 12.3). It says nothing about what was asked.
- **Drain:** a client's run through its mailbox's messages (Section 12.3).
- **Held:** of an accepted message, kept apart because the mailbox has not allowed its sender (Section 12.3).
- **Replay set:** what a client keeps of each message it accepted (Section 12.3, check 8).
- **Replay floor:** the cutoff a mailbox's replay set has been pruned to (Section 12.3, check 8).
- **Deletion time:** the time by which a client deletes a retired prekey (Section 12.4).
- **Take:** of a renewal, the date of the pass of the upload the server took for it (Section 12.4).
- **The remedy:** the steps of Section 12.6, for a stolen working key.

**Time**

- **Skew allowance:** one hour, left for clocks that differ (Section 3.4).
- **Message margin:** the time a client leaves before a certificate ends for what it signed to be read.

## 3. Primitives

### 3.1. Algorithms

| Name | Role | Specification |
|---|---|---|
| age v1 | encryption to a prekey: `mlkem768x25519`, the hybrid ML-KEM-768 + X25519 recipient | C2SP age |
| Ed25519 | the identity key of type `ssh-ed25519`; the working key of an identity of either type of Section 4.1 | RFC 8032 |
| ECDSA P-256 | the identity key of type `ecdsa-sha2-nistp256` | FIPS 186-5, RFC 5656 |
| SSHSIG | every signature: the signer's signature over SHA-512 of the message, in a namespace | OpenSSH `PROTOCOL.sshsig` |
| SSH public key text | `<key type> <base64 of the wire key>` | RFC 4253 §6.6, RFC 8709, RFC 5656 |
| OpenSSH certificate | the certificate of a working key | draft-ietf-sshm-cert |
| SHA-384 | the address | FIPS 180-4 |
| SHA-256 | a message id | FIPS 180-4 |
| UUID | an envelope's `msg_id` | RFC 9562 |

A keyed hash in this protocol is HMAC or HKDF, inside age. Plain SHA-384 and SHA-256 are applied only to public inputs: the identity document and the ciphertext.

### 3.2. Signatures

Every signature is an SSHSIG (`PROTOCOL.sshsig`) in one form, the bytes `ssh-keygen -Y sign` writes. The signing key signs

    "SSHSIG" ‖ str(namespace) ‖ str("") ‖ str("sha512") ‖ str(SHA-512(message))

and the signature is the blob

    "SSHSIG" ‖ u32(1) ‖ str(signing key) ‖ str(namespace) ‖ str("") ‖ str("sha512") ‖ str(signature)

armoured as `ssh-signature`: the blob's base64 (Section 2.2) in lines of 70 characters, the last of 1 to 70, between a BEGIN line and an END line.

```abnf
ssh-signature    = %s"-----BEGIN SSH SIGNATURE-----" LF
                   signature-text
                   %s"-----END SSH SIGNATURE-----" LF
signature-text   = *( 2signature-line ) ( signature-even / signature-odd )
                   ; two lines of 70 hold 35 groups of 4
signature-line   = 70base64-char LF
signature-even   = signature-groups LF
signature-odd    = 68base64-char
                   ( 2base64-char LF 2base64-char [ signature-groups ]
                   / 2base64-char LF base64-char-by-4 "="
                   / base64-char base64-char-by-16 LF "==" ) LF
                   ; a group of 4 split across the last two lines
signature-groups = *16( 4base64-char ) ( 4base64-char / base64-final )
```

- **The signing key** is the signer (Section 5), in SSH wire form. For what the identity signs, it is the bare identity key: the bytes of the identity document's line 2. For what a working key signs, it is the certificate.
- **The signature** is of the signing key's type; for a certificate, that is the type of the key it certifies.
  - For `ssh-ed25519`: `str("ssh-ed25519") ‖ str(R ‖ S)`, 64 bytes (RFC 8709 §6).
  - For `ecdsa-sha2-nistp256`: `str("ecdsa-sha2-nistp256") ‖ str(mpint(r) ‖ mpint(s))` (RFC 5656 §3.1.2), each mpint as RFC 4251 §5 defines it, with no leading zero byte but the one a set high bit needs.
- **One form.** A verifier MUST parse the armour and the blob, and MUST write the signature again in this form from what it parsed. Where the two differ by any byte, the verifier MUST refuse the signature before it verifies it. A verifier thus refuses every other way of writing a signature, among them:
  - another line end or line length;
  - whitespace in the base64;
  - a byte before the BEGIN line or after the last LF;
  - a version other than 1, or a hash other than `sha512`;
  - a reserved field that is not empty;
  - a NUL in a string;
  - an integer with a leading zero byte it does not need.
- **ECDSA's two values of s.** Where (r, s) is a P-256 signature, so is (r, n − s), where n is the group's order (FIPS 186-5). Each is a signature in the one form. A verifier MUST take both, and a signer may write either. Anyone who has seen one can write the other (Sections 5.2, 8). An Ed25519 signature has no second form.
- **A bound on size, per identity key type.** Each identity key type has a bound: the longest signature, in the one form, that a mailbox of that type can make, with its identity or with a working key under a certificate as `ssh-keygen -s` makes one (Section 5.2: a 32-byte nonce, no extension, an empty reserved field). In the one form, only the namespace and, for ECDSA, the lengths of r and s change a signature's size.

  | Identity key type | Bound | What reaches it |
  |---|---|---|
  | `ssh-ed25519` | exactly 850 bytes | a prekey statement or a pass under a certificate |
  | `ecdsa-sha2-nistp256` | 947 bytes | the same, with the identity's r and s on the certificate each in 33 bytes, the most the one form gives them |

  - Everything a mailbox signs is held to its identity's bound. In a message, the bound is on the block from its BEGIN line through the LF after its END line (Section 9.2).
  - Every verifier, a server included, MUST refuse a signature longer than the signing mailbox's bound before it verifies it.
  - Every verifier MUST refuse a signature longer than the largest bound of any key type before it reads it.
  - A key type added later states its own bound and leaves the others unchanged.
- **An Ed25519 signature's S is below the group order L** (RFC 8032 §5.1.7), in an SSHSIG and on a certificate.
- **The Ed25519 equation** is RFC 8032 §5.1.7's without the cofactor, `[S]B = R + [k]A'`, compared as encodings: a neutral R is taken, and R with a point of small order added is refused.

### 3.3. JSON

A server and a client read every JSON text the protocol carries by these rules: a request body, an answer, an envelope (Section 9.1) with the body in it, a revocation record (Section 10.3), and what a client keeps of one. A text that breaks a rule is refused whole:

| Text | Refused as |
|---|---|
| a request | `400 bad_request` |
| an answer | no answer |
| an envelope | `bad_envelope` |

- **I-JSON** (RFC 7493): UTF-8; no member named twice in any object, after its escapes are read; no surrogate and no Unicode noncharacter in a name or a string, written or escaped.
- **One object,** and nothing after it but whitespace (space, tab, LF, CR). No byte-order mark.
- **Names match exactly,** case and all.
- **Numbers** are in JSON's grammar (RFC 8259 §6), and none is of greater magnitude than a binary64 holds. A nonzero number too small for binary64 is taken.
- **Depth:** at most 32, objects and arrays counted together, the outermost as one.
- **Types.** A member the protocol defines is of its type, and null is the same as the member left out, except the envelope's `body` (Section 9.1). An integer member is written in digits alone, from 0 to 2^53 − 1: `1.0` and `1e0` are not integers.
- **Bytes.** A byte member is base64 (Section 2.2).
- **Unknown members.** A server MUST refuse a member it does not know in what it is sent. Every other reader MUST ignore one: a client in an answer, a mailbox in an envelope. The rules above still hold inside it.

### 3.4. Clocks

Every check of a time is exact at the clock of whoever makes the check: the check adds nothing for clocks that differ. Instead, whoever makes a time-limited object leaves the **skew allowance**, one hour, for clocks that differ, in the way each object's section states:

- a certificate's start, at a verifier (Section 5.2);
- a prekey statement's ceiling, at a server (Section 6);
- a pass's lifetime (Section 8);
- a reader's limit on `sent_at` (Section 12.3, check 6);
- when a client renews a prekey whose prekey lifetime is a day or more (Section 12.4);
- how long a server keeps a message (Section 13).

A client that renews a prekey whose prekey lifetime is less than a day leaves five minutes instead (Section 12.4).

## 4. Identity and address

### 4.1. The identity document

The identity document `D` is two lines of ASCII: the tag, then one SSH public key as `<key type> <base64 of the wire key>`, with no comment:

```
dromas-v1
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI<43 characters>
```

The key MUST be of a type this section defines, in its one encoding, and MUST meet that type's key checks. Its one encoding is the wire key as OpenSSH writes it back once read: the key, parsed as the type line 2 names and written again, gives the same bytes. One key thus has one document, and one address.

| Key type | Document | What fixes its one encoding | Key checks | Working key |
|---|---|---|---|---|
| `ssh-ed25519` | 91 bytes | the key's length, 32 (RFC 8709 §4) | a canonical point, not of small order | `ssh-ed25519` |
| `ecdsa-sha2-nistp256` | 171 bytes | the curve's name, `nistp256`, and an uncompressed point | the point on the curve, not the point at infinity, each coordinate below the field's size | `ssh-ed25519` |

- **Every client MUST verify every key type this section defines.** A client MUST NOT make an identity of a type other than `ssh-ed25519` unless its owner asks for one.
- **A key type defined after this version is OPTIONAL** (Section 14): a client verifies it or not, and a server registers it or not (Section 11.7). A mailbox of an optional type can be written to, and heard from, only by mailboxes whose clients verify that type (Section 7). This version defines no optional type.
- **The working key column** is the type of every working key an identity of that type certifies (Section 5). A type defined later states its own, as it states its own signature bound (Section 3.2).
- **The form is the same for every key type, defined here or not:** `identity-document`, where `key-type` is a key type's name as RFC 4251 §6 writes one, and the base64 is of an SSH wire key that begins with that same name.

  ```abnf
  identity-document = %s"dromas-v1" LF key-type SP base64-nonempty LF
  key-type          = key-name [ "@" domain ]   ; at most 64 characters
  key-name          = 1*( %x21-2B / %x2D-3F / %x41-7E )   ; printable ASCII, no "," or "@"
  domain            = domain-label 1*( "." domain-label )
  domain-label      = ( ALPHA / DIGIT ) [ *61( ALPHA / DIGIT / "-" ) ( ALPHA / DIGIT ) ]
  ```
- **A reader MUST refuse a well-formed document of a type it does not verify as `unsupported_key_type`, never as malformed.** A document that breaks the form, or does not hash to its address, is evidence of tampering; one of a type the reader lacks is not.

### 4.2. The address

The address is the SHA-384 of `D`: the name of its form, `d`, then `_`, then those 48 bytes in base64url (RFC 4648 §5) without padding, 66 characters in all: `printf d_; openssl dgst -sha384 -binary < identity.doc | openssl base64 -A | tr '+/' '-_'`. 48 bytes take no padding and leave no unused bit, so each address has one text form:

```abnf
address      = address-form "_" 64base64url-char
address-form = %s"d"   ; the one form this version defines
```

- **SHA-384 is NIST's category 4 for collisions** (SHA-256 is category 2), and the address is the one part of a mailbox that never changes: whatever else is renewed, a second preimage of it names the mailbox for good.
- **The text before the first `_` names the address's form, and that `_` is the delimiter, whatever follows it:** the name of a form never contains `_`, and the 64 characters after it may begin with `-` or `_`. This version defines one form, `d`, and a reader MUST refuse an address of any other form as no address.
- **It begins with its form's name, so a command line never reads an address as an option.**
- **It is compared as an exact string, case and all:** two addresses that differ only in case are two addresses.
- It commits to the identity key alone: renewing the prekey, or changing its type, never changes it.
- It has no checksum: a mistyped address names no mailbox.
- Line 1 of `D` is the protocol's version. Another deployment uses its own tag, and so its own address space.

### 4.3. The link

An address is shared as the link `https://<server>/to/<address>`.

- A server MUST serve a page there for exactly an `address` (Section 4.2), and `404` for any other path. The page is fixed text, the address and the server's own link to it, never text a mailbox supplied, sent with `Referrer-Policy: no-referrer` and `X-Robots-Tag: noindex`. A query on the link is ignored.
- A client given a link MUST take the address from its path, MUST NOT request the page, and MUST refuse a link to any server but its own.

## 5. Signers

Every prekey statement, pass and message a mailbox signs carries an SSHSIG (Section 3.2). Its signing key is its **signer**, in one of two forms:

| Signer | Rank |
|---|---|
| The identity key, the key of the identity document's line 2, byte for byte | 0 |
| A certificate the identity made of a working key (Section 5.2) | its certificate serial, 1 to 2^53 − 1 |

- **The identity also signs certificates and revocations (Section 10).** A certificate's own signature, and a revocation's, are verified against the identity key directly: neither is a signer, and neither is ranked.
- **A verifier takes the signer from the signature.** It MUST refuse a signing key of neither form, a bare working key among them.
- **A verifier checks a certificate against the identity it holds for the address (Section 5.2)** before it verifies the signature under the certificate.

### 5.1. What a verifier takes

- **The identity's own signature,** on a prekey statement, a pass or a message, is taken whatever revocation of certificates the verifier holds for the address. A revocation of certificates does not cover it; only the revocation of the address ends it (Section 10.1).
- **A certificate above the revocation serial.** Where a verifier holds a revocation of certificates for the address (Section 10.2), it MUST refuse a signer that is a certificate whose certificate serial is at or below that revocation serial, the two compared as numbers. A verifier that holds none takes every certificate that meets Section 5.2.
- **Each party applies these rules by what it holds:**
  - a reader at check 7 (revocation) of Section 12.3, and never at check 5 (signature);
  - a sender when it looks up the recipient before every post (Section 12.2);
  - a server by what it keeps of the address (Sections 8, 11.7, 11.9).

### 5.2. Certificates

A certificate is an OpenSSH user certificate, and a verifier MUST check every row of this table:

| Field | Rule |
|---|---|
| Type | a user certificate |
| Certified key | the working key: of the type Section 4.1's table gives for the identity's type, held to that type's key checks, and never the identity key |
| Serial | the certificate serial, a serial (Section 2.2) |
| Key id | exactly `dromas-cert-v1` |
| Principals | exactly one: the mailbox's address |
| Validity | `valid after` ≤ now < `valid before`, at the verifier's clock, in whole seconds, and each at or before `9999-12-31T23:59:59Z` |
| Critical options | none. A verifier MUST refuse a certificate with any (draft-ietf-sshm-cert-01 §2.1.1) |
| Extensions | none. A verifier MUST refuse a certificate with any |
| Reserved | empty |
| Nonce | exactly 32 bytes |
| Signed by | the identity key, byte for byte as the identity document carries it, its signature in the one form of the key's type (Section 3.2) |

- **A certificate is checked at the verifier's clock, exactly, and only once the identity's signature on it has verified.** It holds from its `valid after` and has ended at its `valid before` (Section 2.2). A certificate with a date past `9999-12-31T23:59:59Z`, `ssh-keygen`'s `forever` among them, is refused.
- **A verifier sets no bound on how long a certificate runs.** A server bounds what it takes (Section 11.7, step 6); a reader takes any certificate within its window.
- **The certified key is never the identity key.** SSHSIG does not sign its signing-key field, so a signature can be carried under any certificate of the key that made it. The identity's own signature on a prekey statement, rank 0, wrapped in a certificate of the identity key above the revocation serial, would otherwise rank at that certificate's serial, above the later prekey statements under certificates (Section 5.3).
- **Every certificate MUST certify a new working key,** one that no certificate before it certified. Otherwise a working key's signature under a certificate of certificate serial s1 could be wrapped in a certificate of the same key at a higher certificate serial, s2, and taken past a revocation of certificates through s1. No verifier can check this: its maker keeps to it.
- **A maker dates a certificate's start the skew allowance before it makes it** (Section 3.4). It makes the certificate run as long as whoever holds the identity chooses, up to the server's maximum (Section 11.18). With `ssh-keygen`, a certificate of `$DAYS` days and the skew allowance, `-U` added for an identity in an agent:

      ssh-keygen -s identity -I dromas-cert-v1 -n "$A" -z "$SERIAL" -V "-1h:+${DAYS}d" -O clear work.pub

- **Certificate serials.**
  - The maker gives a certificate the certificate serial its own clock gives, in seconds since 1970-01-01T00:00:00Z. Where that is not higher than the highest certificate serial the maker knows of for the mailbox, it gives one above that highest. The highest it knows of is the highest of:
    - the current certificate's, as served;
    - those of what the mailbox registered, renewed or signed under;
    - the highest certificate serial the identity has revoked (Section 10.2).

    A certificate serial is thus always above the address's revocation serial.
  - A verifier MUST NOT read a certificate serial as a time, or bound it by its clock. Of two certificate serials, the higher ranks higher.
  - Two certificates of one identity can share a certificate serial. A verifier that meets both takes either, and keeps nothing of it beyond the certificate serial.
- **Nothing may depend on a P-256 identity's signature having one set of bytes.** Anyone who has seen (r, s) can write (r, n − s) (Section 3.2). This holds on a certificate the identity made, and on the prekey statements, passes and messages it signs. Section 8 applies it to passes.

### 5.3. Order

- **A mailbox's prekey statements are ordered by `(c, p)`,** where `c` is the rank of the prekey statement's signer and `p` its prekey serial (Section 6). Of two prekey statements:

  | Signers | Which is later |
  |---|---|
  | of different rank | the one whose signer has the higher rank, at any prekey serial |
  | the same signer | the one with the higher prekey serial |
  | different certificates of the same certificate serial | neither |

  Once a prekey statement under a certificate is taken, a prekey statement the identity signs is earlier than it, and is refused at a server and at a sender.
- **Who orders them:** a server orders a mailbox's prekey statements by `(c, p)` (Section 11.9), and so does a sender, by what it has seen (Sections 6, 12.2).
- **What a signer of a higher rank retires:**
  - at the server, every prekey statement by a signer of a lower rank (Section 11.9), and the pass of every certificate but the current certificate (Section 8); the identity's pass is still taken;
  - at a sender, every prekey statement by a signer of a lower rank;
  - at a reader, nothing: a message is taken by its sender's identity, or under any certificate of it that the reader takes (Section 5.1) and that holds when the message is read.

## 6. The prekey statement

A prekey statement, `prekey-statement`, is signed by the mailbox's signer (Section 5), in the namespace `dromas-prekey-v1:<A>`, `prekey-namespace`:

```abnf
prekey-statement = %s"dromas-prekey-v1" LF address LF serial LF
                   age-recipient LF time LF
prekey-namespace = %s"dromas-prekey-v1:" address
age-recipient    = %s"age" *bech32-hrp-char "1" 6*bech32-char
bech32-hrp-char  = %x21-40 / %x5B-7E   ; printable ASCII but A-Z
bech32-char      = "0" / %x32-39 / %x61 / %x63-68 / %x6A-6E / %x70-7A
                   ; BIP 173's alphabet: no 1, b, i or o
```

- **Serial:** the prekey serial, a serial (Section 2.2). Under one signer, each prekey statement's prekey serial is higher than the last one's.
- **Key:** an age recipient, whose Bech32 human-readable part, before its last `1`, names its type (BIP 173). It is written as age writes a recipient: its checksum valid, its data whole bytes, and of any length.
  - The one prekey type is `mlkem768x25519`, named `age1pq`.
  - A key whose X25519 half is of small order breaks the prekey statement.
  - A prekey type added later is an addition inside v1 (Section 14).
- **`not_after`:** a time.
  - A client signs a prekey statement to end one prekey lifetime after it makes it (Section 12.4).
  - A server takes a prekey statement that ends at most 8 days, the longest prekey lifetime (Section 12.4), and the skew allowance after its own clock reads.
- **A sender MUST NOT encrypt to a prekey past its `not_after`.** There is no grace period.
- **A sender MUST keep, for each mailbox, the latest `(c, p)` it has seen on the mailbox's prekey statements.** It MUST refuse an earlier prekey statement (Section 5.3).
- **A sender MUST check a prekey statement against every other rule before its key's type.** A prekey statement that meets every other rule, and whose key is a well-formed Bech32 string whose human-readable part begins `age` and is not `age1pq` (X25519's `age` among them), is refused as `unsupported_prekey_type`, never as one that does not verify. Nothing is sealed or posted to it.

## 7. Sender key types

A mailbox declares the optional identity key types its client verifies (Section 4.1), so that a sender of an optional type does not write to a mailbox that cannot read it. Every mailbox takes senders of every type Section 4.1 defines.

- **A mailbox SHOULD declare every optional key type its client verifies, in its registration (Section 11.7) and in each renewal (Section 11.9),** in `sender_key_types`, a list of key type names. A lookup carries the list (Section 11.8).
- **A mailbox MUST NOT list a type Section 4.1 defines.**
- **A sender whose identity key is of an optional type that the recipient did not list MUST NOT send.** A server refuses such a send, `403 sender_key_type` (Section 11.10, step 7).
- **The declaration is the current prekey's.** A renewal replaces it, with nothing if it carries none. An identical retry changes nothing (Sections 11.7, step 5; 11.9).
- **It is not signed.** What a recipient takes is decided by what its client verifies (Section 12.3), never by this list.
- **A server keeps the names of the optional types it knows and ignores the rest,** a type Section 4.1 defines among them.

## 8. The pass

A mailbox proves itself to its server, on every call it makes as itself, with a pass it signs for itself. The server issues nothing for it and stores nothing.

The pass, `pass-statement`, is signed by the mailbox's signer (Section 5), in the namespace `dromas-pass-v1:<A>`, `pass-namespace`:

```abnf
pass-statement = %s"dromas-pass-v1" LF address LF server-name LF time LF
pass-namespace = %s"dromas-pass-v1:" address
server-name    = server-host [ ":" server-port ]
server-host    = dns-name / "[" IPv6address "]"   ; in lowercase
dns-name       = dns-label *( "." dns-label )   ; at most 253 characters
dns-label      = dns-char [ *61( dns-char / "-" ) dns-char ]
dns-char       = %x61-7A / DIGIT
server-port    = %x31-39 *4DIGIT   ; no leading zero, at most 65535
```

- **The server name** is the host of the mailbox's URL for its server, written as an origin is (RFC 6454 §6.2): lowercase, with no user information, and with its port only when the port is not the scheme's default. Its host is a name of DNS labels (RFC 1123 §2.1), an IPv4 address among them, or an IPv6 address in brackets; what else RFC 3986's `host` allows (IPvFuture, a zone, percent-encoding, any other character) is no server name. `https://dromas.dev:443` and `https://dromas.dev` name `dromas.dev`; `http://127.0.0.1:8080` names `127.0.0.1:8080`.
- **A server MUST write the `Host` of a request the same way,** and drop a port of 443 or 80 alike, so that checking a pass never depends on the scheme a request came by.
- **A client writes the pass as the value of one `Authorization` line,** `pass-authorization`, its signature the base64 of the armoured signature. A request with two `Authorization` lines carries no pass.

  ```abnf
  pass-authorization = %s"dromas-pass" SP
                       %s"address=" DQUOTE address DQUOTE "," SP
                       %s"not_after=" DQUOTE time DQUOTE "," SP
                       %s"signature=" DQUOTE base64-nonempty DQUOTE
  ```

- **A server reads it as RFC 9110 §11 lets a recipient read credentials,** `pass-credentials`: the three parameters in any order, each once, as a list (RFC 9110 §5.6.1), and each value a token or a quoted-string (RFC 9110 §5.6.2, §5.6.4) that holds no backslash and is in its value's form. It verifies the signature over the pass built from those values, never over the header's text.

  ```abnf
  pass-credentials = pass-scheme 1*SP [ pass-gap ] pass-params [ pass-gap ]
  pass-scheme      = "dromas-pass"
  pass-params      = address-param pass-gap not-after-param pass-gap signature-param
                   / address-param pass-gap signature-param pass-gap not-after-param
                   / not-after-param pass-gap address-param pass-gap signature-param
                   / not-after-param pass-gap signature-param pass-gap address-param
                   / signature-param pass-gap address-param pass-gap not-after-param
                   / signature-param pass-gap not-after-param pass-gap address-param
  pass-gap         = 1*( OWS "," ) OWS   ; empty list elements skipped
  address-param    = "address" BWS "=" BWS ( address / DQUOTE address DQUOTE )
  not-after-param  = "not_after" BWS "=" BWS DQUOTE time DQUOTE
  signature-param  = "signature" BWS "=" BWS
                     ( base64-token / DQUOTE base64-nonempty DQUOTE )
  base64-token     = 1*( 4( ALPHA / DIGIT / "+" ) )   ; base64 that is a token
  ```

- **A server MUST refuse a value longer than the form a client writes with a signature of the largest bound** any identity key type has (Section 3.2), whitespace included, with `401 unauthorized`, before it hashes, parses or verifies it.
- **Lifetime.** A client signs a pass to end 10 minutes after it signs it. A server takes a pass that has not ended and ends at most the skew allowance after its own clock reads.
- **A client keeps its pass for every call until it is about to end.** A client MUST NOT use a kept pass with more than 10 minutes left: its clock has gone back since it signed it.
- **A server MAY remember a pass it has verified,** by its values with the signature in its one form (Section 3.2): one pass, however the header wrote it. A P-256 identity's pass is one pass with s or with n − s (Section 3.2). A server that remembers passes MUST compare a P-256 identity's pass with s taken as the lower of the two.
- **Which signer.** A server takes a pass:
  - by the mailbox's identity, always;
  - by a certificate only where it is exactly the current certificate, as the bytes the server keeps.

  The exception: a registration (Section 11.7) and a renewal (Section 11.9) take a pass by the certificate of the prekey statement they bring.

  A server takes a pass only for a name that is its own. It reads a pass by its signer's form (Section 5).
- **A client that is refused a pass signs another and tries once more.** It MUST NOT try more than once. It MUST NOT try at all after a `certificate_revoked` that it finds final when it looks itself up (Section 12.5). A pass refused twice for its time is the clock (Section 12.1).
- **A lookup (Section 11.8), the link page (Section 4.3), publishing a revocation and looking up revocations (Section 11.15) take no pass.** A server reads none on them, whatever `Authorization` they carry.

**Refusals.** Each is a `401`. `certificate_revoked` comes first; otherwise:

| Code | When |
|---|---|
| `certificate_revoked` | the call takes a pass by the current certificate and the identity has revoked it (Section 11.4, rule 1); or the call brings a prekey statement under a certificate the identity revoked |
| `pass_expired` | at the server's clock, the pass has ended or ends more than the skew allowance ahead, or its certificate has not begun or has ended |
| `certificate_not_current` | the pass is by any certificate other than the one the call takes a pass by, or the certificate the server keeps for the mailbox does not verify |
| `unauthorized` | there is no pass, or it is not this mailbox's, or it is not for this server, or it does not verify |

A `certificate_not_current` answer names, in `certificate_serial`, the certificate the call should have used:

| Call | `certificate_serial` |
|---|---|
| any call but a registration or a renewal | the current certificate's |
| a registration or a renewal, refused for its pass | that of the certificate under the prekey statement it brings |
| a renewal whose prekey statement is signed by a signer of lower rank, or by another certificate with the current certificate's serial (Section 11.9) | the one its prekey statement must be above |

The member is absent where the signer in question is the identity.

## 9. Messages

### 9.1. The envelope

The envelope is one line of compact JSON, its LF included, and is what the sender signs. The server never sees it.

| Member | Presence | Value |
|---|---|---|
| `v` | required | the integer `1` |
| `from` | required | the sender's address |
| `to` | required | the recipient's address |
| `sent_at` | required | a time: when this copy was posted (Section 12.2) |
| `msg_id` | required | a UUID as RFC 9562 §4 writes one, in lowercase, of any version: `msg-id`. A client makes a random one (version 4) once for each message and keeps it on every copy |
| `identity` | required | the sender's identity document, base64. `from` is its hash |
| `body` | required | the message's content: any JSON value, carried as that value. Its null is a value |
| `subject` | optional | a string |
| `in_reply_to` | optional | a string: the `msg_id` replied to |
| `supersedes` | optional | a string: a `msg_id` of the same sender that this message replaces. A stored message is never altered; the recipient decides |

```abnf
msg-id    = 8lowhexdig "-" 4lowhexdig "-" 4lowhexdig "-" 4lowhexdig "-"
            12lowhexdig   ; the UUID of [RFC 9562], Section 4, in lowercase
lowhexdig = DIGIT / %x61-66   ; HEXDIG in lowercase
```

- **An empty string in `subject`, `in_reply_to` or `supersedes` is the member left out,** and a writer leaves such a member out.
- **A sender MUST refuse an `in_reply_to` or `supersedes` that is not a `msg_id`.** A reader checks only that each is a string.
- **The line is compact UTF-8 JSON** with no line feed of its own. A sender writes it without escaping `<`, `>`, `&` or non-ASCII characters.
- **A sender MUST hold the envelope to the rules its reader holds it to:** Section 3.3, and Section 12.3, check 3 (JSON). The body is nested at most 31 deep, so the envelope at most 32.
- **A sender MUST refuse whole an envelope that breaks a rule,** never repairing it, before the outbox keeps it. An envelope that meets the rules is written compact, its members in the order given, and its strings and numbers the values given.
- **A reader takes any JSON text on the line that Section 3.3 takes as the envelope,** whatever spelling the sender's encoder chose. It MUST read the members from the line exactly as it decrypted it.
- **`v` changes only for a breaking change,** and a reader MUST refuse a `v` it does not know.
- **A reader ignores a member this version does not define (Section 3.3), so two clients may agree on members of their own.** A member this protocol defines has a plain name; one it does not define SHOULD have a collision-resistant name, one containing a domain its writer controls (`example.com/compress`), as RFC 7519 §4.2 has a public claim name, and never an `x-` prefix (RFC 6648). Such a member adds to a message and never changes what a defined member means, so content two clients transform between themselves goes inside `body`.

### 9.2. The plaintext: envelope, signature and padding

The sender signs the envelope with its signer (Section 5), in the namespace `dromas-msg-v1:<recipient>`: with its identity,

    ssh-keygen -q -Y sign -n "dromas-msg-v1:$B" -f identity < P > P.sig

or with its working key under its certificate, `-f work-cert.pub`.

The plaintext is, in order:

```
{"v":1,"from":"…","to":"…","sent_at":"…","msg_id":"…","identity":"…","body":"…"}
-----BEGIN SSH SIGNATURE-----
U1NIU0lH…
-----END SSH SIGNATURE-----
................
```

1. **Line 1 is the envelope,** and its bytes, LF included, are what is signed.
2. **Then the signature,** as `ssh-keygen` writes it, through its END line.
3. **Then padding,** to the end (Section 9.3).

A sender writes it as `plaintext`, and signs in `msg-namespace`:

```abnf
plaintext     = envelope-line ssh-signature padding
envelope-line = 1*( %x00-09 / %x0B-FF ) LF   ; the envelope (Section 9.1)
padding       = *"."   ; Section 9.3
msg-namespace = %s"dromas-msg-v1:" address
```

A reader MUST take the parts apart by these rules:

1. **The signature is verified over line 1 exactly as decrypted,** and the envelope's members are read from those bytes, never from anything rebuilt.
2. **Each part has one place, and nothing is searched for.** Line 2 MUST be the signature's BEGIN line, ending in LF. The block ends at the first END line, which MUST be followed by LF: any other byte there, or none, refuses the message, and no later END line is looked for. What follows is never parsed or shown.
3. **One signature, one identity:** it is verified against the identity the envelope carries, and the verdict is the verification's result alone.

The namespace binds the signature to its recipient: a message signed for one mailbox does not verify in another's namespace.

### 9.3. Padding

Every message is padded so that its ciphertext is a whole number of blocks.

- **A block** is the smallest power of two that is at least 1/16 of the ciphertext's size, and at least 1 KiB.
- **The padding is `.` bytes after the signature's END line, to the end,** as many as make the ciphertext the next whole number of blocks, none for one already on a block. It is not signed.
- **For example,** a ciphertext of 40,000 bytes has a block of 4 KiB and is padded to 40,960; one of 70,000 has a block of 8 KiB and is padded to 73,728.
- **The ciphertext is what is measured.** An age file (C2SP age) of one native stanza and no other, of a plaintext of L bytes, is

      H + 16 + L + 16 × max(1, ⌈L / 65,536⌉)

  bytes: the header, H, 1,627 bytes for its `mlkem768x25519` stanza; the payload's nonce; the plaintext; and a tag for each 64 KiB chunk. A sender computes the padding from that size before encrypting. It MUST refuse to send a file that is not the size it computed.
- **A sender pads a message when it seals it,** for the prekey it seals it to: a message sealed again is padded again.
- **A server does not check padding.** A size limit a server sets MUST be a whole number of its own blocks, as every power of two is, so that a message fits a limit padded exactly when it fits it unpadded.

### 9.4. Encryption

The plaintext is encrypted to the recipient's prekey with age (C2SP age), in the binary format, not armoured: `{ cat P P.sig; padding; } | age -r <recipient> > m.age`. The file has one `mlkem768x25519` stanza. A sender adds no other native stanza.

## 10. Revocation

### 10.1. Revocation of the address

A mailbox's identity can revoke its address for good, and nothing else can.

- **The revocation statement is fixed text,** `revocation-of-address`. It has no date, no reason and no number: a verifier builds it from the address, and a copy signed at any time is the same revocation statement.
- **It is signed by the identity key,** in the namespace `dromas-revoke-v1:<A>`, `revoke-namespace`, with the bare identity key as the signing key. A working key's signature over the same revocation statement, under a certificate of that identity, is not a revocation: a verifier's `allowed_signers` line names the identity key with no `cert-authority`.

  ```abnf
  revocation-of-address = %s"dromas-revoke-v1" LF address LF
  revoke-namespace      = %s"dromas-revoke-v1:" address
  ```

- **It reaches back, and nothing undoes it.** Once a verifier has verified a revocation of the address:
  - it takes nothing signed for that address, whenever it was signed;
  - it sends the address nothing;
  - it keeps the revocation for good. No later answer and no later revocation record undoes it.

### 10.2. Revocation of certificates

The identity revokes every certificate it made through a revocation serial, that serial included, and keeps its address.

- **The revocation statement,** `revocation-of-certificates`, carries the address and the revocation serial.
- **It is signed by the identity key,** in the namespace `dromas-revoke-certs-v1:<A>`, `revoke-certs-namespace`, as `ssh-keygen -q -Y sign -n "dromas-revoke-certs-v1:$A" -f identity < statement > statement.sig`. A working key's signature is refused.

  ```abnf
  revocation-of-certificates = %s"dromas-revoke-certs-v1" LF address LF serial LF
  revoke-certs-namespace     = %s"dromas-revoke-certs-v1:" address
  ```

- **It revokes every certificate of the address whose certificate serial is at or below its revocation serial.**
  - It covers certificates only (Section 5.1).
  - A verifier keeps the one with the highest revocation serial it has verified, and that one stands. One through a lower revocation serial changes nothing, whenever it arrives.
  - There is no way to revoke fewer certificates.
- **What a revoked certificate loses, at a verifier that holds the revocation:**
  - a reader refuses a message signed under it, whenever it was signed (`certificate_revoked`; Section 12.3, check 7);
  - a sender refuses a prekey statement under it (Section 12.2);
  - a server takes no pass and no prekey statement under it (Sections 8, 11.7, 11.9), and refuses a send to a mailbox whose current statement is under it (Section 11.10, step 7).

  The address goes on.
- **A mailbox MUST NOT sign a message under a certificate it holds revoked.**
- **A mailbox MUST NOT make a certificate its identity has revoked:** its next certificate serial is above the highest revocation serial it holds or its server shows it (Section 5.2). A revocation of certificates through 2^53 − 1 leaves no certificate to make.
- **How long a revocation of certificates is kept is not fixed here.** Every certificate it covers that a server took ends at most the server's maximum (Section 11.18) after the certificate's start.

### 10.3. The revocation record

A revocation record carries what an identity revoked, and proves itself: anyone who holds the address can check it with nothing else.

```json
{"type":"revocation_record","identity":"<base64 of D>","revocation_signature":"<base64>","revoked_certificates":"<base64>","revoked_certificates_signature":"<base64>"}
```

- **`type`** is `"revocation_record"`. Every writer writes it: a server in its answers (`api/openapi.yaml` requires it there), and a client in what it sends, keeps and prints. A reader takes a revocation record without it. A reader MUST refuse one that names another type (a server with `400 bad_request`, `member` `type`).
- **`identity`** is the identity document, which MUST hash to the address and meet its key checks (Section 4.1).
- **`revocation_signature`**, if present, is the revocation of the address (Section 10.1), its armoured signature in base64.
- **`revoked_certificates`** with **`revoked_certificates_signature`**, if present, is the revocation of certificates (Section 10.2): its revocation statement's three lines, and the armoured signature over them, each in base64. The revocation statement MUST be exactly that form, its line 2 the address.
- **The members are in any order.** A revocation record carries at least one of the two revocations.
- **A revocation record is worth the same from a server or from a file.** A verifier rebuilds the revocation statement of the address from the address, holds the revocation of certificates to its form, and verifies each signature under the identity document's key.

## 11. The HTTP API

`api/openapi.yaml` gives the shape of every operation below. This section says what each one means and what a server checks.

### 11.1. Transport

- **A client MUST call its server over `https`,** except a server on its own machine named by an address in `127.0.0.0/8` or by `[::1]`, which it MAY call over `http`. The name `localhost` is not such an address.
- **A client MUST refuse a URL of any other form** before it sends anything.
- **A client MUST send a call to a loopback address, or to `localhost`, through no proxy.**
- **A server SHOULD terminate its own TLS,** on hosts its operator runs, with no third party between a client and it. Whatever terminates TLS for a server is part of the server (Section 15.8).
- **A client MUST NOT follow a redirect.** The API never redirects, so a client takes a `3xx` other than `304` as no answer. A pass and a lookup each name an address, and neither is sent anywhere a server points.
- **Every API path is under `/v1/`.**

### 11.2. Requests and answers

- **Bodies.** A JSON body is read by the rules of Section 3.3. A send's body is bytes (Section 11.10).
- **Query parameters** are read as members are. A server MUST refuse a parameter its call does not take, or one given twice, with `400 bad_request`, naming it in `member`. Only these calls take a query parameter:

  | Call | Parameters |
  |---|---|
  | a send (Section 11.10) | `statement` |
  | a list (Section 11.11) | `cursor`, `wait` |

- **`Date`.** Every answer under `/v1/` carries `Date`, set by the clock the server's checks read.
- **No caching.** Every answer under `/v1/` carries `Cache-Control: no-store`.
- **Compression.** A lookup (Section 11.8) MAY be answered with gzip (RFC 9110 §8.4.1.3), and only when the request's `Accept-Encoding` accepts gzip. A server MUST NOT compress any other answer: a list sets values senders chose beside the mailbox's own (BREACH, RFC 7457 §2.6).
- **`HEAD`** is answered on every route that answers `GET` (RFC 9110 §9.1, §9.3.2).
  - A `HEAD` changes nothing: it spends no cursor and hands out none, marks nothing fetched, and spends none of the first-fetch budget.
  - A list's `HEAD` is counted as a poll (Section 11.11), and a fetch's as one of the mailbox's calls.
- **Preconditions** are RFC 9110 §13: a server evaluates `If-Match` and `If-None-Match` on every call (§13.1.1, §13.1.2), at the point §13.2.1 gives, in the order §13.2.2 gives. Where Dromas narrows them:
  - **A call's normal request checks** (§13.2.1) are the match of its route and method, its pass where it takes one, and the read of its target. A registration's and a renewal's pass is by the signer of the prekey statement they bring, so that statement is checked among them.
  - **Which targets carry an `ETag`:** only a list's first page (Section 11.11) and an ACL (Section 11.14).
  - **No target has a modification date.** No answer carries `Last-Modified`, so a condition on a date is ignored (§13.1.3, §13.1.4).
  - **A call refused by a false precondition does nothing but count.** A list counts as a poll, and a fetch or an ack as one of the mailbox's calls.
  - **A write whose conditions held when they were evaluated, and no longer hold when it writes,** is answered `409 mailbox_changed`, and nothing changes. Two operations answer that case their own way: a registration (Section 11.7, step 8) and an ack (Section 11.13).
- **An unknown route** is answered with the router's plain `404` or `405`, with no code. Past a limit, it is answered `429 rate_limited`, as any call is. A client reads a plain `404` or `405` as a route that server does not offer, and a `404` with a code as that code.
- **A body that does not arrive in time** → `408 request_timeout`.
- **A server that cannot complete a request,** or is not ready to answer, answers `503 unavailable` with `Retry-After` rather than answer wrong. A client takes any other `5xx` as the same failure, with no wait given.

### 11.3. Errors

- **Every error body is RFC 9457 Problem Details** (`application/problem+json`), in AEP-193's form: `type`, `title`, `status`, `detail`, `code`, `hint`, and a member of its own for each value the error carries. Every value `detail` names is also one of those members.
- **`type`** is `tag:dromas.dev,2026-10-26:errors/<code>` (RFC 4151), the same at every server. It resolves to nothing.
- **Every `code` is on the closed list `api/openapi.yaml` gives, under the status it gives, with its own `type`.** A client ignores a code it does not know, a code under another status, and a code with another `type`: it takes no branch on it, shows none of it, and shows only the status. A member that breaks its type makes the body no answer (Section 3.3).
- **A client MUST NOT show `detail`, `hint`, `title`, or any other text a server sent** (a status line, a header or trailer line, a `Location`, a list's cursor in a path). These are a server's words, for a person reading the body.
  - A client says what a code means in its own words, with the members it has checked against their types.
  - A client shows a string member only once it has checked that the member is a value it knows.
  - A client names a call whose answer it cannot read by the call's method and path, never its query.
  - A `hint` names a fix as a client command, never a shell line.
- **A client reads `code`, the members, `Retry-After` and `Date` on every answer that is not `2xx`.**
- **`401`** carries `WWW-Authenticate` with the scheme `dromas-pass` (Section 8).
- **`Retry-After`,** on a `429`, on a `503` and on every list answer (Section 11.11), is delay-seconds: digits alone. A client reads an HTTP-date there as no wait.
- **A code names what a client does about the error.** Where one call can fail for causes a client meets differently, each cause has its own code.

What each code means, and what a client does on it:

| Status | Code | Meaning |
|---|---|---|
| 400 | `bad_request` | not a JSON text Section 3.3 takes; a member or query parameter missing, unknown, repeated, or with a value its call does not take, a byte member not base64 in its one encoding among them, which `member` names; a send's body that is not an age file (Section 11.10, step 4) |
| 400 | `bad_address` | a path address that is not an `address` (Section 4.2) |
| 400 | `identity_mismatch` | an identity document that does not hash to the address, or breaks its form (Section 4.1) |
| 400 | `unsupported_key_type` | an identity key type this protocol does not define or this server does not register; `key_types` lists those it registers |
| 400 | `bad_key` | an identity key that fails its type's key checks; a malformed or badly signed prekey statement or certificate |
| 400 | `prekey_not_new` | a prekey equal to the current one or a retired one the server keeps (Section 11.9) |
| 400 | `prekey_time` | at the server's clock, a certificate not yet begun or ended, or a prekey statement ended or ending more than 8 days and an hour ahead: nearly always the client's clock |
| 400 | `certificate_too_long` | a certificate that runs longer than the server's maximum (Section 11.7, step 6); `max_certificate_lifetime` gives the maximum |
| 400 | `wrong_recipient_type` | a send's age file without exactly one native stanza of the current prekey's type (Section 11.10, step 7) |
| 400 | `bad_revocation` | a revocation whose signature is not the identity's over its revocation statement (Section 11.15) |
| 401 | `unauthorized`, `pass_expired`, `certificate_not_current`, `certificate_revoked` | the pass is refused; Section 8's table gives when each is answered. On `certificate_not_current`, `certificate_serial` names the certificate concerned (Section 8) |
| 403 | `unknown_sender` | the mailbox a send's pass names is not registered |
| 403 | `sender_revoked` | the sending mailbox is revoked |
| 403 | `sender_not_allowed` | the recipient's ACL does not admit the sender (Section 11.14): final for the message |
| 403 | `sender_key_type` | the recipient's client does not verify the sender's identity key type (Section 7); `key_type` names it |
| 403 | `adopt_key_refused` | an adopt key this server did not make, or that is spent, expired or revoked (Section 11.16) |
| 404 | `no_such_mailbox` | the mailbox was never registered, or is forgotten |
| 404 | `no_such_message` | the message is gone: acked or expired |
| 404 | `not_revoked` | the server keeps no revocation of the address (Section 11.15) |
| 408 | `request_timeout` | the body did not arrive in time: send it again |
| 409 | `conflict` | what the server keeps is newer, or different: a registration that is not an identical retry; a renewal by the current signer at a prekey serial not higher (`prekey_serial` names the one to exceed); a revocation of certificates through a lower revocation serial than the one kept (`through` names it) |
| 409 | `mailbox_changed` | the mailbox changed during the request, or a precondition it carried no longer held at its write: send it again unchanged |
| 409 | `stale_prekey` | a newer prekey exists: look up the mailbox again and seal the same envelope |
| 409 | `prekey_expired` | the recipient's current prekey has ended, or its current signer is revoked: its owner must renew (Section 11.10, step 7) |
| 410 | `revoked` | the address is revoked, for good; `record` carries the revocation record (Section 10.3), which a client checks (Section 12.5) |
| 410 | `version_retired` | the server no longer accepts the request's version (Section 11.17); `min_version`, if the server sets a version floor, is the lowest it accepts |
| 412 | `precondition_failed` | a precondition was false (Section 11.2) |
| 413 | `too_large` | a body or ciphertext over its cap, or an ACL with more entries than the server accepts; `limit` gives the cap |
| 429 | `rate_limited` | past a limit (Section 11.5); `subject`, `quota_id`, and for a number a day `limit` |
| 429 | `fetch_paced` | the mailbox's fetches are paced, of new messages or of messages fetched before (Section 11.12) |
| 429 | `mailbox_full` | the mailbox stores as many messages as it may; `count` is how many, as its list counts them |
| 429 | `mailbox_busy` | the mailbox has spent its intake budget for now |
| 503 | `unavailable` | the server is not ready to answer |

Every `429` carries `Retry-After`.

### 11.4. The order of refusals

A server chooses which refusal it answers first, except where the two rules below fix it. Outside those rules, where a request fails several checks, a server answers the one it finds first, and a client acts on whichever comes. The steps of Sections 11.7 and 11.10 are numbered to be cited, not to order them.

0. **A retired version comes before everything** (Section 11.17). The server answers such a request `410 version_retired` and reads nothing else of it, its pass included. It counts against the IP address's `requests` and nothing else.
1. **A mailbox's state comes before any pass.** Before any pass is judged, a call answers the state of the mailbox whose pass it carries, and a lookup answers the state of the mailbox it looks up. So a mailbox learns its own state whatever pass it carries.

   | Call | Answers before any pass |
   |---|---|
   | A call with a mailbox's own pass | `404 no_such_mailbox` for an unknown mailbox; `410 revoked`; `401 certificate_revoked` where the call takes a pass by the current certificate and the identity has revoked it |
   | A send, about its sender | `403 unknown_sender`, `403 sender_revoked`, `401 certificate_revoked` |
   | A lookup | `404 no_such_mailbox`, or `410 revoked` with its revocation record |
   | Looking up revocations | `404 not_revoked`, or `200` with the revocation record |

### 11.5. Limits

A server chooses what it limits and by how much: no limit's value is part of the protocol, and a server may add limits. A client meets a limit as a `429` (RFC 6585 §4) with `Retry-After`. Unless the code says otherwise, the client waits that long and then retries. Every server keeps these promises:

- **A drain of a full mailbox always completes.** Its list pages are never refused. Its fetches may be paced, each `429 fetch_paced` saying when to go on, but are never stopped for good.
- **A mailbox's counts are spent only** by calls that carried its pass, by messages sent to it, and by each revocation of certificates its identity publishes through a higher revocation serial. An IP address's counts are spent only by calls from it.
- **The identity's acts are counted apart from what a working key can spend:**
  - a renewal by a signer of higher rank than the current signer is counted apart from one by the current signer (Section 11.9);
  - a renewal made while the current signer is revoked is not counted at all (Section 11.9);
  - a revocation of certificates through a higher revocation serial is counted on a quota of its own;
  - a revocation of the address is counted against no mailbox (Section 11.15).

  So whoever holds a stolen working key cannot keep the identity from revoking that key's certificate.
- **A request that would change nothing is refused by no count of what it would make new:** a registration of a mailbox the server keeps (Section 11.7, step 5), an identical retry of a renewal (Section 11.9), a revocation the server already keeps (Section 11.15), and a send of a ciphertext its recipient stores (Section 11.10, step 6). Only the budgets that bound any call may defer one with a `429`: the IP address's `requests` and `misses`, and the mailbox's `calls`.

**Who a call is counted against.** An **IP address** here is the IPv4 address or the IPv6 /64 a call comes from. No caller is exempt.

| Call | Counted against |
|---|---|
| A call with a mailbox's pass | that mailbox |
| A call with no pass | its IP address; a server MAY also count by a wider range |
| A lookup, whatever it carries | its IP address |
| A check that fails, or a call that names no mailbox | its IP address |

**How a refusal names its limit.**

- A limit with no code of its own is answered `429 rate_limited`. (The limits with codes of their own are `fetch_paced`, `mailbox_busy` and `mailbox_full`.) `subject` says whom the call was counted against (`mailbox` or `ip`), and `quota_id` says which limit.
- Where one call would spend several limits, `Retry-After` is the time when every limit the server can check without doing the call's work lets the same call through (RFC 8555 §6.6), and the name given is that limit's.
- A limit the server can check only after that work, after it reads storage or checks a signature, can still refuse the call once that wait is over.
- A send's `sends` is checked only after its dedupe, so a send past its `calls` is told the wait for `calls`.
- **A number a day** is a budget of that many for each mailbox, refilled one at a time, evenly over the day. `limit` gives the number.

| `quota_id` | `subject` | Counts |
|---|---|---|
| `polls` | `mailbox` | its polls (Section 11.11), `304` and `412` answers included |
| `pages` | `mailbox` | its continuations, sized so that a full mailbox always drains |
| `calls` | `mailbox` | its other calls with its pass, a send, a renewal and an identical retry of its registration included |
| `sends` | `mailbox` | its sends, a number a day, counted only for sends that carried its pass |
| `renewals` | `mailbox` | its renewals by its current signer, a number a day |
| `certificates` | `mailbox` | its renewals by a signer of higher rank than its current signer, a number a day, except one made while its current signer is revoked |
| `certificate_revocations` | `mailbox` | each revocation of certificates its identity publishes through a higher revocation serial, a number a day |
| `registrations` | `ip` | registrations from an IPv4 address or IPv6 /64, and what a server counts as one (Section 11.15) |
| `registrations_range` | `ip` | the same, from an IPv6 /48 |
| `misses` | `ip` | calls that named no mailbox, or carried nothing that verified |
| `sends_before_pass` | `ip` | sends, counted before their pass is read |
| `requests` | `ip` | every request except one that carries a pass the server has already verified, on a call that takes that pass; registrations, renewals and requests no route matches are counted |

A server's limit that is not one of these names none in `quota_id`.

### 11.6. Mailbox states

At a server, a mailbox is in one of three states:

- **unknown:** the server keeps no mailbox at the address. It was never registered, or the server has forgotten it (below).
- **active:** registered and in use.
- **revoked:** its identity has revoked the address (Section 10.1), for good.

What each call gets in each state:

| Call | unknown | active | revoked |
|---|---|---|---|
| Register, `PUT /v1/<A>` | `201`; the mailbox is now active | an identical retry → `200`; any other → `409 conflict` | `410 revoked` |
| Renew, `PUT /v1/<A>/prekey` | `404 no_such_mailbox` | `200` | `410 revoked` |
| Any other call with the mailbox's own pass (list, fetch, ack, the ACL, joining or leaving an account: Sections 11.11 to 11.14, 11.16) | `404 no_such_mailbox` | answered as its section says | `410 revoked` |
| Publish a revocation of the address, `PUT /v1/<A>/revocations` | the server keeps it, and the address is now revoked: `201` | the address is now revoked, and its messages are deleted: `201` | `410 revoked`, with the revocation record |
| Publish a revocation of certificates, the same route | the server keeps it, and the address stays unknown: `200` | `200` | `410 revoked`, with the revocation record |

- **A send** answers its sender's state as `403 unknown_sender` or `403 sender_revoked` (Section 11.10, step 1), and its recipient's state as the table does.
- **There is no `DELETE` of a mailbox.** A mailbox leaves a server in one of two ways: its identity revokes the address, for good, or it stops renewing and is forgotten. To stop taking messages for a while, it sets its ACL (Section 11.14).
- **Forgotten.** A mailbox that has stopped renewing is forgotten once the date until which Section 13 keeps it has passed. It is then unknown. It comes back by registering anew, without its ACL or its account. The server still keeps what its identity revoked.
- **There is no pending state.** A registration carries a pass minutes old, so a mailbox is active from its first call.
- **A request that took effect, sent again byte for byte,** gets `200` and the same result. A different request that conflicts with it gets `409 conflict`. A send of a ciphertext its recipient already stores is answered as Section 11.10, step 6, says.

### 11.7. Register

`PUT /v1/<A>` carries `identity`, `prekey` and `prekey_signature` (the exact bytes of each, in base64), and `sender_key_types` if the mailbox declares any. It carries a pass by the signer of that prekey statement. The mailbox is in use at once.

The server checks:

1. The address in the path is an `address` (Section 4.2) → otherwise `bad_address`.
2. The body has exactly those members → otherwise `bad_request`, naming the member.
3. `identity` hashes to the address and has the form of an identity document (Section 4.1) → otherwise `identity_mismatch`.
4. **The key's type and the key:**

   | Check | Otherwise |
   |---|---|
   | the type is one this protocol defines | `unsupported_key_type` |
   | the key is in its one encoding | `identity_mismatch` |
   | the key meets its type's key checks | `bad_key` |

   A server registers `ssh-ed25519` and whichever other defined types its operator chooses, and answers the rest `unsupported_key_type`, naming its list.
5. **A registration never changes a registered mailbox.** A registration of a mailbox the server keeps is answered from what the server holds of the mailbox, whatever step 6 would answer:

   | The server holds | The request | Answer |
   |---|---|---|
   | an active mailbox | an identical retry | `200 {}` once its pass (step 7) is verified, or `401 certificate_revoked` if the identity has revoked the certificate of that prekey statement |
   | an active mailbox | anything else | `409 conflict` |
   | a revoked address, with a mailbox or none | any | `410 revoked`, with its revocation record |

   Any other registration is counted against its IP address (Section 11.5, `registrations`).
6. **The prekey statement and its signer.** Each of these holds, otherwise `bad_key`, or `prekey_time` where what fails is the certificate's time or the prekey statement's `not_after`:
   - the prekey statement has the form of Section 6;
   - line 2 is the address;
   - line 4 is a valid age recipient of the prekey type, its X25519 half not of small order;
   - the signer is that identity, or a certificate of it that meets every rule of Section 5.2;
   - the namespace is `dromas-prekey-v1:<A>`;
   - the prekey statement's `not_after` is after now and at most 8 days and an hour after now.

   Further:
   - a certificate that runs longer than the server's maximum (Section 11.18), measured as `valid before` less `valid after`, unless it is the current certificate → `400 certificate_too_long`;
   - a certificate the identity revoked, by what the server keeps of the address (Section 5.1) → `401 certificate_revoked`.
7. **The pass** (Section 8) is this mailbox's, for this server, not ended, and by the identity or by the signer of step 6 → otherwise a `401`. The signer shows the identity chose this key, and a pass minutes old shows its holder is present: nobody can register an identity they do not hold, and a prekey statement that leaked cannot be registered by whoever found it.
8. **A new mailbox** is stored `active` → `201 {}`. If another request registered the mailbox meanwhile, the registration is answered as step 5 answers it.
9. **The server keeps the prekey statement's signer** as the current signer: the identity, or the certificate and its certificate serial. Which passes it then takes is Section 8's; which later prekey statements it takes, Section 11.9's.

### 11.8. Look up a mailbox

`GET /v1/<A>` takes no pass (Section 8). It answers what a sender needs before it posts, and what a mailbox checks of itself (Sections 12.4, 12.5).

- **An active mailbox** → `200` with `identity`, `prekey` and `prekey_signature`, `sender_key_types` if the mailbox declared any, and `revoked_certificates` with `revoked_certificates_signature` if the server keeps a revocation of its certificates.
- **An unknown address** → `404 no_such_mailbox`, whatever certificates its identity has revoked (looking up revocations serves those: Section 11.15).
- **A revoked address** → `410 revoked`, with the revocation record in `record`.
- **A lookup is public.** A client sends no pass on it. It is counted as Section 11.5 says.

### 11.9. Renew the prekey

`PUT /v1/<A>/prekey` carries `prekey` and `prekey_signature`, and `sender_key_types` if the mailbox declares any. It carries a pass by that prekey statement's signer or by the identity.

The server takes a renewal because its prekey statement is later than the current statement and is signed by the identity or by a working key the identity certified. No pass by the current signer is needed. So a mailbox that lost its working key comes back by renewing under a new certificate, and a mailbox moves to a new certificate, its first included, by renewing.

- **An identical retry** is answered from what the server holds of the mailbox, whatever the checks below would answer: `200 {}` once its pass is verified, or `401 certificate_revoked` while the current signer is revoked.
- **Any other prekey statement** is verified as at registration (Section 11.7, steps 6 and 7), and MUST be later than the current statement by `(c, p)` (Section 5.3): signed by a signer of higher rank, at any prekey serial, or by the current signer at a higher prekey serial. Otherwise:

  | The new prekey statement is signed | Answer |
  |---|---|
  | by the current signer, at a prekey serial that is not higher | `409 conflict`; `prekey_serial` names the prekey serial it must exceed |
  | by a signer of lower rank, or by another certificate with the current certificate's serial | `401 certificate_not_current`; `certificate_serial` names the certificate serial it must be above, where that is a certificate |
  | under a certificate the identity revoked | `401 certificate_revoked` |

- **A prekey equal to the current prekey, or to a retired prekey the server keeps,** → `400 prekey_not_new`. For this check alone, a server keeps the newest of a mailbox's retired prekeys (it chooses how many), each until its prekey statement's `not_after`.
- **On success:**
  - the new prekey becomes the current prekey, and its signer the current signer, so the server no longer takes a pass by a certificate it replaced (Section 8);
  - its declaration (Section 7) is the mailbox's from then on;
  - the answer is `200 {}`.
- **Counting** is Section 11.5's (`renewals`, `certificates`). An identical retry spends neither. Past either, the renewal is answered `429 rate_limited`, naming it.
- **A mailbox the server has forgotten** → `404 no_such_mailbox` (Section 12.4).

### 11.10. Send

`POST /v1/<B>/messages?statement=<hash>` carries the sender's pass, and its body is the age file's exact bytes (`Content-Type: application/octet-stream`). It is answered `201` with `id`, `received_at` and `expires_at`, and `Location: /v1/<B>/messages/<id>`. The sender is the mailbox the pass names.

`statement` is REQUIRED. It names the prekey statement the age file is sealed to: the SHA-256 of the prekey statement's bytes as the lookup of `B` served them (Section 11.8), never of its signature, as `statement-hash`. Another encoding of the same signature (Section 3.2) does not change it.

```abnf
statement-hash = 64lowhexdig
```

- **The message id** in `id` and `Location` is the SHA-256 of the age file's bytes, `message-id`, and it names the message in Sections 11.11 to 11.13.

  ```abnf
  message-id = 64lowhexdig
  ```

- **A server reads the body as bytes, whatever its `Content-Type` says.**
- **A ciphertext is at most 16 MiB (16,777,216 bytes).** A server's caps on it, an account's included, are at most that. Every reader takes a ciphertext of any size up to it, so every reader reads a message any server takes. A larger one is a new version of the protocol.
- **The server checks no signature on a message:** there is none for it to see. It holds a sender to the pass alone.

The server checks the steps below; only step 9 stores anything. The server takes every count before the work it guards, and gives it back if that work does not happen.

1. **Who is sending: the pass** (Section 8), read for the mailbox it names. In this order:
   1. the IP address's `sends_before_pass`;
   2. the mailbox the pass names: unknown → `403 unknown_sender`; revoked → `403 sender_revoked`;
   3. the pass's own refusals, and the sender's `calls` (Section 11.5), which every send spends, a send of a ciphertext `B` already stores (step 6) included.
2. **The path and the query.** `<B>` is an address → otherwise `bad_address`. `statement` is given once, as `statement-hash`, and no other parameter is given → otherwise `bad_request`, naming the parameter.
3. **The body's size** is at most the sender's cap → otherwise `413 too_large`, with the cap in `limit`. The server refuses by the `Content-Length` before it reads anything, or while it reads.
4. **It is an age file** → otherwise `bad_request`, an empty body included, with no `member`.
5. **`B` is active:** unknown → `404 no_such_mailbox`; revoked → `410 revoked`.
6. **Dedupe, while the message is live.** The send's preconditions are evaluated before this step (Section 11.2). The message id is the ciphertext's SHA-256. If `B` stores a message with that message id unacked, from any sender → `200` with the receipt the server kept, whatever has changed at `B` since. Nothing of a message is kept once it is gone: the same bytes sent after that are a new message, stored and counted as any send is. A reader that accepted them once skips or refuses them (Section 12.3, check 8).
7. **`B` accepts messages on this prekey, from this sender:**

   | Failure | Answer |
   |---|---|
   | `B`'s current prekey is past its `not_after`, or the current signer is one `B`'s identity has revoked | `409 prekey_expired` |
   | The sender's identity key is of an optional type (Section 4.1) that `B` did not declare (Section 7) | `403 sender_key_type` |
   | `statement` is not the hash of `B`'s current statement | `409 stale_prekey` |
   | The age file does not have **exactly one native stanza** (`mlkem768x25519`) of the current prekey's type | `wrong_recipient_type` |

   A stanza whose type matches `^[!-~]{1,8}-grease$` with at most 4 arguments is ignored, as age has a reader ignore a stanza it does not recognise; its body is bounded by nothing but the message cap. The server cannot check which key the file is sealed to.
8. **Quotas, counted only for sends that carried their sender's pass:**

   | Failure | Answer |
   |---|---|
   | `B`'s ACL does not admit the sender | `403 sender_not_allowed`: one code and one wording whether the sender is denied or only not allowed |
   | The sender has spent its `sends` | `429 rate_limited` |
   | `B` stores as many messages as its cap | `429 mailbox_full`, with a `Retry-After` of the poll interval |
   | `B` has spent its intake budget | `429 mailbox_busy`, with a `Retry-After` of the time until `B` accepts another |

   A send of a ciphertext `B` already stores (step 6) spends none of the intake budget.
9. **Store the message** (Section 13) → `201`. If a concurrent send of the same ciphertext got there first, this send is answered as step 6 answers it. Any other failure → `503 unavailable`, and the sender posts the message again from its outbox.

### 11.11. List

`GET /v1/<B>/messages` carries the mailbox's pass → `200` with `count`, `messages` and `next_cursor`, and `Retry-After`.

- **`count`** is the number of live messages in the mailbox.
- **`messages`** is one page of up to 50 list entries, oldest first by `received_at`. Each list entry has the message id in `id`, `size` (its ciphertext's length, Section 11.10), `received_at` and `expires_at`, and nothing of the sender.
- **`next_cursor`** is null after the last page. An empty mailbox is `{"count":0,"messages":[],"next_cursor":null}`.
- **A cursor is opaque,** `cursor` (AEP-158). The next list carries it unchanged, as `?cursor=`.

  ```abnf
  cursor = 1*( ALPHA / DIGIT / "-" / "_" )
  ```

- **`Retry-After` on every list answer, a `304` included, is the server's poll interval,** in seconds.
- **A first page carries an `ETag`,** a strong validator of the page's body, which changes whenever a message enters or leaves the page or the count. A page after the first carries none.
- **Every poll is counted** (`polls`), a `304` and a `412` included: a mailbox that polls faster than its budget is refused `429`, never answered `304`.
- **A list with a cursor is a continuation** only if all of these hold:
  - it is a `GET` whose preconditions hold;
  - the mailbox stores messages after the cursor;
  - the cursor is the `next_cursor` the server last handed the mailbox.

  A continuation spends `pages`. Only a `GET` answered `200` uses up its cursor and gets the next one. After a false precondition, a refusal or a `HEAD`, the mailbox still holds the cursor it had. The server answers any other cursor it can read all the same, and counts that list as a poll; with nothing after that cursor, the page is empty.
- **The server checks:**

  | Check | Otherwise |
  |---|---|
  | `<B>` is an address | `400 bad_address` |
  | the only parameters are `cursor` and `wait`, each at most once | `400 bad_request`, naming the parameter |
  | `cursor` has the form above, and the server can read it as a cursor it makes | `400 bad_request`, naming it |
  | `wait` is delay-seconds, digits alone | `400 bad_request`, naming it |
  | a list does not carry both `cursor` and `wait` | `400 bad_request`, naming it |

- **A first page MAY be a long poll:**
  - A list without a cursor MAY carry `wait`, a number of seconds.
  - Where its `If-None-Match` would make it `304`, the server waits to answer. When the first page's tag no longer matches the `If-None-Match`, the server answers the page as any list is answered. When the wait has passed, it answers the `304`.
  - A server makes a long poll wait at most a time of its own, which is no part of the protocol. It MAY answer the `304` sooner, and it answers it at once when it stops serving.
  - A list whose `If-None-Match` would not make it `304`, or whose `wait` is `0`, is answered at once.
  - A long poll is one poll, counted when it arrives (Section 11.5).
- **Lists are eventually consistent:** a list may show a just-acked message, or miss a just-stored one, for a moment.

### 11.12. Fetch

`GET /v1/<B>/messages/<id>` carries the mailbox's pass → `200`, `application/octet-stream`, the ciphertext's exact bytes with a `Content-Length`, and nothing else. A message that is gone → `404 no_such_message`.

- **Ranges** are RFC 9110 §14. A server honours one form alone, `range-asked`: one open-ended range whose `N` is below the ciphertext's length. It answers it `206 Partial Content`, with the bytes from `N` to the end and `Content-Range` as `content-range-206`.
- **A server ignores every other `Range`** (§14.2) and answers the whole body, `200`.

  ```abnf
  range-asked       = "bytes=" 1*DIGIT "-"
  content-range-206 = "bytes" SP range-number "-" range-number "/" range-number
  range-number      = "0" / %x31-39 *DIGIT   ; no leading zero
  ```

- **A fetch carries no validator.** The message id is the ciphertext's SHA-256, so the bytes under a message id never change. So an `If-Range` is false (§13.1.5), and the `Range` beside it is ignored.
- **Counting.** A ranged fetch is counted as any fetch is.

  | Fetch | Spends | Past it |
  |---|---|---|
  | a message's first fetch | the mailbox's first-fetch budget | `429 fetch_paced` |
  | a refetch | an allowance the size of its poll budget, the mailbox's alone and apart from its polls | `429 fetch_paced` |

- **Bodies are served by the API itself, never by a redirect.**

### 11.13. Ack

`DELETE /v1/<B>/messages/<id>` carries the mailbox's pass → `204`. It deletes the message; nothing of it is kept (Section 11.10, step 6). A message that is gone → `404 no_such_message`. Reading a message does not delete it.

### 11.14. ACL

A mailbox's ACL says which senders its server accepts the mailbox's messages from.

- **Semantics.** A mailbox with no ACL accepts messages from everyone. With one, `deny` is checked first, then `allow`, and a sender matched by neither is refused, `403 sender_not_allowed`.
- **Set the ACL: `PUT /v1/<B>/acl`,** with the pass, replaces the ACL whole with `allow` and `deny`.
  - Each ACL entry is an address, or `*`, which matches every address: `acl-entry`. There is no other pattern, and repeats are taken once.

    ```abnf
    acl-entry = "*" / address
    ```

  - `allow` is REQUIRED (`["*"]` is everyone and `[]` nobody): a document without it → `400 bad_request`.
  - `deny` may be left out, for none.
  - More entries, in both lists together, than the server accepts → `413 too_large`. A server MUST NOT accept more than 10,000, and MAY accept fewer.
  - The answer is `200` with the ACL as stored, and no `ETag`. It lists `*` first, then each address once, in ascending order of its 48 bytes (not of its text, whose alphabet puts `2`–`7` after `z`).
- **Get the ACL: `GET /v1/<B>/acl`,** with the pass, answers the ACL as stored, in that order, with its `ETag`: a strong validator that changes whenever the lists change. For a mailbox with no ACL, it answers `{"allow":["*"],"deny":[]}`, with a tag too. There is no `DELETE`.
- **A client MUST treat an ACL answer with more than 10,000 entries,** in both lists together, as an answer that breaks its form.
- **A `PUT` MAY carry `If-Match`** (RFC 9110 §13.1.1), so that it replaces only the ACL its writer read.
- **Every mailbox has an ACL and its tag,** a mailbox with no ACL set included, so `If-None-Match: *` on a `PUT` is false (§13.1.2).
- **Setting the ACL deletes no message.** A change refuses later sends only. A send that cleared the check while the ACL changed may still be stored after the change.
- **A mailbox pauses by setting its ACL** to `{"allow":[],"deny":["*"]}`, which refuses every sender.
- **The ACL is not signed.** Only its mailbox gets it, and only the server acts on it.

### 11.15. Revocations

Neither call takes a pass (Section 8).

- **Publish a revocation: `PUT /v1/<A>/revocations`** with a revocation record (Section 10.3). The server verifies it as a client does, and keeps for good what verifies, whether or not a mailbox is at the address.

  | The body carries | Answer |
  |---|---|
  | a revocation of the address | `201 {}`. A mailbox at the address is revoked for good, and its messages and what the server holds of the mailbox are deleted |
  | a revocation of certificates, through a revocation serial no lower than the one kept, if one is kept | `200 {}` |
  | a revocation of certificates through a lower revocation serial than the one kept | `409 conflict` (RFC 9110 §9.3.4), `through` naming the one kept |
  | anything, for an address already revoked | `410 revoked`, with its revocation record |
  | an identity document that does not hash to the address | `400 identity_mismatch` |
  | a signature that does not verify | `400 bad_revocation` |

  **Both in one body:**
  - a revocation of certificates through a revocation serial no higher than the one kept changes nothing, whether or not it verifies;
  - the revocation of the address is kept once it verifies (`201`);
  - any other part that does not verify refuses the body whole, and nothing in it is kept.

  **A revocation of the address is counted against no mailbox.** A server counts it against its caller's IP address, and MAY count one for an address it keeps nothing of as a registration (`registrations`).

  **A revocation record kept for an address with no mailbox binds a later registration:** the registration is answered `410 revoked` while the address is revoked, and the server takes no certificate the identity revoked.
- **Look up revocations: `GET /v1/<A>/revocations`** → `200` with the revocation record the server keeps for the address, whether or not a mailbox is there, or `404 not_revoked`.

### 11.16. Accounts

A mailbox may belong to one account at its server. The protocol fixes how a mailbox joins and leaves an account; the server decides what an account does, within the promises of Section 11.5. A server without accounts does not offer these routes.

- **Join: `PUT /v1/<A>/account`** carries the pass and `key`, an adopt key → `200` with `account`, the account id. It spends one use of the key, unless the mailbox is already in that account. A mailbox in another account moves to this one.
- **Leave: `DELETE /v1/<A>/account`** carries the pass → `204`, whether or not the mailbox is in an account. An account can also drop a mailbox from its side.
- **Joining or leaving changes nothing about the mailbox's messages.**
- **An adopt key** is `dra1_` followed by base64url without padding (RFC 4648 §5) of one byte or more, at most 1,024 bytes in all: `adopt-key`. The server decides what it encodes.

  ```abnf
  adopt-key      = %s"dra1_" ( 1*254( 4base64url-char ) [ 2base64url-char / 3base64url-char ]
                           / 2base64url-char / 3base64url-char )   ; at most 1,024 bytes
  base64url-char = ALPHA / DIGIT / "-" / "_"
  ```

  - A client holds an adopt key to that form and reads nothing else of it.
  - A client MUST NOT take an adopt key as a command-line argument, where process lists and transcripts keep it.
  - A key this server did not make, or that is spent, expired or revoked → `403 adopt_key_refused`.
- **An account id** is `account-id`.

  ```abnf
  account-id = 1*32( %x61-7A / DIGIT / "-" )
  ```

- **An account's caps on a ciphertext** are within Section 11.10's.

### 11.17. Versions

- **Every request names the version of the client that makes it,** in `Dromas-Version`: the version of its release, written as SemVer 2.0.0 writes one ([SemVer] §2, §9), with no build metadata, as `dromas-version-value` gives: its major, minor and patch and an optional pre-release.

  ```abnf
  dromas-version-value = version-number "." version-number "." version-number
                         [ "-" pre-release ]
  version-number = "0" / %x31-39 *DIGIT
  pre-release    = pre-release-id *( "." pre-release-id )
  pre-release-id = version-number
                 / *DIGIT ( ALPHA / "-" ) *( ALPHA / DIGIT / "-" )
  ```

  - A client's major and minor are those of the protocol it implements. A major of `0` marks a release from before version 1 is declared.
  - A request without the field, or with a value of another form, is read as version `0.0.0`.
- **A server's version policy** is its own. It has four parts:
  - a **version floor**;
  - its **deprecations**: the versions it deems deprecated from a date;
  - its **sunsets**: the versions it has scheduled to stop accepting;
  - the **latest version** it names, if it names one.

  A version in the policy is `policy-version`: a major and a minor, and a patch where written.

  ```abnf
  policy-version = version-number "." version-number [ "." version-number ]
  ```

  A deprecation or a sunset is a date and one of:
  - `below` a version: it covers every version SemVer orders before that version (here a version written `MAJOR.MINOR` stands for `MAJOR.MINOR.0`);
  - `version`: it covers every version whose major, minor and, where written, patch are that version's.

  A server MUST NOT schedule a sunset earlier than a deprecation of the same `below` or `version` (RFC 9745 §4).
- **`Deprecation`.** An answer under `/v1/` to a request whose version a deprecation covers carries `Deprecation` (RFC 9745 §2.1). The date is the earliest of the deprecations that cover the version.
  - The field's scope is the version the request names, on every route under `/v1/`. That is wider than the resource RFC 9745 §2.2 gives the field by default.
  - No other answer carries it.
  - No answer carries a `deprecation` link (RFC 9745 §3): a URL is a server's text, which a client never shows (Section 11.3).
- **`Sunset`.** An answer under `/v1/` to a request whose version a sunset covers carries `Sunset` (RFC 8594 §3), with the same scope. The date is the earliest of the sunsets that cover the version. No other answer carries it.
- **A retired version.** A request whose version is below the version floor, or covered by a sunset whose date has passed at the server's clock, is answered `410 version_retired`, with `min_version` where the server sets a version floor. Section 11.4, rule 0, places it first. The server's document accepts every version.
- **A client reads `Deprecation` and `Sunset` on every answer, and nothing more of them.** A value of another form than RFC 9745 §2.1's or RFC 8594 §3's counts as absent, and a client shows neither as the server's words (Section 11.3).
- **A client SHOULD get the server's document** (Section 11.18) at most once a day.
- **A client SHOULD tell its owner** of a deprecation or a sunset of its own version, and of a `latest_version` higher than its own version by SemVer precedence ([SemVer] §11) in the parts `latest_version` writes.

### 11.18. The server's document

`GET /v1/` takes no pass. It answers the server's version policy and the capabilities it serves, the same to every caller:

- **`min_version`** is the version floor, when the server sets one.
- **`latest_version`** is the latest version the server names, when it names one.
- **`deprecations`** lists each deprecation as `below` or `version`, and `at`: its date, as a time (Section 2.2).
- **`sunsets`** lists each scheduled sunset the same way. A sunset whose date has passed stays until the server raises its version floor past it.
- **`max_certificate_lifetime`** is the server's maximum: the longest a certificate the server accepts may run, `valid before` less `valid after`, in seconds (Section 11.7, step 6). It is more than the TTL and an hour.
- **`capabilities`** lists each capability of version 1 the server serves, by `name`. A name is `capability-name`, and is defined in this section. A client ignores a name it does not know, and never uses a capability the server does not list. This version defines none.

  ```abnf
  capability-name = %x61-7A *63( %x61-7A / DIGIT / "_" )
  ```

A server lists at most 64 deprecations, 64 sunsets and 64 capabilities, and a client reads no more. A document with more, or with a sunset earlier than a deprecation of the same `below` or `version`, breaks its form.

## 12. Clients

### 12.1. Keys and what a client keeps

- **A mailbox's keys and state are kept in a directory only its owner can read** (0700, or the system's own equivalent where it has no modes), never in the working directory.
- **The identity key** is a plain OpenSSH private key file, with no passphrase and no seed derivation. It is backed up: losing it loses the address (Section 12.5).
- **A prekey MUST NOT be backed up, synced, derived from a seed, or kept where old versions survive** (a secrets manager, a version-control system, a snapshot): a prekey that survives its deletion voids the forward secrecy of Section 15.3. A working key is not backed up either: a restored mailbox makes new ones.
- **What a client keeps,** each by the rule of its section:
  - each peer's identity, pinned once something it signed has verified (Sections 12.2, 12.3);
  - the latest `(c, p)` it has seen on each peer's prekey statements (Section 6);
  - every revocation it has verified, never removed; these decide which certificates it takes, of each peer and of its own mailbox (Sections 5.1, 12.5);
  - its replay set and replay floor (Section 12.3, check 8);
  - when it signed each of its prekeys (Section 12.4);
  - its outbox, as written (Section 12.2).
- **A client MUST NOT adopt a server's `Date` as its clock, nor tell its owner to set the clock by it.** When a refusal is for a time, the client reports the difference between its clock and the server's `Date`, as the server's word, and points its owner to a time source the owner trusts. A mailbox checks everything against its own clock (Section 15.7).
- **A key that reads messages decrypts the messages a client fetches (Section 12.3), and nothing else a server sends.**
- **A client MUST lock a mailbox for the whole of every command that writes it,** so that the mailbox has one consumer. A command that finds the mailbox locked waits.
- **A mailbox runs in one place at a time,** where its working keys, prekeys and state are kept. A machine that is to run beside it is a mailbox of its own, with its own identity and address. A mailbox restored from its identity alone takes the mailbox's place (Section 12.4).

### 12.2. Sending

**Look up the recipient before every post** (Section 11.8), whether the message is new or has waited, and check what the lookup serves before anything is sealed:

- **Identity:** the pinned identity, or at first contact the served one.
  - The served identity document MUST hash to the address and meet Section 4.1, as a server checks it at registration.
  - One of a key type the client does not verify is refused, and nothing is sealed.
  - The client pins it only once its prekey statement has verified, and a later lookup never replaces the pin.
- **A verifier built on `ssh-keygen`** uses an `allowed_signers` file of exactly one line, chosen by the signer's form: `<address> <key>` for the identity's own signature, and `<address> cert-authority <key>` for a certificate. It builds the file for each verification from the identity document it has hash-checked, never from a server's text. It always verifies a revocation with the first form (Section 10.1).
- **The prekey statement:**
  - its signature: by that identity, or by a certificate of it that meets every rule of Section 5.2;
  - every rule of Section 6: its form and namespace, line 2 the recipient's address, its key, its `not_after` not passed, and the prekey statement not earlier than what the sender has seen (Section 5.3).

  The sender records its `(c, p)` only once it clears every check.
- **Revocations:**
  - The sender keeps whatever revocation of the recipient's address or certificates the lookup carries, once it verifies (Section 12.5).
  - The sender posts nothing to an address it keeps a revocation of, and refuses such an address before any call.
  - The sender refuses a prekey statement under a certificate that the recipient's revocation of certificates covers, as the sender keeps it at that point (Section 5.1), and seals nothing.
- **No answer is not "none held."** If the lookup fails, or serves no mailbox (`404 no_such_mailbox`, or `410 revoked` with nothing that verifies), nothing is sealed or posted, and the message waits in the outbox. The code of a failed lookup is no answer about the message.
- **What the lookup shows, or leaves out, is the server's word and is kept nowhere,** so the next post looks the recipient up again.

**The outbox:**

- **Every post of an outbox entry carries the same envelope, never a new message.** A new message makes a new `msg_id`, and a reader that acts on messages would act twice. The outbox keeps the envelope until a receipt arrives.
- **A sealed file is made for one post and never kept.** Each post takes these steps in order:
  1. It looks the recipient up, as above.
  2. It dates the envelope: `sent_at` is the sender's clock, and every other member stays as it was.
  3. It signs the envelope with the signer it signs messages under (Sections 9.2, 12.7), and seals it to the prekey whose prekey statement cleared the checks above.
  4. It posts the sealed file.

  A message that waited therefore goes to what the sender knows at its post.
- **A post is dated after the lookup, and never earlier than when its outbox entry was written.** If, when it dates a post, the client's clock reads earlier than that, the entry waits until the clock passes that time.
- **An outbox entry is posted for the TTL from when it was written, and never after,** as the clock reads each time a post is dated. So no copy of a message is dated more than the TTL after the first.
- **On `409 stale_prekey`, the sender posts the same envelope again at once, rather than leaving it in the outbox:** it looks the recipient up, then dates and seals the envelope anew, as above.
- **Final refusals.** A refusal that no later post of the message can clear is final, and the message is never posted again:

  - `bad_address`;
  - `too_large`;
  - `sender_not_allowed` (only the recipient can change its ACL).

  **Any other refusal leaves the message in the outbox,** to be posted again after `Retry-After` where the answer gives one.
- **An outbox entry whose signer is a certificate the mailbox keeps revoked waits until its renewal is taken:** Section 10.2 forbids signing under it.
- **A `410 revoked`, to the lookup or to the send, is checked, never believed** (Section 12.5).
- **A post whose answer is lost costs a send.** The server stored the message. The next post is a new ciphertext, which the server stores and counts as a new message, and the recipient delivers the message once (Section 12.3, check 9).

### 12.3. Receiving

A client drains every page of its list (Section 11.11), and fetches the message of each list entry (Section 11.12):

- **It reads at most the listed `size`.** A body of another length breaks the answer's form: the drain stops there, nothing is judged or acked, and the message stays on the server.
- **A fetch that was cut resumes by range** (Section 11.12). The whole (the bytes kept and the range) is checked against the listed `size` and the message id (check 8), as a body read whole is.
- **A `206` breaks its form** if its `Content-Range` is not the range asked for, or not of the listed size.

**The checks, in this order.** Each check names the code it refuses with. Only a message that clears all of them is accepted.

1. **Check 1, decryption.** The reader decrypts the file, trying every retained prekey (`undecryptable`). A reader MAY refuse as `undecryptable`, before any key agreement, a file without exactly one native stanza, which no server takes as a send (Section 11.10, step 7).
2. **Check 2, layout.** The plaintext has the layout of Section 9.2: the envelope on line 1, then a signature block from line 2 to the first END line, followed by LF (`bad_message`).
3. **Check 3, JSON.** The envelope is one JSON object, as Section 3.3 reads one, and its body meets the same rules (`bad_envelope`). The body is passed on as it is.
4. **Check 4, identity.** The sender's identity document comes from the envelope's `identity`.
   - It is base64, has the form of Section 4.1, and has a key that meets its checks (`bad_identity`).
   - `from` MUST be its hash (`reattributed`).
   - A well-formed document of a key type the reader does not verify, whose hash is `from`, is refused as `unsupported_key_type`.

5. **Check 5, signature.** The signature is over line 1 exactly as decrypted, in the namespace `dromas-msg-v1:<this mailbox>`, by that identity, or by a working key under a certificate of it that holds now (Section 5.2) (`bad_signature`).
6. **Check 6, envelope.** The rest of the envelope, in this order:

   | Order | Rule | Otherwise |
   |---|---|---|
   | 1 | a body, and every member of its type, `from` and `to` being addresses | `bad_envelope` |
   | 2 | `v` is the integer 1 | `bad_envelope` |
   | 3 | `to` is this mailbox | `reattributed` |
   | 4 | `msg_id` is in its form | `bad_envelope` |
   | 5 | `sent_at` is no older than the TTL | `stale` |
   | 5 | `sent_at` is not before the mailbox's replay floor (check 8) | `stale` |
   | 5 | `sent_at` is no more than the skew allowance ahead of this mailbox's clock | `future` |

7. **Check 7, revocation.** The mailbox keeps no revocation of the sender's address (Section 12.5), and none of the sender's certificates that covers the signer (Section 5.1).
   - The reader looks up the sender's mailbox (Section 11.8) once for each sender in a drain, after that sender's signature has verified. It keeps what the lookup carries once it verifies.

   | What the reader keeps, or the lookup shows | Outcome |
   |---|---|
   | a revocation of the sender's address | the message is refused as `revoked`, and so is every later message from that address (Section 10.1) |
   | a revocation of the sender's certificates through a revocation serial at or above the signer's certificate serial | the message is refused as `certificate_revoked` |
   | the sender's mailbox served with no revocation, `404 no_such_mailbox`, or `410 revoked` with nothing that verifies | "none held" |
   | any other result | no answer: the drain stops, and the message stays on the server |

8. **Check 8, replay.** The SHA-256 of the ciphertext, computed by the reader, is not in the replay set (`replay`).
   - A listed `id` that differs from it is refused (`bad_id`).
   - The replay set keeps each message's hash, with its `sent_at`, `from` and `msg_id`, until `sent_at` + 14 days and an hour.
   - **A prune that drops a line first raises the mailbox's replay floor to its cutoff,** in the same write or before it. The cutoff is the date of the pass the prune goes by (Section 12.4), less 14 days and an hour.
   - A prune that drops nothing leaves the replay floor as it is.
   - A `sent_at` before the replay floor, or less than the TTL above it, is `stale` whatever the clock reads.
9. **Check 9, duplicate.** `(from, msg_id)` is not in the replay set. A match is a copy of a message already delivered: the reader acks it and does not deliver it again.

**A reader never asks the server who a sender is:** the message carries what proves it, and the reader pins the sender's identity only once the message clears every check.

**The verdict.**

- **A client keeps a message decrypted, adds it to the replay set, and delivers it only when it accepts the message.** A client acks each message once it has kept its verdict: accepted (delivered or held) or refused. Until then the message stays on the server, and a later drain may serve it again: delivery is at least once.
- **A refused message is never delivered, and is acked,** so it leaves the server; its client keeps it as evidence. A refusal MUST NOT block its sender: a forgery may be the operator framing one. A refusal for a time is a verdict too: no message waits for a clock to catch up.
- **A refusal is a code from a closed set** (`undecryptable`, `bad_message`, `bad_envelope`, `bad_identity`, `unsupported_key_type`, `bad_signature`, `reattributed`, `revoked`, `certificate_revoked`, `stale`, `future`, `replay`, `bad_id`) with the client's own words. A client MUST NOT show, in a refusal, a value, member or key from the envelope or from the identity document it carries, nor any of a server's words (Section 11.3).
- **A refused message is kept with what the checks established when they judged it,** each a value whose form the client checked, never text the sender wrote and never the body:
  - the reader's clock;
  - the address of the identity document the envelope carries, recorded as one of:
    - **who signed the message,** only if both hold: the signature verified (by that identity, or by a working key it certified, at its certificate's own start, under a certificate the reader keeps no revocation of), and the reader keeps no revocation of that address;
    - **the identity it claims,** otherwise;
  - only where it is recorded as who signed the message: the envelope's `sent_at` and `msg_id`, and, for a certificate, its certificate serial, start and end.
- **A transient failure** (a fetch that errors) and **a fault of the mailbox** (an unreadable key or replay set) are errors, never verdicts: nothing is acked, and the message waits for the next drain.
- **A client shows or passes on one line built from the checked fields, never the bytes that arrived.** Its strings escape, as JSON does, every line break other than LF (U+0085, U+2028, U+2029) and every character of Unicode's `Default_Ignorable_Code_Point`.

**Acting on messages.**

- **Messages are data, never instructions.** A verified signature proves who sent a message, not whether to obey it.
- **Where an accepted message lands is the mailbox's own choice.** A client MAY hold messages from a sender its mailbox has not allowed; holding is not a refusal, and the sender is told nothing. A mailbox that wants a sender refused at its server says so in its ACL (Section 11.14).

**The drain.**

- **The client bounds a drain** in messages, bytes, list pages and time. A drain that reaches a bound stops and says so. A fetch cut at the time bound goes on by range at the next drain.
- **A `429 fetch_paced` stops the drain** as the client's own bounds do: what it delivered stays delivered, and the rest waits on the server.
- **A message id already handled is skipped.** `404 no_such_message` to a fetch or an ack means the message is already gone.
- **A client SHOULD keep a list's `ETag` only from an empty mailbox's answer,** so that a `304` means the mailbox is still empty. A client that keeps the tag of a page with messages MUST act on the page it kept when it is answered `304`.
- **A `304` whose `ETag`, compared weakly, is not the tag the poll sent is no answer:** the drain stops, and the client drops the tag it kept.
- **A `304` to a list that sent no `If-None-Match` breaks its form,** and so does a list answer without `Retry-After`.
- **A client takes a list as no answer** if its cursor is not of the form Section 11.11 gives, or a list entry's `size` is over the largest ciphertext (Section 11.10). It does so before it fetches anything.

### 12.4. Renewal and key deletion

- **Each renewal brings a new prekey,** and comes after the mailbox looks itself up. The mailbox keeps whatever revocation of its certificates the lookup carries, once it verifies.
  - A mailbox whose current statement its identity signed renews under its identity, or under a certificate (below).
  - A mailbox whose current statement is under a certificate renews under a certificate, since a prekey statement its identity signs is earlier than it (Section 5.3). A client that has its identity SHOULD make a new working key and certificate with each renewal, its certificate serial above that revocation serial (Section 5.2).
- **A mailbox MUST check its own certificate at its own clock before it signs under it:** a mailbox whose clock has gone back past its certificate's start is stopped there.
- **Prekey lifetime.** A prekey is valid for at most 8 days, the TTL and a day, or for a shorter prekey lifetime its owner sets.
  - A client renews while enough of its prekey is left for its senders' clocks, and for the renewal's own calls. For a prekey lifetime of a day or more, that is the skew allowance (Section 3.4) and an hour for the calls; for a shorter one, five minutes for senders' clocks and five for the calls.
  - A client times its renewals by when it signed each prekey, and the early deletion of its retired prekeys by when the server took a renewal (below); never by a prekey statement's `not_after`.
  - A prekey for which the client does not know when it signed it is renewed at once.
- **A prekey serial is signed once.** A renewal the server did not take is sent again exactly as it was signed. A client signs a new key, at a later prekey serial, only once any one of these is true:
  1. that prekey statement has ended;
  2. it was made for longer than the prekey lifetime now allows;
  3. the client does not know when it was made;
  4. it is under a certificate its identity has revoked;
  5. it is under a certificate that began less than an hour before the mailbox's clock;
  6. it is under a certificate that runs longer than the server's maximum (Section 11.18);
  7. another key type is wanted;
  8. its server keeps a prekey statement no earlier than it (below).

  The list is not an order. The key passed over is kept, and deleted as a retired prekey.
- **A renewal answered `409 conflict`, or `401 certificate_not_current` under the second pass Section 8 allows, is final for its prekey statement:** the server keeps a prekey statement no earlier than it, and takes only a later one (Section 11.9). The mailbox then looks itself up (Section 11.8):
  - If the server serves the mailbox's own prekey statement, the server took the renewal and its answer was lost.
  - Any other prekey statement is one the mailbox did not make, and the rules below decide whether the mailbox renews again.
- **A client MUST NOT use a kept certificate that began less than an hour before its clock:** it was made while the clock read ahead. The client makes the prekey statement and working key made with it again, by the clock as it reads: for the same prekey at a mailbox's first registration, and at a later prekey serial at a renewal.
- **Deletion.** A retired prekey is deleted at its deletion time: at the latest, its prekey statement's `not_after` plus the TTL.
  - A drain that stopped at a bound deletes no retired prekey.
  - A client SHOULD delete every retired prekey, the newest included, earlier, after a drain that empties the mailbox, by a list the server took under a pass dated more than an hour after the take of the current prekey's renewal.
  - Otherwise it keeps the newest retired prekey, and SHOULD delete the others after such a drain by a list the server took under a pass dated more than an hour after the take of the newest retired prekey's renewal, or, where the client does not know that take, after it signed the newest retired prekey.
  - While the client knows neither when the server took the newest retired prekey nor when it signed it, every retired prekey is kept.
  - While a renewal under a new certificate waits, a mailbox whose current statement is under a certificate has two working keys: the one under its current certificate, which signs its passes, and the one the waiting prekey statement is signed with. The working key from before such a renewal is deleted once the renewal is taken.
- **A client deletes keys, and prunes its replay set, by its clock only once a server has taken a pass dated by that clock, and then by that pass's date** (its `not_after` less ten minutes), never by a reading of the clock.
  - A drain's pass counts once the server answers the drain's list.
  - A renewal's pass counts once the server takes the renewal.
  - While a renewal is not yet taken, every retired prekey is kept.

  Section 15.3 gives the reason.
- **A mailbox its server has forgotten** sends its renewal as a registration (Section 11.7), at a prekey serial above every prekey statement it has registered or renewed.
- **When a mailbox looks itself up (Section 11.8), it checks what the server serves by every rule of Section 5 except a certificate's dates.** What it needs from the lookup is that its identity signed the prekey statement, and where it stands in the mailbox's order. A certificate's dates bind what is signed under it, never a renewal past it, so a served prekey statement is never one the mailbox cannot renew past because of a date.
  - A served prekey statement that does not verify, or that is earlier than one the mailbox has registered or renewed, stops the renewals the mailbox makes on its own.
  - Its owner's renewal goes on past it: by renewing above it where the mailbox's prekey statements are its identity's, and by the remedy (Section 12.6) where they are under certificates.
- **A mailbox whose lookup of itself shows a prekey statement it did not make** MUST say so to its owner, with what it recommends:

  | The prekey statement is | What is stolen | The mailbox |
  |---|---|---|
  | signed by its identity, or under a certificate of a working key the mailbox did not make | its identity | SHOULD recommend that its owner revoke the address (Section 10.1); a revocation of certificates cannot end it, since whoever has the identity makes a higher certificate as easily (Section 15.5) |
  | under a certificate of the mailbox's own working key, where the identity is kept beside the mailbox's other keys | that working key, and the identity kept beside it | SHOULD recommend revoking the address |
  | under a certificate of the mailbox's own working key, where the identity is kept elsewhere | that working key | SHOULD recommend the remedy (Section 12.6) |

- **When its server serves a prekey statement the mailbox did not make:**
  - A mailbox that has registered or renewed a prekey statement of its own MUST NOT renew above one it did not make unless its owner asks.
  - A mailbox that has registered or renewed none, restored from its identity alone, is served its lost machine's prekey statement this way, as one it did not make. Its first renewal takes the mailbox over.
  - A mailbox MUST NOT sign messages under a certificate of its own working key that such a prekey statement shows is stolen. Once the remedy runs, or the address is revoked, readers refuse those messages too, so the outbox waits for the mailbox's next certificate.
  - A mailbox that signs its messages with its identity does not wait for such a prekey statement: no certificate of its own is in doubt.
  - A mailbox knows of such a prekey statement from its last lookup of itself. One its server serves only since then is unknown to it until it looks itself up again.
- **A mailbox that does not renew** is sent nothing once its prekey's `not_after` passes (`409 prekey_expired` to its senders).
  - Under a certificate, it can poll while the certificate lasts, and by its identity's pass where it has its identity.
  - Once the certificate has ended, its server takes no pass under it, and a renewal brings the mailbox back while the server keeps it.
- **A mailbox moves to certificates by renewing under one** (Section 11.9), and only when its owner asks. Its prekey statements are under certificates from then on (Section 5.3). What it signed with its identity stays good.

### 12.5. Revocation at a client

- **A client takes "revoked" only with a revocation that verifies.** A `410 revoked`, a `403 sender_revoked` or a `401 certificate_revoked` is a server's word.

  | Answer | The client |
  |---|---|
  | `410 revoked` with a revocation record | verifies the record |
  | `410 revoked` with no revocation record, or one that does not verify | looks up the mailbox the `410` is about (Section 11.8), unless that lookup is what answered |
  | `403 sender_revoked` | looks up its own mailbox, unless that lookup is what answered |
  | `401 certificate_revoked` | as the next bullet says |

  - On a `410` or a `403 sender_revoked`, a client looks a mailbox up only as this table says.
  - A revocation record that verifies is final.
  - Without one, the call has failed and nothing more: no state changes, and a message stays in the outbox.
  - What a server shows that does not verify counts as nothing shown.
- **On `401 certificate_revoked`, a client looks up its own mailbox before it tries again.**
  - If the lookup carries a revocation that verifies and covers the certificate the client signs under, the revocation is final: the client signs no second pass for the call, and the mailbox renews instead, under a new certificate.
  - If it carries none, the client signs one more pass, as for any `401` (Section 8).
- **A mailbox checks for revocations, and publishes its own revocations, at its own server alone.** It checks:
  - for the recipient's, by the lookup before every post;
  - for each sender's, once in a drain;
  - whenever its owner says.

  Where its server keeps no mailbox for it, a mailbox looks up its own revocations with `GET …/revocations`.
- **What it has verified about an address, from any source and at any time, it keeps for good,** and checks every send and every read against, with no call to anyone. A revocation record handed over is checked and kept as a served one is. "None held" is kept nowhere.
- **A mailbox that keeps a revocation of its own address is finished, whatever any server answers.**
  - It signs nothing more as that address: no pass, no message, no certificate.
  - It never posts an outbox entry. A drain, a renewal and every call that carries a pass are refused before anything is asked.
  - It still checks for, keeps and publishes revocations, which carry no pass.
  - A revocation it keeps that no longer verifies is a fault, and stops it the same way.
- **Publishing a revocation answered `410 revoked` is done** once the revocation record the answer carries verifies, or, if it carries none that verifies, once the revocation record from the client's next lookup of its own mailbox verifies.
- **A client publishes a revocation of an address only with the address written out in full,** as its owner's confirmation, and writes no revocation record unasked.
- **A revocation record made in advance** is the body of the call that publishes it, and publishing it later needs no mailbox and no key. Whoever has it can revoke the address, so it is kept apart from the identity.

### 12.6. The remedy for a stolen working key

The remedy is for a working key stolen without the identity that certified it. A stolen identity is answered by revoking its address instead (Section 10.1). The remedy runs where the identity is. Its steps are in order: it revokes, then renews.

1. **Look up the mailbox** at its own server, and keep whatever revocation of the mailbox's certificates the lookup carries, once it verifies. An answer the client cannot use does not stop the remedy.
2. **Revoke every certificate below the certificate serial the client's clock gives.** Sign a revocation of certificates through one below it, or sign again one through a higher revocation serial that is kept already, and publish it once (Section 11.15).
   - A `409 conflict` means the server keeps one through a higher revocation serial: the client looks up the mailbox again, keeps that one once it verifies, and renews above it.
   - A conflict that shows none that verifies and covers the certificates stops the remedy there, with nothing renewed.
3. **Then renew,** under a new working key and a certificate whose certificate serial is one above the revocation serial. Send nothing an earlier renewal left: whoever copied the mailbox while it waited has that renewal's working key. Until the server takes the new prekey, the mailbox signs no message.

A mailbox whose revocation of its certificates covers the certificate it uses renews at its next drain.

### 12.7. A mailbox whose identity is kept elsewhere

A mailbox whose identity is kept elsewhere signs under certificates. The identity holder makes the mailbox's certificates and revocations; the mailbox keeps everything else (Section 12.1).

- **The mailbox makes each working key and keeps it; the identity holder certifies only its public half.** In this order:
  1. The mailbox gives the identity holder the public key and the lowest certificate serial the certificate may have: one above the highest the mailbox knows of for itself (Section 5.2).
  2. The identity holder sets the certificate serial by Section 5.2, at that serial or above, and makes the certificate for no longer than the server's maximum (Section 11.18).
  3. The mailbox checks the certificate against every rule of Section 5.2, against a certificate serial no lower than the one it gave, and against the key it made.
  4. The mailbox renews under it (Section 11.9). It uses a certificate that began less than an hour before the mailbox's clock (Section 12.4) only once that hour has passed.

  A prekey never leaves the mailbox, and the identity holder has no key that reads messages.
- **Between certificates, the mailbox renews under its current certificate:** each renewal brings a new prekey at a higher prekey serial.
- **A message signed under a certificate is taken only while the certificate holds at its reader's clock** (Section 5.2), and may wait the TTL to be read. So an identity holder gives the mailbox a certificate at least as often as the server's maximum, less the client's message margin (Section 2.3).
- **Once the certificate has ended,** its server takes no pass under it, and nothing goes on until the identity holder gives the mailbox a certificate of higher rank. The server takes the renewal under that certificate while it keeps the mailbox (Sections 11.9, 13). Once the server has forgotten the mailbox, that renewal registers the mailbox anew (Section 12.4).
- **Wherever Sections 12.5 and 12.6 have a mailbox renew under a new certificate,** this mailbox waits for its identity holder to give it one. A certificate the identity holder gives it counts as its owner asking (Section 12.4).
- **A renewal answered `409 conflict` or `401 certificate_not_current`** is final for its prekey statement (Section 12.4). The mailbox signs no prekey statement at a higher prekey serial to get above the one served: whoever has the same working key can sign at any prekey serial, and only a certificate of higher rank gets above it.
  - After such a `409`, a prekey statement the lookup shows that the mailbox did not make is under the certificate of the mailbox's own working key, so that working key is stolen. The mailbox signs no message under that certificate (Section 12.4), and recommends the remedy.
  - After such a `401`, the mailbox waits for a certificate above the certificate serial the answer names.
- **Both revocations are the identity holder's** (Section 10).
- **The remedy (Section 12.6) is split,** in this order:
  1. The identity holder runs steps 1 and 2.
  2. The mailbox makes a new working key, never one made before the revocation.
  3. The identity holder certifies it above the revocation serial.
  4. The mailbox renews under it (step 3).

## 13. What a server stores, and for how long

How a server stores things is its own choice. What it promises a client is this.

- **Per mailbox,** the server stores:
  - the identity document;
  - its state (Section 11.6);
  - the current statement and its signature, as they were sent, and serves them as they were sent;
  - the current signer, and for a certificate its certificate serial;
  - the sender key types the mailbox declared;
  - for the newest retired prekeys, the type, `not_after` and a hash of the key (Section 11.9);
  - when the mailbox was registered;
  - its account, if any;
  - its ACL.

  It stores no private key, no pass, and nothing of when the mailbox was last seen.
- **Per address, with a mailbox or none,** the server stores the identity document and the revocations its identity made (Section 10), each kept for good and served to whoever looks them up.
- **A mailbox is kept until nothing valid for it is left,** that is, until the later of:
  - its current prekey's `not_after` plus the TTL;
  - the latest `valid before` of any certificate the server took for it.

  A registration and every renewal raise that date, and nothing lowers it. Past that date a server MAY forget the mailbox at any moment, and the mailbox is then unknown (Section 11.6).
- **Per message,** the server stores the ciphertext exactly as the send's body carried it, its size, `received_at` and `expires_at`. Its message id is the ciphertext's SHA-256. The server stores no sender and no signature: the message's signature, which names its sender, is inside the ciphertext.
- **A `200` or `201` to a send means the message is stored.**
- **A message expires 7 days less an hour and two minutes after it is received,** read or not:

      TTL − skew allowance − 2 minutes

  The two minutes are for a post's own time, from when the sender dates it until the server receives it. `expires_at` is the authority: a server MUST NOT serve a message past it.
- **An ack deletes the message,** and nothing of it is kept (Section 11.10, step 6).
- **A revocation of the address** does to every message in the mailbox what an ack does to one, and what the server holds of the mailbox goes with it; the revocation stays (Section 11.15).
- **Setting the ACL** deletes no message (Section 11.14).
- **Logs.** A server MUST NOT log an address or a message id in the clear: each is replaced by a short hash of itself, which only someone who already has it can match. A server MUST NOT log a pass or a request body.

## 14. Compatibility and versioning

Until version 1 is declared stable, nothing is final. From that declaration:

- **Unknown members are read as Section 3.3 says.**
- **Signed statements match their format exactly:** the identity document, the prekey statement, the pass, and the two revocation statements. A new format takes a new tag. Each namespace is versioned on its own.
- **A new member is optional.**
- **A server MAY add a route.** Section 11.2 says how a client reads a route a server does not offer.
- **A client names its version on every request, and a server says when it stops taking one** (Section 11.17):
  - a `Deprecation` on every answer to a version it deems deprecated;
  - a `Sunset` on every answer to a version it will stop taking;
  - `410 version_retired` once it has stopped taking it.
- **A feature that is not in version 1.0** is a capability the server lists in the server's document (Section 11.18), or a later minor version's. A client uses it only where the server lists it, or names that minor version as its latest.
- **`dromas-revoke-v1` stays valid for good,** so a revocation signed in advance keeps working.
- **A new identity key type is an addition inside v1:** the identity document has one form for every type (Section 4.1), so a type defined later takes no new tag. It is optional, and a mailbox declares it once its client verifies it (Section 7).
- **A new prekey type is an addition inside v1** the same way (Section 6).

What version 1 fixes, and where each can grow inside it:

| Fixed | Grows by |
|---|---|
| The signed statements, byte for byte (Sections 4.1, 6, 8, 10) | a new format under a new tag; a new identity or prekey key type |
| A certificate's type, key id, principal and one form, with no critical option or extension; its validity, a window checked at the verifier, whose longest is each server's own (Sections 5.2, 11.18) | a certificate's new format under a new key id |
| The two signer forms, their ranks, and the order `(c, p)` (Section 5) | nothing |
| The namespaces, `dromas-<purpose>-v1:<address>` | a new purpose |
| A signature's form, and its bound for each key type (Section 3.2) | a new key type, with a bound of its own |
| The plaintext: the envelope, the signature block, the padding (Section 9) | the envelope's optional members; `v` changes only for a breaking change |
| JSON as Section 3.3 reads it; a byte member's one encoding; a message's ciphertext as a send's body | nothing |
| The largest ciphertext, 16 MiB (Section 11.10) | a new version of the protocol |
| The routes, methods, statuses, codes and members (`api/openapi.yaml`) | a route; an optional member; a code, which a client that lacks it reads by its status |
| The pass's scheme and its three parameters (Section 8) | a new scheme |
| An error's `type` (Section 11.3); the link (Section 4.3) | a new code |

## 15. Security considerations

### 15.1. Threat model

Dromas protects a message's content from everyone but its recipient, and its sender's identity from forgery by anyone, the server included. It does not hide from the server who writes to whom.

- **The adversaries:** the server and its operator, whatever terminates TLS for it included (Section 15.8), who see everything stored and in transit; a network attacker, who sees TLS; a registered sender; many cheap identities; and a compromised or prompt-injected mailbox.
- **The assets, in order:** a message's content; its sender's authenticity; the authenticity of a mailbox's keys; availability; metadata.
- **The trust root is the address.** It is exchanged out of band, so whoever delivers it to a sender is trusted to deliver the right one, as with any key fingerprint.
- **Every defence below holds for a client that verifies and gates:** one that runs every check of Section 12 and acts on none that failed. Code or commands a server serves are not such a client.

### 15.2. What a hostile server can and cannot do

- **It sees:** addresses, identity documents, certificates and prekeys; who sends to whom; each message's size to within a padding block (Section 9.3); times and IP addresses; ciphertext; each mailbox's passes; which account a mailbox is in; and each mailbox's ACL, whose allow list names senders the mailbox takes messages from who may never have written to it, and whose deny list names the senders it blocked.
- **It never sees:** plaintext, a private key, an age file key, or any message's signature. **It keeps nothing signed that says who wrote to whom:** a pass names a mailbox and a time, and no recipient and no message.
- **It can:**
  - deny service;
  - drop, delay or delete messages, undetectably;
  - say a mailbox is revoked when it is not, which a client checks and does not believe (Section 12.5);
  - keep a revocation from a peer that has never seen it (Section 15.6);
  - replay old messages, which Section 12.3 refuses;
  - serve a sender a stale prekey, but only one the mailbox's signer signed, only until its `not_after`, and not to a sender that has seen a later one;
  - act as a mailbox toward itself;
  - take messages a mailbox's ACL refuses, which the mailbox's client still judges, or refuse messages it admits, which it could drop anyway;
  - lie once in the ACL that a mailbox restored without its own lists adopts, so that a verified sender's messages land as allowed, or messages it could drop anyway are dropped;
  - write anything in an error's words, which a client never shows, or in a receipt, an account id or a cursor, which a client checks against their forms before it uses one (Section 11.3);
  - serve altered instructions to an agent that runs what it is served.
- **It cannot,** against a client that verifies and gates: read messages, forge a sender, reattribute a message, substitute a key (that would need a SHA-384 second preimage or a signature forgery), alter a message undetected, revoke an address, make a pass, or turn a pass into plaintext or a signature on a message.

### 15.3. Forward secrecy

- **Every mailbox has forward secrecy, bounded by how often it renews and deletes,** given a client that deletes retired prekeys (Section 12.4).
  - A message cannot be recovered from the mailbox's keys once every prekey it could be encrypted to is deleted: at the latest its prekey statement's `not_after` plus the TTL, 15 days after that prekey statement was made (Section 6).
  - A client that deletes every retired prekey after a drain that empties its mailbox brings that to its first such drain more than an hour after its next renewal is taken: about a day after the message arrives for a mailbox that renews daily, and about six hours for one that renews every six hours.
  - The hour is for sends in flight: a server takes a send only to the current prekey statement (Section 11.10, step 7), so once a renewal is taken a send to the prekey it replaced is refused, and one the server took before is stored as its request ends, which a list an hour later lists.
  - The bound asks nothing of the server, and keeps no state per peer: the mailbox renews, drains and deletes on its own.
- **A message waiting on the server is exposed until the mailbox fetches it.** A mailbox that stops renewing keeps its ended prekey as its current one, and its last messages stay exposed until it renews.
- **A mailbox that no server takes a pass from keeps its retired prekeys** past their deletion time, until one does: it cannot tell its clock far ahead from a server that refuses it, and deletes by that clock no key a message may wait for.
- **Forward secrecy depends on real deletion.** A key kept in a backup, a synced folder or a store with version history survives its deletion.
- **There is no per-message forward secrecy and no post-compromise security:** version 1 has no ratchet. A leaked prekey reads every message encrypted to it until the mailbox renews and deletes it.
- **Forward secrecy per reply can be added as an optional envelope member** (not yet defined in v1, but not precluded): the envelope is inside the encryption, where the server cannot see it, and a reader ignores a member it does not know (Sections 3.3, 14).

### 15.4. Sender authentication

- **Each message carries its sender's signature inside the encryption,** by the sender's identity or by a working key it certified, verified by the recipient. The signature resists key-compromise impersonation: a leaked prekey lets nobody forge a sender to its mailbox.
- **The signature is bound to its recipient,** by the namespace and the envelope's `to`, so a recipient cannot forward a signed message to a third mailbox as one from its sender, the known flaw of signing before encrypting.
- **It is not deniable toward a recipient:** a recipient can show anyone what a sender signed. That is an audit trail for programs that act on messages, and a privacy cost. The server keeps no message signature and can prove nothing of who sent what.
- **A message a mailbox cannot decrypt has no sender at all:** the server keeps none (Section 13), so a mailbox restored without its prekeys cannot learn whom to ask to send again.
- **A reader decrypts before it verifies.** Until the signature is checked, it handles bytes nobody vouched for: age's, then the envelope's. age is made to take files from anyone, and a refusal is acked whatever its reason, so neither the server nor the sender learns which check failed. Each part of the plaintext has one place, and nothing is searched for (Section 9.2).
- **age to a public key authenticates nobody:** only the signature inside makes a message its sender's. The padding after it is unsigned, and never read.

### 15.5. Stolen keys

- **A stolen identity key is the worst case, and only revoking the address answers it** (Section 10.1).
  - Its thief can sign as the mailbox, with the identity itself or through a certificate of a working key of its own, register and renew prekey statements, and read new messages, until the owner revokes the address.
  - Revoking the address is for good: recovery is a new address, announced out of band.
  - A thief that has the identity can also revoke, which gains it nothing.
  - A mailbox that signs with its identity keeps it beside its other keys, so a copy of the mailbox is a copy of the identity. A mailbox that signs under certificates can keep it elsewhere, at the cost of an identity holder who certifies a new working key at least as often as Section 12.7 requires.
- **A revocation of certificates does not cover the identity's own signature** (Section 10.2). Refusing that signature would stop no thief: whoever has the identity makes a certificate above any revocation as easily. A revocation of certificates answers a stolen working key, and for that needs nothing of the identity's signatures.
- **A lost identity key's address can be revoked only by a revocation record made in advance** (Section 12.5). Without one, the address stops taking messages once its prekey ends, and its correspondents are told the new address out of band.
- **A stolen working key** signs as the mailbox until its certificate ends, if a server delivers the messages, or until its certificate is revoked.
  - A server takes a prekey statement under a certificate no longer than the server's maximum (Section 11.18). A reader takes any certificate within its window, and a certificate no server took signs messages while it lasts, and can register its address at a server that has forgotten it.
  - A renewal under a new certificate ends the key at the server at once, and at every sender that has seen the new certificate.
  - A revocation of its certificate ends it wherever the revocation is seen (Section 12.6).
  - Messages the owner sent under a revoked certificate and not yet read are refused with the thief's.
- **How long a certificate runs is the choice of whoever holds the identity, up to the server's maximum:** a shorter one ends a stolen working key sooner, and needs the identity more often. A certificate is checked at the reader's clock, since nothing vouches for when a signature was made, and a message may wait the TTL before it is read. So a certificate signs messages that every reader takes for the server's maximum less the client's message margin (Section 2.3).
- **A stolen pass,** for the minutes it has left and at that server only:
  - gives the mailbox's metadata and ciphertext;
  - gives control of its availability: deleting unacked messages for good, leaving its account, and setting its ACL, with which it can pause the mailbox until its owner sets the next;
  - gives sending in its name, which verifies at no recipient;
  - never gives plaintext, signs no message and no prekey statement, and cannot revoke the address.

  All of it is within what a hostile server can do (Section 15.2), at the one server the pass names. Hence a pass of ten minutes, kept in a directory only the owner reads.
- **A stolen prekey** reads every message encrypted to it, until the mailbox renews. A sender that has seen no later statement can still be served the old one until its `not_after`, so a mailbox that renews often MAY set a shorter prekey lifetime (Section 12.4).

### 15.6. Revocation's limits

- **A server can keep a revocation from a peer that has never seen it,** answering that it keeps none, and nothing tells the peer otherwise. The lookup before every post narrows that in time and no further: what a client was shown while its server answered truly, it keeps after that server stops. Only a revocation record from the mailbox's owner, handed over out of band, answers a server that hides a revocation record from its first answer.
- **A mailbox checks revocations at its own server alone** (Section 12.5). Asking another server too, such as one running another implementation, would help against a server that withholds a revocation, but only if the revoked mailbox's owner and the reader both use that other server. This version defines no second server.
- **This protocol bounds no time a revocation can go unseen by a peer whose server hides it.** The usual answer is key transparency, a log of what a server serves that clients check, which this protocol does not have.

### 15.7. Clocks

- **A client never sets its clock by a server's `Date`** (Section 12.1): a clock set by a lying server would make an ended certificate valid again and a stale message fresh.
- **A mailbox whose clock is further off than the skew allowance permits is stopped** before it fetches messages it would judge by that clock, prunes its replay set by it, or deletes a prekey by it:
  - in most cases at its pass (`401 pass_expired`);
  - if its clock is more than the skew allowance ahead when it makes a certificate, when the server checks that certificate (`400 prekey_time`).
- **A certificate a server takes need not have begun at every reader.** A message under a certificate that a reader's clock has not reached is refused (`bad_signature`), acked and lost; the skew allowance by which its maker dates a certificate back covers that (Section 5.2).
- **Two clocks whose passes a server takes are less than an hour apart:** a server takes a pass from a mailbox whose clock is less than 10 minutes behind its own, or up to 50 ahead.
- **What this does not cover.** A pass is checked when it is first shown, and a server MAY remember it for its ten minutes (Section 8). Two cases escape:
  - a clock that moves by minutes inside those ten;
  - a clock that goes back by more than an hour in the middle of a drain.

  Either can put a `sent_at` past a reader's skew allowance, or have a reader judge messages by a clock no server took. The reader then refuses the message, and keeps it with what its checks found, its clock among them. Each case needs a clock that is already wrong.
- **The replay floor** costs only messages that a hostile server took a pass for at a clock an honest server refuses, which that server could withhold anyway.

### 15.8. Metadata

- **The server sees the social graph,** times, IP addresses and each message's size to within a padding block. Padding hides a length, not a pattern of traffic: who writes to whom, when and how often. A sender that is not one of the reference clients may pad differently or not at all, and the server cannot tell.
- **The server keeps no sender beside its recipient once a message is stored** (Section 13). It sees the sender at the send, with its IP address and the time, and the message while it is live, and a server that turns hostile can record all of it from then on.
- **The lookup before a post** tells the server that the mailbox is about to be written to, which the post tells it a moment later.
- **A lookup in gzip** has a length that depends on the mailbox's own bytes, so it can help a network observer narrow down whom a sender is about to write to.
- **Whatever terminates TLS for a server is part of it:** it sees what the server sees, and each pass in transit, which for its minutes does at that server what Section 15.5 lists. A third party there would take the server's place for every mailbox whose pass it sees, which is why Section 11.1 keeps TLS inside the server.
- **An address in a URL** is seen wherever URLs are logged: by whoever terminates TLS, in a chat that unfurls links, in a transcript. A leaked address is answered by a new mailbox, and revoking the old address.

### 15.9. Addresses, messages and availability

- **An address is a send capability:** keeping it private is the first defence against unwanted messages. Sending to a mailbox hands it the sender's address, so it can write back.
- **Authentic is not safe.** A signed message from a compromised or prompt-injected sender is genuine. Messages are data, never instructions (Section 12.3), and a mailbox's ACL, whose allow list names the senders it takes, is its defence at the server.
- **An open mailbox can be filled** to its caps, and its intake budget spent (Section 11.10, step 8), by one verified sender within its own limits or by enough verified identities, delaying its other senders' messages. The mailbox's ACL answers one sender: denying it refuses its later sends, and the mailbox drains what it sent (Section 11.14). An allow list is the full defence for a mailbox that knows its senders; a deny list is not, since a new identity is cheap.
- **A hostile server's cost to a reader is bounded** by the reader's drain bounds (Section 12.3). Messages from senders a server made up, which do not decrypt or verify, leave no pin. A sender a server made with keys of its own is a real sender, at an address nobody gave the reader.
- **Silent drops are undetectable;** the protocol has no receipt from the recipient.

### 15.10. Post-quantum

- **Content** is protected against harvest-now-decrypt-later: every prekey is post-quantum.
- **Signatures are classical,** Ed25519 or P-256. A forgery needs a quantum computer at signing time, and the transferable audit trail holds until one exists. A post-quantum signature key would be added as an identity key type inside v1 (Section 14), with a new address for the mailbox that moves to it.
- **The TLS connections are separate,** and protected as their endpoints negotiate.

## 16. Test vectors

`testdata/vectors/` holds the conformance vectors. Each file is one scenario, named for its verdict, with its expected outcome; `testdata/vectors/README.md` describes their format and how each is checked. A conforming client meets every vector of the parts it implements. The crypto and container vectors are checked by `age`, `ssh-keygen` and `sha256sum`, tools this project did not write; their keys are published test keys.

## 17. References

- **[age]** C2SP, "age", https://c2sp.org/age
- **[draft-ietf-sshm-cert]** "OpenSSH certificates", draft-ietf-sshm-cert
- **[PROTOCOL.sshsig]** OpenSSH, `PROTOCOL.sshsig`
- **[AEP-158]**, **[AEP-193]** API Enhancement Proposals: pagination; errors
- **[BIP 173]** Bech32
- **[FIPS 180-4]**, **[FIPS 186-5]**
- **[RFC 2119]**, **[RFC 8174]** BCP 14
- **[RFC 3339]** timestamps · **[RFC 3986]** URIs · **[RFC 4151]** the `tag` URI scheme · **[RFC 4251]**, **[RFC 4253]** SSH · **[RFC 4648]** base64 and base64url · **[RFC 5234]** ABNF · **[RFC 5656]** ECDSA in SSH · **[RFC 6454]** origins · **[RFC 6648]** deprecating the `X-` prefix · **[RFC 7405]** case-sensitive strings in ABNF · **[RFC 7457]** TLS attacks · **[RFC 7493]** I-JSON · **[RFC 7519]** JSON Web Token · **[RFC 8032]** EdDSA · **[RFC 8259]** JSON · **[RFC 8555]** ACME · **[RFC 8594]** the `Sunset` header field · **[RFC 8709]** Ed25519 in SSH · **[RFC 9110]** HTTP semantics · **[RFC 9457]** problem details · **[RFC 9562]** UUIDs · **[RFC 9651]** Structured Field Values · **[RFC 9745]** the `Deprecation` header field
- **[SemVer]** Semantic Versioning 2.0.0, https://semver.org/spec/v2.0.0.html

## Appendix A. Where `ssh-keygen` differs (informative)

`ssh-keygen -Y verify` checks a signature and does not define one: a client built on it checks a signature against Section 3.2 itself. It takes these forms, which Section 3.2 refuses:

- a BEGIN line ending in CRLF;
- whitespace anywhere in the base64;
- other line lengths;
- a NUL before the final LF;
- anything after the END line;
- version 0;
- `sha256`;
- a reserved field that is not empty;
- a string that no signature covers ending in a NUL;
- an ECDSA number with leading zero bytes, up to 2,049 bytes in all.

Its builds also differ from Sections 3.2 and 4.1:

- **An Ed25519 signature's R.** OpenSSH's own Ed25519 from 10.6p1, libsodium's, used where libcrypto has none, refuses the neutral R, and takes R with a point of order 2 or 4 added; it refuses R with a point of order 8 added.
- **An Ed25519 signature's S.** `ssh-keygen` before 10.3p1 on OpenSSL's Ed25519, and before 10.4p1 on its own, takes S + L.
- **An Ed25519 key.** Go's `crypto/ed25519`, and OpenSSH before 10.6p1, take small-order and non-canonical keys, so a client checks them itself.
- **A P-256 key's coordinates.** OpenSSH up to 10.2p1 refuses a coordinate of 128 bits or fewer, or of n − 1 or more, which OpenSSH from 10.3p1 and `golang.org/x/crypto/ssh` take.
- **A P-256 key at all.** OpenSSH built without OpenSSL reads none, so a client built on it does not verify every type Section 4.1 defines.
- **Certificates.** `ssh-keygen -Y verify` takes a certificate with a second principal, a critical option, extensions out of order or given twice, or a validity of `forever`, checks no key id, and knows a serial only through a revocation list, so a client built on it reads those fields from the certificate itself.

## Appendix B. Collected ABNF

Every rule of this document, in alphabetical order, with the rules it takes from other documents as prose (Section 2.2). The core rules of RFC 5234 Appendix B are not repeated.

```abnf
account-id                 = 1*32( %x61-7A / DIGIT / "-" )
acl-entry                  = "*" / address
address                    = address-form "_" 64base64url-char
address-form               = %s"d"   ; the one form this version defines
address-param              = "address" BWS "=" BWS ( address / DQUOTE address DQUOTE )
adopt-key                  = %s"dra1_" ( 1*254( 4base64url-char ) [ 2base64url-char / 3base64url-char ]
                                     / 2base64url-char / 3base64url-char )   ; at most 1,024 bytes
age-recipient              = %s"age" *bech32-hrp-char "1" 6*bech32-char
base64                     = *( 4base64-char ) [ base64-final ]
base64-char                = ALPHA / DIGIT / "+" / "/"
base64-char-by-16          = %s"A" / %s"Q" / %s"g" / %s"w"   ; its value a multiple of 16
base64-char-by-4           = %s"A" / %s"E" / %s"I" / %s"M" / %s"Q" / %s"U" / %s"Y"
                             / %s"c" / %s"g" / %s"k" / %s"o" / %s"s" / %s"w"
                             / "0" / "4" / "8"   ; its value a multiple of 4
base64-final               = 2base64-char base64-char-by-4 "="
                             / base64-char base64-char-by-16 "=="
base64-nonempty            = 1*( 4base64-char ) [ base64-final ] / base64-final
base64-token               = 1*( 4( ALPHA / DIGIT / "+" ) )   ; base64 that is a token
base64url-char             = ALPHA / DIGIT / "-" / "_"
bech32-char                = "0" / %x32-39 / %x61 / %x63-68 / %x6A-6E / %x70-7A
                             ; BIP 173's alphabet: no 1, b, i or o
bech32-hrp-char            = %x21-40 / %x5B-7E   ; printable ASCII but A-Z
BWS                        = <BWS, see [RFC 9110], Section 5.6.3>
capability-name            = %x61-7A *63( %x61-7A / DIGIT / "_" )
content-range-206          = "bytes" SP range-number "-" range-number "/" range-number
cursor                     = 1*( ALPHA / DIGIT / "-" / "_" )
dns-char                   = %x61-7A / DIGIT
dns-label                  = dns-char [ *61( dns-char / "-" ) dns-char ]
dns-name                   = dns-label *( "." dns-label )   ; at most 253 characters
domain                     = domain-label 1*( "." domain-label )
domain-label               = ( ALPHA / DIGIT ) [ *61( ALPHA / DIGIT / "-" ) ( ALPHA / DIGIT ) ]
dromas-version-value       = version-number "." version-number "." version-number
                             [ "-" pre-release ]
envelope-line              = 1*( %x00-09 / %x0B-FF ) LF   ; the envelope (Section 9.1)
full-date                  = <full-date, see [RFC 3339], Section 5.6>
identity-document          = %s"dromas-v1" LF key-type SP base64-nonempty LF
IPv6address                = <IPv6address, see [RFC 3986], Section 3.2.2>
key-name                   = 1*( %x21-2B / %x2D-3F / %x41-7E )   ; printable ASCII, no "," or "@"
key-type                   = key-name [ "@" domain ]   ; at most 64 characters
lowhexdig                  = DIGIT / %x61-66   ; HEXDIG in lowercase
message-id                 = 64lowhexdig
msg-id                     = 8lowhexdig "-" 4lowhexdig "-" 4lowhexdig "-" 4lowhexdig "-"
                             12lowhexdig   ; the UUID of [RFC 9562], Section 4, in lowercase
msg-namespace              = %s"dromas-msg-v1:" address
not-after-param            = "not_after" BWS "=" BWS DQUOTE time DQUOTE
OWS                        = <OWS, see [RFC 9110], Section 5.6.3>
padding                    = *"."   ; Section 9.3
pass-authorization         = %s"dromas-pass" SP
                             %s"address=" DQUOTE address DQUOTE "," SP
                             %s"not_after=" DQUOTE time DQUOTE "," SP
                             %s"signature=" DQUOTE base64-nonempty DQUOTE
pass-credentials           = pass-scheme 1*SP [ pass-gap ] pass-params [ pass-gap ]
pass-gap                   = 1*( OWS "," ) OWS   ; empty list elements skipped
pass-namespace             = %s"dromas-pass-v1:" address
pass-params                = address-param pass-gap not-after-param pass-gap signature-param
                             / address-param pass-gap signature-param pass-gap not-after-param
                             / not-after-param pass-gap address-param pass-gap signature-param
                             / not-after-param pass-gap signature-param pass-gap address-param
                             / signature-param pass-gap address-param pass-gap not-after-param
                             / signature-param pass-gap not-after-param pass-gap address-param
pass-scheme                = "dromas-pass"
pass-statement             = %s"dromas-pass-v1" LF address LF server-name LF time LF
plaintext                  = envelope-line ssh-signature padding
policy-version             = version-number "." version-number [ "." version-number ]
pre-release                = pre-release-id *( "." pre-release-id )
pre-release-id             = version-number
                             / *DIGIT ( ALPHA / "-" ) *( ALPHA / DIGIT / "-" )
prekey-namespace           = %s"dromas-prekey-v1:" address
prekey-statement           = %s"dromas-prekey-v1" LF address LF serial LF
                             age-recipient LF time LF
range-asked                = "bytes=" 1*DIGIT "-"
range-number               = "0" / %x31-39 *DIGIT   ; no leading zero
revocation-of-address      = %s"dromas-revoke-v1" LF address LF
revocation-of-certificates = %s"dromas-revoke-certs-v1" LF address LF serial LF
revoke-certs-namespace     = %s"dromas-revoke-certs-v1:" address
revoke-namespace           = %s"dromas-revoke-v1:" address
serial                     = %x31-39 *15DIGIT   ; no leading zero
server-host                = dns-name / "[" IPv6address "]"   ; in lowercase
server-name                = server-host [ ":" server-port ]
server-port                = %x31-39 *4DIGIT   ; no leading zero, at most 65535
signature-even             = signature-groups LF
signature-groups           = *16( 4base64-char ) ( 4base64-char / base64-final )
signature-line             = 70base64-char LF
signature-odd              = 68base64-char
                             ( 2base64-char LF 2base64-char [ signature-groups ]
                             / 2base64-char LF base64-char-by-4 "="
                             / base64-char base64-char-by-16 LF "==" ) LF
                             ; a group of 4 split across the last two lines
signature-param            = "signature" BWS "=" BWS
                             ( base64-token / DQUOTE base64-nonempty DQUOTE )
signature-text             = *( 2signature-line ) ( signature-even / signature-odd )
                             ; two lines of 70 hold 35 groups of 4
ssh-signature              = %s"-----BEGIN SSH SIGNATURE-----" LF
                             signature-text
                             %s"-----END SSH SIGNATURE-----" LF
statement-hash             = 64lowhexdig
time                       = full-date %s"T" time-hour ":" time-minute ":" time-sec %s"Z"
time-hour                  = <time-hour, see [RFC 3339], Section 5.6>
time-minute                = <time-minute, see [RFC 3339], Section 5.6>
time-sec                   = %x30-35 DIGIT   ; 00 to 59
version-number             = "0" / %x31-39 *DIGIT
```

// Package client is the dromas client library: one mailbox's files,
// and every step of the protocol a mailbox takes, done once so that every client
// built on it behaves the same.
//
// Verification is always on. Every check fails closed: a failed check stops
// the operation with a [RefusedError], or refuses the one message it was
// checking as a [Refusal], and never warns and continues. Message
// bodies are data, never instructions.
//
// Its files, in the stock tools' own file formats where a stock tool reads the
// file (an OpenSSH key, age key files), each under the place its [Layout] names:
// base-dir for what has no other, mail-dir for inbox/, held/, refused/ and sent/,
// rotating-keys-dir for work/ and prekeys/, inbox-dir for inbox/ alone, and
// identity-file for the identity. The base directory is never split:
//
//	identity, identity.pub     the identity key, OpenSSH format: Ed25519 unless the mailbox was made on another type; back it up. It signs certificates and revocations, and what the mailbox says until it has published a statement under a certificate (init --offline, cert switch; client/signer.go)
//	identity.doc, address      the identity document and its SHA-384, the address
//	work/<serial>              working keys, each with its certificate (.pub, -cert.pub): they sign what the mailbox says; never backed up, since a restored mailbox makes new ones
//	prekeys/<serial>.agekey    age keys, each statement signed for one, when it was signed and when its server took it, kept in state; never backed up
//	outbox/, outbox/failed/    envelopes awaiting a receipt, sealed afresh at every post and never kept sealed; ones that can never be delivered
//	inbox/, refused/        accepted messages; refused messages, never delivered, as served, each of the server's strings in base64 that is not of its form (keepRefused): why each was refused, and what the checks established when they judged it, are in state
//	held/                      accepted messages from senders this mailbox has not allowed, as inbox/ keeps it: allow moves a sender's to inbox/, block deletes it
//	partial                    the ciphertext of the message a fetch was reading, as far as it read it: one message's at a time, which state names with how many of its bytes are synced, so a fetch that was cut goes on from there (partial.go). It goes once its message is judged or gone
//	allowed, blocked           the senders whose messages land in inbox/, and those whose messages are dropped: one address to a line, blank lines and lines starting with # skipped. send, alias and allow add to allowed, block to blocked; both are yours to edit
//	sent/                      sent messages the server took: each envelope with the server's receipt
//	aliases                    this mailbox's own names for peers: "<name> <address>" lines
//	gate                       on Windows alone, an empty file whose lock is the mailbox's gate, which a waiting writer holds so that readings that overlap do not hold it back: elsewhere the gate is a flock on the directory itself, where the system has flock (gate_unix.go)
//	state                      a bbolt database of what the client writes about the mailbox itself, each change one transaction (client/store.go): the prekey serial it published last, each prekey statement it signed, when, and when its server took it, the renewal under way and one that failed, a restore's adoption of its server's ACL until it is done, a statement its server served in its name that it did not make, the pass it last signed, the ETag of the last empty poll, the ACL its server last took, and that a sync replaced one holding what no file of the mailbox says until recv reports it, its own revocations once one verifies, what its server's answers last said of its version (a deprecation and a sunset, each with its date) and when it last read its server's document, with the latest version that named, what it pinned of each peer, the replay set and its floor, why each message in refused/ was refused, the address it belongs to, and the policy SetPolicy or a restore recorded. It is the mailbox's lock too: each command that writes the mailbox holds it open from its start to its end, a second process waits for it, and one killed leaves nothing. A base directory on NFS is refused where the system can tell
//
// The mailbox's format is one number for what each of these files holds, the
// state file's records among them, kept in the state file (format.go): every
// command refuses a mailbox of a newer format, a reading one of an older, and a
// command that writes the mailbox brings an older one forward. Where a part is,
// is its Layout's and no format's. From the protocol freeze, a change to what a
// file holds is a new format, with its step and its line here; before it,
// format 1 changes in place;
// each format and what it changed:
//
//	1   the files above, and the state file's buckets as client/store.go lists them
package client

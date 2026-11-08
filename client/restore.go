package client

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
)

// A mailbox's state file may be lost, or put back from a copy, and its
// prekeys/ is never backed up (PROTOCOL.md §12.1). A state that does not match
// prekeys/ holds a replay set older than the keys: those keys open a message
// the mailbox delivered under the state it no longer holds, which a server may
// serve again, and the replay set cannot tell it from one that waited. Each
// command that writes the mailbox reads two signals of that, under the lock
// (hold, matchKeys):
//
//   - a key in prekeys/ that the state names nowhere (unnamedKeys), as after
//     the state is lost, or put back from a copy older than a renewal, with
//     prekeys/ kept;
//   - prekeys/generation ahead of the state's generation, as after the state is
//     put back from a copy older than a message the mailbox accepted. Each
//     message accepted raises the state's generation, in the transaction that
//     records it seen, and then the file takes the count (countOne), so a crash
//     leaves the file at or below the state, never ahead. A count kept per
//     message, not per drain, is behind for a copy taken inside a drain too, as
//     Active Directory's update sequence number counts each update.
//
// Either stops every command that receives, sends, renews or hands over a
// message, before it lists, acks or renews anything (keysMatch), with the
// refusal StateNotTheKeys, which no wait mends. Its owner's acknowledgement,
// dromas init --restore (Options.Restore), adopts the keys found and owes the
// replay floor as an adoption owes it (keyFloorOwed, beginFloor). That is how
// ZFS refuses to import a pool another system last used until its owner runs
// zpool import -f, and how an Active Directory domain controller that detects
// a rollback of its update sequence numbers quarantines itself until its
// administrator acts. A mailbox restored from its identity alone holds neither
// signal, so it needs no acknowledgement and owes no floor.

// StateNotTheKeys is the code of a command refused because the mailbox's state
// does not match its prekeys/ (keysMatch): only its owner's dromas init
// --restore mends it.
const StateNotTheKeys = "state_not_the_keys"

// NotTheKeys reports whether err is a command refused because the mailbox's
// state does not match its prekeys/ (StateNotTheKeys).
func NotTheKeys(err error) bool {
	var re *RefusedError
	return errors.As(err, &re) && re.Code == StateNotTheKeys
}

// keyGeneration is the state's count of the messages the mailbox accepted, in
// the mailbox's own bucket, and generationFile is the same count in prekeys/:
// each a decimal number, absent as 0. A mailbox that counted once a drain, as
// clients before #798 did, reads on from its count: only that it never falls
// matters.
const (
	keyGeneration  = "generation"
	generationFile = "generation"
)

// Restored is what an acknowledged restore found and did (Options.Restore,
// [Mailbox.Restored]): the keys it adopted, and whether the state's generation
// was behind prekeys/generation. A restore that found neither changed nothing.
type Restored struct {
	Adopted    int
	RolledBack bool
}

// Acknowledged reports whether the restore found a state that did not match
// its prekeys/, and so adopted what it found and owes the replay floor.
func (r Restored) Acknowledged() bool { return r.Adopted > 0 || r.RolledBack }

// Restored is what Init's acknowledgement of a restore found and did
// (Options.Restore), or nil where Init was not asked for one.
func (b *Mailbox) Restored() *Restored { return b.restored }

// RestoredBy returns what an acknowledgement of a restore found and did, where
// err is the error of an Init under Options.Restore that failed after the
// acknowledgement committed: the restore stands, and the mailbox owes the
// replay floor. It returns nil for a nil err and for any other error, that of
// an Init whose acknowledgement found nothing to do included.
func RestoredBy(err error) *Restored {
	var ar *afterRestore
	if errors.As(err, &ar) {
		return &ar.r
	}
	return nil
}

// afterRestore is the error of an Init that failed after its acknowledgement
// of a restore committed: err, with what the acknowledgement did.
type afterRestore struct {
	r   Restored
	err error
}

func (e *afterRestore) Error() string { return e.err.Error() }
func (e *afterRestore) Unwrap() error { return e.err }

// keysMatch is the refusal of a command that receives, sends, renews or hands
// over a message, where the command's hold found the state does not match its
// prekeys/ (matchKeys): nil otherwise.
func (b *Mailbox) keysMatch() error { return b.notKeys }

// matchKeys reads the two signals, at every command, under the mailbox's lock
// (hold) and after its folders, so a prekeys/ on a volume not mounted is
// refused first and marks nothing. With a signal, it marks the command refused
// (notKeys) and writes nothing; under Options.Restore it adopts each key it
// found as a retired prekey that prune deletes, owes the replay floor, drops
// the pass the state kept, which may be dated before the last message a lost
// replay set took (beginFloor dates the floor by the next one a server takes),
// and takes the file's generation, all in one transaction. Then, a signal or
// none, it writes prekeys/generation where it is missing or behind the state:
// a missing file is a mailbox that has not counted yet, and owes nothing.
func (b *Mailbox) matchKeys() error {
	// Options.Restore is Init's alone, and acknowledges once: a later command
	// of the Mailbox Init returns is stopped for its owner as any other is.
	restoring := b.restoring
	b.notKeys, b.restored, b.restoring = nil, nil, false
	found, kept, file, there, err := b.keysState()
	if err != nil {
		return err
	}
	ahead := there && file > kept
	switch {
	case restoring:
		r := &Restored{Adopted: len(found), RolledBack: ahead}
		if !r.Acknowledged() {
			b.restored = r
			break
		}
		kept = max(kept, file)
		if err := b.update(func(s store) error {
			for _, n := range found {
				if err := s.put(bucketAdopted, serialKey(n), []byte{}); err != nil {
					return err
				}
			}
			if err := s.del(bucketMailbox, "pass"); err != nil {
				return err
			}
			if err := s.put(bucketMailbox, keyGeneration, strconv.AppendUint(nil, kept, 10)); err != nil {
				return err
			}
			return s.put(bucketMailbox, keyFloorOwed, []byte("1"))
		}); err != nil {
			return err
		}
		b.restored = r
		step("keys adopted")
	case len(found) > 0 || ahead:
		b.notKeys = notTheKeys(len(found), ahead)
		return nil
	}
	if there && file >= kept {
		return nil
	}
	if err := b.writeGeneration(kept); err != nil {
		return err
	}
	step("generation synced")
	return nil
}

// keysState is what the two signals read: the keys in prekeys/ that the state
// names nowhere (unnamedKeys), the state's generation, and the file's, with
// whether the file is there.
func (b *Mailbox) keysState() (found []uint64, kept, file uint64, there bool, err error) {
	if found, err = b.unnamedKeys(); err != nil {
		return nil, 0, 0, false, err
	}
	if err = b.view(func(s store) error {
		kept, err = decimal(s.get(bucketMailbox, keyGeneration), keyGeneration)
		return err
	}); err != nil {
		return nil, 0, 0, false, err
	}
	file, there, err = b.generationOnDisk()
	return found, kept, file, there, err
}

// notMatched is what status says of the two signals, read-only: the refusal a
// command that receives would meet, or nil.
func (b *Mailbox) notMatched() error {
	found, kept, file, there, err := b.keysState()
	if err != nil {
		return err
	}
	if ahead := there && file > kept; len(found) > 0 || ahead {
		return notTheKeys(len(found), ahead)
	}
	return nil
}

// notTheKeys is the refusal of a command that receives, sends, renews or hands
// over a message on a state that does not match its prekeys/: found keys the
// state names nowhere, and the state's generation behind the file's where
// ahead is set.
func notTheKeys(found int, ahead bool) error {
	var why []string
	if found > 0 {
		why = append(why, fmt.Sprintf("prekeys/ holds %d prekeys the state does not name, as after the state is lost, or put back from a copy older than a renewal, with prekeys/ kept", found))
	}
	if ahead {
		why = append(why, "prekeys/generation counts more messages accepted than the state does, as after the state is put back from a copy older than the mailbox")
	}
	return &RefusedError{Code: StateNotTheKeys, Reason: "this mailbox's state does not match its prekeys: " + strings.Join(why, "; and ") + ". Such a state cannot tell a message this mailbox delivered under the state it lost from one that waited, so the mailbox receives, sends, delivers and renews nothing, and acks nothing, until its owner runs dromas init --restore: from then, every message sent before its server next takes one of its calls, and in the hour after, is refused as stale (stale_after_restore)"}
}

// unnamedKeys is each key file in prekeys/ that the state file names nowhere
// (namedKeys): no statement's, no adopted key's, and not the renewal under
// way's, whose key is made before its statement is kept. A file whose name is no
// serial is no key this client made, and it stays where its owner put it; one
// that holds no age key opens nothing, and counts once it holds one.
func (b *Mailbox) unnamedKeys() ([]uint64, error) {
	names, err := matching(b.path("prekeys", "*.agekey"))
	if err != nil {
		return nil, err
	}
	var found []uint64
	// A read: every command comes here, and a write transaction syncs the file
	// even when it changes nothing.
	if err := b.view(func(s store) error {
		named, err := namedKeys(s)
		if err != nil {
			return err
		}
		for _, name := range names {
			if n, ok := serialOfKeyFile(name); ok && !named(n) {
				found = append(found, n)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(found, func(n uint64) bool {
		_, err := readAgeKeys(b.path("prekeys", strconv.FormatUint(n, 10)+".agekey"))
		return err != nil
	}), nil
}

// generationOnDisk is the count prekeys/generation keeps, and whether the file
// is there. It is judged as a prekey file is (checkPrivateFile), and a file
// that is not a decimal number and a line feed is an error, never a count.
func (b *Mailbox) generationOnDisk() (uint64, bool, error) {
	name := b.path("prekeys", generationFile)
	if _, err := os.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err := checkPrivateFile(name); err != nil {
		return 0, false, err
	}
	raw, err := os.ReadFile(name) //nolint:gosec // a path inside the mailbox's own directory
	if err != nil {
		return 0, false, err
	}
	digits, ok := strings.CutSuffix(string(raw), "\n")
	n, perr := strconv.ParseUint(digits, 10, 64)
	if !ok || perr != nil || strconv.FormatUint(n, 10) != digits {
		return 0, false, fmt.Errorf("%s keeps no count of the messages this mailbox accepted: it is a decimal number and a line feed, which this client writes", name)
	}
	return n, true, nil
}

// writeGeneration writes n to prekeys/generation, synced, as writeFile writes
// every file a mailbox keeps.
func (b *Mailbox) writeGeneration(n uint64) error {
	return writeFile(b.path("prekeys", generationFile), append(strconv.AppendUint(nil, n, 10), '\n'))
}

// countOne raises the state's generation by one, in the transaction s that
// records a message as accepted (receiveOne), and returns the count, which
// prekeys/generation takes after that transaction and before the message's
// ack. So the file counts every message the mailbox accepted, a state that
// lacks one is behind it, and a crash between the two leaves the file behind
// the state, which the next command writes again (matchKeys), never ahead of
// it.
func countOne(s store) (uint64, error) {
	kept, err := decimal(s.get(bucketMailbox, keyGeneration), keyGeneration)
	if err != nil {
		return 0, err
	}
	n := kept + 1
	return n, s.put(bucketMailbox, keyGeneration, strconv.AppendUint(nil, n, 10))
}

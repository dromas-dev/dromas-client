package client

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// A mailbox's state file may be lost, or put back from an older copy, and its
// prekeys/ is never backed up (PROTOCOL.md §12.1). Either is a failure of the
// mailbox, and its owner is told once: a key in prekeys/ that the state names
// nowhere (unnamedKeys) stops every command that receives, sends, renews or
// hands over a message, before it lists, acks or renews anything (keysMatch),
// with the refusal RestoreRequired, which no wait mends. Its owner's
// acknowledgement, dromas init --restore (Options.Restore), adopts the keys
// found, and Init then renews. That is how ZFS refuses to import a pool another
// system last used until its owner runs zpool import -f.
//
// The recovery accepts the failure's cost and does nothing more: a state lost
// or put back has lost the replay set it held, so a server may deliver again a
// message the mailbox accepted under that state, while protocol.TTL of its
// sent_at lasts (PROTOCOL.md §15.9). A copy older than a message the mailbox
// accepted, but no older than its last renewal, holds every key and stops
// nothing. A mailbox restored from its identity alone holds no key, so it
// needs no acknowledgement.

// RestoreRequired is the code of a command refused because prekeys/ holds a
// key the mailbox's state names nowhere (keysMatch): only its owner's dromas
// init --restore mends it.
const RestoreRequired = "restore_required"

// NotTheKeys reports whether err is a command refused because the mailbox's
// state does not match its prekeys/ (RestoreRequired).
func NotTheKeys(err error) bool {
	var re *RefusedError
	return errors.As(err, &re) && re.Code == RestoreRequired
}

// Restored is what an acknowledged restore found and did (Options.Restore,
// [Mailbox.Restored]): the keys it adopted. A restore that adopted none
// changed nothing.
type Restored struct {
	Adopted int
}

// Acknowledged reports whether the restore found keys the state named nowhere,
// and so adopted them.
func (r Restored) Acknowledged() bool { return r.Adopted > 0 }

// Restored is what Init's acknowledgement of a restore found and did
// (Options.Restore), or nil where Init was not asked for one.
func (b *Mailbox) Restored() *Restored { return b.restored }

// RestoredBy returns what an acknowledgement of a restore found and did, where
// err is the error of an Init under Options.Restore that failed after the
// acknowledgement committed: the keys it adopted stay adopted. It returns nil
// for a nil err and for any other error, that of an Init whose acknowledgement
// found nothing to do included.
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
// over a message, where the command's hold found a key the state names nowhere
// (matchKeys): nil otherwise.
func (b *Mailbox) keysMatch() error { return b.notKeys }

// matchKeys looks for keys the state names nowhere, at every command, under the
// mailbox's lock (hold) and after its folders, so a prekeys/ on a volume not
// mounted is refused first and marks nothing. With one, it marks the command
// refused (notKeys) and writes nothing; under Options.Restore it adopts each
// key it found as a retired prekey that prune deletes, in one transaction.
func (b *Mailbox) matchKeys() error {
	// Options.Restore is Init's alone, and acknowledges once: a later command
	// of the Mailbox Init returns is stopped for its owner as any other is.
	restoring := b.restoring
	b.notKeys, b.restored, b.restoring = nil, nil, false
	found, err := b.unnamedKeys()
	if err != nil {
		return err
	}
	switch {
	case restoring:
		b.restored = &Restored{Adopted: len(found)}
		if len(found) == 0 {
			return nil
		}
		if err := b.update(func(s store) error {
			for _, n := range found {
				if err := s.put(bucketAdopted, serialKey(n), []byte{}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		step("keys adopted")
	case len(found) > 0:
		b.notKeys = notTheKeys(len(found))
	}
	return nil
}

// notMatched is what status says of the keys the state names nowhere,
// read-only: the refusal a command that receives would meet, or nil.
func (b *Mailbox) notMatched() error {
	found, err := b.unnamedKeys()
	if err != nil || len(found) == 0 {
		return err
	}
	return notTheKeys(len(found))
}

// notTheKeys is the refusal of a command that receives, sends, renews or hands
// over a message where prekeys/ holds found keys the state names nowhere.
func notTheKeys(found int) error {
	return &RefusedError{Code: RestoreRequired, Reason: fmt.Sprintf("this mailbox's state does not match its prekeys: prekeys/ holds %d prekeys the state does not name, as after the state is lost, or put back from a copy older than a renewal. The mailbox receives, sends, delivers and renews nothing until its owner runs dromas init --restore, which adopts those prekeys and renews. A state lost or put back has lost its replay set, so a message the mailbox accepted before may be delivered again", found)}
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

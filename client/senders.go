package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
)

// A mailbox says who may write to it (PROTOCOL.md §12.3, §11.14): the senders it
// allows, whose messages land in inbox/; the senders it blocks, whose messages it
// drops; and, in its policy, where the rest's messages land and whether its
// server refuses them. A message lands by a verified sender only, after every
// check of judge: a sender the mailbox has not allowed is held, never refused,
// since it proved who it is. Its server holds the mailbox's ACL, which the mailbox
// makes from the same files and sends whenever they change, so that the
// server refuses what the mailbox would drop.

// The values of a mailbox's policy.
const (
	// LandHeld and LandInbox are where a message from a sender the mailbox has not
	// allowed lands: held/ (the default), or inbox/ as any other.
	LandHeld  = "held"
	LandInbox = "inbox"
	// AdmitAll and AdmitAllowed are whom the mailbox's server takes messages from:
	// every sender the mailbox has not blocked (the default), or only those it
	// has allowed.
	AdmitAll     = "all"
	AdmitAllowed = "allowed"
)

// Policy is a mailbox's policy. A field left empty takes the next source's
// ([Mailbox.Policy]).
type Policy struct {
	// Unknown is where a message from a sender the mailbox has not allowed lands:
	// LandHeld or LandInbox.
	Unknown string `json:"unknown"`
	// Server is whom the mailbox's server takes messages from: AdmitAll or
	// AdmitAllowed.
	Server string `json:"server"`
}

// DefaultPolicy is the policy of a mailbox nothing has set one for: a message
// from a sender the mailbox has not allowed is held, and the server refuses
// only senders the mailbox has blocked.
func DefaultPolicy() Policy { return Policy{Unknown: LandHeld, Server: AdmitAll} }

func (p Policy) check() error {
	if (p.Unknown != LandHeld && p.Unknown != LandInbox) || (p.Server != AdmitAll && p.Server != AdmitAllowed) {
		return errors.New(`a policy is "unknown held" or "unknown inbox", and "server all" or "server allowed"`)
	}
	return nil
}

// checkSet holds a policy whose fields may be left empty to the values there are.
func (p Policy) checkSet() error {
	if (p.Unknown != "" && p.Unknown != LandHeld && p.Unknown != LandInbox) || (p.Server != "" && p.Server != AdmitAll && p.Server != AdmitAllowed) {
		return errors.New(`a policy's unknown is "held" or "inbox", and its server "all" or "allowed"`)
	}
	return nil
}

// keyPolicy is what SetPolicy or a restore recorded, in the mailbox's bucket.
const keyPolicy = "policy"

// Policy is the mailbox's policy, each field from the first that says it: what
// its owner set (Options.Policy), what SetPolicy or a restore adopting its
// server's ACL recorded in the state file, and the default.
func (b *Mailbox) Policy(ctx context.Context) (Policy, error) {
	return reading(ctx, b, b.policy)
}

// policy is Policy, read under the reading or command that holds the mailbox.
func (b *Mailbox) policy() (Policy, error) {
	p := DefaultPolicy()
	if err := b.view(func(s store) error {
		raw := s.get(bucketMailbox, keyPolicy)
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &p)
	}); err != nil {
		return p, err
	}
	if b.policySet.Unknown != "" {
		p.Unknown = b.policySet.Unknown
	}
	if b.policySet.Server != "" {
		p.Server = b.policySet.Server
	}
	return p, nil
}

// recordPolicy keeps p in the state file.
func (b *Mailbox) recordPolicy(p Policy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return b.update(func(s store) error { return s.put(bucketMailbox, keyPolicy, raw) })
}

// SetPolicy records the mailbox's policy in its state file, under what its owner
// set in Options.Policy. The server learns what it changes at the mailbox's next
// send, recv, allow or block ([Mailbox.SyncACL]).
func (b *Mailbox) SetPolicy(ctx context.Context, p Policy) error {
	if err := p.check(); err != nil {
		return err
	}
	return b.lockedFinishedDo(ctx, func() error { return b.recordPolicy(p) })
}

// addresses reads one of the mailbox's address lists, allowed or blocked: an
// address to a line, with blank lines and lines starting with "#" skipped,
// as OpenSSH's list files' are (#181). Any other line
// is an error, a "#" after an address on its line among them.
func (b *Mailbox) addresses(name string) ([]protocol.Address, error) {
	data, err := os.ReadFile(b.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []protocol.Address
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		a, err := protocol.ParseAddress(line)
		if err != nil {
			return nil, refuse("%s, line %d: not an address (\"d_\" and 64 characters of base64url)", b.path(name), i+1)
		}
		out = append(out, a)
	}
	return out, nil
}

// addTo adds an address to one of the mailbox's address lists, unless it is
// there, and reports whether it added it: a line after what the file holds,
// the whole file written beside it and moved into place (writeFile), so a
// crash leaves the list as it was or with the line, never a line cut short,
// which would stop every drain and send until it was put right.
func (b *Mailbox) addTo(name string, a protocol.Address) (bool, error) {
	n, err := b.addAll(name, []protocol.Address{a})
	return n == 1, err
}

// addAll is addTo of each address in addrs, in one write, and returns how
// many it added.
func (b *Mailbox) addAll(name string, addrs []protocol.Address) (int, error) {
	have, err := b.addresses(name)
	if err != nil {
		return 0, err
	}
	seen := make(map[protocol.Address]bool, len(have)+len(addrs))
	for _, a := range have {
		seen[a] = true
	}
	var lines []byte
	n := 0
	for _, a := range addrs {
		if seen[a] {
			continue
		}
		seen[a] = true
		lines = append(lines, a.String()+"\n"...)
		n++
	}
	if n == 0 {
		return 0, nil
	}
	data, err := os.ReadFile(b.path(name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n') // a line someone wrote without its end
	}
	return n, writeFile(b.path(name), append(data, lines...))
}

// Allowed returns the senders whose messages land in inbox/ whatever the
// policy says: those the allowed file lists and those an alias names.
func (b *Mailbox) Allowed(ctx context.Context) ([]protocol.Address, error) {
	return reading(ctx, b, b.allowed)
}

// allowed is Allowed, read under the reading or command that holds the mailbox.
func (b *Mailbox) allowed() ([]protocol.Address, error) {
	allowed, err := b.addresses("allowed")
	if err != nil {
		return nil, err
	}
	as, err := b.aliases()
	if err != nil {
		return nil, err
	}
	for _, a := range as {
		allowed = append(allowed, a.Address)
	}
	return protocol.NewACLList(false, allowed).Addrs, nil
}

// Blocked returns the senders whose messages this mailbox drops, and its server
// refuses.
func (b *Mailbox) Blocked(ctx context.Context) ([]protocol.Address, error) {
	return reading(ctx, b, b.blocked)
}

// blocked is Blocked, read under the reading or command that holds the mailbox.
func (b *Mailbox) blocked() ([]protocol.Address, error) {
	blocked, err := b.addresses("blocked")
	if err != nil {
		return nil, err
	}
	return protocol.NewACLList(false, blocked).Addrs, nil
}

// Where a message from a verified sender lands.
const (
	landInbox = iota
	landHeld
	landBlocked
)

// senders is what a drain reads once of the mailbox's say over its senders.
type senders struct {
	policy           Policy
	allowed, blocked protocol.ACLList
}

func (b *Mailbox) readSenders() (*senders, error) {
	p, err := b.policy()
	if err != nil {
		return nil, err
	}
	allowed, err := b.allowed()
	if err != nil {
		return nil, err
	}
	blocked, err := b.blocked()
	if err != nil {
		return nil, err
	}
	return &senders{policy: p, allowed: protocol.ACLList{Addrs: allowed}, blocked: protocol.ACLList{Addrs: blocked}}, nil
}

// land says where a message from a verified sender lands: dropped if the mailbox
// blocked it, which wins over allowing it; inbox/ if the mailbox allowed it or
// its policy lands every sender's there; otherwise held/.
func (s *senders) land(from protocol.Address) int {
	switch {
	case s.blocked.Has(from):
		return landBlocked
	case s.allowed.Has(from) || s.policy.Unknown == LandInbox:
		return landInbox
	}
	return landHeld
}

// UnknownSender describes the messages held/ keeps from one sender.
type UnknownSender struct {
	From protocol.Address `json:"from"`
	// IDs name its messages in held/, oldest first by sent_at: held/<id>.json
	// is the line recv would have printed.
	IDs         []string `json:"ids"`
	FirstSentAt string   `json:"first_sent_at"`
	LastSentAt  string   `json:"last_sent_at"`
}

// keptMessage is a line this client kept in inbox/ or held/, read back.
type keptMessage struct {
	ID     string
	From   protocol.Address
	SentAt string
	Env    *protocol.Envelope
	Line   []byte
	kept   time.Time // when the drain wrote it: the order it arrived in, among messages sent in one second, where the file system's clock tells them apart
}

// keptMessages reads dir, inbox/ or held/: each message this client kept there,
// oldest first by sent_at, then in the order they were kept, then by id. Two
// files written within one tick of the file system's clock share a
// modification time (Windows' ticks about every 15.6 ms), so the order
// within a second is the order recv kept them only where their times
// differ; the id makes it the same on every run. A file that is not one of
// this client's lines ([Mailbox.keptLine]) is skipped.
func (b *Mailbox) keptMessages(dir string) ([]keptMessage, error) {
	names, err := matching(b.path(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []keptMessage
	for _, n := range names {
		id := strings.TrimSuffix(filepath.Base(n), ".json")
		if !hexID.MatchString(id) {
			continue
		}
		if err := b.judgeKept(n); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(n) //nolint:gosec // a file of this mailbox's inbox/ or held/
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(n)
		if err != nil {
			return nil, err
		}
		line, from, env, ok := b.keptLine(data)
		if !ok {
			continue
		}
		out = append(out, keptMessage{ID: id, From: from, SentAt: env.SentAt.UTC().Format(protocol.TimeLayout), Env: env, Line: line, kept: fi.ModTime()})
	}
	slices.SortStableFunc(out, func(x, y keptMessage) int {
		if c := strings.Compare(x.SentAt, y.SentAt); c != 0 {
			return c
		}
		if c := x.kept.Compare(y.kept); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	return out, nil
}

// HeldMessages returns the messages held/ keeps, by sender, the sender with the oldest
// first. It reads only held/, and calls no server.
func (b *Mailbox) HeldMessages(ctx context.Context) ([]UnknownSender, error) {
	return reading(ctx, b, there(b, b.heldMessages))
}

// heldMessages is HeldMessages, read under the reading or command that holds the mailbox.
func (b *Mailbox) heldMessages() ([]UnknownSender, error) {
	ms, err := b.keptMessages("held")
	if err != nil {
		return nil, err
	}
	var out []UnknownSender
	at := map[protocol.Address]int{}
	for _, m := range ms {
		i, ok := at[m.From]
		if !ok {
			i = len(out)
			at[m.From] = i
			out = append(out, UnknownSender{From: m.From, FirstSentAt: m.SentAt})
		}
		out[i].IDs = append(out[i].IDs, m.ID)
		out[i].LastSentAt = m.SentAt
	}
	return out, nil
}

// Allow allows a sender: its messages land in inbox/ from now on, whatever the
// policy says, and what held/ keeps from it moves there. A sender the mailbox
// blocks is refused, with nothing done: blocking wins. The moved messages
// are returned as Receive delivers them, oldest first, but for Plaintext and
// Signature, which the mailbox never kept. Then the server is
// sent the mailbox's ACL, if the change is one it holds ([Mailbox.SyncACL]): an
// error from that leaves the sender allowed here.
func (b *Mailbox) Allow(ctx context.Context, a protocol.Address) ([]Delivery, error) {
	return locked(ctx, b, func() ([]Delivery, error) {
		if err := b.keysMatch(); err != nil {
			return nil, err
		}
		moved, err := b.allow(a)
		if err != nil {
			return moved, err
		}
		return moved, b.syncACL(ctx)
	})
}

func (b *Mailbox) allow(a protocol.Address) ([]Delivery, error) {
	// Blocking wins: allowing a sender the mailbox blocks would move its messages to
	// inbox/ while its later messages are dropped, so nothing is done (#179).
	blocked, err := b.blocked()
	if err != nil {
		return nil, err
	}
	if slices.Contains(blocked, a) {
		return nil, refuse("%s is blocked, and blocking wins, so nothing is allowed or moved: delete its line in %s first", a, b.path("blocked"))
	}
	if _, err := b.addTo("allowed", a); err != nil {
		return nil, err
	}
	step("sender allowed")
	ms, err := b.keptMessages("held")
	if err != nil {
		return nil, err
	}
	var moved []Delivery
	for _, m := range ms {
		if m.From != a {
			continue
		}
		// Written and then removed, since inbox-dir may be on another
		// filesystem: a crash between leaves it in both, and the next allow
		// finishes.
		if err := b.judgeKept(b.path("held", m.ID+".json")); err != nil {
			return moved, err
		}
		raw, err := os.ReadFile(b.path("held", m.ID+".json")) //nolint:gosec // a path inside the mailbox's own directory
		if err != nil {
			return moved, err
		}
		if err := writeFile(b.path("inbox", m.ID+".json"), raw); err != nil {
			return moved, err
		}
		step("held message written")
		if err := os.Remove(b.path("held", m.ID+".json")); err != nil {
			return moved, err
		}
		step("held message removed")
		moved = append(moved, Delivery{ID: m.ID, Envelope: m.Env, Line: m.Line})
	}
	if len(moved) == 0 {
		return nil, nil
	}
	return moved, syncDir(b.path("held"))
}

// Block blocks a sender: its messages are dropped from now on, what its server
// holds from it at the next drain among it, and its server refuses its later
// sends; what held/ keeps from it is deleted. It returns how many held messages it deleted. Blocking
// wins over allowing. An error sending the server the mailbox's ACL leaves the
// sender blocked here, and the next send, recv, allow or block sends it.
func (b *Mailbox) Block(ctx context.Context, a protocol.Address) (int, error) {
	return locked(ctx, b, func() (int, error) {
		n, err := b.block(a)
		if err != nil {
			return n, err
		}
		return n, b.syncACL(ctx)
	})
}

func (b *Mailbox) block(a protocol.Address) (int, error) {
	if _, err := b.addTo("blocked", a); err != nil {
		return 0, err
	}
	ms, err := b.keptMessages("held")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range ms {
		if m.From != a {
			continue
		}
		if err := removeIfExists(b.path("held", m.ID+".json")); err != nil {
			return n, err
		}
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, syncDir(b.path("held"))
}

// RebuildAllowed adds back to the allowed file every address the mailbox has
// an alias for or has sent messages to, as sent/ names them, and returns how
// many it added. What the file lists stays. It is for a mailbox whose allowed
// file was lost: send, alias and allow keep it otherwise.
func (b *Mailbox) RebuildAllowed(ctx context.Context) (int, error) {
	return lockedFinished(ctx, b, b.rebuildAllowed)
}

func (b *Mailbox) rebuildAllowed() (int, error) {
	as, err := b.aliases()
	if err != nil {
		return 0, err
	}
	var addrs []protocol.Address
	for _, a := range as {
		addrs = append(addrs, a.Address)
	}
	sent, err := matching(b.path("sent", "*.json"))
	if err != nil {
		return 0, err
	}
	for _, n := range sent {
		to, _, _ := strings.Cut(filepath.Base(n), ".")
		if a, err := protocol.ParseAddress(to); err == nil {
			addrs = append(addrs, a)
		}
	}
	added := 0
	for _, a := range protocol.NewACLList(false, addrs).Addrs {
		ok, err := b.addTo("allowed", a)
		if err != nil {
			return added, err
		}
		if ok {
			added++
		}
	}
	return added, nil
}

// wantACL is the ACL the mailbox's files say its server should hold: it denies
// the senders the mailbox blocked, and allows every other sender, or under
// "server allowed" only those the mailbox allowed.
func (b *Mailbox) wantACL() (*protocol.ACL, error) {
	s, err := b.readSenders()
	if err != nil {
		return nil, err
	}
	acl := &protocol.ACL{Allow: protocol.ACLList{Any: true}, Deny: s.blocked}
	if s.policy.Server == AdmitAllowed {
		acl.Allow = s.allowed
	}
	return acl, nil
}

// tookACL is the ACL the mailbox's server last took, as it answered, which the
// mailbox keeps; nil if it has taken none.
func (b *Mailbox) tookACL() (*protocol.ACL, error) {
	var data []byte
	if err := b.view(func(s store) error { data = s.get(bucketMailbox, "acl"); return nil }); err != nil || data == nil {
		return nil, err
	}
	var d protocol.ACLDocument
	if err := protocol.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("the ACL the mailbox's state file keeps: %w", err)
	}
	acl, err := protocol.CheckACL(&d, 0)
	if err != nil {
		return nil, fmt.Errorf("the ACL the mailbox's state file keeps: %w", err)
	}
	return acl, nil
}

// ACLPending reports whether the mailbox's files say an ACL other than the one
// its server last took: the next send, recv, allow or block sends it.
func (b *Mailbox) ACLPending(ctx context.Context) (bool, error) {
	return reading(ctx, b, b.aclPending)
}

// aclPending is ACLPending inside a command or a reading.
func (b *Mailbox) aclPending() (bool, error) {
	want, err := b.wantACL()
	if err != nil {
		return false, err
	}
	took, err := b.tookACL()
	if err != nil {
		return false, err
	}
	return !want.SameLists(took), nil
}

// aclWhy says, in this client's words, what changed where the ACL the mailbox's
// files say is not the one its server last took, as the mailbox knows it: its
// server holds none this mailbox sent, having taken none or none since it
// registered the mailbox anew (sendRenewal); or the mailbox's own lists
// changed.
func (b *Mailbox) aclWhy() (string, error) {
	took, err := b.tookACL()
	if err != nil {
		return "", err
	}
	if took == nil {
		return "this mailbox's server holds no ACL this mailbox sent: it has taken none, or none since it registered the mailbox anew", nil
	}
	return "this mailbox's lists have changed since its server last took its ACL", nil
}

// SyncACL sends the mailbox's server the ACL the mailbox's files say, unless that
// is the one it took last, which is the case for a mailbox that has blocked
// nobody and lets every sender reach the server: such a mailbox sends none, and
// its server holds none. Each one sent replaces whatever the server holds,
// whoever wrote it, and carries no If-Match (PROTOCOL.md §11.14): the mailbox's
// files are what its ACL is, so what another machine of the mailbox sent is
// replaced, not merged, and a precondition would only add a read and a 412
// to end the same way. One the server answers concurrent_modification (the mailbox changed
// while it handled the request) is sent again as it is, once. The server's
// answer must be the ACL sent, and is kept in the mailbox's state. It reads
// nothing of the server first, so it does not see an ACL another writer put
// there since: [Mailbox.Receive] reads that at each renewal it makes (checkACL).
func (b *Mailbox) SyncACL(ctx context.Context) error {
	return b.lockedDo(ctx, func() error { return b.syncACL(ctx) })
}

func (b *Mailbox) syncACL(ctx context.Context) error {
	want, err := b.wantACL()
	if err != nil {
		return err
	}
	took, err := b.tookACL()
	if err != nil {
		return err
	}
	if want.SameLists(took) {
		return nil
	}
	return b.sendACL(ctx, want)
}

// checkACL is syncACL with the ACL the mailbox's server holds read first, as
// Receive does after each renewal it makes, about once a day: another writer
// may have put one since this mailbox last sent its own, another machine of
// the mailbox, whoever holds its pass (PROTOCOL.md §15.5) or the server
// itself, and syncACL, which compares the mailbox's files with what it last
// sent, would never see it. It reads GET …/acl and compares the lists with
// those the mailbox's files make, never a tag, whose making is the server's
// own; where they differ it sends the mailbox's own, as syncACL does, with no
// If-Match, and reports whether the server held an ACL other than the one
// this mailbox last sent. A read that fails is no answer: the mailbox sends
// what syncACL would. A heal that did not happen, the read or the put
// failed, is made at the next renewal, so another writer's ACL can last a day
// more: a failure's cost (#832).
func (b *Mailbox) checkACL(ctx context.Context) (replaced bool, err error) {
	held, err := b.heldACL(ctx)
	if err != nil {
		return false, b.syncACL(ctx)
	}
	want, err := b.wantACL()
	if err != nil {
		return false, err
	}
	took, err := b.tookACL()
	if err != nil {
		return false, err
	}
	switch {
	case !want.SameLists(held):
		if err := b.sendACL(ctx, want); err != nil {
			return false, err
		}
		return !held.SameLists(took), nil
	case !want.SameLists(took):
		// The server already holds what the mailbox's files say: it is kept as
		// taken, and nothing is sent.
		return false, b.keepTook(held)
	}
	return false, nil
}

// heldACL reads the ACL the mailbox's server holds: one allowing everyone and
// denying nothing for a mailbox that has put none.
func (b *Mailbox) heldACL(ctx context.Context) (*protocol.ACL, error) {
	var d protocol.ACLDocument
	if _, err := b.owner(ctx, http.MethodGet, b.mailboxPath("/acl"), nil, &d); err != nil {
		return nil, err
	}
	return protocol.CheckACL(&d, protocol.MaxACLEntries)
}

// sendACL sends the mailbox's ACL, want, and keeps the server's answer as the
// ACL it took: once more after a concurrent_modification, as it is.
func (b *Mailbox) sendACL(ctx context.Context, want *protocol.ACL) error {
	got, err := b.putACL(ctx, want)
	if code(err) == protocol.CodeConcurrentModification {
		got, err = b.putACL(ctx, want)
	}
	if err != nil {
		why, werr := b.aclWhy()
		if werr != nil {
			return errors.Join(fmt.Errorf("the server did not take this mailbox's ACL: %w", err), werr)
		}
		return fmt.Errorf("the server did not take this mailbox's ACL, sent where %s: %w", why, err)
	}
	return b.keepTook(got)
}

// keepTook keeps acl as the ACL the mailbox's server last took.
func (b *Mailbox) keepTook(acl *protocol.ACL) error {
	data, err := json.Marshal(acl.Document())
	if err != nil {
		return err
	}
	return b.update(func(s store) error { return s.put(bucketMailbox, "acl", data) })
}

// putACL sends an ACL, and returns the server's answer, which must be that
// ACL.
func (b *Mailbox) putACL(ctx context.Context, acl *protocol.ACL) (*protocol.ACL, error) {
	var d protocol.ACLDocument
	if _, err := b.owner(ctx, http.MethodPut, b.mailboxPath("/acl"), acl.Document(), &d); err != nil {
		return nil, err
	}
	got, err := protocol.CheckACL(&d, protocol.MaxACLEntries)
	if err != nil || !got.SameLists(acl) {
		return nil, errors.New("the server answered with another ACL than the one sent")
	}
	return got, nil
}

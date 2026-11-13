package client

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dromas-dev/dromas-client/protocol"
)

// SendGroup sends one message to a group: to is every member but this
// mailbox, and the envelope's group is every member, this mailbox among them
// (PROTOCOL.md §9.1, §12.2). Each other member gets an outbox entry of its
// own, with to naming that member, all with one msg_id, one group and one
// body, every entry written before any is posted; each post is then sealed
// for that member alone, as [Mailbox.SendWith] seals one. Writing to the
// members allows each.
//
// Nothing is written where a member is one this mailbox holds a revocation
// of, or where the group needs more sends than the budget at the mailbox's
// server ever holds at once, as its last send's answer reported it.
//
// The entries go as one unit: where the mailbox's estimate of its budget
// (from the Dromas-RateLimit of its sends' answers) shows that the entries
// still to go do not fit, they wait together, untried, until they do, and
// [Flushed.Queued] says until when; the estimate is checked again after each
// post. A refusal about one member leaves that member's entry waiting; one
// about this mailbox or the path to its server leaves every entry not yet
// tried waiting, as a flush does ([Mailbox.FlushReport]). recv --follow
// posts what waits once it is due.
func (b *Mailbox) SendGroup(ctx context.Context, to []protocol.Address, body string, f Fields) (*Flushed, error) {
	return locked(ctx, b, func() (*Flushed, error) {
		if err := b.keysMatch(); err != nil {
			return nil, err
		}
		members, err := b.groupOf(to)
		if err != nil {
			return nil, err
		}
		env, err := protocol.NewEnvelope(b.id.Address(), members[0], body, b.now())
		if err != nil {
			return nil, err
		}
		return b.sendGroup(ctx, members, env, f)
	})
}

// SendGroupJSON is [Mailbox.SendGroup] with a body that is one JSON value, as
// [Mailbox.SendJSON] is [Mailbox.SendWith].
func (b *Mailbox) SendGroupJSON(ctx context.Context, to []protocol.Address, body []byte, f Fields) (*Flushed, error) {
	return locked(ctx, b, func() (*Flushed, error) {
		if err := b.keysMatch(); err != nil {
			return nil, err
		}
		members, err := b.groupOf(to)
		if err != nil {
			return nil, err
		}
		env, err := protocol.NewJSONEnvelope(b.id.Address(), members[0], body, b.now())
		if err != nil {
			return nil, err
		}
		return b.sendGroup(ctx, members, env, f)
	})
}

// groupOf is the other members of a group sent to as to: each once, this
// mailbox left out, and at least one.
func (b *Mailbox) groupOf(to []protocol.Address) ([]protocol.Address, error) {
	var members []protocol.Address
	for _, a := range to {
		if a != b.id.Address() && !slices.Contains(members, a) {
			members = append(members, a)
		}
	}
	if len(members) == 0 {
		return nil, refuse("a group needs a member besides this mailbox")
	}
	return members, nil
}

func (b *Mailbox) sendGroup(ctx context.Context, members []protocol.Address, env *protocol.Envelope, f Fields) (*Flushed, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	for _, m := range members {
		if held, err := b.revoked(m); err != nil {
			return nil, err
		} else if held {
			return nil, fmt.Errorf("nothing is written or sent to the group: %w", &RevokedError{Address: m})
		}
	}
	// A group the budget can never hold at once is refused: it would wait
	// for good.
	var g *budget
	if err := b.view(func(s store) error { g = readBudget(s); return nil }); err != nil {
		return nil, err
	}
	if g != nil && int64(len(members)) > g.Cap {
		return nil, refuse("a message to %d other members needs %d sends at once, and this mailbox's server lets it make %d at most: nothing is written or sent", len(members), len(members), g.Cap)
	}
	if err := env.SetGroup(members); err != nil {
		return nil, err
	}
	env.Subject, env.InReplyTo, env.Supersedes = f.Subject, f.InReplyTo, f.Supersedes
	env.Identity = b.id.Document()
	// Every member's entry, each read back as its reader will read it,
	// before any is kept, then every entry kept before any post.
	lines := make([][]byte, len(members))
	for i, m := range members {
		e := *env
		e.To = m
		p, err := e.Line()
		if err != nil {
			return nil, err
		}
		var bad *protocol.EnvelopeError
		if _, err := protocol.OpenEnvelopeRecord(p, b.id.Address(), m); errors.As(err, &bad) {
			return nil, refuse("nothing is written or sent, since its reader would refuse this message: %s", bad.Why)
		} else if err != nil {
			return nil, err
		}
		lines[i] = p
	}
	names := make([]string, len(members))
	for i, m := range members {
		if _, err := b.addTo("allowed", m); err != nil {
			return nil, err
		}
		stem := b.path("outbox", m.String()+"."+env.MsgID)
		if err := writeFile(stem+".P", lines[i]); err != nil {
			return nil, err
		}
		names[i] = stem + ".P"
	}
	step("group written")
	aclErr := b.syncACL(ctx)
	if gone := ownRevoked(aclErr); gone != nil {
		var errs []error
		for _, n := range names {
			errs = append(errs, b.undelivered(n[:len(n)-len(".P")], gone))
		}
		return nil, errors.Join(errs...)
	}
	out := &Flushed{}
	errs, _ := b.sendUnit(ctx, names, newLookups(), aclErr, out)
	return out, errors.Join(errs...)
}

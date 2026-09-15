package main

import (
	"context"
	"fmt"
	"math"
	"math/big"

	"github.com/nutzwtf/nutz-verify/internal/chain"
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// searchSpan is how far back the first page of a backwards RootPosted search reaches:
// about two hours of chain 4663 at ~190 ms a block, which covers the usual case of the
// previous Root having been posted an hour ago. Each further page doubles.
const searchSpan = 40_000

// book memoizes the Distributor's ledger() reads for one run, all pinned to one block.
//
// The tip rather than each Root's own block, and that is safe: funded[] cannot change
// once a Root is posted (notifyEpochFunding requires the Epoch to be past the mark), a
// Void keeps the funding and clears the Root, and a Skip keeps the funding too. What the
// tip says about funding is what every Root was posted against, and reading everything at
// one block is what makes the reads comparable.
type book struct {
	reader  *chain.Reader
	at      uint64
	ledgers map[uint64]chain.Ledger
}

func newBook(reader *chain.Reader, at uint64) *book {
	return &book{reader: reader, at: at, ledgers: map[uint64]chain.Ledger{}}
}

func (b *book) ledger(ctx context.Context, id uint64) (chain.Ledger, error) {
	if l, ok := b.ledgers[id]; ok {
		return l, nil
	}

	l, err := b.reader.Ledger(ctx, chain.KindEpoch, id, b.at)
	if err != nil {
		return chain.Ledger{}, err
	}
	b.ledgers[id] = l

	return l, nil
}

// addFunding is carry plus the Epoch's funding, as a new value.
func (b *book) addFunding(ctx context.Context, carry chain.Amounts, id uint64) (chain.Amounts, error) {
	l, err := b.ledger(ctx, id)
	if err != nil {
		return chain.Amounts{}, err
	}

	var out chain.Amounts
	for t := range out {
		out[t] = new(big.Int).Add(carry[t], l.Funded[t])
	}

	return out, nil
}

// stands reports whether the RootPosted log is the Epoch's standing Root: the ledger still
// holds a Root, and it is this one. A Void clears rootPostedAt and a re-post logs again,
// so the log stream alone cannot say which of an Epoch's logs, if any, is current.
func (b *book) stands(ctx context.Context, posted chain.RootPosted) (bool, error) {
	l, err := b.ledger(ctx, posted.ID)
	if err != nil {
		return false, err
	}

	return l.HasRoot() && l.Root == posted.Root, nil
}

// standingRoot is the Epoch's current RootPosted log, searched for in [from, to], or nil
// when the Epoch has no Root.
//
// The two sources have to agree: a ledger holding a Root that no log in the range posted
// is an endpoint that has not served the log it should have, and is an error rather than
// "no Root".
func (v *verifier) standingRoot(ctx context.Context, id, from, to uint64) (*chain.RootPosted, error) {
	l, err := v.book.ledger(ctx, id)
	if err != nil {
		return nil, err
	}
	if !l.HasRoot() {
		return nil, nil
	}

	if from > to {
		return nil, fmt.Errorf("epoch %d has a Root in the ledger, but the %s head is block %d and the Epoch closes at block %d",
			id, v.opts.finality, to, from-1)
	}

	roots, err := v.reader.Roots(ctx, from, to)
	if err != nil {
		return nil, err
	}

	// The last log for this id: a Root re-posted after a Void logs again, and the ledger
	// holds the later one.
	for i := len(roots) - 1; i >= 0; i-- {
		if roots[i].Kind == chain.KindEpoch && roots[i].ID == id {
			if roots[i].Root != l.Root {
				return nil, fmt.Errorf("epoch %d: the ledger holds Root %s, and the last RootPosted log in blocks %d..%d "+
					"carries %s", id, l.Root, from, to, roots[i].Root)
			}

			return &roots[i], nil
		}
	}

	return nil, fmt.Errorf("epoch %d: the ledger holds Root %s, but no RootPosted log for it in blocks %d..%d",
		id, l.Root, from, to)
}

// previousRoot is the standing Root of the latest Epoch before target, found by searching
// the RootPosted stream backwards from the tip.
//
// Backwards and widening from the tip, rather than the whole stream from deploy: the
// stream is a log an hour with a header read per log, and a Dispute-window run cannot
// afford a year of them to find the one an hour ago (spec §9). From the tip and not from
// the target's end block because Roots are posted in order, so the previous Epoch's can
// have been posted late, after the target closed.
func (v *verifier) previousRoot(ctx context.Context, target, tip uint64) (chain.RootPosted, bool, error) {
	bound := target

	return v.searchBack(ctx, tip, func(posted chain.RootPosted) (bool, error) {
		if posted.Kind != chain.KindEpoch || posted.ID >= bound {
			return false, nil
		}

		ok, err := v.book.stands(ctx, posted)
		if err != nil || ok {
			return ok, err
		}

		// Voided and not re-posted. Only the latest Root of a Kind can be voided, so
		// everything before it still stands; look below.
		bound = posted.ID

		return false, nil
	})
}

// latestEpoch is the Epoch of the most recent standing Root.
func (v *verifier) latestEpoch(ctx context.Context, tip chain.Tip) (uint64, error) {
	posted, found, err := v.searchBack(ctx, tip.Block.Number, func(posted chain.RootPosted) (bool, error) {
		if posted.Kind != chain.KindEpoch {
			return false, nil
		}

		return v.book.stands(ctx, posted)
	})
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("no Root has been posted at %s finality", v.opts.finality)
	}

	return posted.ID, nil
}

// searchBack walks the RootPosted stream backwards from upper in widening pages, stopping
// at the first log — newest first — that want accepts.
func (v *verifier) searchBack(ctx context.Context, upper uint64, want func(chain.RootPosted) (bool, error)) (chain.RootPosted, bool, error) {
	floor := v.dep.DistributorBlock
	span := uint64(searchSpan)

	for hi := upper; hi >= floor; {
		lo := floor
		if hi-floor > span {
			lo = hi - span
		}

		roots, err := v.reader.Roots(ctx, lo, hi)
		if err != nil {
			return chain.RootPosted{}, false, err
		}

		for i := len(roots) - 1; i >= 0; i-- {
			ok, err := want(roots[i])
			if err != nil {
				return chain.RootPosted{}, false, err
			}
			if ok {
				return roots[i], true, nil
			}
		}

		if lo == floor {
			break
		}

		hi = lo - 1
		if span <= math.MaxUint64/2 {
			span *= 2
		}
	}

	return chain.RootPosted{}, false, nil
}

// standingRoots is every standing Epoch Root in [first, last), from the whole stream.
// This is --chain's read, and it is the one spec §7 says the Dispute window does not
// afford: a header per Root, from deploy.
func (v *verifier) standingRoots(ctx context.Context, first, last, tip uint64) (map[uint64]chain.RootPosted, error) {
	roots, err := v.reader.Roots(ctx, v.dep.DistributorBlock, tip)
	if err != nil {
		return nil, err
	}

	out := map[uint64]chain.RootPosted{}
	for _, posted := range roots {
		if posted.Kind != chain.KindEpoch || posted.ID < first || posted.ID >= last {
			continue
		}

		ok, err := v.book.stands(ctx, posted)
		if err != nil {
			return nil, err
		}
		if ok {
			out[posted.ID] = posted // block order, so a re-post replaces the voided one
		} else {
			delete(out, posted.ID)
		}
	}

	return out, nil
}

// deployEpoch is the first Epoch the Distributor can have been funded for: the one its
// deploy block falls in. The mark starts one before it, so nothing earlier is fundable.
func (v *verifier) deployEpoch(ctx context.Context) (uint64, error) {
	deployed, err := v.reader.BlockByNumber(ctx, v.dep.DistributorBlock)
	if err != nil {
		return 0, err
	}

	return uint64(deployed.Timestamp / twab.EpochSeconds), nil
}

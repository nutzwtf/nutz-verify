package twab

import "fmt"

// Ledger is Replay run once over the history instead of once per Epoch. Advanced Epoch by
// Epoch it emits each Epoch's Holders as it passes the boundary, so a walk over E Epochs
// of an N-transfer history costs one pass over N plus the Holders at each boundary — not
// the E passes that make --chain quadratic over Replay (ticket 07).
//
// Every piece of a Holder carries forward cleanly, which is what makes this a walk and not
// a redesign: balances by construction, firstBuyAt is set once, lastSellAt only grows, and
// the Excluded set only grows too. What does not carry is the accrual: at each Epoch the
// integral restarts from zero at the window's opening, exactly as Replay begins it.
//
// The Ledger is not what the Cases pin — Replay is, and a Case is single-Epoch by
// construction (ADR-0003). The Ledger's whole correctness claim is that it agrees with
// Replay, byte for byte, over the same history; that is what its tests assert.
type Ledger struct {
	ordered    []Transfer
	exclusions []Exclusion

	// next is the first transfer not yet applied.
	next int

	state replayState

	// floor is the earliest Epoch Advance still accepts: every one before it has been emitted or
	// skipped past, and cannot be asked for again.
	floor uint64

	// failed is the error a previous Advance returned. The state after a rejected history
	// describes nothing, so every later call returns the same refusal.
	failed error
}

// NewLedger takes the same history Replay does — the whole NUTZ Transfer stream from the
// token's creation block, and the whole ExcludedAppended stream — in any order, and
// neither slice is modified.
func NewLedger(transfers []Transfer, exclusions []Exclusion) *Ledger {
	return &Ledger{
		ordered:    sortedByTimestamp(transfers),
		exclusions: exclusions,
		state:      replayState{balances: map[Address]*standing{}},
	}
}

// Advance closes Epoch epochID and returns its Holders: what Replay(epochID, ...) would
// return over the same history, in the same order. Epochs may be skipped — the walk from
// deploy only asks about the rooted ones — but never revisited: epochID must be later than
// the last one closed, or Advance refuses.
func (l *Ledger) Advance(epochID uint64) ([]Holder, error) {
	if l.failed != nil {
		return nil, l.failed
	}
	if epochID < l.floor {
		return nil, fmt.Errorf("twab: epoch %d has already closed; the ledger has passed every epoch before %d", epochID, l.floor)
	}

	window := WindowOf(epochID)
	l.state.open(window)

	// Replay's walk, resumed: every transfer sharing a timestamp is applied before any
	// balance is judged, and the first at or past the boundary ends this Epoch.
	for i := l.next; i < len(l.ordered); {
		ts := l.ordered[i].Timestamp
		if ts >= window.End {
			break
		}

		for ; i < len(l.ordered) && l.ordered[i].Timestamp == ts; i++ {
			if err := l.state.apply(l.ordered[i]); err != nil {
				return nil, l.fail(fmt.Errorf("twab: transfer %d at %d: %w", i, ts, err))
			}
		}

		if err := l.state.settle(ts); err != nil {
			return nil, l.fail(err)
		}

		l.next = i
	}

	l.floor = epochID + 1

	return l.state.holders(ExcludedAsOf(epochID, l.exclusions)), nil
}

func (l *Ledger) fail(err error) error {
	l.failed = err

	return err
}

// open starts a new window: every listed standing's accrual restarts at its opening, and
// those holding nothing leave the active list until a transfer touches them again. A
// window that begins several Epochs after the last one closed needs nothing more — the
// transfers between are applied with since already at the new opening, so they move
// balances without accruing, as transfers before Replay's window do.
func (r *replayState) open(window Window) {
	r.window = window

	kept := r.active[:0]
	for _, account := range r.active {
		s := r.balances[account]
		s.weighted.SetInt64(0)
		s.since = window.Start

		if s.balance.Sign() == 0 {
			s.listed = false

			continue
		}

		kept = append(kept, account)
	}

	// The tail of the old list is not cleared: it holds addresses, not references, and the
	// next boundary overwrites it.
	r.active = kept
}

package twab

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"math/big"
	"slices"
)

// Fragments, not sentinels: Replay always wraps these as "twab: transfer N at T: ...".
var (
	errNilValue      = errors.New("value is nil, which is not the same as zero")
	errNegativeValue = errors.New("value is negative, and uint256 has no sign")
)

// Address is a 20-byte Ethereum address.
type Address [20]byte

// zeroAddress is the mint and burn counterparty. It is never a Holder: an ERC-20 mint is
// Transfer(0x0, to, value) and a burn is Transfer(from, 0x0, value), so crediting or
// debiting it would manufacture a balance that never existed.
var zeroAddress Address

// Transfer is one NUTZ Transfer(from, to, value) log, reduced to what the rules need.
// Value must be non-nil and non-negative.
type Transfer struct {
	Timestamp int64
	From      Address
	To        Address
	Value     *big.Int
}

// Exclusion is one ExcludedAppended log: the account, and the timestamp of the block the
// log landed in. The Distributor emits one for every base entry at deploy as well as for
// every executed append, so the log stream alone yields the set (engineering spec §3.5).
type Exclusion struct {
	Timestamp int64
	Account   Address
}

// Holder is one eligible address's standing at the close of an Epoch.
type Holder struct {
	Account Address

	// TWAB is floor(Σ balance × duration / 3600) over the Epoch, always positive.
	TWAB *big.Int

	// FirstBuyAt is the timestamp of the address's first incoming transfer ever, and
	// LastSellAt that of its most recent outgoing one — whatever the destination, so
	// wallet-to-wallet moves and burns reset a streak too (engineering spec §4.3).
	// LastSellAt is 0 when the address has never sent; a real block timestamp never is.
	FirstBuyAt int64
	LastSellAt int64
}

// Replay rebuilds balances from transfers and reduces Epoch epochID to its eligible
// Holders, sorted by address ascending so the result is byte-identical run to run.
//
// transfers is the whole NUTZ Transfer history from the token's creation block: the part
// before the Epoch establishes opening balances, and the part inside it carves the
// sub-intervals TWAB integrates over. Anything at or after the Epoch's closing instant is
// ignored rather than rejected, so a caller may hand over a longer history than it needs.
//
// Only Holders with a positive TWAB are returned. An address with no TWAB has no weight
// and so no allocation, and Excluded addresses have no balance for any purpose at all.
//
// An input that drives a balance below zero is rejected: it means the history is
// incomplete, and the Verifier must not answer confidently from it.
func Replay(epochID uint64, transfers []Transfer, exclusions []Exclusion) ([]Holder, error) {
	window := WindowOf(epochID)
	excluded := ExcludedAsOf(epochID, exclusions)

	ordered := sortedByTimestamp(transfers)
	replay := replayState{window: window, balances: map[Address]*standing{}}

	for i := 0; i < len(ordered); {
		ts := ordered[i].Timestamp
		if ts >= window.End {
			break // sorted, so nothing further belongs to this Epoch either
		}

		// Every transfer sharing a timestamp is applied before any balance is judged. Within
		// one block the deltas commute and each sub-interval has zero length, so the order
		// the logs happen to arrive in is not part of the input — but a wallet that receives
		// and forwards in the same block passes through a negative balance in one of those
		// orders, and that is an artefact of the ordering rather than a broken history.
		for ; i < len(ordered) && ordered[i].Timestamp == ts; i++ {
			if err := replay.apply(ordered[i]); err != nil {
				return nil, fmt.Errorf("twab: transfer %d at %d: %w", i, ts, err)
			}
		}

		if err := replay.settle(ts); err != nil {
			return nil, err
		}
	}

	return replay.holders(excluded), nil
}

// sortedByTimestamp is a copy of transfers ascending by timestamp. Order within a block is
// immaterial — balance deltas commute, and a sub-interval of zero length accrues nothing —
// but the walk has to be ordered for the accrual to measure real durations.
func sortedByTimestamp(transfers []Transfer) []Transfer {
	ordered := slices.Clone(transfers)
	slices.SortStableFunc(ordered, func(a, b Transfer) int {
		return cmp.Compare(a.Timestamp, b.Timestamp)
	})

	return ordered
}

// ExcludedAsOf is the Excluded set of Epoch epochID: every account whose ExcludedAppended log
// landed in a block before the Epoch closed.
//
// Spec §5: an entry applies to **the whole Epoch containing its block**, not from its block
// onward, so an append at minute 30 zeroes the address for the entire hour. The set is final
// when the Epoch ends, like the TWAB.
//
// Ascending by address and de-duplicated. That is not presentation: engineering spec §4.5
// hashes the set with abi.encodePacked, which is order-sensitive, so "the set" has to mean
// one byte string and not a bag. internal/chain computes that hash over exactly this order —
// the rule lives here, with the other rules the Cases pin (ADR-0003), and is not restated
// there.
//
// exclusions is the whole ExcludedAppended stream and may run past this Epoch; it is not
// modified.
func ExcludedAsOf(epochID uint64, exclusions []Exclusion) []Address {
	window := WindowOf(epochID)

	seen := make(map[Address]bool, len(exclusions))
	set := make([]Address, 0, len(exclusions))

	for _, e := range exclusions {
		if e.Timestamp >= window.End || seen[e.Account] {
			continue
		}

		seen[e.Account] = true
		set = append(set, e.Account)
	}

	slices.SortFunc(set, func(a, b Address) int { return bytes.Compare(a[:], b[:]) })

	return set
}

// standing is one address's running balance and the integral accrued so far.
type standing struct {
	balance *big.Int

	// since is when balance took effect, never earlier than the window opening: time
	// before the Epoch belongs to no sub-interval of it.
	since int64

	// weighted is Σ balance × duration over the sub-intervals closed so far.
	weighted *big.Int

	// bought distinguishes "has never received" from a buy at timestamp 0, which would
	// otherwise be overwritten by the next incoming transfer and shorten the streak.
	bought     bool
	firstBuyAt int64
	lastSellAt int64

	// listed is whether the address is on active: it has held a balance or moved one since
	// the window opened, so it may have a TWAB to reduce. Replay lists every address it
	// ever sees; the Ledger takes idle ones off the list at each boundary.
	listed bool
}

type replayState struct {
	window   Window
	balances map[Address]*standing

	// active is every address that can have a TWAB in the current window, in the order
	// first listed. holders reduces exactly these, so a Ledger carrying a million wallets
	// that sold out long ago pays for none of them at a boundary.
	active []Address

	// moved is the addresses touched by the block being applied, so settle judges those
	// rather than re-walking every balance once per block.
	moved []Address

	// scratch is the product a sub-interval accrues, reused so a walk over N transfers
	// does not allocate N big.Ints it drops at once.
	scratch big.Int
}

func (r *replayState) apply(tr Transfer) error {
	switch {
	case tr.Value == nil:
		return errNilValue
	case tr.Value.Sign() < 0:
		return errNegativeValue
	}

	if tr.From != zeroAddress {
		from := r.at(tr.From)
		from.accrueTo(tr.Timestamp, &r.scratch)
		from.balance.Sub(from.balance, tr.Value)

		// Any outgoing transfer is a sell, and the walk is ascending, so the last one wins.
		from.lastSellAt = tr.Timestamp

		r.moved = append(r.moved, tr.From)
	}

	if tr.To != zeroAddress {
		to := r.at(tr.To)
		to.accrueTo(tr.Timestamp, &r.scratch)
		to.balance.Add(to.balance, tr.Value)

		if !to.bought {
			to.bought, to.firstBuyAt = true, tr.Timestamp
		}
	}

	return nil
}

// settle closes the block: a balance still negative once every transfer sharing the
// timestamp has been applied means the history is genuinely unusable, not merely reordered.
func (r *replayState) settle(ts int64) error {
	for _, account := range r.moved {
		if balance := r.balances[account].balance; balance.Sign() < 0 {
			return fmt.Errorf("twab: %#x holds %s after the transfers at %d: the transfer "+
				"history does not start at the token's creation block, or is missing logs",
				account, balance, ts)
		}
	}

	r.moved = r.moved[:0]

	return nil
}

// at is the address's standing, created holding nothing since the Epoch opened.
//
// An existing standing that is not listed has been idle — holding nothing, accruing
// nothing — since some earlier window, and its since is that window's. It rejoins at the
// current window's opening, like a new one: time before the window belongs to no
// sub-interval of it, and its balance is zero anyway.
func (r *replayState) at(a Address) *standing {
	s, ok := r.balances[a]
	if !ok {
		s = &standing{balance: new(big.Int), weighted: new(big.Int)}
		r.balances[a] = s
	}

	if !s.listed {
		s.listed, s.since = true, r.window.Start
		r.active = append(r.active, a)
	}

	return s
}

// accrueTo closes the sub-interval ending at ts. A transfer before the Epoch opened moves
// the balance without accruing anything, and a second transfer in the same block closes a
// sub-interval of zero length.
func (s *standing) accrueTo(ts int64, scratch *big.Int) {
	if ts <= s.since {
		return
	}

	scratch.SetInt64(ts - s.since)
	s.weighted.Add(s.weighted, scratch.Mul(scratch, s.balance))
	s.since = ts
}

// holders closes every open sub-interval at the Epoch boundary and divides once.
func (r *replayState) holders(excluded []Address) []Holder {
	out := make([]Holder, 0, len(r.active))
	divisor := big.NewInt(EpochSeconds)

	for _, account := range r.active {
		if _, found := slices.BinarySearchFunc(excluded, account, compareAddresses); found {
			continue
		}

		// Spec §5: the final sub-interval runs to the Epoch's closing instant, not to the
		// timestamp of its last block.
		s := r.balances[account]
		s.accrueTo(r.window.End, &r.scratch)

		// One division, over the whole sum. Dividing per sub-interval would lose precision
		// in proportion to the number of transfers, so two wallets holding identically over
		// the hour would score differently for having traded more often.
		average := new(big.Int).Quo(s.weighted, divisor)
		if average.Sign() <= 0 {
			continue // no weight, so no allocation either way
		}

		out = append(out, Holder{
			Account:    account,
			TWAB:       average,
			FirstBuyAt: s.firstBuyAt,
			LastSellAt: s.lastSellAt,
		})
	}

	// active is in first-seen order, which is an accident of the history; the Root is not.
	// Ascending by address, which is also the order the Cases record allocations in.
	slices.SortFunc(out, func(a, b Holder) int {
		return bytes.Compare(a.Account[:], b.Account[:])
	})

	return out
}

func compareAddresses(a, b Address) int {
	return bytes.Compare(a[:], b[:])
}

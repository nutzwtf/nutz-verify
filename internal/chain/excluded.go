package chain

import (
	"bytes"
	"slices"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// ExcludedSet is the Excluded set of one Epoch: ascending by address, de-duplicated.
//
// The order is not cosmetic. Engineering spec §4.5's exclusion set hash is
// keccak256(abi.encodePacked(set)), which is order-sensitive, so "the set" has to mean one
// byte string and not a bag.
type ExcludedSet []Address

// ExcludedAsOf rebuilds the Excluded set of Epoch epochID from the ExcludedAppended stream
// alone: every account whose log landed in a block with timestamp < 3600(e+1).
//
// There is no excluded() subtraction here and there must not be. excluded() returns today's
// list, which is the wrong answer for any Epoch but the current one, and ADR-0002 makes the
// set a thing we take as of the Epoch. The logs suffice because the Distributor's
// constructor emits ExcludedAppended for every base entry as well — a change nutz-contracts
// made on 2026-09-14 after this repo flagged that the base list was otherwise unrecoverable
// from logs (verified in src/NutzDistributor.sol).
//
// Spec §5: an entry applies to the whole Epoch containing its block, not from its block
// onward, so an append at minute 30 zeroes the address for the entire hour. The set is final
// when the Epoch ends, like the TWAB.
//
// exclusions is the whole stream and may run past this Epoch; it is not modified.
func ExcludedAsOf(epochID uint64, exclusions []Exclusion) ExcludedSet {
	window := twab.WindowOf(epochID)

	seen := make(map[Address]bool, len(exclusions))
	set := make(ExcludedSet, 0, len(exclusions))

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

// Hash is engineering spec §4.5's exclusion set hash: keccak256(abi.encodePacked(set)) over
// the ascending, de-duplicated addresses.
//
// encodePacked, not encode: each address contributes its twenty bytes with no padding and
// the array carries no length prefix. Recomputing it costs nothing once the set is built and
// turns a published artifact's exclusion set into a one-line cross-check.
func (s ExcludedSet) Hash() Hash {
	packed := make([]byte, 0, len(s)*addressSize)
	for _, a := range s {
		packed = append(packed, a[:]...)
	}

	return keccak256(packed)
}

// Contains reports whether the address is Excluded for this Epoch.
func (s ExcludedSet) Contains(a Address) bool {
	_, found := slices.BinarySearchFunc(s, a, func(x, y Address) int { return bytes.Compare(x[:], y[:]) })

	return found
}

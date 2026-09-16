package chain

import (
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// ExcludedSet is the Excluded set of one Epoch: ascending by address, de-duplicated.
type ExcludedSet []Address

// ExcludedAsOf rebuilds the Excluded set of Epoch epochID from the ExcludedAppended stream
// alone, in chain types.
//
// The rule is twab.ExcludedAsOf's and is deliberately not restated here. It is one of the
// spec §5 resolutions ADR-0003 makes normative in this repo, the Cases pin it there, and a
// second copy would be a second thing to change when a Case changes — the divergence those
// Cases exist to catch, manufactured internally. All this adds is the type conversion at the
// seam between the chain and the rules, the way internal/alloc converts to merkle.Address.
//
// There is no excluded() subtraction here and there must not be. excluded() returns today's
// list, which is the wrong answer for any Epoch but the current one, and ADR-0002 makes the
// set a thing we take as of the Epoch. The logs suffice because the Distributor's constructor
// emits ExcludedAppended for every base entry as well — a change nutz-contracts made on
// 2026-09-14 after this repo flagged that the base list was otherwise unrecoverable from logs
// (verified in src/NutzDistributor.sol).
//
// exclusions is the whole stream and may run past this Epoch; it is not modified.
func ExcludedAsOf(epochID uint64, exclusions []Exclusion) ExcludedSet {
	stream := make([]twab.Exclusion, 0, len(exclusions))
	for _, e := range exclusions {
		stream = append(stream, twab.Exclusion{Timestamp: e.Timestamp, Account: twab.Address(e.Account)})
	}

	ordered := twab.ExcludedAsOf(epochID, stream)

	set := make(ExcludedSet, 0, len(ordered))
	for _, account := range ordered {
		set = append(set, Address(account))
	}

	return set
}

// Hash is engineering spec §4.5's exclusion set hash: keccak256(abi.encodePacked(set)) over
// the ascending, de-duplicated addresses.
//
// encodePacked, not encode: each address contributes its twenty bytes with no padding and the
// array carries no length prefix. Recomputing it costs nothing once the set is built and
// turns a published artifact's exclusion set into a one-line cross-check.
//
// The order is the set's, which is why twab sorts rather than leaving it to presentation: a
// hash over a bag is not a hash.
func (s ExcludedSet) Hash() Hash {
	packed := make([]byte, 0, len(s)*addressSize)
	for _, a := range s {
		packed = append(packed, a[:]...)
	}

	return keccak256(packed)
}

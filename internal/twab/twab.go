// Package twab replays NUTZ balances over one Epoch and reduces them to the
// time-weighted average balance the allocation rules weigh Holders by.
//
// Everything here is a pure function over an in-memory transfer list: no RPC, no Cache,
// no chain types. That is what makes the Cases in testdata/cases hermetic, and it is the
// reason the rules can be normative here (ADR-0003) while the chain-facing code is tested
// separately.
package twab

// EpochSeconds is the length of an Epoch. Engineering spec §4.2 fixes it at one hour.
const EpochSeconds = 3600

// Window is Epoch e's half-open range [3600e, 3600(e+1)) in unix seconds. End belongs to the
// next Epoch, so consecutive windows neither overlap nor leave a second unclaimed.
//
// End is the instant the Epoch closes, not the timestamp of its last block. Spec §5
// resolves the difference in End's favour: an Epoch is a time window observed through
// blocks, so the final sub-interval of a balance runs to End. Under the other reading
// Σ duration ≠ 3600 and TWAB stops being comparable across Epochs.
type Window struct {
	Start int64
	End   int64
}

// WindowOf is Epoch epochID's window.
func WindowOf(epochID uint64) Window {
	start := int64(epochID) * EpochSeconds

	return Window{Start: start, End: start + EpochSeconds}
}

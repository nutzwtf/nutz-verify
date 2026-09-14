package twab_test

import (
	"slices"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// Replay's own tests cover what the Excluded set does to a Holder. These cover the shape of
// the set itself, because it is exported and internal/chain hashes it: engineering spec
// §4.5's exclusion set hash is over abi.encodePacked(set), so the ordering and the
// de-duplication below are load-bearing rather than cosmetic, and a hash over a bag would be
// no hash at all.

func TestExcludedAsOf_IsAscendingAndDeduplicated(t *testing.T) {
	t.Parallel()

	got := twab.ExcludedAsOf(epoch1, []twab.Exclusion{
		{Timestamp: 40, Account: carol},
		{Timestamp: 10, Account: bob},
		{Timestamp: 20, Account: alice},
		{Timestamp: 30, Account: bob}, // the contract refuses this, but a re-read log stream can carry it
	})

	want := []twab.Address{alice, bob, carol}
	slices.SortFunc(want, func(a, b twab.Address) int { return slices.Compare(a[:], b[:]) })

	if !slices.Equal(got, want) {
		t.Errorf("set = %x, want %x", got, want)
	}
}

func TestExcludedAsOf_TheWholeEpochContainingTheBlock(t *testing.T) {
	t.Parallel()

	// Spec §5: an entry applies to the whole Epoch containing its block, not from its block
	// onward. Epoch 1 is [3600, 7200), so an append at minute 30 zeroes the address for the
	// whole hour, and the closing instant belongs to the next Epoch.
	tests := []struct {
		name    string
		at      int64
		inEpoch bool
	}{
		{"before the Epoch opened", epoch1Start - 1, true},
		{"the instant it opened", epoch1Start, true},
		{"halfway through", epoch1Start + 1800, true},
		{"its last second", epoch1End - 1, true},
		{"the closing instant", epoch1End, false},
		{"an Epoch later", epoch1End + 3600, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set := twab.ExcludedAsOf(epoch1, []twab.Exclusion{{Timestamp: tt.at, Account: alice}})

			if slices.Contains(set, alice) != tt.inEpoch {
				t.Errorf("an append at %d is in epoch %d's set: %v, want %v",
					tt.at, epoch1, !tt.inEpoch, tt.inEpoch)
			}
		})
	}
}

func TestExcludedAsOf_KeepsTheCallerStream(t *testing.T) {
	t.Parallel()

	// The caller holds the whole ExcludedAppended stream and reuses it for the next Epoch, so
	// reconstruction must not sort or otherwise disturb it.
	stream := []twab.Exclusion{
		{Timestamp: 30, Account: carol},
		{Timestamp: 10, Account: alice},
	}

	twab.ExcludedAsOf(epoch1, stream)

	if stream[0].Account != carol {
		t.Error("ExcludedAsOf reordered the log stream it was given")
	}
}

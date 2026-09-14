package chain

import (
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// Epoch 1000 is [3,600,000, 3,603,600).
const (
	epoch     = 1000
	epochOpen = epoch * twab.EpochSeconds
	epochShut = epochOpen + twab.EpochSeconds
)

func TestExcludedAsOf(t *testing.T) {
	t.Parallel()

	alice, bob, carol := repeatAddr(0xa1), repeatAddr(0xb2), repeatAddr(0xc3)

	tests := []struct {
		name       string
		exclusions []Exclusion
		want       []Address
	}{
		{
			name: "an entry from an earlier Epoch still counts",
			exclusions: []Exclusion{
				{Timestamp: 0, Account: alice},
				{Timestamp: epochOpen - 1, Account: bob},
			},
			want: []Address{alice, bob},
		},
		{
			// Spec §5: an entry applies to the whole Epoch containing its block, not from
			// its block onward, so an append at minute 30 zeroes the address for the hour.
			name:       "an entry landing mid-Epoch covers the whole Epoch",
			exclusions: []Exclusion{{Timestamp: epochOpen + 1800, Account: alice}},
			want:       []Address{alice},
		},
		{
			name:       "an entry in the last second of the Epoch still covers it",
			exclusions: []Exclusion{{Timestamp: epochShut - 1, Account: alice}},
			want:       []Address{alice},
		},
		{
			// The set is final when the Epoch ends, like the TWAB. The closing instant
			// belongs to the next Epoch, so an entry there is that Epoch's business.
			name:       "an entry at the closing instant does not",
			exclusions: []Exclusion{{Timestamp: epochShut, Account: alice}},
			want:       nil,
		},
		{
			name:       "an entry from a later Epoch does not reach back",
			exclusions: []Exclusion{{Timestamp: epochShut + 3600, Account: alice}},
			want:       nil,
		},
		{
			name: "the set is ascending and de-duplicated whatever order the logs arrive in",
			exclusions: []Exclusion{
				{Timestamp: 40, Account: carol},
				{Timestamp: 10, Account: bob},
				{Timestamp: 20, Account: alice},
				{Timestamp: 30, Account: bob},
			},
			want: []Address{alice, bob, carol},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ExcludedAsOf(epoch, tt.exclusions)

			if len(got) != len(tt.want) {
				t.Fatalf("set = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("set[%d] = %s, want %s", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExcludedSet_Contains(t *testing.T) {
	t.Parallel()

	alice, bob := repeatAddr(0xa1), repeatAddr(0xb2)
	set := ExcludedAsOf(epoch, []Exclusion{{Timestamp: 1, Account: alice}})

	if !set.Contains(alice) {
		t.Error("Contains(alice) = false")
	}
	if set.Contains(bob) {
		t.Error("Contains(bob) = true")
	}
}

func TestExcludedSet_Hash(t *testing.T) {
	t.Parallel()

	// keccak256(abi.encodePacked(set)) over the ascending, de-duplicated addresses —
	// engineering spec §4.5's exclusion set hash. Every expectation below comes from
	// `cast keccak` over the packed bytes, not from this package.
	dead, err := ParseAddress("0x000000000000000000000000000000000000dEaD")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		set  ExcludedSet
		want string
	}{
		{
			name: "the empty set is the empty keccak",
			set:  nil,
			want: "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
		},
		{
			name: "one address is twenty packed bytes, not a padded word",
			set:  ExcludedSet{repeatAddr(0xa1)},
			want: "0x860e2f2a059defb50824af2b024b4e7a37754d03676b00de684d95d112aefd87",
		},
		{
			name: "three addresses concatenate",
			set:  ExcludedSet{repeatAddr(0xa1), repeatAddr(0xb2), repeatAddr(0xc3)},
			want: "0xe4e59c2d092defb7a3a538970d472178897e5d0b99fd01741513cd9f73bf147a",
		},
		{
			name: "the base entry sorts ahead of a later append",
			set:  ExcludedSet{dead, repeatAddr(0xa1)},
			want: "0x3a771b4df8bb7f22e74fcf6d05369cb7d0251c3715e1f8294e7660bc95bcacc1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.set.Hash().String(); got != tt.want {
				t.Errorf("Hash = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestExcludedSet_HashDependsOnOrder(t *testing.T) {
	t.Parallel()

	// The hash is over the ascending set, so a set built in log order rather than address
	// order hashes to something else entirely. Nothing but ordering distinguishes these two.
	ascending := ExcludedSet{repeatAddr(0xa1), repeatAddr(0xb2)}
	reversed := ExcludedSet{repeatAddr(0xb2), repeatAddr(0xa1)}

	if ascending.Hash() == reversed.Hash() {
		t.Error("Hash is order-insensitive, so it cannot be abi.encodePacked")
	}
}

func TestExcludedAsOf_KeepsTheCallerSlice(t *testing.T) {
	t.Parallel()

	// The caller hands over the whole ExcludedAppended stream and reuses it for the next
	// Epoch, so reconstruction must not sort or otherwise disturb it.
	exclusions := []Exclusion{
		{Timestamp: 30, Account: repeatAddr(0xc3)},
		{Timestamp: 10, Account: repeatAddr(0xa1)},
	}

	ExcludedAsOf(epoch, exclusions)

	if exclusions[0].Account != repeatAddr(0xc3) {
		t.Error("ExcludedAsOf reordered the log stream it was given")
	}
}

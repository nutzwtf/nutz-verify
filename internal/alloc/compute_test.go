package alloc_test

import (
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/alloc"
	"github.com/nutzwtf/nutz-verify/internal/merkle"
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

var (
	alice = repeat(0xa1)
	bob   = repeat(0xb2)
	carol = repeat(0xc3)
	dev   = repeat(0xdd)
)

// Spec §5: base[t] = funded[t] + carryIn[t], and the sole Holder takes all of it.
// Allocating over funded alone would hand back 1000 and silently strand the Carry.
func TestCompute_SpendsFundingPlusCarry(t *testing.T) {
	t.Parallel()

	result := compute(t, alloc.Params{
		EpochID:   epochID,
		Transfers: []twab.Transfer{holds(alice, 1000, 0)},
		Funded:    same(1000),
		CarryIn:   same(500),
	})

	requireAllocations(t, result, allocation{account: alice, twab: 1000, multBps: alloc.MultBpsBase,
		weight: 1000 * alloc.MultBpsBase, amounts: same(1500)})
	requireAmounts(t, "Totals", result.Totals, same(1500))
	requireAmounts(t, "CarryOut", result.CarryOut, same(0))
}

// Spec §5: weight(a) = TWAB(a) × multBps(a), and the 10000 cancels between weight and W,
// so dividing it out early is pure floor loss applied per Holder.
//
//	weights 3×12500 = 37500 and 1×10000 = 10000, W = 47500, base 100
//	  alice = floor(100 × 37500 / 47500) = 78    bob = floor(100 × 10000 / 47500) = 21
//	divided down to 3 and 1 first, W = 4:
//	  alice = floor(100 × 3 / 4)         = 75    bob = floor(100 × 1 / 4)         = 25
func TestCompute_WeightKeepsTheBpsUndivided(t *testing.T) {
	t.Parallel()

	result := compute(t, alloc.Params{
		EpochID: epochID,
		Transfers: []twab.Transfer{
			holds(alice, 3, alloc.StreakWeek+3), // 1.25x
			holds(bob, 1, 0),                    // 1.00x
		},
		Funded:  same(100),
		CarryIn: same(0),
	})

	requireAllocations(t, result,
		allocation{account: alice, twab: 3, multBps: alloc.MultBpsWeek, weight: 37500, amounts: same(78)},
		allocation{account: bob, twab: 1, multBps: alloc.MultBpsBase, weight: 10000, amounts: same(21)},
	)
	requireAmounts(t, "Totals", result.Totals, same(99))
	requireAmounts(t, "CarryOut", result.CarryOut, same(1)) // the rounding dust rolls into the next Root
}

// Engineering spec §4.3: mult(DEV_WALLET) = 1.00 always. Held at 1.0x while its streak
// would otherwise promote it to 1.50x, exactly as Alice's does.
func TestCompute_DevWalletNeverEarnsAMultiplier(t *testing.T) {
	t.Parallel()

	result := compute(t, alloc.Params{
		EpochID: epochID,
		Transfers: []twab.Transfer{
			holds(dev, 100, alloc.StreakMonth+10),
			holds(alice, 100, alloc.StreakMonth+10),
		},
		DevWallet: dev,
		Funded:    same(250),
		CarryIn:   same(0),
	})

	// W = 100×10000 + 100×15000 = 2,500,000, so 250 splits 100/150. Without the override
	// both weights would be 1,500,000 and each would take 125.
	requireAllocations(t, result,
		allocation{account: alice, twab: 100, multBps: alloc.MultBpsMonth, weight: 1_500_000, amounts: same(150)},
		allocation{account: dev, twab: 100, multBps: alloc.MultBpsBase, weight: 1_000_000, amounts: same(100)},
	)
}

// Spec §5: omission is computed last. Carol's weight counts towards W whether or not she
// ends up in the tree, so dropping her before the division would inflate every other
// Holder — here Alice to a round 1,000,000 instead of 999,999.
func TestCompute_OmissionIsComputedLast(t *testing.T) {
	t.Parallel()

	// W = 1,000,000×10000 + 1×10000 = 10,000,010,000.
	holdings := []twab.Transfer{holds(alice, 1_000_000, 0), holds(carol, 1, 0)}

	t.Run("a Holder who floors to zero in all five tokens leaves the tree", func(t *testing.T) {
		t.Parallel()

		result := compute(t, alloc.Params{
			EpochID: epochID, Transfers: holdings, Funded: same(1_000_000), CarryIn: same(0),
		})

		requireAllocations(t, result, allocation{account: alice, twab: 1_000_000,
			multBps: alloc.MultBpsBase, weight: 10_000_000_000, amounts: same(999_999)})
	})

	t.Run("a Holder who floors to zero in four of five stays", func(t *testing.T) {
		t.Parallel()

		// floor(20,000,000,000 × 10000 / 10,000,010,000) = 19,999 in the last token only.
		funded := same(1_000_000)
		funded[4] = big.NewInt(20_000_000_000)

		result := compute(t, alloc.Params{
			EpochID: epochID, Transfers: holdings, Funded: funded, CarryIn: same(0),
		})

		requireAllocations(t, result,
			allocation{account: alice, twab: 1_000_000, multBps: alloc.MultBpsBase,
				weight: 10_000_000_000, amounts: tokens(999_999, 999_999, 999_999, 999_999, 19_999_980_000)},
			allocation{account: carol, twab: 1, multBps: alloc.MultBpsBase,
				weight: 10000, amounts: tokens(0, 0, 0, 0, 19_999)},
		)
	})
}

// Spec §5: W == 0 is a legitimate state, not an error. A funded Epoch with no eligible
// Holders gets no Root — engineering spec §3.3's Skipped mechanism rolls its funding into
// Carry — and the Verifier reports MATCH for it, never INDETERMINATE.
func TestCompute_NoEligibleHoldersYieldsNoRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   alloc.Params
		carryOut [alloc.TokenCount]*big.Int
	}{
		{
			name:     "no transfers at all",
			params:   alloc.Params{EpochID: epochID, Funded: same(5000), CarryIn: same(100)},
			carryOut: same(5100),
		},
		{
			name: "every Holder Excluded",
			params: alloc.Params{
				EpochID:    epochID,
				Transfers:  []twab.Transfer{holds(alice, 1_000_000_000, 0)},
				Exclusions: []twab.Exclusion{{Timestamp: 0, Account: alice}},
				Funded:     same(5000),
				CarryIn:    same(100),
			},
			carryOut: same(5100),
		},
		{
			name: "every allocation floors away",
			params: alloc.Params{
				EpochID:   epochID,
				Transfers: []twab.Transfer{holds(alice, 1000, 0), holds(bob, 1000, 0)},
				Funded:    same(1),
				CarryIn:   same(0),
			},
			carryOut: same(1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := compute(t, tt.params)

			if result.HasRoot {
				t.Errorf("HasRoot is true with %d allocations; a Skipped Epoch posts no Root",
					len(result.Allocations))
			}
			if result.Tree != nil {
				t.Error("Tree is non-nil for an Epoch with no Root")
			}
			if result.Root != (merkle.Hash{}) {
				t.Errorf("Root = %#x, want the zero hash", result.Root)
			}
			if len(result.Allocations) != 0 {
				t.Errorf("got %d allocations, want none", len(result.Allocations))
			}

			requireAmounts(t, "Totals", result.Totals, same(0))
			// Every unspent unit stays as Carry for the next Root.
			requireAmounts(t, "CarryOut", result.CarryOut, tt.carryOut)
		})
	}
}

// The tree the Root comes from must be the tree the allocations describe, claim for claim,
// so a proof generated for Allocations[i] verifies.
func TestCompute_TreeCarriesTheAllocations(t *testing.T) {
	t.Parallel()

	result := compute(t, alloc.Params{
		EpochID: epochID,
		Transfers: []twab.Transfer{
			holds(alice, 500, 0), holds(bob, 300, 8), holds(carol, 200, 31),
		},
		Funded:  same(10_000),
		CarryIn: same(0),
	})

	if !result.HasRoot {
		t.Fatal("HasRoot is false for three funded Holders")
	}
	if result.Tree.Len() != len(result.Allocations) {
		t.Fatalf("tree holds %d claims against %d allocations",
			result.Tree.Len(), len(result.Allocations))
	}

	for i, a := range result.Allocations {
		claim := merkle.Claim{
			ID:      new(big.Int).SetUint64(epochID),
			Account: merkle.Address(a.Account),
			Amounts: a.Amounts,
		}

		single, err := merkle.New([]merkle.Claim{claim})
		if err != nil {
			t.Fatalf("hashing allocation %d: %v", i, err)
		}

		if got := result.Tree.Leaf(i); got != single.Root() {
			t.Errorf("tree claim %d is %#x, but allocation %d hashes to %#x",
				i, got, i, single.Root())
		}

		if !merkle.Verify(result.Root, result.Tree.Leaf(i), result.Tree.Proof(i)) {
			t.Errorf("the proof for allocation %d does not verify against the Root", i)
		}
	}
}

// Engineering spec §7: same inputs, same Root, byte for byte. Map iteration order is the
// standing threat, so the shuffled Recompute has to agree too.
func TestCompute_IsDeterministic(t *testing.T) {
	t.Parallel()

	params := func() alloc.Params {
		return alloc.Params{
			EpochID: epochID,
			Transfers: []twab.Transfer{
				holds(alice, 500, 0), holds(bob, 300, 8), holds(carol, 200, 31),
				holds(dev, 700, 40), holds(repeat(0x11), 900, 3), holds(repeat(0x22), 4, 1),
			},
			Exclusions: []twab.Exclusion{{Timestamp: 0, Account: repeat(0xcc)}},
			DevWallet:  dev,
			Funded:     tokens(1_000_000, 7, 999_999_999, 0, 12_345),
			CarryIn:    tokens(3, 0, 1, 0, 678),
		}
	}

	first := fingerprint(compute(t, params()))

	for i := range len(params().Transfers) {
		p := params()
		// Reverse on one pass, rotate on the rest: neither may reach the Root.
		if i == 0 {
			slices.Reverse(p.Transfers)
		} else {
			p.Transfers = slices.Concat(p.Transfers[i:], p.Transfers[:i])
		}

		if got := fingerprint(compute(t, p)); got != first {
			t.Fatalf("Recompute %d differs from the first:\n got %s\nwant %s", i, got, first)
		}
	}
}

// big.Int has in-place methods, so a Recompute must not corrupt what it was handed —
// the CLI reuses funded and carryIn across the four Assertions.
func TestCompute_DoesNotMutateParams(t *testing.T) {
	t.Parallel()

	params := alloc.Params{
		EpochID:   epochID,
		Transfers: []twab.Transfer{holds(alice, 500, 0), holds(bob, 300, 8)},
		Funded:    same(1000),
		CarryIn:   same(50),
	}

	before := []string{}
	for i := range params.Funded {
		before = append(before, params.Funded[i].String(), params.CarryIn[i].String())
	}
	for _, tr := range params.Transfers {
		before = append(before, tr.Value.String())
	}

	compute(t, params)

	after := []string{}
	for i := range params.Funded {
		after = append(after, params.Funded[i].String(), params.CarryIn[i].String())
	}
	for _, tr := range params.Transfers {
		after = append(after, tr.Value.String())
	}

	if !slices.Equal(before, after) {
		t.Errorf("Compute changed its inputs:\n got %v\nwant %v", after, before)
	}
}

func TestCompute_RejectsUnusableFunding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*alloc.Params)
		wantMsg string
	}{
		{
			name:    "a nil funded entry",
			mutate:  func(p *alloc.Params) { p.Funded[2] = nil },
			wantMsg: "funded 2: is nil",
		},
		{
			name:    "a nil carryIn entry",
			mutate:  func(p *alloc.Params) { p.CarryIn[0] = nil },
			wantMsg: "carryIn 0: is nil",
		},
		{
			name:    "a negative funded entry",
			mutate:  func(p *alloc.Params) { p.Funded[4] = big.NewInt(-1) },
			wantMsg: "funded 4: is negative",
		},
		{
			name:    "a negative carryIn entry",
			mutate:  func(p *alloc.Params) { p.CarryIn[3] = big.NewInt(-7) },
			wantMsg: "carryIn 3: is negative",
		},
		{
			name:    "a history that does not reach the token's creation",
			mutate:  func(p *alloc.Params) { p.Transfers = []twab.Transfer{holds(alice, -1, 0)} },
			wantMsg: "negative",
		},
		{
			// funded + carryIn is a sum of two chain-read uint256s, and ADR-0004 leaves the
			// decoding to us, so a base that no leaf can carry has to be refused rather than
			// silently truncated into a wrong-but-valid Root.
			name: "a base wider than the leaf can carry",
			mutate: func(p *alloc.Params) {
				maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
				for t := range p.Funded {
					p.Funded[t] = maxUint256
					p.CarryIn[t] = maxUint256
				}
			},
			wantMsg: "needs 257 bits",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			params := alloc.Params{
				EpochID:   epochID,
				Transfers: []twab.Transfer{holds(alice, 1000, 0)},
				Funded:    same(1000),
				CarryIn:   same(0),
			}
			tt.mutate(&params)

			_, err := alloc.Compute(params)
			if err == nil {
				t.Fatal("Compute accepted an Epoch it cannot allocate")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

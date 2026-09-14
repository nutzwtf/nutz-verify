// Package alloc turns an Epoch's replayed Holders into the Allocations a Root commits to:
// streak, Multiplier, weight, per-token allocation and the tree itself.
//
// Like internal/twab it is a pure function over an in-memory transfer list. ADR-0003 makes
// the rules here normative — if the private indexer disagrees, the indexer is the bug —
// while the tree shape belongs to the Distributor and OpenZeppelin StandardMerkleTree, so
// the leaf hashing is delegated to internal/merkle rather than restated.
package alloc

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/nutzwtf/nutz-verify/internal/merkle"
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// TokenCount is the number of reward tokens an Epoch funds: SPY, NVDA, MU, SPCX, USDG.
// It is the leaf's amount count because it has to be — a Root carries exactly five.
const TokenCount = merkle.AmountCount

// Amounts is one value per reward token, in each token's own decimals.
type Amounts = [TokenCount]*big.Int

// Params is everything one Epoch's Allocations depend on. Every field is read from the
// chain by the caller; nothing here reaches for it.
type Params struct {
	EpochID uint64

	// Transfers is the whole NUTZ Transfer history from the token's creation block, and
	// Exclusions the whole ExcludedAppended stream. Both may run past this Epoch.
	Transfers  []twab.Transfer
	Exclusions []twab.Exclusion

	// DevWallet is held at 1.00x however long its streak (engineering spec §4.3). The zero
	// Address names no wallet, which is what a Case without one wants.
	DevWallet twab.Address

	Funded  Amounts
	CarryIn Amounts
}

// Allocation is one Holder's row of a Root: what it is owed, and the working that got
// there. TWAB, MultBps and Weight are carried because engineering spec §4.5 publishes
// them, and because a MISMATCH is far easier to place when the intermediate values show.
type Allocation struct {
	Account twab.Address
	TWAB    *big.Int
	MultBps int64
	Weight  *big.Int
	Amounts Amounts
}

// Result is one Recompute of one Epoch.
type Result struct {
	// Allocations are the tree's claims, ascending by address — the order engineering spec
	// §4.5's allocations.json and the Cases both record, so a diff against the indexer is
	// stable. Holders omitted from the tree are not here either.
	Allocations []Allocation

	// Totals is Σ alloc(a,t), and CarryOut the rounding dust funded + carryIn − Totals that
	// stays in the Distributor for the next Root.
	Totals   Amounts
	CarryOut Amounts

	// TotalWeight is W, summed over every eligible Holder — including those the omission
	// rule later drops from the tree.
	TotalWeight *big.Int

	// Tree is nil and Root the zero hash when HasRoot is false: an Epoch with no eligible
	// Holders, or none whose allocation survives flooring, posts no Root at all. Spec §5
	// makes that a legitimate state rather than an error.
	Tree    *merkle.Tree
	Root    merkle.Hash
	HasRoot bool
}

// Compute recomputes one Epoch. The returned Result shares no mutable state with params.
func Compute(params Params) (Result, error) {
	base, err := params.base()
	if err != nil {
		return Result{}, err
	}

	holders, err := twab.Replay(params.EpochID, params.Transfers, params.Exclusions)
	if err != nil {
		return Result{}, err
	}

	result := Result{TotalWeight: new(big.Int)}
	for t := range result.Totals {
		result.Totals[t] = new(big.Int)
	}

	weighted := params.weigh(holders, result.TotalWeight)

	// Spec §5: alloc(a,t) = floor(base[t] × weight(a) / W). W stays undivided, so the 10000
	// in each Multiplier cancels instead of being floored away per Holder.
	//
	// W is zero exactly when there are no eligible Holders — Replay returns only positive
	// TWABs and no Multiplier is below 10000 — so this loop simply does not run then, and
	// spec §5's "W == 0 is a legitimate state" needs no special case.
	for i := range weighted {
		row := &weighted[i]
		for t := range row.Amounts {
			row.Amounts[t] = new(big.Int).Quo(
				new(big.Int).Mul(base[t], row.Weight), result.TotalWeight)
		}
	}

	// Spec §5: omission is computed last. A Holder leaves the tree only once all five of
	// its allocations have floored to zero, and W above already counted it either way — so
	// dropping it here cannot change anybody else's share.
	for _, row := range weighted {
		if isEmpty(row.Amounts) {
			continue
		}

		result.Allocations = append(result.Allocations, row)
		for t := range row.Amounts {
			result.Totals[t].Add(result.Totals[t], row.Amounts[t])
		}
	}

	for t := range base {
		result.CarryOut[t] = new(big.Int).Sub(base[t], result.Totals[t])
	}

	if len(result.Allocations) == 0 {
		return result, nil // Skipped: engineering spec §3.3 rolls the funding into Carry
	}

	tree, err := merkle.New(result.claims(params.EpochID))
	if err != nil {
		return Result{}, fmt.Errorf("alloc: epoch %d: %w", params.EpochID, err)
	}

	result.Tree, result.Root, result.HasRoot = tree, tree.Root(), true

	return result, nil
}

// weigh turns Holders into unfilled Allocations, accumulating W as it goes.
func (p Params) weigh(holders []twab.Holder, totalWeight *big.Int) []Allocation {
	window := twab.WindowOf(p.EpochID)

	rows := make([]Allocation, 0, len(holders))
	for _, h := range holders {
		multBps := MultBps(StreakDays(window, h))
		if h.Account == p.DevWallet {
			multBps = MultBpsBase // flat 1.00x, whatever its streak would earn
		}

		// weight(a) = TWAB(a) × multBps(a), never divided down (spec §5).
		weight := new(big.Int).Mul(h.TWAB, big.NewInt(multBps))
		totalWeight.Add(totalWeight, weight)

		rows = append(rows, Allocation{
			Account: h.Account,
			TWAB:    new(big.Int).Set(h.TWAB),
			MultBps: multBps,
			Weight:  weight,
		})
	}

	return rows
}

// claims is the Allocations as tree rows. Ordering does not matter here — the tree sorts by
// leaf hash — but it is preserved so Tree.Leaf(i) is Allocations[i].
func (r Result) claims(epochID uint64) []merkle.Claim {
	id := new(big.Int).SetUint64(epochID)

	claims := make([]merkle.Claim, 0, len(r.Allocations))
	for _, a := range r.Allocations {
		claims = append(claims, merkle.Claim{
			ID:      id,
			Account: merkle.Address(a.Account),
			Amounts: a.Amounts,
		})
	}

	return claims
}

// Fragments, not sentinels: base always wraps these as "alloc: funded N: ...".
var (
	errNilAmount      = errors.New("is nil, which is not the same as zero")
	errNegativeAmount = errors.New("is negative, and funding cannot be")
)

// base is spec §5's base[t] = funded[t] + carryIn[t]: allocation spends funding plus Carry,
// not funding alone, or the previous Epoch's rounding dust would be stranded forever.
func (p Params) base() (Amounts, error) {
	var base Amounts

	for t := range base {
		if err := checkAmount(p.Funded[t]); err != nil {
			return base, fmt.Errorf("alloc: funded %d: %w", t, err)
		}
		if err := checkAmount(p.CarryIn[t]); err != nil {
			return base, fmt.Errorf("alloc: carryIn %d: %w", t, err)
		}

		base[t] = new(big.Int).Add(p.Funded[t], p.CarryIn[t])
	}

	return base, nil
}

func checkAmount(x *big.Int) error {
	switch {
	case x == nil:
		return errNilAmount
	case x.Sign() < 0:
		return errNegativeAmount
	default:
		return nil
	}
}

func isEmpty(amounts Amounts) bool {
	for _, a := range amounts {
		if a.Sign() != 0 {
			return false
		}
	}

	return true
}

package alloc_test

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/alloc"
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// An Epoch far enough from the unix epoch that thirty-day streaks fit before it.
const epochID = 494000

var window = twab.WindowOf(epochID)

// repeat fills an address with one byte, so Cases read as 0xa1a1…a1.
func repeat(b byte) twab.Address {
	var a twab.Address
	for i := range a {
		a[i] = b
	}

	return a
}

// holds mints value to account far enough before the Epoch that the balance is constant
// across the whole hour — so the Holder's TWAB is exactly value — and late enough that
// its streak is exactly streakDays.
func holds(account twab.Address, value, streakDays int64) twab.Transfer {
	return twab.Transfer{
		Timestamp: window.End - (streakDays+1)*day + 1,
		To:        account,
		Value:     big.NewInt(value),
	}
}

// same is the five-token vector with one value in every slot.
func same(v int64) [alloc.TokenCount]*big.Int {
	return tokens(v, v, v, v, v)
}

// tokens is the five-token vector [SPY, NVDA, MU, SPCX, USDG].
func tokens(v0, v1, v2, v3, v4 int64) [alloc.TokenCount]*big.Int {
	return [alloc.TokenCount]*big.Int{
		big.NewInt(v0), big.NewInt(v1), big.NewInt(v2), big.NewInt(v3), big.NewInt(v4),
	}
}

func compute(t *testing.T, params alloc.Params) alloc.Result {
	t.Helper()

	result, err := alloc.Compute(params)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	return result
}

// allocation is one expected row, written out in full so a wrong TWAB, Multiplier or
// weight is named rather than showing up only as a wrong amount.
type allocation struct {
	account twab.Address
	twab    int64
	multBps int64
	weight  int64
	amounts [alloc.TokenCount]*big.Int
}

// requireAllocations compares the whole list, order included: spec §5 wants them ascending
// by address so a diff against the indexer is stable.
func requireAllocations(t *testing.T, result alloc.Result, expected ...allocation) {
	t.Helper()

	got := make([]string, 0, len(result.Allocations))
	for _, a := range result.Allocations {
		got = append(got, fmt.Sprintf("%#x twab=%s mult=%d weight=%s amounts=%s",
			a.Account, a.TWAB, a.MultBps, a.Weight, vector(a.Amounts)))
	}

	want := make([]string, 0, len(expected))
	for _, e := range expected {
		want = append(want, fmt.Sprintf("%#x twab=%d mult=%d weight=%d amounts=%s",
			e.account, e.twab, e.multBps, e.weight, vector(e.amounts)))
	}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("allocations:\n got %s\nwant %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
}

// requireAmounts compares one five-token vector against another. The Cases carry theirs as
// decimal strings, so requireAmountStrings in cases_test.go is the same assertion over those.
func requireAmounts(t *testing.T, what string, got, expected alloc.Amounts) {
	t.Helper()

	if actual, want := vector(got), vector(expected); actual != want {
		t.Errorf("%s = %s, want %s", what, actual, want)
	}
}

func vector(v [alloc.TokenCount]*big.Int) string {
	parts := make([]string, 0, len(v))
	for _, x := range v {
		parts = append(parts, x.String())
	}

	return "[" + strings.Join(parts, " ") + "]"
}

// fingerprint is every byte of a Result that a Recompute commits to, flattened so two
// Recomputes can be compared for equality rather than field by field.
func fingerprint(result alloc.Result) string {
	var b strings.Builder

	fmt.Fprintf(&b, "root=%#x hasRoot=%t W=%s totals=%s carryOut=%s\n",
		result.Root, result.HasRoot, result.TotalWeight, vector(result.Totals), vector(result.CarryOut))

	for i, a := range result.Allocations {
		fmt.Fprintf(&b, "%d %#x twab=%s mult=%d weight=%s amounts=%s\n",
			i, a.Account, a.TWAB, a.MultBps, a.Weight, vector(a.Amounts))
	}

	if result.Tree != nil {
		for i := range result.Tree.Len() {
			fmt.Fprintf(&b, "leaf %d %#x proof %v\n", i, result.Tree.Leaf(i), result.Tree.Proof(i))
		}
	}

	return b.String()
}

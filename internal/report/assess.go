// Package report turns a Recompute into a Verdict and puts it in front of a reader — human
// or Signer — without losing which of the four Assertions broke.
//
// ADR-0002: MATCH is not "the Root matched". It asserts four things, reported as four
// separate lines, because a MISMATCH that does not say which invariant broke is
// unactionable at 3am inside a 30-minute Dispute window. And Verdicts are three-valued:
// INDETERMINATE is what "could not check" is called, and it is never MATCH. The warm Signer
// signs only on exit 0, so anything here that let a failed or unfinished check exit 0 would
// quietly turn the 2-of-3 into a 1-of-3.
//
// Everything in this package is pure: it takes what the chain said and what the Verifier
// computed, and it renders. Reading the chain is cmd/nutz-verify's job.
package report

import (
	"fmt"
	"math/big"

	"github.com/nutzwtf/nutz-verify/chain"
)

// Verdict is the outcome of a Recompute (CONTEXT.md). Exactly three, and the third is
// load-bearing.
type Verdict string

const (
	Match         Verdict = "MATCH"
	Mismatch      Verdict = "MISMATCH"
	Indeterminate Verdict = "INDETERMINATE"
)

// ExitCode is the process exit status a Verdict maps to: 0, 1, 2. The Signer signs only on
// 0, and 2 is never 0.
func (v Verdict) ExitCode() int {
	switch v {
	case Match:
		return 0
	case Mismatch:
		return 1
	default:
		return 2
	}
}

// Worst is the Verdict of a run made of several: any INDETERMINATE makes the run
// INDETERMINATE, otherwise any MISMATCH makes it MISMATCH.
//
// INDETERMINATE outranks MISMATCH because the two answer different questions. A MISMATCH
// says the Recompute completed and something is wrong; an INDETERMINATE says it did not,
// and an incomplete Recompute cannot vouch for what a partial MISMATCH means.
func Worst(verdicts ...Verdict) Verdict {
	worst := Match
	for _, v := range verdicts {
		switch {
		case v == Indeterminate:
			return Indeterminate
		case v == Mismatch:
			worst = Mismatch
		}
	}

	return worst
}

// Status is what one Assertion found.
type Status string

const (
	Pass Status = "pass"
	Fail Status = "fail"

	// NotApplicable is an Assertion with nothing to compare — carryIn when no Root is
	// posted, say. It is neither a pass nor a fail and never moves the Verdict; it is
	// reported so the four lines are always four.
	NotApplicable Status = "n/a"
)

// The four Assertions of ADR-0002, by name and in the order they are reported, and the
// fifth that --expect adds (ticket 15).
const (
	AssertRoot    = "root"
	AssertTotals  = "totals"
	AssertCap     = "cap"
	AssertCarryIn = "carryIn"

	// AssertExpected is reported only when there is an Expectation: whether it equals
	// the posted Root. With no Root posted the Expectation is the Root Assertion's
	// comparand instead, and this line is NotApplicable, so a run with --expect always
	// has five lines.
	AssertExpected = "expected"
)

// Assertion is one of the four things MATCH commits to, with what was compared. Name is
// one of the Assert constants.
type Assertion struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

// Recompute is what the Verifier computed for an Epoch from chain data alone.
type Recompute struct {
	// HasRoot is false when no eligible Holder survives flooring: spec §5's W == 0, a
	// legitimate no-Root state. Root and Totals are then the zero hash and zeros.
	HasRoot bool
	Root    chain.Hash
	Totals  chain.Amounts

	// Holders is how many rows the tree would hold, for the message when the chain and
	// the Recompute disagree about whether there is a tree at all.
	Holders int

	// ExpectedCarryIn is what the previous Epoch should have left behind — the carryOut
	// of the previous rooted Epoch's own Recompute, plus the funding of every Skipped
	// Epoch between — and CarryFrom says where it came from, for the line.
	ExpectedCarryIn chain.Amounts
	CarryFrom       string
}

// Posted is what the chain holds for the Epoch: the Distributor's ledger() and the
// RootPosted log the Root was committed with.
type Posted struct {
	// HasRoot is ledger().rootPostedAt != 0. Skipped is a funded Epoch a later Root passed
	// over, its funding rolled into Carry; it never has a Root.
	HasRoot bool
	Skipped bool
	Root    chain.Hash

	// Totals and CarryIn are the RootPosted log's; Funded is ledger()'s, which cannot
	// change once the Epoch is rooted or Skipped. Totals and CarryIn are zero when HasRoot
	// is false.
	Totals  chain.Amounts
	Funded  chain.Amounts
	CarryIn chain.Amounts
}

// Assessment is what Assess found: the Verdict, the four Assertions behind it, and — for
// INDETERMINATE — why nothing could be asserted, with no Assertions at all.
type Assessment struct {
	Verdict    Verdict
	Assertions []Assertion
	Reason     string
}

// Assess compares a Recompute with what the chain posted, and — when expected is not nil —
// with an Expectation: a Root someone expects for the Epoch (--expect, ticket 15).
//
// The Assertions are always four and always in ADR-0002's order, five with an Expectation. An
// Assertion with nothing to compare is NotApplicable rather than dropped, so the reader
// never has to wonder whether a missing line was a pass.
//
// An Expectation changes one thing: an Epoch with no Root posted is no longer INDETERMINATE.
// The warm Signer of engineering spec §3.2 signs a Root before it is posted, and the
// comparison it needs — "is this the Root I recompute?" — has to happen inside this binary
// and not in a wrapper, or the checksum pin guards the wrong code. With a Root posted the
// Expectation is checked against it as a fifth line, and the four lines are as they were.
func Assess(recompute Recompute, posted Posted, expected *chain.Hash) Assessment {
	switch {
	case posted.HasRoot:
		assessed := assessPosted(recompute, posted)
		if expected != nil {
			assessed = withExpected(assessed, assertExpected(*expected, posted.Root))
		}

		return assessed
	case posted.Skipped:
		assessed := assessSkipped(recompute)
		if expected != nil {
			assessed = withExpected(assessed, Assertion{Name: AssertExpected, Status: Fail, Detail: fmt.Sprintf(
				"expected %s, but the Epoch was skipped by a later Root: nothing can be posted for it", expected)})
		}

		return assessed
	case expected != nil:
		return assessExpected(recompute, posted, *expected)
	case recompute.HasRoot:
		// The Root is simply not up yet: the Epoch closed, the Verifier has a tree, and the
		// Distributor has nothing to compare it to. That is not MISMATCH — nothing has been
		// checked and found wrong — and it must not be MATCH.
		return Assessment{
			Verdict: Indeterminate,
			Reason:  fmt.Sprintf("no Root posted yet: the Recompute has one over %d Holders", recompute.Holders),
		}
	default:
		// Spec §5: a funded Epoch with no eligible Holders gets no Root, and the Verifier
		// agrees with the chain that there is nothing to post. The same when the Epoch was
		// never funded at all: the contract marks only funded Epochs Skipped, so an
		// unfunded gap stays unmarked forever, and there is still nothing to post.
		return fromAssertions([]Assertion{
			{Name: AssertRoot, Status: Pass, Detail: "no Root posted, and none recomputed: no eligible Holders"},
			{Name: AssertTotals, Status: Pass, Detail: "nothing committed, and nothing allocated"},
			{Name: AssertCap, Status: NotApplicable, Detail: "no totals committed"},
			{Name: AssertCarryIn, Status: NotApplicable, Detail: "no Root posted; the funding stays as Carry for the next Root"},
		})
	}
}

func assessPosted(recompute Recompute, posted Posted) Assessment {
	return fromAssertions([]Assertion{
		assertRoot(recompute, posted),
		assertTotals(recompute.Totals, posted.Totals),
		assertCap(posted),
		assertCarryIn(recompute, posted.CarryIn),
	})
}

// assessExpected is an Expectation with no Root posted: it stands in for the posted Root. The Root line compares the Recompute with it. Totals has nothing posted to compare
// with, so the line shows the Recompute's own, which is what a MATCH vouches for. The cap is
// the Recompute's totals against the ledger's funded plus the carryIn the Recompute ran
// with, which without a Root is the expected one; and carryIn is that expected value with
// its provenance, since the chain holds no posted value yet. So MATCH reads: "my Recompute
// is the Root you expect, and it fits the cap".
func assessExpected(recompute Recompute, posted Posted, expected chain.Hash) Assessment {
	if !recompute.HasRoot {
		return fromAssertions([]Assertion{
			{Name: AssertRoot, Status: Fail, Detail: fmt.Sprintf(
				"expected %s, but the Recompute has no eligible Holders and so no Root", expected)},
			{Name: AssertTotals, Status: Pass, Detail: "nothing allocated"},
			{Name: AssertCap, Status: NotApplicable, Detail: "no totals recomputed"},
			{Name: AssertCarryIn, Status: NotApplicable, Detail: "no Root posted; the funding stays as Carry for the next Root"},
			expectedStoodIn(),
		})
	}

	root := Assertion{Name: AssertRoot, Status: Pass, Detail: fmt.Sprintf("%s, as expected; no Root posted yet", expected)}
	if recompute.Root != expected {
		root = Assertion{Name: AssertRoot, Status: Fail, Detail: fmt.Sprintf("recomputed %s, expected %s", recompute.Root, expected)}
	}

	return fromAssertions([]Assertion{
		root,
		{Name: AssertTotals, Status: Pass, Detail: fmt.Sprintf("%s recomputed; nothing posted to compare with", formatAmounts(recompute.Totals))},
		assertCapOver(recompute.Totals, posted.Funded, recompute.ExpectedCarryIn),
		{Name: AssertCarryIn, Status: Pass, Detail: fmt.Sprintf(
			"%s = %s; nothing posted to compare with", formatAmounts(recompute.ExpectedCarryIn), recompute.CarryFrom)},
		expectedStoodIn(),
	})
}

// expectedStoodIn is the fifth line when there was no posted Root for the Expectation to
// be compared with: it was the Root line's comparand instead.
func expectedStoodIn() Assertion {
	return Assertion{Name: AssertExpected, Status: NotApplicable, Detail: "no Root posted: the expectation was the root line's comparand"}
}

// withExpected appends the fifth line and re-derives the Verdict.
func withExpected(assessed Assessment, expected Assertion) Assessment {
	return fromAssertions(append(assessed.Assertions, expected))
}

func assertExpected(expected, posted chain.Hash) Assertion {
	if expected != posted {
		return Assertion{Name: AssertExpected, Status: Fail, Detail: fmt.Sprintf("expected %s, posted %s", expected, posted)}
	}

	return Assertion{Name: AssertExpected, Status: Pass, Detail: "equals the posted Root"}
}

func assessSkipped(recompute Recompute) Assessment {
	root := Assertion{Name: AssertRoot, Status: Pass, Detail: "skipped by a later Root, and no Root recomputed: no eligible Holders"}
	totals := Assertion{Name: AssertTotals, Status: Pass, Detail: "nothing committed, and nothing allocated"}

	if recompute.HasRoot {
		root = Assertion{Name: AssertRoot, Status: Fail, Detail: fmt.Sprintf(
			"skipped by a later Root, but the Recompute has a Root %s over %d Holders", recompute.Root, recompute.Holders)}
		totals = Assertion{Name: AssertTotals, Status: Fail, Detail: fmt.Sprintf(
			"nothing committed, but the Recompute allocates %s", formatAmounts(recompute.Totals))}
	}

	return fromAssertions([]Assertion{
		root,
		totals,
		{Name: AssertCap, Status: NotApplicable, Detail: "no totals committed"},
		{Name: AssertCarryIn, Status: NotApplicable, Detail: "skipped; the funding rolled into the next Root's Carry"},
	})
}

// fromAssertions is MISMATCH if any Assertion failed, MATCH otherwise.
func fromAssertions(assertions []Assertion) Assessment {
	verdict := Match
	for _, a := range assertions {
		if a.Status == Fail {
			verdict = Mismatch
		}
	}

	return Assessment{Verdict: verdict, Assertions: assertions}
}

func assertRoot(recompute Recompute, posted Posted) Assertion {
	switch {
	case !recompute.HasRoot:
		return Assertion{Name: AssertRoot, Status: Fail, Detail: fmt.Sprintf(
			"posted %s, but the Recompute has no eligible Holders and so no Root", posted.Root)}
	case recompute.Root != posted.Root:
		return Assertion{Name: AssertRoot, Status: Fail, Detail: fmt.Sprintf("recomputed %s, posted %s", recompute.Root, posted.Root)}
	default:
		return Assertion{Name: AssertRoot, Status: Pass, Detail: posted.Root.String()}
	}
}

func assertTotals(recomputed, posted chain.Amounts) Assertion {
	if t, differ := firstDifference(recomputed, posted); differ {
		return Assertion{Name: AssertTotals, Status: Fail, Detail: fmt.Sprintf(
			"token %d: recomputed %s, posted %s (recomputed %s, posted %s)",
			t, recomputed[t], posted[t], formatAmounts(recomputed), formatAmounts(posted))}
	}

	return Assertion{Name: AssertTotals, Status: Pass, Detail: formatAmounts(posted)}
}

// assertCap is ADR-0002's third line: totals[i] <= funded[i] + carryIn[i], the invariant
// that bounds a malicious Root to one Epoch's funding. The contract enforces it in postRoot;
// checking it again is cheap and means the Verifier's MATCH does not rest on the contract
// having done so.
func assertCap(posted Posted) Assertion {
	return assertCapOver(posted.Totals, posted.Funded, posted.CarryIn)
}

// assertCapOver is the cap for whichever totals and carryIn are being asserted: the
// posted ones when a Root stands, the Recompute's own under an Expectation.
func assertCapOver(totals, funded, carryIn chain.Amounts) Assertion {
	for t := range totals {
		available := new(big.Int).Add(funded[t], carryIn[t])
		if totals[t].Cmp(available) > 0 {
			return Assertion{Name: AssertCap, Status: Fail, Detail: fmt.Sprintf(
				"token %d: totals %s > funded %s + carryIn %s", t, totals[t], funded[t], carryIn[t])}
		}
	}

	return Assertion{Name: AssertCap, Status: Pass, Detail: fmt.Sprintf(
		"totals <= funded %s + carryIn %s in every token", formatAmounts(funded), formatAmounts(carryIn))}
}

func assertCarryIn(recompute Recompute, posted chain.Amounts) Assertion {
	if t, differ := firstDifference(recompute.ExpectedCarryIn, posted); differ {
		return Assertion{Name: AssertCarryIn, Status: Fail, Detail: fmt.Sprintf(
			"token %d: posted %s, but %s leaves %s (posted %s, expected %s)",
			t, posted[t], recompute.CarryFrom, recompute.ExpectedCarryIn[t],
			formatAmounts(posted), formatAmounts(recompute.ExpectedCarryIn))}
	}

	return Assertion{Name: AssertCarryIn, Status: Pass, Detail: fmt.Sprintf("%s = %s", formatAmounts(posted), recompute.CarryFrom)}
}

// firstDifference is the first token at which two Amounts differ. A nil on either side is
// a difference: nothing here may read nil as zero.
func firstDifference(a, b chain.Amounts) (int, bool) {
	for t := range a {
		if a[t] == nil || b[t] == nil || a[t].Cmp(b[t]) != 0 {
			return t, true
		}
	}

	return 0, false
}

// formatAmounts is the five values as "[a b c d e]", in the token order SPY, NVDA, MU,
// SPCX, USDG.
func formatAmounts(amounts chain.Amounts) string {
	return list(Decimals(amounts))
}

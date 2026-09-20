package report_test

import (
	"math/big"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/chain"
	"github.com/nutzwtf/nutz-verify/internal/report"
)

func amounts(vs ...int64) chain.Amounts {
	var out chain.Amounts
	for i, v := range vs {
		out[i] = big.NewInt(v)
	}

	return out
}

var (
	rootA = chain.Hash{0xa1}
	rootB = chain.Hash{0xb2}
)

// agreeing is an Epoch whose Recompute and posted Root agree on everything.
func agreeing() (report.Recompute, report.Posted) {
	recompute := report.Recompute{
		HasRoot:         true,
		Root:            rootA,
		Totals:          amounts(1125, 0, 375, 0, 5),
		ExpectedCarryIn: amounts(500, 0, 0, 0, 7),
		CarryFrom:       "carryOut of epoch 999",
	}
	posted := report.Posted{
		HasRoot: true,
		Root:    rootA,
		Totals:  amounts(1125, 0, 375, 0, 5),
		Funded:  amounts(1000, 0, 500, 1, 0),
		CarryIn: amounts(500, 0, 0, 0, 7),
	}

	return recompute, posted
}

func statuses(assertions []report.Assertion) string {
	parts := make([]string, 0, len(assertions))
	for _, a := range assertions {
		parts = append(parts, a.Name+":"+string(a.Status))
	}

	return strings.Join(parts, " ")
}

func TestAssess_AgreementIsMatchOnFourLines(t *testing.T) {
	t.Parallel()

	recompute, posted := agreeing()
	got := report.Assess(recompute, posted, nil)

	if got.Verdict != report.Match {
		t.Fatalf("verdict = %s (%s), want MATCH", got.Verdict, got.Reason)
	}
	if got, want := statuses(got.Assertions), "root:pass totals:pass cap:pass carryIn:pass"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
}

func TestAssess_ABrokenInvariantNamesItsOwnLine(t *testing.T) {
	t.Parallel()

	// Four lines, not one boolean: a MISMATCH has to say which invariant broke. Each break
	// below touches one input and must fail exactly one Assertion, so a reader at 3am is
	// told where to look rather than merely that something is wrong.
	tests := []struct {
		name   string
		breaks func(*report.Recompute, *report.Posted)
		want   string
		detail string
	}{
		{
			name:   "a different Root",
			breaks: func(r *report.Recompute, p *report.Posted) { p.Root = rootB },
			want:   "root:fail totals:pass cap:pass carryIn:pass",
			detail: "recomputed 0xa100",
		},
		{
			name:   "different totals",
			breaks: func(r *report.Recompute, p *report.Posted) { p.Totals = amounts(1125, 0, 376, 0, 5) },
			want:   "root:pass totals:fail cap:pass carryIn:pass",
			detail: "token 2: recomputed 375, posted 376",
		},
		{
			name: "totals over the cap",
			breaks: func(r *report.Recompute, p *report.Posted) {
				// Posted totals agree with the Recompute; it is the funding that does not cover
				// them, which is what a Root spending more than the Epoch has looks like.
				p.Funded = amounts(1000, 0, 300, 1, 0)
			},
			want:   "root:pass totals:pass cap:fail carryIn:pass",
			detail: "token 2: totals 375 > funded 300 + carryIn 0",
		},
		{
			name:   "carryIn not what the previous Epoch left",
			breaks: func(r *report.Recompute, p *report.Posted) { r.ExpectedCarryIn = amounts(500, 0, 0, 0, 8) },
			want:   "root:pass totals:pass cap:pass carryIn:fail",
			detail: "token 4: posted 7, but carryOut of epoch 999 leaves 8",
		},
		{
			name:   "a Root posted over no eligible Holders",
			breaks: func(r *report.Recompute, p *report.Posted) { r.HasRoot, r.Root = false, chain.Hash{} },
			want:   "root:fail totals:pass cap:pass carryIn:pass",
			detail: "no eligible Holders and so no Root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recompute, posted := agreeing()
			tt.breaks(&recompute, &posted)

			got := report.Assess(recompute, posted, nil)
			if got.Verdict != report.Mismatch {
				t.Fatalf("verdict = %s, want MISMATCH", got.Verdict)
			}
			if got := statuses(got.Assertions); got != tt.want {
				t.Errorf("assertions = %q, want %q", got, tt.want)
			}

			var failed report.Assertion
			for _, a := range got.Assertions {
				if a.Status == report.Fail {
					failed = a
				}
			}
			if !strings.Contains(failed.Detail, tt.detail) {
				t.Errorf("detail = %q, want it to contain %q", failed.Detail, tt.detail)
			}
		})
	}
}

func TestAssess_NoRootPostedYetIsIndeterminate(t *testing.T) {
	t.Parallel()

	// The Recompute has a tree and the Distributor has nothing. Nothing has been checked
	// and found wrong, so not MISMATCH; and nothing has been checked, so never MATCH.
	recompute, _ := agreeing()
	recompute.Holders = 2

	got := report.Assess(recompute, report.Posted{}, nil)
	if got.Verdict != report.Indeterminate {
		t.Fatalf("verdict = %s, want INDETERMINATE", got.Verdict)
	}
	if !strings.Contains(got.Reason, "no Root posted yet") || !strings.Contains(got.Reason, "2 Holders") {
		t.Errorf("reason = %q", got.Reason)
	}
}

func TestAssess_AFundedEpochWithNoRootAndNoHoldersIsMatch(t *testing.T) {
	t.Parallel()

	// Spec §5: W == 0 is a legitimate no-Root state. The chain has nothing, the Recompute
	// has nothing, and they agree — exit 0, never INDETERMINATE.
	got := report.Assess(report.Recompute{}, report.Posted{Funded: amounts(1000, 0, 0, 0, 0)}, nil)
	if got.Verdict != report.Match {
		t.Fatalf("verdict = %s, want MATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:pass totals:pass cap:n/a carryIn:n/a"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
}

func TestAssess_ASkippedEpoch(t *testing.T) {
	t.Parallel()

	// Skipped is the contract's word for "a later Root passed this Epoch over and its
	// funding rolled into Carry". Right when the Recompute finds no Holders either; a
	// MISMATCH when the Recompute has a tree the indexer never posted.
	got := report.Assess(report.Recompute{}, report.Posted{Skipped: true}, nil)
	if got.Verdict != report.Match {
		t.Fatalf("no Holders: verdict = %s, want MATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:pass totals:pass cap:n/a carryIn:n/a"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}

	recompute, _ := agreeing()
	got = report.Assess(recompute, report.Posted{Skipped: true}, nil)
	if got.Verdict != report.Mismatch {
		t.Fatalf("with a tree: verdict = %s, want MISMATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:fail totals:fail cap:n/a carryIn:n/a"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
}

func TestVerdict_ExitCodes(t *testing.T) {
	t.Parallel()

	// The load-bearing rule: 2 is never 0. The Signer signs on 0 alone.
	for v, want := range map[report.Verdict]int{report.Match: 0, report.Mismatch: 1, report.Indeterminate: 2} {
		if got := v.ExitCode(); got != want {
			t.Errorf("%s exits %d, want %d", v, got, want)
		}
	}
	if got := report.Verdict("").ExitCode(); got != 2 {
		t.Errorf("an unset Verdict exits %d, want 2", got)
	}
}

func TestWorst_IndeterminateOutranksMismatch(t *testing.T) {
	t.Parallel()

	if got := report.Worst(report.Match, report.Mismatch, report.Match); got != report.Mismatch {
		t.Errorf("Worst = %s, want MISMATCH", got)
	}
	if got := report.Worst(report.Mismatch, report.Indeterminate); got != report.Indeterminate {
		t.Errorf("Worst = %s, want INDETERMINATE", got)
	}
	if got := report.Worst(); got != report.Match {
		t.Errorf("Worst of nothing = %s, want MATCH", got)
	}
}

// Ticket 15: an Expectation, judged before the Root is posted.
func TestAssess_AnExpectationStandsInForTheRootNotYetPosted(t *testing.T) {
	t.Parallel()

	// The chain holds no Root; the Recompute has one. Without an Expectation that is
	// INDETERMINATE. With one, it is the root line's comparand, and MATCH says
	// "my Recompute is the Root you expect, and it fits the cap".
	recompute, _ := agreeing()
	recompute.Holders = 2
	chainSide := report.Posted{Funded: amounts(1000, 0, 500, 1, 0)}

	got := report.Assess(recompute, chainSide, &rootA)
	if got.Verdict != report.Match {
		t.Fatalf("verdict = %s (%s), want MATCH", got.Verdict, got.Reason)
	}
	if got, want := statuses(got.Assertions), "root:pass totals:pass cap:pass carryIn:pass expected:n/a"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
	if d := got.Assertions[2].Detail; !strings.Contains(d, "funded [1000 0 500 1 0] + carryIn [500 0 0 0 7]") {
		t.Errorf("the cap is asserted against funded plus the expected carryIn; detail = %q", d)
	}
	if d := got.Assertions[3].Detail; !strings.Contains(d, "carryOut of epoch 999") {
		t.Errorf("carryIn names its provenance; detail = %q", d)
	}

	other := rootA
	other[0] ^= 0xff
	got = report.Assess(recompute, chainSide, &other)
	if got.Verdict != report.Mismatch {
		t.Fatalf("another Expectation: verdict = %s, want MISMATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:fail totals:pass cap:pass carryIn:pass expected:n/a"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
	if d := got.Assertions[0].Detail; !strings.Contains(d, "recomputed "+rootA.String()) || !strings.Contains(d, "expected "+other.String()) {
		t.Errorf("root detail = %q", d)
	}
}

func TestAssess_AnExpectationThatBreaksTheCapIsMismatch(t *testing.T) {
	t.Parallel()

	// The Recompute agrees with the Expectation but allocates more than the Distributor
	// holds for the Epoch: the cap line fails, as it would against a posted Root.
	recompute, _ := agreeing()
	got := report.Assess(recompute, report.Posted{Funded: amounts(100, 0, 500, 1, 0)}, &rootA)
	if got.Verdict != report.Mismatch {
		t.Fatalf("verdict = %s, want MISMATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:pass totals:pass cap:fail carryIn:pass expected:n/a"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
}

func TestAssess_AnExpectationWhenTheRecomputeHasNoRootIsMismatch(t *testing.T) {
	t.Parallel()

	got := report.Assess(report.Recompute{}, report.Posted{Funded: amounts(1000, 0, 0, 0, 0)}, &rootA)
	if got.Verdict != report.Mismatch {
		t.Fatalf("verdict = %s, want MISMATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:fail totals:pass cap:n/a carryIn:n/a expected:n/a"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
	if d := got.Assertions[0].Detail; !strings.Contains(d, "no eligible Holders") {
		t.Errorf("root detail = %q", d)
	}
}

func TestAssess_AnExpectationAgainstAPostedRootIsAFifthLine(t *testing.T) {
	t.Parallel()

	// A Root is up: the four lines are as they were, and the fifth says whether the
	// Expectation is the posted Root.
	recompute, posted := agreeing()

	got := report.Assess(recompute, posted, &rootA)
	if got.Verdict != report.Match {
		t.Fatalf("verdict = %s, want MATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:pass totals:pass cap:pass carryIn:pass expected:pass"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}

	other := rootA
	other[31] ^= 0xff
	got = report.Assess(recompute, posted, &other)
	if got.Verdict != report.Mismatch {
		t.Fatalf("another Expectation: verdict = %s, want MISMATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:pass totals:pass cap:pass carryIn:pass expected:fail"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
	if d := got.Assertions[4].Detail; !strings.Contains(d, "expected "+other.String()) || !strings.Contains(d, "posted "+rootA.String()) {
		t.Errorf("expected detail = %q", d)
	}

	// Skipped: nothing can be posted for the Epoch any more, so any Expectation is wrong.
	got = report.Assess(report.Recompute{}, report.Posted{Skipped: true}, &rootA)
	if got.Verdict != report.Mismatch {
		t.Fatalf("skipped: verdict = %s, want MISMATCH", got.Verdict)
	}
	if got, want := statuses(got.Assertions), "root:pass totals:pass cap:n/a carryIn:n/a expected:fail"; got != want {
		t.Errorf("assertions = %q, want %q", got, want)
	}
}

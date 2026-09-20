package main

import (
	"context"
	"testing"

	"github.com/nutzwtf/nutz-verify/chain"
	"github.com/nutzwtf/nutz-verify/epoch"
	"github.com/nutzwtf/nutz-verify/internal/report"
)

// assess lays out the Engine's Result and compares it with the chain; it recomputes
// nothing of its own. Every value in the report is one the Result carries, read back.
func TestAssess_ReadsTheEngineResult(t *testing.T) {
	ctx := context.Background()
	node := scenario()

	reader, err := chain.New(chain.Config{
		Endpoints:      []string{node.Serve(t)},
		Token:          testDeployment.Token,
		Distributor:    testDeployment.Distributor,
		CallsPerSecond: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	tip, err := reader.Head(ctx, chain.Safe)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := epoch.New(reader, tip, t.TempDir(), testDeployment, epoch.Options{})
	if err != nil {
		t.Fatal(err)
	}

	result, err := eng.Compute(ctx, 1000)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	got := assess(result, nil)

	if got.Verdict != report.Match {
		t.Fatalf("Verdict = %s (%s), want MATCH", got.Verdict, got.Reason)
	}
	if got.ID != result.EpochID || got.Holders != len(result.Allocations) || got.Skipped != result.Skipped {
		t.Errorf("header fields differ from the Result: %+v", got)
	}
	if got.Recomputed.Root != result.Root.String() {
		t.Errorf("Recomputed.Root = %s, Result.Root %s", got.Recomputed.Root, result.Root)
	}
	if got.Posted == nil || got.Posted.Root != result.Posted.Root.String() || got.Posted.Block != result.Posted.BlockNumber {
		t.Errorf("Posted = %+v, Result.Posted %+v", got.Posted, result.Posted)
	}
	if got.EndBlock == nil || got.EndBlock.Number != result.EndBlock.Number {
		t.Errorf("EndBlock = %+v, Result.EndBlock %+v", got.EndBlock, result.EndBlock)
	}
	for what, pair := range map[string][2][]string{
		"totals":   {got.Recomputed.Totals, report.Decimals(result.Totals)},
		"carryOut": {got.Recomputed.CarryOut, report.Decimals(result.CarryOut)},
		"carryIn":  {got.Recomputed.CarryIn, report.Decimals(result.CarryIn)},
	} {
		for i := range pair[0] {
			if pair[0][i] != pair[1][i] {
				t.Errorf("%s[%d] = %s in the report, %s in the Result", what, i, pair[0][i], pair[1][i])
			}
		}
	}
	if got.Recomputed.TotalWeight != result.TotalWeight.String() {
		t.Errorf("TotalWeight = %s, Result %s", got.Recomputed.TotalWeight, result.TotalWeight)
	}
	if got.Window.Start != result.Window.Start || got.Window.End != result.Window.End {
		t.Errorf("Window = %+v, Result %+v", got.Window, result.Window)
	}
}

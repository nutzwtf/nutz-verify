package main

import (
	"context"

	"github.com/nutzwtf/nutz-verify/chain"
	"github.com/nutzwtf/nutz-verify/epoch"
	"github.com/nutzwtf/nutz-verify/internal/report"
)

// epoch is one Recompute: the Engine rebuilds the Epoch — every rooted one from deploy
// with --chain — and each Result is compared with what the chain holds, the target's also
// with the Expectation if one was given. With --artifacts a published bundle is diffed
// against the target's Result afterwards.
//
// Nothing here computes: every number in the report is read off a Result (ADR-0006), so
// the binary the Signer runs and the library the indexer links agree by construction.
func (v *verifier) epoch(ctx context.Context, id uint64, rep *report.Report) error {
	// The Engine syncs the Cache on its way; whatever it did is reported, also after a
	// failure, so a reorg or a repair never goes unmentioned.
	defer v.reportCache(rep)

	var results []epoch.Result
	var err error
	if v.opts.chain {
		results, err = v.engine.Walk(ctx, id)
	} else {
		var one epoch.Result
		one, err = v.engine.Compute(ctx, id)
		results = []epoch.Result{one}
	}
	if err != nil {
		return err
	}

	verdicts := make([]report.Verdict, 0, len(results))
	for i, r := range results {
		var expected *chain.Hash
		if i == len(results)-1 {
			expected = v.opts.expect // the Expectation is for the Epoch asked about, not the links --chain walks
		}
		assessed := assess(r, expected)
		rep.Epochs = append(rep.Epochs, assessed)
		verdicts = append(verdicts, assessed.Verdict)
	}
	rep.Verdict = report.Worst(verdicts...)

	target := rep.Epochs[len(rep.Epochs)-1]
	if target.Reason != "" {
		rep.Reason = target.Reason
	}

	// After the Verdict, from a separate argument, into a separate section: the bundle is
	// compared with the Recompute and can never be one of its inputs (ADR-0002).
	if v.opts.artifacts != "" {
		artifacts := compareArtifacts(v.opts.artifacts, results[len(results)-1])
		rep.Artifacts = &artifacts
	}

	return nil
}

// sync advances the Cache to the tip and reports what it did. No Verdict: nothing was
// recomputed.
func (v *verifier) sync(ctx context.Context, rep *report.Report) error {
	_, err := v.engine.Sync(ctx)
	v.reportCache(rep)

	return err
}

// reportCache copies the Engine's Cache status into the run header, if it synced.
func (v *verifier) reportCache(rep *report.Report) {
	synced, ok := v.engine.Synced()
	if !ok {
		return
	}

	status := &report.Cache{
		Dir:      synced.Dir,
		Records:  synced.Records,
		Through:  synced.Through,
		Appended: synced.Appended,
		Repaired: synced.Repaired,
	}
	if synced.Reorg != nil {
		status.Reorg = &report.Reorg{Block: synced.Reorg.Block, Dropped: synced.Reorg.Dropped}
	}
	rep.Run.Cache = status
}

// assess compares one Result with what the chain holds — and with an Expectation, when
// there is one — and lays it all out for the report. It reads the Result and recomputes
// nothing of its own.
func assess(r epoch.Result, expected *chain.Hash) report.Epoch {
	recompute := report.Recompute{
		HasRoot:         r.HasRoot,
		Root:            r.Root,
		Totals:          r.Totals,
		Holders:         len(r.Allocations),
		ExpectedCarryIn: r.ExpectedCarryIn,
		CarryFrom:       r.CarryFrom,
	}
	chainSide := report.Posted{Skipped: r.Skipped, Funded: r.Funded}
	if r.Posted != nil {
		chainSide.HasRoot, chainSide.Root = true, r.Posted.Root
		chainSide.Totals, chainSide.CarryIn = r.Posted.Totals, r.Posted.CarryIn
	}

	assessed := report.Assess(recompute, chainSide, expected)

	out := report.Epoch{
		ID:      r.EpochID,
		Window:  report.Window{Start: r.Window.Start, End: r.Window.End},
		Holders: len(r.Allocations),
		Recomputed: report.Recomputed{
			HasRoot:     r.HasRoot,
			Totals:      report.Decimals(r.Totals),
			CarryOut:    report.Decimals(r.CarryOut),
			TotalWeight: r.TotalWeight.String(),
			CarryIn:     report.Decimals(r.CarryIn),
		},
		Skipped:    r.Skipped,
		Assertions: assessed.Assertions,
		Verdict:    assessed.Verdict,
		Reason:     assessed.Reason,
	}
	if expected != nil {
		out.Expected = expected.String()
	}
	if r.HasRoot {
		out.Recomputed.Root = r.Root.String()
	}
	if r.EndBlock != nil {
		out.EndBlock = &report.Block{Number: r.EndBlock.Number, Hash: r.EndBlock.Hash.String()}
	}
	if r.Posted != nil {
		out.Posted = &report.PostedRoot{
			Root:    r.Posted.Root.String(),
			Block:   r.Posted.BlockNumber,
			Totals:  report.Decimals(r.Posted.Totals),
			Funded:  report.Decimals(r.Funded),
			CarryIn: report.Decimals(r.Posted.CarryIn),
		}
	}

	return out
}

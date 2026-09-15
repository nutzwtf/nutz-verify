package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/nutzwtf/nutz-verify/internal/alloc"
	"github.com/nutzwtf/nutz-verify/internal/cache"
	"github.com/nutzwtf/nutz-verify/internal/chain"
	"github.com/nutzwtf/nutz-verify/internal/report"
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// syncChunk is how many blocks each Cache read covers. Smaller than the Cache's default:
// the chunk is also how often progress is printed and where an interrupted sync resumes,
// and at USDG's density on the public endpoint 2,000 blocks is about two minutes — so a
// twenty-minute catch-up says something every two minutes instead of nothing for ten.
const syncChunk = 2000

// epoch is one Recompute: read what the chain holds for the Epoch, rebuild the
// Allocations from the Cache, compare, and — with --artifacts — diff a published bundle
// against the result afterwards.
func (v *verifier) epoch(ctx context.Context, id uint64, tip chain.Tip, rep *report.Report) error {
	endBlock, err := v.reader.EndBlock(ctx, id, tip)
	if err != nil {
		return err
	}

	history, err := v.history(ctx, endBlock, rep)
	if err != nil {
		return err
	}

	exclusions, err := v.exclusions(ctx, endBlock.Number)
	if err != nil {
		return err
	}

	// The Carry the target Epoch should have been posted with is what the previous rooted
	// Epoch left behind, plus the funding of every Epoch between that was passed over. With
	// --chain that walk starts at deploy and asserts every link on the way.
	walk := walker{v: v, history: history, exclusions: exclusions, rep: rep}

	var carry chain.Amounts
	var from string
	if v.opts.chain {
		carry, from, err = walk.fromDeploy(ctx, id, tip)
	} else {
		carry, from, err = walk.fromPrevious(ctx, id, tip)
	}
	if err != nil {
		return err
	}

	posted, err := v.standingRoot(ctx, id, endBlock.Number+1, tip.Block.Number)
	if err != nil {
		return err
	}

	target, result, err := walk.assess(ctx, id, posted, carry, from)
	if err != nil {
		return err
	}
	target.EndBlock = &report.Block{Number: endBlock.Number, Hash: endBlock.Hash.String()}
	rep.Epochs = append(rep.Epochs, target)

	verdicts := make([]report.Verdict, 0, len(rep.Epochs))
	for _, e := range rep.Epochs {
		verdicts = append(verdicts, e.Verdict)
	}
	rep.Verdict = report.Worst(verdicts...)
	if target.Reason != "" {
		rep.Reason = target.Reason
	}

	// After the Verdict, from a separate argument, into a separate section: the bundle is
	// compared with the Recompute and can never be one of its inputs (ADR-0002).
	if v.opts.artifacts != "" {
		artifacts := compareArtifacts(v.opts.artifacts, result)
		rep.Artifacts = &artifacts
	}

	return nil
}

// sync advances the Cache to the tip and reports what it did. No Verdict: nothing was
// recomputed.
func (v *verifier) sync(ctx context.Context, tip chain.Tip, rep *report.Report) error {
	_, err := v.history(ctx, tip.Block, rep)

	return err
}

// history opens the Cache, advances it to through, and returns every transfer it holds in
// block order, in the rules' own type.
func (v *verifier) history(ctx context.Context, through chain.Block, rep *report.Report) ([]twab.Transfer, error) {
	dir, err := cache.Dir(v.dep.ChainID, v.dep.Token)
	if err != nil {
		return nil, err
	}

	c, err := cache.Open(dir, cache.Options{
		ChainID: v.dep.ChainID,
		Token:   v.dep.Token,
		Start:   v.dep.TokenBlock,
		Fresh:   v.opts.fresh,
	})
	if err != nil {
		return nil, err
	}
	defer c.Close()

	status := &report.Cache{Dir: dir}
	rep.Run.Cache = status
	if repaired := c.Repaired(); repaired != nil {
		status.Repaired = repaired.String()
		v.progress("%s", repaired)
	}

	from := v.dep.TokenBlock
	if last, ok := c.Last(); ok {
		from = last.BlockNumber + 1
	}
	if from <= through.Number {
		v.progress("cache: syncing blocks %d..%d", from, through.Number)
	}

	synced, err := c.Sync(ctx, v.reader, through, cache.SyncOptions{
		Chunk: syncChunk,
		Progress: func(block uint64) {
			v.progress("cache: through block %d of %d", block, through.Number)
		},
	})
	if synced.Reorg != nil {
		status.Reorg = &report.Reorg{Block: synced.Reorg.Block, Dropped: synced.Reorg.Dropped}
	}
	status.Appended = synced.Appended
	if err != nil {
		return nil, err
	}

	if err := c.Close(); err != nil {
		return nil, err
	}

	status.Records = c.Len()
	if last, ok := c.Last(); ok {
		status.Through = last.BlockNumber
	}

	// One more open: Close is what makes the synced records durable, and reading them
	// back through a fresh scan verifies every CRC over exactly the bytes on disk.
	c, err = cache.Open(dir, cache.Options{ChainID: v.dep.ChainID, Token: v.dep.Token, Start: v.dep.TokenBlock})
	if err != nil {
		return nil, err
	}
	defer c.Close()

	history := make([]twab.Transfer, 0, c.Len())
	if err := c.Each(func(r cache.Record) error {
		history = append(history, twab.Transfer{
			Timestamp: r.Timestamp,
			From:      twab.Address(r.From),
			To:        twab.Address(r.To),
			Value:     r.Value,
		})

		return nil
	}); err != nil {
		return nil, err
	}

	return history, nil
}

// exclusions is the whole ExcludedAppended stream through the end block, in the rules'
// type. Read live: it is a handful of logs from the Distributor's deploy block on.
func (v *verifier) exclusions(ctx context.Context, through uint64) ([]twab.Exclusion, error) {
	stream, err := v.reader.Exclusions(ctx, v.dep.DistributorBlock, through)
	if err != nil {
		return nil, err
	}

	out := make([]twab.Exclusion, 0, len(stream))
	for _, e := range stream {
		out = append(out, twab.Exclusion{Timestamp: e.Timestamp, Account: twab.Address(e.Account)})
	}

	return out, nil
}

// walker carries the Carry forward Epoch by Epoch and assesses each rooted one.
type walker struct {
	v          *verifier
	history    []twab.Transfer
	exclusions []twab.Exclusion
	rep        *report.Report
}

// fromPrevious finds the previous rooted Epoch, recomputes it, and carries its carryOut
// forward over the Epochs between, returning what the target should have been posted
// with. Without a previous Root the walk starts at deploy with nothing.
func (w walker) fromPrevious(ctx context.Context, target uint64, tip chain.Tip) (chain.Amounts, string, error) {
	previous, found, err := w.v.previousRoot(ctx, target, tip.Block.Number)
	if err != nil {
		return chain.Amounts{}, "", err
	}
	if !found {
		return w.fromDeploy(ctx, target, tip)
	}

	// The previous Epoch's Recompute uses its own posted carryIn: one link is asserted
	// here, and --chain is where the whole chain is.
	_, result, err := w.assess(ctx, previous.ID, &previous, previous.CarryIn, "its own posted carryIn")
	if err != nil {
		return chain.Amounts{}, "", err
	}

	return w.carryOver(ctx, chain.Amounts(result.CarryOut), previous.ID+1, target,
		fmt.Sprintf("carryOut of epoch %d", previous.ID))
}

// fromDeploy walks from the Distributor's first Epoch to the target. With --chain every
// rooted Epoch on the way is recomputed and asserted, and the Carry that reaches the
// target is the recomputed one; otherwise there is no Root before the target, and the
// Carry is the funding of everything before it.
func (w walker) fromDeploy(ctx context.Context, target uint64, tip chain.Tip) (chain.Amounts, string, error) {
	first, err := w.v.deployEpoch(ctx)
	if err != nil {
		return chain.Amounts{}, "", err
	}
	if target < first {
		return chain.Amounts{}, "", fmt.Errorf("epoch %d predates the Distributor, deployed in epoch %d", target, first)
	}

	carry := zeroAmounts()
	from := "nothing before deploy"

	if !w.v.opts.chain {
		return w.carryOver(ctx, carry, first, target, from)
	}

	standing, err := w.v.standingRoots(ctx, first, target, tip.Block.Number)
	if err != nil {
		return chain.Amounts{}, "", err
	}

	skipped := 0
	for id := first; id < target; id++ {
		posted, rooted := standing[id]
		if !rooted {
			carry, err = w.v.book.addFunding(ctx, carry, id)
			if err != nil {
				return chain.Amounts{}, "", err
			}
			skipped++

			continue
		}

		link, result, err := w.assess(ctx, id, &posted, carry, carrySource(from, skipped))
		if err != nil {
			return chain.Amounts{}, "", err
		}
		w.rep.Epochs = append(w.rep.Epochs, link)

		carry, from, skipped = chain.Amounts(result.CarryOut), fmt.Sprintf("carryOut of epoch %d", id), 0
	}

	return carry, carrySource(from, skipped), nil
}

// carryOver adds the funding of every Epoch in [from, to) to carry: each was, or will be,
// passed over by the target's Root, and the contract rolls its funding into Carry.
func (w walker) carryOver(ctx context.Context, carry chain.Amounts, from, to uint64, source string) (chain.Amounts, string, error) {
	skipped := 0
	for id := from; id < to; id++ {
		var err error
		carry, err = w.v.book.addFunding(ctx, carry, id)
		if err != nil {
			return chain.Amounts{}, "", err
		}

		skipped++
	}

	return carry, carrySource(source, skipped), nil
}

// carrySource is where an expected carryIn came from, for the Assertion line.
func carrySource(base string, skipped int) string {
	switch skipped {
	case 0:
		return base
	case 1:
		return base + " + the funding of 1 skipped Epoch"
	default:
		return fmt.Sprintf("%s + the funding of %d skipped Epochs", base, skipped)
	}
}

// assess recomputes one Epoch and compares it with what the chain holds. posted is the
// standing RootPosted log, nil when there is none; expected is what the Carry chain says
// the Epoch should have been posted with, and from says where that came from.
func (w walker) assess(ctx context.Context, id uint64, posted *chain.RootPosted, expected chain.Amounts, from string) (report.Epoch, alloc.Result, error) {
	ledger, err := w.v.book.ledger(ctx, id)
	if err != nil {
		return report.Epoch{}, alloc.Result{}, err
	}

	// Root and totals are recomputed over the carryIn the Root was actually posted with:
	// if that carryIn is wrong, Assertion 4 says so, and the other three still say whether
	// the tree was right for the inputs the indexer had. Running them over the expected
	// Carry instead would fail all four for one mistake.
	carryIn := expected
	if posted != nil {
		carryIn = posted.CarryIn
	}

	result, err := alloc.Compute(alloc.Params{
		EpochID:    id,
		Transfers:  w.history,
		Exclusions: w.exclusions,
		DevWallet:  twab.Address(w.v.dep.DevWallet),
		Funded:     alloc.Amounts(ledger.Funded),
		CarryIn:    alloc.Amounts(carryIn),
	})
	if err != nil {
		return report.Epoch{}, alloc.Result{}, err
	}

	recompute := report.Recompute{
		HasRoot:         result.HasRoot,
		Root:            chain.Hash(result.Root),
		Totals:          chain.Amounts(result.Totals),
		Holders:         len(result.Allocations),
		ExpectedCarryIn: expected,
		CarryFrom:       from,
	}
	chainSide := report.Posted{Skipped: ledger.Skipped, Funded: ledger.Funded}
	if posted != nil {
		chainSide.HasRoot, chainSide.Root = true, posted.Root
		chainSide.Totals, chainSide.CarryIn = posted.Totals, posted.CarryIn
	}

	assessed := report.Assess(recompute, chainSide)

	epoch := report.Epoch{
		ID:      id,
		Window:  report.Window{Start: twab.WindowOf(id).Start, End: twab.WindowOf(id).End},
		Holders: len(result.Allocations),
		Recomputed: report.Recomputed{
			HasRoot:     result.HasRoot,
			Totals:      report.Decimals(chain.Amounts(result.Totals)),
			CarryOut:    report.Decimals(chain.Amounts(result.CarryOut)),
			TotalWeight: result.TotalWeight.String(),
			CarryIn:     report.Decimals(carryIn),
		},
		Skipped:    ledger.Skipped,
		Assertions: assessed.Assertions,
		Verdict:    assessed.Verdict,
		Reason:     assessed.Reason,
	}
	if result.HasRoot {
		epoch.Recomputed.Root = chain.Hash(result.Root).String()
	}
	if posted != nil {
		epoch.Posted = &report.PostedRoot{
			Root:    posted.Root.String(),
			Block:   posted.BlockNumber,
			Totals:  report.Decimals(posted.Totals),
			Funded:  report.Decimals(ledger.Funded),
			CarryIn: report.Decimals(posted.CarryIn),
		}
	}

	return epoch, result, nil
}

func zeroAmounts() chain.Amounts {
	var out chain.Amounts
	for t := range out {
		out[t] = new(big.Int)
	}

	return out
}

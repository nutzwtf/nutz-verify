// Package epoch is the Engine: the one code path that reads the chain and rebuilds an
// Epoch's Allocations, totals, Carry and Root (ADR-0006).
//
// It has two callers and one job. cmd/nutz-verify compares what it returns against the
// chain and reports a Verdict; the private indexer writes what it returns out as a Bundle.
// Neither recomputes a field of its own, so the Root the Signer's verifier rebuilds and
// the Root the indexer posts come from one function, and byte-identity between them is a
// property of the code rather than of a test.
//
// Everything a Result holds comes from the --rpc endpoints through chain.Reader, read at
// one tip; nothing published is ever an input (ADR-0002). The rules themselves live in
// internal/twab and internal/alloc and are pinned by testdata/cases; this package adds the
// chain to them and nothing else.
package epoch

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/nutzwtf/nutz-verify/cache"
	"github.com/nutzwtf/nutz-verify/chain"
	"github.com/nutzwtf/nutz-verify/internal/alloc"
	"github.com/nutzwtf/nutz-verify/internal/twab"
	"github.com/nutzwtf/nutz-verify/merkle"
)

// DefaultSyncChunk is how many blocks each Cache read covers when Options.SyncChunk is
// zero. Smaller than the Cache's own default: the chunk is also how often progress is
// reported and where an interrupted sync resumes, and at USDG's density on the public
// endpoint 2,000 blocks is about two minutes — so a twenty-minute catch-up says something
// every two minutes instead of nothing for ten.
const DefaultSyncChunk = 2000

// Options is what a caller may vary about an Engine. Nothing here changes a Result: the
// chunk is pacing, Fresh is the Cache's lifecycle, Progress is where to say what is
// happening.
type Options struct {
	// SyncChunk is blocks per Cache read; zero is DefaultSyncChunk.
	SyncChunk int

	// Fresh discards the Cache and rebuilds it from the token's creation block.
	Fresh bool

	// Progress, when set, receives one line per event worth showing a person: a Cache
	// repair, each chunk of a sync. Never a Result.
	Progress func(format string, args ...any)
}

// Engine is one run at one tip.
//
// The tip is bound at New because everything downstream is a question about it: the
// Distributor's ledger reads are pinned to it, the Cache is brought to it once, and the
// Ledger that replays the history advances through it Epoch by Epoch (ticket 07). A run
// that wants a newer tip makes a new Engine.
type Engine struct {
	reader   *chain.Reader
	tip      chain.Tip
	cacheDir string
	dep      Deployment
	opts     Options
	book     *book

	// Set by Sync, once.
	synced *Synced

	// Set by load, once: the whole history in the rules' types, and the whole
	// ExcludedAppended stream in both the chain's and the rules' types.
	loaded      bool
	history     []twab.Transfer
	exclusions  []chain.Exclusion
	replayables []twab.Exclusion

	// ledger replays history forward; floor is the first Epoch it still accepts. A lower
	// Epoch rebuilds it from the retained history.
	ledger *twab.Ledger
	floor  uint64

	first *uint64 // deployEpoch, memoized
}

// New binds an Engine to a Reader, a tip the caller resolved with Reader.Head, the
// directory the Cache lives in, and the Deployment to verify against. It makes no calls.
//
// The Reader carries a token and a Distributor of its own, and so does the Deployment; an
// Engine given two different answers would read one contract's history against another's
// ledger and report on nothing, so New refuses the pair.
func New(reader *chain.Reader, tip chain.Tip, cacheDir string, dep Deployment, opts Options) (*Engine, error) {
	if reader == nil {
		return nil, errors.New("epoch: no Reader; every input a Recompute has comes from one (ADR-0002)")
	}
	if err := dep.Check(); err != nil {
		return nil, err
	}
	if reader.Token() != dep.Token || reader.Distributor() != dep.Distributor {
		return nil, fmt.Errorf("epoch: the Reader is for token %s and Distributor %s, and the Deployment pins %s and %s",
			reader.Token(), reader.Distributor(), dep.Token, dep.Distributor)
	}
	if cacheDir == "" {
		return nil, errors.New("epoch: no Cache directory")
	}

	if opts.SyncChunk <= 0 {
		opts.SyncChunk = DefaultSyncChunk
	}

	return &Engine{
		reader:   reader,
		tip:      tip,
		cacheDir: cacheDir,
		dep:      dep,
		opts:     opts,
		book:     newBook(reader, tip.Block.Number),
	}, nil
}

// Tip is the tip the Engine was bound to.
func (e *Engine) Tip() chain.Tip { return e.tip }

// Synced is what a Sync did to the Cache.
type Synced struct {
	Dir     string
	Records int64  // held after the sync
	Through uint64 // the newest block a held record is from; 0 when empty

	Appended int64
	Reorg    *cache.Reorg // nil when the held history still stood

	// Repaired is what Open had to do to the file, in cache.Repair's words, or "".
	Repaired string
}

// Sync brings the Cache to the tip and reports what it did. It runs once: a second call,
// and the one Compute and Walk make, returns the first's report. On an error partway the
// report still says what was appended and dropped before it, so a run that failed can
// show it.
func (e *Engine) Sync(ctx context.Context) (Synced, error) {
	if e.synced != nil {
		return *e.synced, nil
	}

	status := Synced{Dir: e.cacheDir}
	history, err := e.sync(ctx, &status)
	if err != nil {
		return status, err
	}

	e.synced = &status
	e.history = history

	return status, nil
}

// Synced is Sync's report, if Sync has run: for a caller that let Compute make the call
// and wants the Cache's status afterwards, an error included.
func (e *Engine) Synced() (Synced, bool) {
	if e.synced == nil {
		return Synced{}, false
	}

	return *e.synced, true
}

// sync opens the Cache, advances it to the tip, and returns every transfer it holds in
// block order, in the rules' own type. status is filled as it goes.
func (e *Engine) sync(ctx context.Context, status *Synced) ([]twab.Transfer, error) {
	through := e.tip.Block

	c, err := cache.Open(e.cacheDir, cache.Options{
		ChainID: e.dep.ChainID,
		Token:   e.dep.Token,
		Start:   e.dep.TokenBlock,
		Fresh:   e.opts.Fresh,
	})
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if repaired := c.Repaired(); repaired != nil {
		status.Repaired = repaired.String()
		e.progress("%s", repaired)
	}

	from := e.dep.TokenBlock
	if last, ok := c.Last(); ok {
		from = last.BlockNumber + 1
	}
	if from <= through.Number {
		e.progress("cache: syncing blocks %d..%d", from, through.Number)
	}

	synced, err := c.Sync(ctx, e.reader, through, cache.SyncOptions{
		Chunk: uint64(e.opts.SyncChunk),
		Progress: func(block uint64) {
			e.progress("cache: through block %d of %d", block, through.Number)
		},
	})
	status.Reorg = synced.Reorg
	status.Appended = synced.Appended
	if err != nil {
		// Whatever was appended before the failure reaches the file on Close; the
		// deferred one does that, and the next run resumes after it.
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
	c, err = cache.Open(e.cacheDir, cache.Options{ChainID: e.dep.ChainID, Token: e.dep.Token, Start: e.dep.TokenBlock})
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

// load is everything a Compute needs read once per Engine: the Cache synced and held, and
// the whole ExcludedAppended stream through the tip. Read live: it is a handful of logs
// from the Distributor's deploy block on.
//
// Through the tip and not each Epoch's end block, which is the same set for the rules: an
// entry applies to the Epoch its block's timestamp falls in, and every block past an end
// block is in a later Epoch.
func (e *Engine) load(ctx context.Context) error {
	if e.loaded {
		return nil
	}

	if _, err := e.Sync(ctx); err != nil {
		return err
	}

	stream, err := e.reader.Exclusions(ctx, e.dep.DistributorBlock, e.tip.Block.Number)
	if err != nil {
		return err
	}

	replayables := make([]twab.Exclusion, 0, len(stream))
	for _, x := range stream {
		replayables = append(replayables, twab.Exclusion{Timestamp: x.Timestamp, Account: twab.Address(x.Account)})
	}

	e.exclusions = stream
	e.replayables = replayables
	e.loaded = true

	return nil
}

// holders closes Epoch id in the Ledger and returns its Holders. The Ledger only moves
// forward; asked for an Epoch it has passed, the Engine replays the retained history from
// the start rather than refusing — a sort and one pass, which is what a single Recompute
// costs anyway.
func (e *Engine) holders(id uint64) ([]twab.Holder, error) {
	if e.ledger == nil || id < e.floor {
		e.ledger = twab.NewLedger(e.history, e.replayables)
	}

	holders, err := e.ledger.Advance(id)
	if err != nil {
		return nil, err
	}
	e.floor = id + 1

	return holders, nil
}

// Window is Epoch e's half-open range [3600e, 3600(e+1)) in unix seconds.
type Window struct {
	Start int64
	End   int64
}

// Allocation is one Holder's row of a Root: what it is owed, and the working that got
// there. TWAB, MultBps and Weight are carried because engineering spec §4.5 publishes
// them, and because a MISMATCH is far easier to place when the intermediate values show.
type Allocation struct {
	Account chain.Address
	Amounts chain.Amounts
	TWAB    *big.Int
	MultBps int64
	Weight  *big.Int

	// LeafIndex is the node index of this row's leaf in Tree — OpenZeppelin's treeIndex,
	// what a Bundle's tree.json carries and a proof is derived from.
	LeafIndex int
}

// Result is one Epoch rebuilt from the chain: what the Engine read, and what the rules
// made of it. It compares nothing. Every field is a fresh value the caller may keep.
type Result struct {
	EpochID  uint64
	Window   Window
	Finality chain.Finality

	// EndBlock is the last block with a timestamp inside the window, found for the Epoch
	// a Compute or Walk was asked about. Walk's earlier links carry nil: it costs a search
	// per Epoch and no link's Result needs it.
	EndBlock *chain.Block

	// What the Distributor's ledger holds for the Epoch at the tip.
	Skipped bool
	Funded  chain.Amounts

	// CarryIn is what the Allocations were computed over: the posted carryIn when a Root
	// stands, ExpectedCarryIn otherwise. ExpectedCarryIn is what the Carry chain says the
	// Epoch should have been posted with — the previous rooted Epoch's recomputed carryOut
	// plus the funding of every Skipped Epoch between — and CarryFrom says where that came
	// from, as the Assertion prints it. Computing over the posted value is deliberate: if
	// it is wrong, Assertion 4 says so, and the other three still say whether the tree was
	// right for the inputs the indexer had.
	CarryIn         chain.Amounts
	ExpectedCarryIn chain.Amounts
	CarryFrom       string

	// Posted is the standing RootPosted log for the Epoch at the tip, or nil.
	Posted *chain.RootPosted

	// Totals is Σ alloc(a,t), and CarryOut the rounding dust funded + carryIn − Totals that
	// stays in the Distributor for the next Root. TotalWeight is W, summed over every
	// eligible Holder including those the omission rule later drops from the tree.
	Totals      chain.Amounts
	CarryOut    chain.Amounts
	TotalWeight *big.Int

	// Excluded is the Epoch's Excluded set, rebuilt from the ExcludedAppended stream as of
	// the Epoch; its Hash is engineering spec §4.5's exclusion set hash.
	Excluded chain.ExcludedSet

	// Tree is nil and Root the zero hash when HasRoot is false: an Epoch with no eligible
	// Holders, or none whose allocation survives flooring, posts no Root at all. Spec §5
	// makes that a legitimate state rather than an error.
	HasRoot bool
	Root    chain.Hash
	Tree    *merkle.Tree

	// Allocations are the tree's claims, ascending by address — the order engineering
	// spec §4.5's allocations.json and the Cases both record. Holders omitted from the
	// tree are not here either.
	Allocations []Allocation
}

// Compute rebuilds one Epoch at the tip.
//
// It finds the Epoch's end block — a chain.EpochNotClosed error means the Epoch has not
// closed at the tip's finality: not yet, never a guess — brings the Cache to the tip,
// derives the expected carryIn as the Carry chain says, finds the standing Root, and runs
// the rules over the posted carryIn when a Root stands. The previous rooted Epoch is
// recomputed on the way, over its own posted carryIn, to know its carryOut.
func (e *Engine) Compute(ctx context.Context, id uint64) (Result, error) {
	endBlock, err := e.reader.EndBlock(ctx, id, e.tip)
	if err != nil {
		return Result{}, err
	}

	if err := e.load(ctx); err != nil {
		return Result{}, err
	}

	expected, from, err := e.expectedCarry(ctx, id)
	if err != nil {
		return Result{}, err
	}

	posted, err := e.standingRoot(ctx, id, endBlock.Number+1, e.tip.Block.Number)
	if err != nil {
		return Result{}, err
	}

	result, err := e.compute(ctx, id, posted, expected, from)
	if err != nil {
		return Result{}, err
	}
	result.EndBlock = &endBlock

	return result, nil
}

// Walk is the full Carry chain: every rooted Epoch from the Distributor's deploy Epoch
// through target, in order, target last whether or not it has a Root. Each link is
// computed over its own posted carryIn, and its ExpectedCarryIn is the carry the walk
// itself accumulated — the previous link's recomputed carryOut plus the funding of every
// unrooted Epoch between, which contribute no Result. That is what makes a Walk different
// from a series of Computes, and the read spec §7 says the Dispute window cannot afford: a
// header per Root, from deploy.
func (e *Engine) Walk(ctx context.Context, target uint64) ([]Result, error) {
	endBlock, err := e.reader.EndBlock(ctx, target, e.tip)
	if err != nil {
		return nil, err
	}

	if err := e.load(ctx); err != nil {
		return nil, err
	}

	first, err := e.deployEpoch(ctx)
	if err != nil {
		return nil, err
	}
	if target < first {
		return nil, errPredates(target, first)
	}

	standing, err := e.standingRoots(ctx, first, target)
	if err != nil {
		return nil, err
	}

	carry := zeroAmounts()
	from := "nothing before deploy"
	skipped := 0
	var out []Result

	for id := first; id < target; id++ {
		posted, rooted := standing[id]
		if !rooted {
			carry, err = e.book.addFunding(ctx, carry, id)
			if err != nil {
				return nil, err
			}
			skipped++

			continue
		}

		link, err := e.compute(ctx, id, &posted, carry, carrySource(from, skipped))
		if err != nil {
			return nil, err
		}
		out = append(out, link)

		carry, from, skipped = link.CarryOut, fmt.Sprintf("carryOut of epoch %d", id), 0
	}

	posted, err := e.standingRoot(ctx, target, endBlock.Number+1, e.tip.Block.Number)
	if err != nil {
		return nil, err
	}

	last, err := e.compute(ctx, target, posted, carry, carrySource(from, skipped))
	if err != nil {
		return nil, err
	}
	last.EndBlock = &endBlock

	return append(out, last), nil
}

// expectedCarry is what target should have been posted with: the previous rooted Epoch's
// recomputed carryOut carried forward over the Epochs between, or, without a previous
// Root, the funding of everything since deploy.
func (e *Engine) expectedCarry(ctx context.Context, target uint64) (chain.Amounts, string, error) {
	previous, found, err := e.previousRoot(ctx, target)
	if err != nil {
		return chain.Amounts{}, "", err
	}

	if !found {
		first, err := e.deployEpoch(ctx)
		if err != nil {
			return chain.Amounts{}, "", err
		}
		if target < first {
			return chain.Amounts{}, "", errPredates(target, first)
		}

		return e.carryOver(ctx, zeroAmounts(), first, target, "nothing before deploy")
	}

	// The previous Epoch over its own posted carryIn: one link is derived here, and Walk
	// is where the whole chain is.
	result, err := e.compute(ctx, previous.ID, &previous, previous.CarryIn, "its own posted carryIn")
	if err != nil {
		return chain.Amounts{}, "", err
	}

	return e.carryOver(ctx, result.CarryOut, previous.ID+1, target,
		fmt.Sprintf("carryOut of epoch %d", previous.ID))
}

// carryOver adds the funding of every Epoch in [from, to) to carry: each was, or will be,
// passed over by the target's Root, and the contract rolls its funding into Carry.
func (e *Engine) carryOver(ctx context.Context, carry chain.Amounts, from, to uint64, source string) (chain.Amounts, string, error) {
	skipped := 0
	for id := from; id < to; id++ {
		var err error
		carry, err = e.book.addFunding(ctx, carry, id)
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

// compute runs the rules over one Epoch. posted is its standing Root, nil when none;
// expected is what the Carry chain says it should have been posted with, from where.
func (e *Engine) compute(ctx context.Context, id uint64, posted *chain.RootPosted, expected chain.Amounts, from string) (Result, error) {
	ledger, err := e.book.ledger(ctx, id)
	if err != nil {
		return Result{}, err
	}

	carryIn := expected
	if posted != nil {
		carryIn = posted.CarryIn
	}

	holders, err := e.holders(id)
	if err != nil {
		return Result{}, err
	}

	computed, err := alloc.Allocate(alloc.Params{
		EpochID:   id,
		DevWallet: twab.Address(e.dep.DevWallet),
		Funded:    alloc.Amounts(ledger.Funded),
		CarryIn:   alloc.Amounts(carryIn),
	}, holders)
	if err != nil {
		return Result{}, err
	}

	window := twab.WindowOf(id)
	result := Result{
		EpochID:         id,
		Window:          Window{Start: window.Start, End: window.End},
		Finality:        e.tip.Finality,
		Skipped:         ledger.Skipped,
		Funded:          ledger.Funded,
		CarryIn:         carryIn,
		ExpectedCarryIn: expected,
		CarryFrom:       from,
		Posted:          posted,
		Totals:          chain.Amounts(computed.Totals),
		CarryOut:        chain.Amounts(computed.CarryOut),
		TotalWeight:     computed.TotalWeight,
		Excluded:        chain.ExcludedAsOf(id, e.exclusions),
		HasRoot:         computed.HasRoot,
		Root:            chain.Hash(computed.Root),
		Tree:            computed.Tree,
		Allocations:     make([]Allocation, 0, len(computed.Allocations)),
	}

	for i, a := range computed.Allocations {
		row := Allocation{
			Account: chain.Address(a.Account),
			Amounts: chain.Amounts(a.Amounts),
			TWAB:    a.TWAB,
			MultBps: a.MultBps,
			Weight:  a.Weight,
		}
		if computed.Tree != nil {
			row.LeafIndex = computed.Tree.TreeIndex(i)
		}
		result.Allocations = append(result.Allocations, row)
	}

	return result, nil
}

func errPredates(target, first uint64) error {
	return fmt.Errorf("epoch %d predates the Distributor, deployed in epoch %d", target, first)
}

func (e *Engine) progress(format string, args ...any) {
	if e.opts.Progress != nil {
		e.opts.Progress(format, args...)
	}
}

func zeroAmounts() chain.Amounts {
	var out chain.Amounts
	for t := range out {
		out[t] = new(big.Int)
	}

	return out
}

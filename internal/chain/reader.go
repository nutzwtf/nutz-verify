package chain

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// MaxLogRange is the widest block range one eth_getLogs may cover.
//
// 2,000 is the public-RPC cap of engineering spec §4.1, not a tuning parameter: a wider
// range is rejected by the endpoints a hostile stranger is most likely to be using, and the
// Verifier existing for that stranger is the whole point. An archive endpoint that would
// allow more gains only round trips.
const MaxLogRange = 2000

// defaultConcurrency is how many block headers are fetched at once.
//
// A log carries no timestamp, so every block that produced one needs its header, and that is
// the round-trip-bound part of a sync. Small enough not to look like an attack to a
// rate-limited public endpoint; large enough that the latency does not dominate.
const defaultConcurrency = 8

// defaultTimeout bounds a single request. A hung endpoint must become INDETERMINATE rather
// than a run that never ends, because the Dispute window does end.
const defaultTimeout = 30 * time.Second

// Finality is which chain tip history is read at (spec §4). Default safe.
type Finality string

// The three levels, spelled as the block tags JSON-RPC takes.
const (
	Latest    Finality = "latest"
	Safe      Finality = "safe"
	Finalized Finality = "finalized"
)

// ParseFinality reads the --finality flag.
func ParseFinality(s string) (Finality, error) {
	switch f := Finality(s); f {
	case Latest, Safe, Finalized:
		return f, nil
	default:
		return "", fmt.Errorf("chain: finality %q is not one of %s, %s or %s", s, Latest, Safe, Finalized)
	}
}

// Disagreement is two endpoints answering the same pinned question differently.
//
// It is never resolved by picking one. ADR-0002 turns "trust your provider" into "trust that
// two providers are not colluding", and that only holds if a contradiction stops the run:
// silently preferring the first endpoint would leave the cross-check looking like it passed.
// The caller reports INDETERMINATE.
type Disagreement struct {
	// Read names the question both endpoints were asked, at the block it was pinned to.
	Read string
	A, B string // endpoint labels, by position and host
}

func (d *Disagreement) Error() string {
	return fmt.Sprintf("chain: %s and %s disagree about %s; nothing here can say which is right",
		d.A, d.B, d.Read)
}

// EpochNotClosed is an Epoch whose closing instant the chain has not reached at the
// requested finality — so its end block does not exist yet, and neither does its TWAB.
//
// This is INDETERMINATE and not MISMATCH: nothing has been checked and found wrong. Lowering
// --finality is a choice the user makes, never one the Verifier makes for them.
type EpochNotClosed struct {
	EpochID uint64
	Tip     Tip
}

func (e *EpochNotClosed) Error() string {
	return fmt.Sprintf("chain: epoch %d closes at %d, and the %s head is block %d at %d",
		e.EpochID, twab.WindowOf(e.EpochID).End, e.Tip.Finality, e.Tip.Block.Number, e.Tip.Block.Timestamp)
}

// Tip is a chain tip reconciled across every endpoint, together with the finality level it
// was read at. The two travel together because an end block is only as settled as the tip it
// was searched under, and a report that says "block 41,000" without saying "finalized" has
// left out the half that makes it mean anything.
type Tip struct {
	Block    Block
	Finality Finality
}

// Config is what a Reader needs to talk to a chain.
type Config struct {
	// Endpoints is one or more RPC URLs. Every read is made against all of them and any
	// disagreement is an error (ADR-0002); a single endpoint is trusted because there is
	// nothing to compare it to, which the run header says out loud.
	Endpoints []string

	// Token is the NUTZ contract, whose Transfer logs are the balance history. Distributor
	// is the contract carrying ledger(), excluded(), RootPosted and ExcludedAppended.
	Token       Address
	Distributor Address

	// HTTPClient and Concurrency are optional.
	HTTPClient  *http.Client
	Concurrency int
}

// Reader reads chain history through one or more endpoints.
//
// Every history read is pinned to a block number the caller chose, never to a tip: two
// endpoints asked for "latest" answer honestly and differently, and treating that as a
// contradiction would make the cross-check fire constantly and mean nothing. Head is where
// the tips are reconciled, once, and everything downstream is a question about a fixed block.
type Reader struct {
	endpoints   []*endpoint
	token       Address
	distributor Address
	concurrency int
}

// New builds a Reader. It makes no requests: an unreachable endpoint is discovered by the
// first read, which is where it belongs, but a URL we could never have posted to is a
// configuration mistake and is worth refusing before a run starts.
func New(cfg Config) (*Reader, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, errors.New("chain: no RPC endpoint; every input a Recompute has comes from one (ADR-0002)")
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	r := &Reader{token: cfg.Token, distributor: cfg.Distributor, concurrency: concurrency}
	for i, raw := range cfg.Endpoints {
		e, err := newEndpoint(raw, i+1, client)
		if err != nil {
			return nil, err
		}

		r.endpoints = append(r.endpoints, e)
	}

	// A zero address is not a contract that returns nothing — it is a flag that never got
	// filled in, and it would report an Epoch with no transfers and no Root.
	if cfg.Token == (Address{}) {
		return nil, errors.New("chain: no NUTZ token address")
	}
	if cfg.Distributor == (Address{}) {
		return nil, errors.New("chain: no Distributor address")
	}

	return r, nil
}

// Token and Distributor are the addresses this Reader was built for, echoed in every run's
// header because ADR-0002 wants the unverifiable inputs shown rather than buried.
func (r *Reader) Token() Address       { return r.token }
func (r *Reader) Distributor() Address { return r.distributor }

// Endpoints names the endpoints by position and host. The URLs themselves are not returned:
// a provider URL's path is routinely an API key, and this string ends up in run headers,
// JSON reports and pasted issues.
func (r *Reader) Endpoints() []string {
	out := make([]string, 0, len(r.endpoints))
	for _, e := range r.endpoints {
		out = append(out, e.label)
	}

	return out
}

// ChainID is eth_chainId, cross-checked. Two endpoints on different chains is the failure
// this catches — a stale URL pointing at a testnet answers every other question plausibly.
func (r *Reader) ChainID(ctx context.Context) (uint64, error) {
	return crossCheck(ctx, r, "eth_chainId",
		func(ctx context.Context, e *endpoint) (uint64, error) {
			raw, err := e.call(ctx, "eth_chainId")
			if err != nil {
				return 0, err
			}

			var encoded string
			if err := json.Unmarshal(raw, &encoded); err != nil {
				return 0, fmt.Errorf("eth_chainId: %w", err)
			}

			id, err := parseQuantity(encoded)
			if err != nil {
				return 0, fmt.Errorf("eth_chainId: %w", err)
			}

			return id, nil
		},
		func(id uint64) []byte { return appendUint64(nil, id) })
}

// Head is the block history is read at: the lowest tip any endpoint has reached at this
// finality level, re-read by number so the endpoints are actually compared.
//
// The minimum rather than a disagreement, because tips legitimately differ. Two endpoints are
// not contradicting each other by being a block apart; they are contradicting each other by
// reporting different blocks at the same height, which is what the second read checks. Taking
// the lowest also means every subsequent read asks for a block all of them have.
func (r *Reader) Head(ctx context.Context, level Finality) (Tip, error) {
	tips := make([]Block, len(r.endpoints))
	if err := r.eachEndpoint(ctx, func(ctx context.Context, i int, e *endpoint) error {
		tip, err := e.blockByTag(ctx, string(level))
		if err != nil {
			// safe and finalized are not universal; a node without them answers null, which
			// call reports as having no answer. Say which tag was asked for.
			return fmt.Errorf("%s head: %w", level, err)
		}

		tips[i] = tip

		return nil
	}); err != nil {
		return Tip{}, err
	}

	lowest := slices.MinFunc(tips, func(a, b Block) int { return cmp.Compare(a.Number, b.Number) })
	if len(r.endpoints) == 1 {
		return Tip{Block: lowest, Finality: level}, nil
	}

	agreed, err := r.BlockByNumber(ctx, lowest.Number)
	if err != nil {
		return Tip{}, err
	}

	return Tip{Block: agreed, Finality: level}, nil
}

// BlockByNumber reads one header, cross-checked. Two endpoints disagreeing here is a fork or
// a lie, and either way nothing downstream is worth computing.
func (r *Reader) BlockByNumber(ctx context.Context, number uint64) (Block, error) {
	return crossCheck(ctx, r, fmt.Sprintf("block %d", number),
		func(ctx context.Context, e *endpoint) (Block, error) { return e.blockByTag(ctx, quantity(number)) },
		Block.canonical)
}

// Timestamps resolves block numbers to block timestamps.
//
// A log carries no time of its own and the rules weigh Holders by seconds, so this is the
// join between the two. One header per distinct block, fetched concurrently and cross-checked
// individually; duplicates in the argument cost nothing.
func (r *Reader) Timestamps(ctx context.Context, blocks []uint64) (map[uint64]int64, error) {
	wanted := slices.Clone(blocks)
	slices.Sort(wanted)
	wanted = slices.Compact(wanted)

	out := make(map[uint64]int64, len(wanted))
	var mu sync.Mutex

	if err := r.inParallel(ctx, len(wanted), func(ctx context.Context, i int) error {
		block, err := r.BlockByNumber(ctx, wanted[i])
		if err != nil {
			return err
		}

		mu.Lock()
		defer mu.Unlock()
		out[wanted[i]] = block.Timestamp

		return nil
	}); err != nil {
		return nil, err
	}

	return out, nil
}

// Transfers is every NUTZ Transfer log in the inclusive block range, timestamped, in
// (block, index) order.
func (r *Reader) Transfers(ctx context.Context, from, to uint64) ([]Transfer, error) {
	logs, err := r.logs(ctx, r.token, topicTransfer, from, to)
	if err != nil {
		return nil, err
	}

	out := make([]Transfer, 0, len(logs))
	for _, l := range logs {
		transfer, err := decodeTransfer(l)
		if err != nil {
			return nil, err
		}

		out = append(out, transfer)
	}

	stamps, err := stampsFor(ctx, r, out, func(t Transfer) uint64 { return t.BlockNumber })
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Timestamp = stamps[out[i].BlockNumber]
	}

	return out, nil
}

// Exclusions is every ExcludedAppended log in the inclusive block range, timestamped.
//
// The stream, not a set: ExcludedAsOf turns it into the set of a particular Epoch, and which
// Epoch that is cannot be known here. The Distributor's constructor emits one of these per
// base entry, so a range starting at its deploy block yields the whole history.
func (r *Reader) Exclusions(ctx context.Context, from, to uint64) ([]Exclusion, error) {
	logs, err := r.logs(ctx, r.distributor, topicExcludedAppended, from, to)
	if err != nil {
		return nil, err
	}

	out := make([]Exclusion, 0, len(logs))
	for _, l := range logs {
		exclusion, err := decodeExcludedAppended(l)
		if err != nil {
			return nil, err
		}

		out = append(out, exclusion)
	}

	stamps, err := stampsFor(ctx, r, out, func(e Exclusion) uint64 { return e.BlockNumber })
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Timestamp = stamps[out[i].BlockNumber]
	}

	return out, nil
}

// Roots is every RootPosted log in the inclusive block range, timestamped.
//
// Both kinds are returned: they share a topic0 and are told apart by the first indexed
// argument, so filtering here would cost a second query for nothing.
func (r *Reader) Roots(ctx context.Context, from, to uint64) ([]RootPosted, error) {
	logs, err := r.logs(ctx, r.distributor, topicRootPosted, from, to)
	if err != nil {
		return nil, err
	}

	out := make([]RootPosted, 0, len(logs))
	for _, l := range logs {
		posted, err := decodeRootPosted(l)
		if err != nil {
			return nil, err
		}

		out = append(out, posted)
	}

	stamps, err := stampsFor(ctx, r, out, func(p RootPosted) uint64 { return p.BlockNumber })
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Timestamp = stamps[out[i].BlockNumber]
	}

	return out, nil
}

// Ledger is the Distributor's book for one period, read at a pinned block.
//
// at is not optional and there is no "latest" form. The book of a closed Epoch does still
// change — claimed[] moves as Allocations are collected — so a Recompute that read it at the
// tip would be comparing against a different answer each time it ran.
func (r *Reader) Ledger(ctx context.Context, kind Kind, id, at uint64) (Ledger, error) {
	data, err := r.callAt(ctx, fmt.Sprintf("ledger(%s, %d)", kind, id), encodeLedgerCall(kind, id), at)
	if err != nil {
		return Ledger{}, err
	}

	return decodeLedger(data)
}

// Excluded is the Distributor's excluded() list as of a pinned block.
//
// This is a cross-check, never an input. The Excluded set of an Epoch comes from the
// ExcludedAppended stream through ExcludedAsOf (ADR-0002): excluded() returns today's list,
// which is the right answer only for the current Epoch and silently the wrong one for every
// other. What it is good for is confirming that the log stream reconstructs the list the
// contract actually holds.
func (r *Reader) Excluded(ctx context.Context, at uint64) ([]Address, error) {
	data, err := r.callAt(ctx, "excluded()", selectorExcluded[:], at)
	if err != nil {
		return nil, err
	}

	return decodeAddressArray(data)
}

// EndBlock is the last block of Epoch epochID: the last one with timestamp < 3600(e+1).
//
// Spec §5 fixes the boundary at the Epoch's closing instant rather than at this block's
// timestamp — the TWAB's final sub-interval runs to the boundary — so this answers "which
// logs are in the Epoch", not "when did the Epoch end". No hermetic Case can pin that
// distinction, which is why it lives here (testdata/cases/README.md).
//
// tip bounds the search and is what makes the answer depend on the finality level: an Epoch
// whose closing instant the tip has not reached yet has no end block at that level, and that
// is EpochNotClosed rather than a guess at the tip.
func (r *Reader) EndBlock(ctx context.Context, epochID uint64, tip Tip) (Block, error) {
	end := twab.WindowOf(epochID).End
	head := tip.Block

	if head.Timestamp < end {
		return Block{}, &EpochNotClosed{EpochID: epochID, Tip: tip}
	}

	genesis, err := r.BlockByNumber(ctx, 0)
	if err != nil {
		return Block{}, err
	}
	if genesis.Timestamp >= end {
		return Block{}, fmt.Errorf("chain: epoch %d closes at %d, before block 0 at %d: "+
			"this epoch predates the chain", epochID, end, genesis.Timestamp)
	}

	// Block timestamps are non-decreasing, so "the last block before the boundary" is a
	// binary search. Invariant: lo is before the boundary and hi is at or after it.
	//
	// The probed headers are kept: lo always names a block the search has already read, so
	// re-fetching it at the end would be a round trip for something already in hand.
	probed := map[uint64]Block{0: genesis}

	lo, hi := uint64(0), head.Number
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2

		block, err := r.BlockByNumber(ctx, mid)
		if err != nil {
			return Block{}, err
		}

		probed[mid] = block

		if block.Timestamp < end {
			lo = mid
		} else {
			hi = mid
		}
	}

	return probed[lo], nil
}

// ---------------------------------------------------------------- the plumbing

// logs pages eth_getLogs across the range and cross-checks each page.
//
// Pages are capped at MaxLogRange because that is the public-RPC cap, and the range is
// inclusive at both ends because that is what eth_getLogs means by fromBlock and toBlock —
// an off-by-one here drops a block's transfers and changes every Holder's TWAB.
func (r *Reader) logs(ctx context.Context, address Address, topic0 Hash, from, to uint64) ([]eventLog, error) {
	if from > to {
		return nil, fmt.Errorf("chain: block range %d..%d runs backwards", from, to)
	}

	var out []eventLog
	for start := from; ; start += MaxLogRange {
		// Measured as a distance from start rather than as start + MaxLogRange, which is the
		// addition that wraps once a caller asks about the top of the uint64 range.
		end := to
		if to-start >= MaxLogRange {
			end = start + MaxLogRange - 1
		}

		page, err := crossCheck(ctx, r, fmt.Sprintf("eth_getLogs %d..%d", start, end),
			func(ctx context.Context, e *endpoint) ([]eventLog, error) {
				return e.getLogs(ctx, address, topic0, start, end)
			},
			canonicalLogs)
		if err != nil {
			return nil, err
		}

		out = append(out, page...)

		// Tested here rather than in the loop condition for the same reason: the increment
		// only happens when another whole page is left, so start never runs past to.
		if end == to {
			break
		}
	}

	return out, nil
}

// callAt is a cross-checked eth_call at a pinned block. The returned bytes are compared
// undecoded, so two endpoints that disagree about a return we would have rejected anyway
// still disagree.
func (r *Reader) callAt(ctx context.Context, what string, data []byte, at uint64) ([]byte, error) {
	return crossCheck(ctx, r, fmt.Sprintf("%s at block %d", what, at),
		func(ctx context.Context, e *endpoint) ([]byte, error) {
			return e.ethCall(ctx, r.distributor, data, at)
		},
		func(b []byte) []byte { return b })
}

// stampsFor resolves the block timestamps of a decoded log slice.
func stampsFor[T any](ctx context.Context, r *Reader, events []T, blockOf func(T) uint64) (map[uint64]int64, error) {
	blocks := make([]uint64, 0, len(events))
	for _, e := range events {
		blocks = append(blocks, blockOf(e))
	}

	return r.Timestamps(ctx, blocks)
}

// crossCheck asks every endpoint the same pinned question and returns the answer only if all
// of them gave it.
//
// An endpoint that errors fails the whole read rather than being dropped in favour of the
// ones that answered. Falling back to a quorum of the reachable would mean the cross-check
// quietly weakens exactly when an endpoint is unavailable, which is the moment ADR-0002 is
// worried about; a user who wants one endpoint's answer passes one endpoint.
func crossCheck[T any](
	ctx context.Context,
	r *Reader,
	what string,
	read func(context.Context, *endpoint) (T, error),
	canonical func(T) []byte,
) (T, error) {
	var zero T

	if len(r.endpoints) == 1 {
		answer, err := read(ctx, r.endpoints[0])
		if err != nil {
			return zero, fmt.Errorf("chain: %s: %s: %w", r.endpoints[0].label, what, err)
		}

		return answer, nil
	}

	answers := make([]T, len(r.endpoints))
	if err := r.eachEndpoint(ctx, func(ctx context.Context, i int, e *endpoint) error {
		answer, err := read(ctx, e)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}

		answers[i] = answer

		return nil
	}); err != nil {
		return zero, err
	}

	first := canonical(answers[0])
	for i := 1; i < len(answers); i++ {
		if !bytes.Equal(first, canonical(answers[i])) {
			return zero, &Disagreement{Read: what, A: r.endpoints[0].label, B: r.endpoints[i].label}
		}
	}

	return answers[0], nil
}

// eachEndpoint runs one read against every endpoint at once, and reports the failure that
// came first so "which one is down" is never a guess.
//
// First to fail, not lowest-numbered: the first failure cancels the others, so every endpoint
// after it reports a cancellation we caused ourselves. Naming the lowest-numbered of those
// would blame a healthy endpoint for its neighbour being down, which is the opposite of what
// the caller needs to act on.
func (r *Reader) eachEndpoint(ctx context.Context, fn func(context.Context, int, *endpoint) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu      sync.Mutex
		failure error
	)

	var wg sync.WaitGroup
	for i, e := range r.endpoints {
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := fn(ctx, i, e)
			if err == nil {
				return
			}

			mu.Lock()
			defer mu.Unlock()

			if failure == nil {
				failure = fmt.Errorf("chain: %s: %w", e.label, err)
				cancel() // the read has already failed; do not make the others finish paying
			}
		}()
	}
	wg.Wait()

	return failure
}

// inParallel runs n indexed jobs over a bounded pool, stopping at the first failure.
func (r *Reader) inParallel(ctx context.Context, n int, fn func(context.Context, int) error) error {
	if n == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)

	var (
		mu      sync.Mutex
		failure error
	)

	var wg sync.WaitGroup
	for range min(n, r.concurrency) {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := range jobs {
				err := fn(ctx, i)
				if err == nil {
					continue
				}

				mu.Lock()
				if failure == nil {
					failure = err
					cancel() // as above: the first failure is the one worth reporting
				}
				mu.Unlock()

				return
			}
		}()
	}

	for i := range n {
		select {
		case jobs <- i:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()

	if failure != nil {
		return failure
	}

	return ctx.Err()
}

// ------------------------------------------------------------ per-endpoint reads

func (e *endpoint) blockByTag(ctx context.Context, tag string) (Block, error) {
	// false: transaction hashes rather than whole transactions. The bodies are megabytes we
	// would throw away, and a Recompute needs only the header.
	raw, err := e.call(ctx, "eth_getBlockByNumber", tag, false)
	if err != nil {
		return Block{}, err
	}

	var wire wireBlock
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Block{}, fmt.Errorf("eth_getBlockByNumber %s: %w", tag, err)
	}

	block, err := wire.decode()
	if err != nil {
		return Block{}, fmt.Errorf("eth_getBlockByNumber %s: %w", tag, err)
	}

	return block, nil
}

func (e *endpoint) getLogs(ctx context.Context, address Address, topic0 Hash, from, to uint64) ([]eventLog, error) {
	filter := map[string]any{
		"fromBlock": quantity(from),
		"toBlock":   quantity(to),
		"address":   address.String(),
		"topics":    []any{topic0.String()},
	}

	raw, err := e.call(ctx, "eth_getLogs", filter)
	if err != nil {
		return nil, err
	}

	var wire []wireLog
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("eth_getLogs %d..%d: %w", from, to, err)
	}

	out := make([]eventLog, 0, len(wire))
	for _, w := range wire {
		l, err := w.decode()
		if err != nil {
			return nil, fmt.Errorf("eth_getLogs %d..%d: %w", from, to, err)
		}

		// The filter already said which contract and which topic0. A log that is neither is
		// an endpoint that ignored the filter, and taking its word for the rest of the page
		// is not something to do quietly.
		if l.Address != address {
			return nil, fmt.Errorf("chain: eth_getLogs %d..%d: a log from %s came back for %s",
				from, to, l.Address, address)
		}
		if len(l.Topics) == 0 || l.Topics[0] != topic0 {
			return nil, fmt.Errorf("chain: eth_getLogs %d..%d: a log at block %d index %d does not carry "+
				"the topic it was filtered on", from, to, l.BlockNumber, l.LogIndex)
		}
		if l.BlockNumber < from || l.BlockNumber > to {
			return nil, fmt.Errorf("chain: eth_getLogs %d..%d: a log came back from block %d",
				from, to, l.BlockNumber)
		}

		out = append(out, l)
	}

	sortLogs(out)

	return out, nil
}

func (e *endpoint) ethCall(ctx context.Context, to Address, data []byte, at uint64) ([]byte, error) {
	raw, err := e.call(ctx, "eth_call", map[string]any{"to": to.String(), "data": hexBytes(data)}, quantity(at))
	if err != nil {
		return nil, err
	}

	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("eth_call at block %d: %w", at, err)
	}

	out, err := parseHexBytes(encoded)
	if err != nil {
		return nil, fmt.Errorf("eth_call at block %d: return data %w", at, err)
	}

	return out, nil
}

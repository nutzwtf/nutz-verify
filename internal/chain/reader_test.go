package chain

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"strings"
	"testing"
)

var (
	nutz        = repeatAddr(0x11)
	distributor = repeatAddr(0x22)
	alice       = repeatAddr(0xa1)
	bob         = repeatAddr(0xb2)
)

func readerOver(t *testing.T, nodes ...*fakeNode) *Reader {
	t.Helper()

	urls := make([]string, 0, len(nodes))
	for _, n := range nodes {
		urls = append(urls, n.serve(t))
	}

	r, err := New(Config{Endpoints: urls, Token: nutz, Distributor: distributor})
	if err != nil {
		t.Fatalf("New = %v", err)
	}

	return r
}

func TestNew_Refuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
	}{
		// A Recompute with no endpoint has no inputs at all (ADR-0002), and a zero contract
		// address is a flag that never got filled in: it would report an Epoch with no
		// transfers and no Root rather than failing.
		{"no endpoint", Config{Token: nutz, Distributor: distributor}},
		{"no token", Config{Endpoints: []string{"http://localhost:8545"}, Distributor: distributor}},
		{"no distributor", Config{Endpoints: []string{"http://localhost:8545"}, Token: nutz}},
		{"a websocket URL", Config{Endpoints: []string{"ws://localhost:8545"}, Token: nutz, Distributor: distributor}},
		{"a path with no host", Config{Endpoints: []string{"/var/run/geth.ipc"}, Token: nutz, Distributor: distributor}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := New(tt.cfg); err == nil {
				t.Error("New = nil error, want refusal")
			}
		})
	}
}

func TestEndpoints_NameByHostNotURL(t *testing.T) {
	t.Parallel()

	// A provider URL's path is routinely an API key, and this string goes into run headers,
	// JSON reports and pasted issues.
	r, err := New(Config{
		Endpoints:   []string{"https://rpc.example.com/v1/deadbeefsecret"},
		Token:       nutz,
		Distributor: distributor,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, label := range r.Endpoints() {
		if strings.Contains(label, "deadbeefsecret") {
			t.Errorf("endpoint label %q carries the URL path", label)
		}
		if !strings.Contains(label, "rpc.example.com") {
			t.Errorf("endpoint label %q does not name the host", label)
		}
	}
}

func TestChainID(t *testing.T) {
	t.Parallel()

	node := newFakeNode(10, 12)
	got, err := readerOver(t, node).ChainID(t.Context())
	if err != nil {
		t.Fatalf("ChainID = %v", err)
	}
	if got != 4663 {
		t.Errorf("ChainID = %d, want 4663", got)
	}
}

func TestChainID_DisagreementIsNotResolved(t *testing.T) {
	t.Parallel()

	// A stale URL pointing at a testnet answers every other question plausibly.
	a, b := newFakeNode(10, 12), newFakeNode(10, 12)
	b.chainID = 46630

	_, err := readerOver(t, a, b).ChainID(t.Context())

	var disagreement *Disagreement
	if !errors.As(err, &disagreement) {
		t.Fatalf("ChainID = %v, want a Disagreement", err)
	}
}

func TestHead_TakesTheLowestTipAndComparesIt(t *testing.T) {
	t.Parallel()

	// Two endpoints a block apart are not contradicting each other. Reading history at the
	// lower of the two asks only for blocks both of them have.
	a, b := newFakeNode(100, 12), newFakeNode(100, 12)
	a.tips[Safe], b.tips[Safe] = 90, 88

	tip, err := readerOver(t, a, b).Head(t.Context(), Safe)
	if err != nil {
		t.Fatalf("Head = %v", err)
	}
	if tip.Block.Number != 88 {
		t.Errorf("head = %d, want the lower tip 88", tip.Block.Number)
	}
	if tip.Finality != Safe {
		t.Errorf("finality = %s, want %s", tip.Finality, Safe)
	}
}

func TestHead_DisagreementAtTheSameHeight(t *testing.T) {
	t.Parallel()

	// Different blocks at the same height is a fork or a lie, and is the contradiction the
	// cross-check exists for.
	a, b := newFakeNode(100, 12), newFakeNode(100, 12)
	b.blocks[50].Hash = blockHash(50, 0xff)
	a.tips[Safe], b.tips[Safe] = 50, 50

	_, err := readerOver(t, a, b).Head(t.Context(), Safe)

	var disagreement *Disagreement
	if !errors.As(err, &disagreement) {
		t.Fatalf("Head = %v, want a Disagreement", err)
	}
}

func TestHead_AnUnsupportedTagIsNotBlockZero(t *testing.T) {
	t.Parallel()

	// A node without the tag answers null. Unmarshalling that into a header yields block
	// zero, which is a confident wrong answer where the caller is owed INDETERMINATE.
	node := newFakeNode(100, 12)
	delete(node.tips, Finalized)

	_, err := readerOver(t, node).Head(t.Context(), Finalized)
	if err == nil {
		t.Fatal("Head = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), string(Finalized)) {
		t.Errorf("Head error does not name the tag: %v", err)
	}
}

func TestReads_FailWhenAnyEndpointFails(t *testing.T) {
	t.Parallel()

	// Not a quorum of the reachable: the cross-check would then weaken exactly when an
	// endpoint is unavailable, which is the moment ADR-0002 is worried about.
	a, b := newFakeNode(100, 12), newFakeNode(100, 12)
	b.failWith["eth_getBlockByNumber"] = "rate limited"

	_, err := readerOver(t, a, b).BlockByNumber(t.Context(), 10)
	if err == nil {
		t.Fatal("BlockByNumber = nil error, want the failure to surface")
	}
	if !strings.Contains(err.Error(), "endpoint 2") {
		t.Errorf("error does not name which endpoint failed: %v", err)
	}
}

func TestParseFinality(t *testing.T) {
	t.Parallel()

	for _, want := range []Finality{Latest, Safe, Finalized} {
		got, err := ParseFinality(string(want))
		if err != nil || got != want {
			t.Errorf("ParseFinality(%q) = %q, %v", want, got, err)
		}
	}

	if _, err := ParseFinality("pending"); err == nil {
		t.Error("ParseFinality(pending) = nil error, want refusal")
	}
}

func TestLogs_PageInTwoThousandBlockRanges(t *testing.T) {
	t.Parallel()

	// The public-RPC cap of engineering spec §4.1. The fake node refuses a wider range the
	// way those endpoints do, so a regression here fails rather than silently working
	// against an archive node and breaking for the stranger the Verifier is for.
	node := newFakeNode(5001, 1)
	r := readerOver(t, node)

	if _, err := r.Transfers(t.Context(), 0, 4999); err != nil {
		t.Fatalf("Transfers = %v", err)
	}

	want := []string{"0..1999", "2000..3999", "4000..4999"}
	if got := node.seenRanges(); !slices.Equal(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestLogs_PageBoundariesAreInclusive(t *testing.T) {
	t.Parallel()

	// An off-by-one at a page boundary drops a block's transfers and changes every Holder's
	// TWAB, silently. These four blocks straddle both ends of the first page.
	node := newFakeNode(2002, 1)
	for _, block := range []uint64{0, 1999, 2000, 2001} {
		node.addTransfer(block, 0, nutz, alice, bob, 1)
	}

	got, err := readerOver(t, node).Transfers(t.Context(), 0, 2001)
	if err != nil {
		t.Fatalf("Transfers = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d transfers, want 4", len(got))
	}
	for i, block := range []uint64{0, 1999, 2000, 2001} {
		if got[i].BlockNumber != block {
			t.Errorf("transfer %d is from block %d, want %d", i, got[i].BlockNumber, block)
		}
	}
}

func TestLogs_ExactRangeIsOnePage(t *testing.T) {
	t.Parallel()

	node := newFakeNode(2001, 1)
	if _, err := readerOver(t, node).Transfers(t.Context(), 1, MaxLogRange); err != nil {
		t.Fatalf("Transfers = %v", err)
	}

	if got, want := node.seenRanges(), []string{"1..2000"}; !slices.Equal(got, want) {
		t.Errorf("ranges = %v, want %v", got, want)
	}
}

func TestTransfers_AreTimestampedFromTheirBlock(t *testing.T) {
	t.Parallel()

	// A log carries no time of its own, and the rules weigh Holders by seconds.
	node := newFakeNode(20, 15)
	node.addTransfer(5, 0, nutz, alice, bob, 7)
	node.addTransfer(9, 2, nutz, bob, alice, 3)

	got, err := readerOver(t, node).Transfers(t.Context(), 0, 19)
	if err != nil {
		t.Fatalf("Transfers = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d transfers, want 2", len(got))
	}

	if got[0].Timestamp != 75 || got[1].Timestamp != 135 {
		t.Errorf("timestamps = %d, %d, want 75, 135", got[0].Timestamp, got[1].Timestamp)
	}
	if got[0].Value.Cmp(big.NewInt(7)) != 0 || got[1].Value.Cmp(big.NewInt(3)) != 0 {
		t.Errorf("values = %s, %s, want 7, 3", got[0].Value, got[1].Value)
	}
	if got[0].From != alice || got[0].To != bob {
		t.Errorf("first transfer = %s -> %s", got[0].From, got[0].To)
	}
}

func TestTransfers_DoNotPickUpTheDistributorsLogs(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)
	node.addTransfer(5, 0, nutz, alice, bob, 7)
	node.addTransfer(6, 0, repeatAddr(0x99), alice, bob, 7) // some other ERC-20
	node.addExclusion(7, 0, distributor, alice)

	got, err := readerOver(t, node).Transfers(t.Context(), 0, 19)
	if err != nil {
		t.Fatalf("Transfers = %v", err)
	}
	if len(got) != 1 || got[0].BlockNumber != 5 {
		t.Errorf("got %d transfers, want only NUTZ's at block 5", len(got))
	}
}

func TestExclusions_CarryTheirBlockTimestamp(t *testing.T) {
	t.Parallel()

	// Which Epoch an entry lands in is decided by its block timestamp, so this is the field
	// the whole-Epoch rule turns on.
	node := newFakeNode(20, 600)
	node.addExclusion(3, 1, distributor, alice)
	node.addExclusion(11, 0, distributor, bob)

	got, err := readerOver(t, node).Exclusions(t.Context(), 0, 19)
	if err != nil {
		t.Fatalf("Exclusions = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d exclusions, want 2", len(got))
	}
	if got[0].Account != alice || got[0].Timestamp != 1800 {
		t.Errorf("first = %s at %d, want alice at 1800", got[0].Account, got[0].Timestamp)
	}
	if got[1].Account != bob || got[1].Timestamp != 6600 {
		t.Errorf("second = %s at %d, want bob at 6600", got[1].Account, got[1].Timestamp)
	}
}

func TestLogs_DisagreementIsNotResolved(t *testing.T) {
	t.Parallel()

	a, b := newFakeNode(20, 15), newFakeNode(20, 15)
	a.addTransfer(5, 0, nutz, alice, bob, 7)
	b.addTransfer(5, 0, nutz, alice, bob, 8) // one wei apart, and unresolvable

	_, err := readerOver(t, a, b).Transfers(t.Context(), 0, 19)

	var disagreement *Disagreement
	if !errors.As(err, &disagreement) {
		t.Fatalf("Transfers = %v, want a Disagreement", err)
	}
	if !strings.Contains(disagreement.Read, "eth_getLogs") {
		t.Errorf("Disagreement does not name the read: %s", disagreement.Read)
	}
}

func TestLogs_OrderIsNotADisagreement(t *testing.T) {
	t.Parallel()

	// Two providers returning the same logs in a different order have not contradicted each
	// other, and the rules replay by timestamp anyway.
	a, b := newFakeNode(20, 15), newFakeNode(20, 15)
	a.addTransfer(5, 0, nutz, alice, bob, 7)
	a.addTransfer(9, 1, nutz, bob, alice, 3)
	b.addTransfer(9, 1, nutz, bob, alice, 3)
	b.addTransfer(5, 0, nutz, alice, bob, 7)

	got, err := readerOver(t, a, b).Transfers(t.Context(), 0, 19)
	if err != nil {
		t.Fatalf("Transfers = %v", err)
	}
	if len(got) != 2 || got[0].BlockNumber != 5 {
		t.Errorf("got %v, want both transfers in block order", got)
	}
}

func TestLogs_RefuseARemovedLog(t *testing.T) {
	t.Parallel()

	// We only ask for ranges at or below a finality level, so a log a reorg took back means
	// the range was not as settled as we asked for.
	node := newFakeNode(20, 15)
	node.addTransfer(5, 0, nutz, alice, bob, 7)
	node.logs[0].Removed = true

	if _, err := readerOver(t, node).Transfers(t.Context(), 0, 19); err == nil {
		t.Error("Transfers = nil error, want a refusal")
	}
}

func TestLogs_RefuseARangeThatRunsBackwards(t *testing.T) {
	t.Parallel()

	if _, err := readerOver(t, newFakeNode(20, 15)).Transfers(t.Context(), 10, 9); err == nil {
		t.Error("Transfers = nil error, want a refusal")
	}
}

func TestLedger_ReadsTheStaticTupleAtAPinnedBlock(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)

	var data []byte
	root := repeatHash(0x5a)
	data = append(data, root[:]...)
	data = append(data, word(big.NewInt(1234))...) // rootPostedAt
	data = append(data, word(big.NewInt(0))...)    // skipped
	for i := range 3 * TokenCount {
		data = append(data, word(big.NewInt(int64(i)+1))...)
	}
	node.returns[hexBytes(encodeLedgerCall(KindEpoch, 42))+"@"+quantity(11)] = hexBytes(data)

	got, err := readerOver(t, node).Ledger(t.Context(), KindEpoch, 42, 11)
	if err != nil {
		t.Fatalf("Ledger = %v", err)
	}
	if got.Root != root || got.RootPostedAt != 1234 || got.Skipped {
		t.Errorf("Ledger = %+v", got)
	}
	if got.Funded[0].Cmp(big.NewInt(1)) != 0 || got.Claimed[TokenCount-1].Cmp(big.NewInt(15)) != 0 {
		t.Errorf("funded/claimed = %v / %v", got.Funded, got.Claimed)
	}
}

func TestLedger_EmptyReturnIsNotAnEmptyLedger(t *testing.T) {
	t.Parallel()

	// The fake node answers "0x" for an address it knows nothing about, which is what a node
	// does for a call to a contract with no code. Decoding that as an all-zero Ledger would
	// report "funded nothing, posted no Root" for a Distributor we never reached.
	if _, err := readerOver(t, newFakeNode(20, 15)).Ledger(t.Context(), KindEpoch, 42, 11); err == nil {
		t.Error("Ledger = nil error, want a refusal")
	}
}

func TestExcluded_ReadsTheDynamicReturn(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)
	node.returns[hexBytes(selectorExcluded[:])+"@"+quantity(7)] = hexBytes(encodeAddressArray([]Address{alice, bob}))

	got, err := readerOver(t, node).Excluded(t.Context(), 7)
	if err != nil {
		t.Fatalf("Excluded = %v", err)
	}
	if len(got) != 2 || got[0] != alice || got[1] != bob {
		t.Errorf("Excluded = %v", got)
	}
}

func TestEndBlock_IsTheLastBlockBeforeTheBoundary(t *testing.T) {
	t.Parallel()

	// Spec §5: the end block is the last block with timestamp < 3600(e+1). No hermetic Case
	// can pin this — a Case carries timestamps, never blocks — so it is pinned here.
	//
	// One block every 15 seconds from zero, so block n closes at 15n and Epoch 0 ends at
	// 3600: block 239 is at 3585 and block 240 is at 3600 exactly, which is the next Epoch's.
	node := newFakeNode(1000, 15)
	r := readerOver(t, node)

	tip, err := r.Head(t.Context(), Safe)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.EndBlock(t.Context(), 0, tip)
	if err != nil {
		t.Fatalf("EndBlock = %v", err)
	}
	if got.Number != 239 {
		t.Errorf("end block = %d at %d, want 239 at 3585", got.Number, got.Timestamp)
	}
}

func TestEndBlock_ABoundaryLandingBetweenBlocks(t *testing.T) {
	t.Parallel()

	// Nothing requires a block at the boundary. Every seven seconds: block 514 is at 3598
	// and block 515 at 3605, so the Epoch's last block is 514.
	node := newFakeNode(2000, 7)
	r := readerOver(t, node)

	tip, err := r.Head(t.Context(), Safe)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.EndBlock(t.Context(), 0, tip)
	if err != nil {
		t.Fatalf("EndBlock = %v", err)
	}
	if got.Number != 514 || got.Timestamp != 3598 {
		t.Errorf("end block = %d at %d, want 514 at 3598", got.Number, got.Timestamp)
	}
}

func TestEndBlock_AnEpochTheTipHasNotReached(t *testing.T) {
	t.Parallel()

	// Not MISMATCH: nothing has been checked and found wrong. Lowering --finality is the
	// user's choice, never one the Verifier makes for them.
	node := newFakeNode(100, 15) // the last block is at 1485, inside Epoch 0
	r := readerOver(t, node)

	tip, err := r.Head(t.Context(), Finalized)
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.EndBlock(t.Context(), 0, tip)

	var open *EpochNotClosed
	if !errors.As(err, &open) {
		t.Fatalf("EndBlock = %v, want EpochNotClosed", err)
	}
	if open.EpochID != 0 || open.Tip.Finality != Finalized {
		t.Errorf("EpochNotClosed = %+v", open)
	}
	if !strings.Contains(open.Error(), string(Finalized)) {
		t.Errorf("EpochNotClosed does not name the finality level: %v", open)
	}
}

func TestEndBlock_FinalityMovesTheAnswer(t *testing.T) {
	t.Parallel()

	// The same Epoch read at two levels: safe has seen past the boundary, finalized has not.
	node := newFakeNode(1000, 15)
	node.tips[Safe], node.tips[Finalized] = 300, 200

	r := readerOver(t, node)

	safeTip, err := r.Head(t.Context(), Safe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.EndBlock(t.Context(), 0, safeTip); err != nil {
		t.Errorf("EndBlock at safe = %v, want the Epoch closed", err)
	}

	finalTip, err := r.Head(t.Context(), Finalized)
	if err != nil {
		t.Fatal(err)
	}

	var open *EpochNotClosed
	if _, err := r.EndBlock(t.Context(), 0, finalTip); !errors.As(err, &open) {
		t.Errorf("EndBlock at finalized = %v, want EpochNotClosed", err)
	}
}

func TestEndBlock_AnEpochBeforeTheChainExisted(t *testing.T) {
	t.Parallel()

	// Genesis at timestamp 1,000,000 puts every Epoch below 277 entirely before block 0.
	node := newFakeNode(100, 15)
	for i := range node.blocks {
		node.blocks[i].Timestamp += 1_000_000
	}

	r := readerOver(t, node)
	tip, err := r.Head(t.Context(), Safe)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.EndBlock(t.Context(), 1, tip); err == nil {
		t.Error("EndBlock = nil error, want a refusal")
	}
}

func TestTimestamps_AreFetchedOncePerDistinctBlock(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)

	got, err := readerOver(t, node).Timestamps(t.Context(), []uint64{5, 5, 9, 5})
	if err != nil {
		t.Fatalf("Timestamps = %v", err)
	}
	if len(got) != 2 || got[5] != 75 || got[9] != 135 {
		t.Errorf("Timestamps = %v", got)
	}

	node.mu.Lock()
	defer node.mu.Unlock()
	headers := 0
	for _, m := range node.requests {
		if m == "eth_getBlockByNumber" {
			headers++
		}
	}
	if headers != 2 {
		t.Errorf("made %d header requests for 2 distinct blocks", headers)
	}
}

func TestReads_HonourACancelledContext(t *testing.T) {
	t.Parallel()

	// A hung endpoint has to become INDETERMINATE rather than a run that never ends, because
	// the Dispute window does end.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := readerOver(t, newFakeNode(20, 15)).BlockByNumber(ctx, 1); err == nil {
		t.Error("BlockByNumber = nil error, want the cancellation to surface")
	}
}

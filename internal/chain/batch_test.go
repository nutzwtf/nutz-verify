package chain

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"
)

// Block headers are the cost of a sync, not the logs: a log carries no timestamp, so every
// block that produced one needs its header, and on chain 4663 that is essentially every
// block. One request per header cannot finish against the public endpoint — ticket 04's
// load test measured 71 requests in three seconds earning a 429 that outlasted the retry
// budget — so headers go out in JSON-RPC batches, and these tests are about the batches.

func (n *fakeNode) seenBatches() []int {
	n.mu.Lock()
	defer n.mu.Unlock()

	return append([]int(nil), n.batches...)
}

func blocksFrom(from, to uint64) []uint64 {
	out := make([]uint64, 0, to-from+1)
	for b := from; b <= to; b++ {
		out = append(out, b)
	}

	return out
}

func TestTimestamps_GoOutInBatches(t *testing.T) {
	t.Parallel()

	node := newFakeNode(2*HeaderBatch+10, 15)
	wanted := blocksFrom(0, 2*HeaderBatch+9)

	got, err := readerOver(t, node).Timestamps(t.Context(), wanted)
	if err != nil {
		t.Fatalf("Timestamps = %v", err)
	}
	for _, b := range wanted {
		if got[b] != int64(b)*15 {
			t.Fatalf("Timestamps[%d] = %d, want %d", b, got[b], b*15)
		}
	}

	// Two full batches and a remainder of ten, and no single-header requests at all.
	batches := node.seenBatches()
	slices.Sort(batches)
	if want := []int{10, HeaderBatch, HeaderBatch}; !slices.Equal(batches, want) {
		t.Errorf("batches = %v, want %v", batches, want)
	}
}

func TestTimestamps_BatchRepliesAreMatchedByID(t *testing.T) {
	t.Parallel()

	// JSON-RPC lets a node answer a batch in any order. A client that trusts position
	// would stamp every transfer with its neighbour's timestamp and change every TWAB.
	node := newFakeNode(20, 15)
	node.shuffleBatch = true

	got, err := readerOver(t, node).Timestamps(t.Context(), []uint64{3, 7, 11})
	if err != nil {
		t.Fatalf("Timestamps = %v", err)
	}
	if got[3] != 45 || got[7] != 105 || got[11] != 165 {
		t.Errorf("Timestamps = %v, want 3:45 7:105 11:165", got)
	}
}

func TestTimestamps_FallBackWhenBatchesAreRefused(t *testing.T) {
	t.Parallel()

	// Not every endpoint batches. One that answers a batch with a single error object is
	// asked again one header at a time, and remembers that so the next batch is not tried.
	node := newFakeNode(20, 15)
	node.noBatch = true

	r := readerOver(t, node)
	got, err := r.Timestamps(t.Context(), []uint64{3, 7})
	if err != nil {
		t.Fatalf("Timestamps = %v", err)
	}
	if got[3] != 45 || got[7] != 105 {
		t.Errorf("Timestamps = %v", got)
	}

	if _, err := r.Timestamps(t.Context(), []uint64{9}); err != nil {
		t.Fatalf("second Timestamps = %v", err)
	}
	if batches := node.seenBatches(); len(batches) != 1 {
		t.Errorf("batches attempted = %v, want one, before the fallback was remembered", batches)
	}
}

func TestTimestamps_OneMissingHeaderInABatchFailsTheRead(t *testing.T) {
	t.Parallel()

	// The node answers null for one block of the batch. That is a hole in the timestamps,
	// and a Recompute with a hole is not a Recompute; the rest of the batch does not make
	// it one.
	node := newFakeNode(20, 15)
	node.missing[7] = true

	if _, err := readerOver(t, node).Timestamps(t.Context(), []uint64{3, 7, 11}); err == nil {
		t.Error("Timestamps = nil error, want a refusal")
	}
}

func TestTimestamps_ABatchRidesOutARateLimit(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)
	node.refuseFirst, node.refuseWith = 2, http.StatusTooManyRequests

	got, err := readerOver(t, node).Timestamps(t.Context(), []uint64{3, 7})
	if err != nil {
		t.Fatalf("Timestamps = %v", err)
	}
	if got[3] != 45 || got[7] != 105 {
		t.Errorf("Timestamps = %v", got)
	}
}

func TestTimestamps_BatchesAreCrossChecked(t *testing.T) {
	t.Parallel()

	// Two endpoints disagreeing about one header inside a batch is the same contradiction
	// as disagreeing about a single one, and it is not resolved by picking.
	a := newFakeNode(20, 15)
	b := newFakeNode(20, 15)
	b.blocks[7].Hash = blockHash(7, 1)

	if _, err := readerOver(t, a, b).Timestamps(t.Context(), []uint64{3, 7, 11}); err == nil {
		t.Error("Timestamps = nil error, want a Disagreement")
	}
}

func TestTimestamps_ABatchReplyThatIsNotForUs(t *testing.T) {
	t.Parallel()

	// A reply whose id matches nothing we sent, or a batch with a reply missing, is an
	// endpoint that did not answer the question; neither is a timestamp.
	node := newFakeNode(20, 15)
	node.override["eth_getBlockByNumber"] = nil // every reply is null

	if _, err := readerOver(t, node).Timestamps(t.Context(), []uint64{3, 7}); err == nil {
		t.Error("Timestamps = nil error, want a refusal")
	}
}

func TestTimestamps_FallBackWhenBatchesAreRefusedOverHTTP(t *testing.T) {
	t.Parallel()

	// The other way an endpoint says no to a batch: a gateway that rejects the array body
	// before any node sees it. Same fallback, and a rate limit is not that — it is retried
	// as a batch rather than mistaken for a refusal.
	node := newFakeNode(20, 15)
	node.batchHTTPStatus = http.StatusBadRequest

	got, err := readerOver(t, node).Timestamps(t.Context(), []uint64{3, 7})
	if err != nil {
		t.Fatalf("Timestamps = %v", err)
	}
	if got[3] != 45 || got[7] != 105 {
		t.Errorf("Timestamps = %v", got)
	}
}

func TestTimestamps_ABatchWithAReplyMissingFailsTheRead(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)
	node.dropFromBatch = true

	if _, err := readerOver(t, node).Timestamps(t.Context(), []uint64{3, 7}); err == nil {
		t.Error("Timestamps = nil error, want a refusal for the unanswered request")
	}
}

func TestTimestamps_TheHeaderMustBeTheBlockAskedFor(t *testing.T) {
	t.Parallel()

	// The id says which question was answered, not that the answer is the block the
	// question named. A node that answers every question with block 5's header is caught
	// in both the batched and the one-at-a-time path.
	for _, noBatch := range []bool{false, true} {
		node := newFakeNode(20, 15)
		node.noBatch = noBatch
		node.override["eth_getBlockByNumber"] = node.block(quantity(5))

		if _, err := readerOver(t, node).Timestamps(t.Context(), []uint64{3, 7}); err == nil {
			t.Errorf("noBatch=%v: Timestamps = nil error, want a refusal of the wrong block", noBatch)
		}
	}
}

func TestTimestamps_ArePacedToTheEndpointsBudget(t *testing.T) {
	t.Parallel()

	// Chain 4663's public endpoint budgets JSON-RPC calls, batched or not, at about fifteen
	// a second with a bucket of about a hundred. Spending that budget on purpose is what
	// lets a sync finish; tripping it and retrying is what the first load test did, and it
	// did not finish. So each endpoint paces its calls, counting every request in a batch.
	node := newFakeNode(200, 15)

	r, err := New(Config{Endpoints: []string{node.serve(t)}, Token: nutz, Distributor: distributor,
		CallsPerSecond: 100})
	if err != nil {
		t.Fatal(err)
	}

	// 200 headers at 100 calls per second: the bucket, two batches deep, covers the first
	// forty and the rest wait for refill, so this takes at least 1.6 seconds.
	started := time.Now()
	if _, err := r.Timestamps(t.Context(), blocksFrom(0, 199)); err != nil {
		t.Fatalf("Timestamps = %v", err)
	}

	floor := time.Duration(float64(200-2*HeaderBatch)/100*float64(time.Second)) - 200*time.Millisecond
	if elapsed := time.Since(started); elapsed < floor {
		t.Errorf("200 headers took %s at 100 calls per second; the pacing is not being applied", elapsed)
	}
}

func TestNew_RefusesANegativeRate(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Endpoints: []string{"http://localhost:8545"}, Token: nutz, Distributor: distributor,
		CallsPerSecond: -1}); err == nil {
		t.Error("New = nil error for a negative rate")
	}
}

func TestTimestamps_ATransportFailureDoesNotDisableBatches(t *testing.T) {
	t.Parallel()

	// A dropped connection says nothing about whether the endpoint batches. Latching
	// "no batches" on it would make every later read pay a request per header.
	node := newFakeNode(20, 15)
	url := node.serve(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	r, err := New(Config{Endpoints: []string{url}, Token: nutz, Distributor: distributor, CallsPerSecond: unpaced})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Timestamps(ctx, []uint64{3, 7}); err == nil {
		t.Fatal("Timestamps = nil error under a cancelled context")
	}

	if _, err := r.Timestamps(t.Context(), []uint64{3, 7}); err != nil {
		t.Fatalf("Timestamps = %v", err)
	}
	if batches := node.seenBatches(); len(batches) != 1 {
		t.Errorf("batches seen = %v, want the one after the cancelled attempt", batches)
	}
}

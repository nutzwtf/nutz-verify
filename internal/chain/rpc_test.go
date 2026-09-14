package chain

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRoots_DecodeFromTheLogStream(t *testing.T) {
	t.Parallel()

	// carryIn is the reason this event is read at all: ledger() publishes funded, totals and
	// the Root, but not the Carry a Root was computed against, and that is Assertion 4.
	node := newFakeNode(20, 15)
	root := repeatHash(0x7e)
	totals, carryIn := amountsOf(1, 2, 3, 4, 5), amountsOf(10, 20, 30, 40, 50)
	node.addRoot(6, 0, distributor, KindEpoch, 4242, root, totals, carryIn)
	node.addRoot(8, 1, distributor, KindDraw, 7, repeatHash(0x11), carryIn, totals)

	got, err := readerOver(t, node).Roots(t.Context(), 0, 19)
	if err != nil {
		t.Fatalf("Roots = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d Roots, want 2", len(got))
	}

	// Both kinds come back: they share a topic0 and are told apart by the first indexed
	// argument, so filtering in the query would cost a second round trip for nothing.
	if got[0].Kind != KindEpoch || got[0].ID != 4242 || got[0].Root != root {
		t.Errorf("first Root = %+v", got[0])
	}
	if got[0].Timestamp != 90 {
		t.Errorf("first Root at %d, want 90", got[0].Timestamp)
	}
	assertAmounts(t, "totals", got[0].Totals, totals)
	assertAmounts(t, "carryIn", got[0].CarryIn, carryIn)

	if got[1].Kind != KindDraw || got[1].ID != 7 {
		t.Errorf("second Root = %+v", got[1])
	}
}

func TestKind_String(t *testing.T) {
	t.Parallel()

	if KindEpoch.String() != "epoch" || KindDraw.String() != "draw" {
		t.Errorf("kinds render as %q and %q", KindEpoch, KindDraw)
	}
	if got := Kind(9).String(); !strings.Contains(got, "9") {
		t.Errorf("an unknown kind renders as %q", got)
	}
}

func TestReader_EchoesItsAddresses(t *testing.T) {
	t.Parallel()

	// ADR-0002 wants the unverifiable inputs shown in every run's header rather than buried.
	r := readerOver(t, newFakeNode(5, 12))

	if r.Token() != nutz || r.Distributor() != distributor {
		t.Errorf("Token/Distributor = %s / %s", r.Token(), r.Distributor())
	}
}

func TestCall_AnHTTPErrorSays_Which(t *testing.T) {
	t.Parallel()

	// A rate limit and a gateway error both arrive as HTML often enough that the status line
	// alone leaves a user guessing which endpoint to blame.
	node := newFakeNode(20, 15)
	node.httpStatus = 429

	_, err := readerOver(t, node).BlockByNumber(t.Context(), 1)
	if err == nil {
		t.Fatal("BlockByNumber = nil error, want the status to surface")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error does not carry the status: %v", err)
	}
}

func TestCall_AJSONRPCErrorKeepsItsCode(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)
	node.failWith["eth_getLogs"] = "query returned more than 10000 results"

	_, err := readerOver(t, node).Transfers(t.Context(), 0, 19)
	if err == nil {
		t.Fatal("Transfers = nil error, want the rpc error to surface")
	}
	if !strings.Contains(err.Error(), "10000 results") {
		t.Errorf("error loses the provider's message: %v", err)
	}
}

func TestReads_RefuseAMalformedResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		result any
		read   func(*Reader) error
	}{
		{
			name:   "a chain id that is not a quantity",
			method: "eth_chainId",
			result: "four thousand six hundred and sixty three",
			read:   func(r *Reader) error { _, err := r.ChainID(t.Context()); return err },
		},
		{
			name:   "a chain id that is not even a string",
			method: "eth_chainId",
			result: 4663,
			read:   func(r *Reader) error { _, err := r.ChainID(t.Context()); return err },
		},
		{
			name:   "a header whose hash is short",
			method: "eth_getBlockByNumber",
			result: wireBlock{Number: "0x1", Hash: "0xdead", ParentHash: "0x" + strings.Repeat("00", 32), Timestamp: "0x1"},
			read:   func(r *Reader) error { _, err := r.BlockByNumber(t.Context(), 1); return err },
		},
		{
			name:   "a header that is not an object",
			method: "eth_getBlockByNumber",
			result: "0x1",
			read:   func(r *Reader) error { _, err := r.BlockByNumber(t.Context(), 1); return err },
		},
		{
			name:   "a log page that is not an array",
			method: "eth_getLogs",
			result: map[string]any{"logs": []any{}},
			read:   func(r *Reader) error { _, err := r.Transfers(t.Context(), 0, 19); return err },
		},
		{
			name:   "call data that is not hex",
			method: "eth_call",
			result: "not data",
			read:   func(r *Reader) error { _, err := r.Excluded(t.Context(), 1); return err },
		},
		{
			name:   "call data that is not a string",
			method: "eth_call",
			result: []string{"0x"},
			read:   func(r *Reader) error { _, err := r.Excluded(t.Context(), 1); return err },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			node := newFakeNode(20, 15)
			node.override[tt.method] = tt.result

			if err := tt.read(readerOver(t, node)); err == nil {
				t.Error("read = nil error, want a refusal")
			}
		})
	}
}

func TestGetLogs_RefuseWhatTheFilterDidNotAskFor(t *testing.T) {
	t.Parallel()

	// An endpoint that ignores the filter is not one whose remaining answers are worth
	// taking on trust, and this is the shape in which a Transfer becomes a Root.
	foreign := wireLog{
		Address:     repeatAddr(0x99).String(),
		Topics:      []string{topicTransfer.String(), addressTopic(alice).String(), addressTopic(bob).String()},
		Data:        hexBytes(word(big.NewInt(1))),
		BlockNumber: quantity(3),
		BlockHash:   repeatHash(0x03).String(),
		LogIndex:    "0x0",
	}

	wrongTopic := foreign
	wrongTopic.Address = nutz.String()
	wrongTopic.Topics = []string{repeatHash(0x99).String(), addressTopic(alice).String(), addressTopic(bob).String()}

	outOfRange := foreign
	outOfRange.Address = nutz.String()
	outOfRange.BlockNumber = quantity(19)

	tests := []struct {
		name string
		log  wireLog
	}{
		{"a log from another contract", foreign},
		{"a log carrying another event", wrongTopic},
		{"a log from outside the range", outOfRange},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			node := newFakeNode(20, 15)
			node.always = []wireLog{tt.log}

			if _, err := readerOver(t, node).Transfers(t.Context(), 0, 5); err == nil {
				t.Error("Transfers = nil error, want a refusal")
			}
		})
	}
}

func TestDisagreement_NamesBothEndpoints(t *testing.T) {
	t.Parallel()

	d := &Disagreement{Read: "block 12", A: "endpoint 1 (a.example)", B: "endpoint 2 (b.example)"}

	message := d.Error()
	for _, want := range []string{"block 12", "a.example", "b.example"} {
		if !strings.Contains(message, want) {
			t.Errorf("Disagreement does not mention %q: %s", want, message)
		}
	}
}

func TestTimestamps_OfNothing(t *testing.T) {
	t.Parallel()

	got, err := readerOver(t, newFakeNode(5, 12)).Timestamps(t.Context(), nil)
	if err != nil {
		t.Fatalf("Timestamps = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Timestamps = %v, want empty", got)
	}
}

func TestTimestamps_AFailureMidPoolSurfaces(t *testing.T) {
	t.Parallel()

	// Block 30 does not exist, so the node answers null for it. One missing header has to
	// fail the whole read: a Recompute with a hole in its timestamps is not a Recompute.
	node := newFakeNode(20, 15)

	if _, err := readerOver(t, node).Timestamps(t.Context(), []uint64{1, 2, 3, 30}); err == nil {
		t.Error("Timestamps = nil error, want a refusal")
	}
}

func TestCall_RefusesABodyThatIsNotJSONRPC(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>gateway timeout</html>"))
	}))
	t.Cleanup(server.Close)

	r, err := New(Config{Endpoints: []string{server.URL}, Token: nutz, Distributor: distributor})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.BlockByNumber(t.Context(), 1); err == nil {
		t.Error("BlockByNumber = nil error, want a refusal")
	}
}

func TestCall_AnEndpointThatIsNotListening(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close() // the port is now closed, which is a provider outage in miniature

	r, err := New(Config{Endpoints: []string{url}, Token: nutz, Distributor: distributor})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.ChainID(t.Context()); err == nil {
		t.Error("ChainID = nil error, want the outage to surface")
	}
}

func TestReads_ADecodeFailureFailsTheWholeRead(t *testing.T) {
	t.Parallel()

	// A log that passes the filter guards and then does not decode: right contract, right
	// topic0, wrong shape. Skipping it would silently drop a Holder's transfer.
	tests := []struct {
		name string
		log  wireLog
		read func(*Reader) error
	}{
		{
			name: "a Transfer with a missing indexed argument",
			log: wireLog{
				Address:     nutz.String(),
				Topics:      []string{topicTransfer.String(), addressTopic(alice).String()},
				Data:        hexBytes(word(big.NewInt(1))),
				BlockNumber: quantity(3),
				BlockHash:   repeatHash(0x03).String(),
				LogIndex:    "0x0",
			},
			read: func(r *Reader) error { _, err := r.Transfers(t.Context(), 0, 5); return err },
		},
		{
			name: "an ExcludedAppended carrying data",
			log: wireLog{
				Address:     distributor.String(),
				Topics:      []string{topicExcludedAppended.String(), addressTopic(alice).String()},
				Data:        hexBytes(word(big.NewInt(1))),
				BlockNumber: quantity(3),
				BlockHash:   repeatHash(0x03).String(),
				LogIndex:    "0x0",
			},
			read: func(r *Reader) error { _, err := r.Exclusions(t.Context(), 0, 5); return err },
		},
		{
			name: "a RootPosted with ten data words",
			log: wireLog{
				Address:     distributor.String(),
				Topics:      []string{topicRootPosted.String(), word32(0).String(), word32(1).String()},
				Data:        hexBytes(make([]byte, 10*wordSize)),
				BlockNumber: quantity(3),
				BlockHash:   repeatHash(0x03).String(),
				LogIndex:    "0x0",
			},
			read: func(r *Reader) error { _, err := r.Roots(t.Context(), 0, 5); return err },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			node := newFakeNode(20, 15)
			node.always = []wireLog{tt.log}

			if err := tt.read(readerOver(t, node)); err == nil {
				t.Error("read = nil error, want a refusal")
			}
		})
	}
}

func TestReads_AMissingTimestampFailsTheWholeRead(t *testing.T) {
	t.Parallel()

	// The logs decode and then the block they came from cannot be read. A Recompute with a
	// hole in its timestamps is not a Recompute.
	tests := []struct {
		name string
		add  func(*fakeNode)
		read func(*Reader) error
	}{
		{
			name: "a Transfer",
			add:  func(n *fakeNode) { n.addTransfer(3, 0, nutz, alice, bob, 1) },
			read: func(r *Reader) error { _, err := r.Transfers(t.Context(), 0, 5); return err },
		},
		{
			name: "an exclusion",
			add:  func(n *fakeNode) { n.addExclusion(3, 0, distributor, alice) },
			read: func(r *Reader) error { _, err := r.Exclusions(t.Context(), 0, 5); return err },
		},
		{
			name: "a Root",
			add: func(n *fakeNode) {
				n.addRoot(3, 0, distributor, KindEpoch, 1, repeatHash(0x7e),
					amountsOf(1, 2, 3, 4, 5), amountsOf(0, 0, 0, 0, 0))
			},
			read: func(r *Reader) error { _, err := r.Roots(t.Context(), 0, 5); return err },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			node := newFakeNode(20, 15)
			tt.add(node)
			node.missing[3] = true

			if err := tt.read(readerOver(t, node)); err == nil {
				t.Error("read = nil error, want a refusal")
			}
		})
	}
}

func TestChainID_AnUnreachableEndpoint(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)
	node.failWith["eth_chainId"] = "method not supported"

	if _, err := readerOver(t, node).ChainID(t.Context()); err == nil {
		t.Error("ChainID = nil error, want a refusal")
	}
}

func TestLedger_ACallThatFails(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)
	node.failWith["eth_call"] = "execution reverted"

	if _, err := readerOver(t, node).Ledger(t.Context(), KindEpoch, 1, 5); err == nil {
		t.Error("Ledger = nil error, want a refusal")
	}
}

func TestEndBlock_GenesisIsTheAnswer(t *testing.T) {
	t.Parallel()

	// One block every 4,000 seconds: block 0 at 0 and block 1 at 4,000, so Epoch 0's last
	// block is the genesis block itself.
	node := newFakeNode(10, 4000)
	r := readerOver(t, node)

	tip, err := r.Head(t.Context(), Safe)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.EndBlock(t.Context(), 0, tip)
	if err != nil {
		t.Fatalf("EndBlock = %v", err)
	}
	if got.Number != 0 {
		t.Errorf("end block = %d, want 0", got.Number)
	}
}

func TestEndBlock_AFailureDuringTheSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		missing uint64
	}{
		{"the genesis probe", 0},
		{"a block inside the search", 499}, // the first midpoint over [0, 999]
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			node := newFakeNode(1000, 15)
			r := readerOver(t, node)

			tip, err := r.Head(t.Context(), Safe)
			if err != nil {
				t.Fatal(err)
			}

			node.missing[tt.missing] = true

			if _, err := r.EndBlock(t.Context(), 0, tip); err == nil {
				t.Error("EndBlock = nil error, want a refusal")
			}
		})
	}
}

func TestReads_RefuseARangeThatRunsBackwards(t *testing.T) {
	t.Parallel()

	r := readerOver(t, newFakeNode(20, 15))

	if _, err := r.Exclusions(t.Context(), 10, 9); err == nil {
		t.Error("Exclusions = nil error, want a refusal")
	}
	if _, err := r.Roots(t.Context(), 10, 9); err == nil {
		t.Error("Roots = nil error, want a refusal")
	}
}

func TestNewEndpoint_RefusesAURLWithNoHost(t *testing.T) {
	t.Parallel()

	if _, err := newEndpoint("http:///rpc", 1, nil); err == nil {
		t.Error("newEndpoint = nil error, want a refusal")
	}
}

func TestCall_RidesOutARateLimit(t *testing.T) {
	t.Parallel()

	// A public endpoint rate-limits, and the Verifier exists for the stranger using one. A 429
	// is not "could not check" — the provider is saying exactly how to succeed — so turning
	// one into INDETERMINATE would fail a Recompute for the reason it is likeliest to fail.
	node := newFakeNode(20, 15)
	node.refuseFirst, node.refuseWith = 3, http.StatusTooManyRequests

	got, err := readerOver(t, node).BlockByNumber(t.Context(), 5)
	if err != nil {
		t.Fatalf("BlockByNumber = %v", err)
	}
	if got.Number != 5 {
		t.Errorf("block = %d, want 5", got.Number)
	}
}

func TestCall_RidesOutAGatewayError(t *testing.T) {
	t.Parallel()

	node := newFakeNode(20, 15)
	node.refuseFirst, node.refuseWith = 2, http.StatusBadGateway

	if _, err := readerOver(t, node).BlockByNumber(t.Context(), 5); err != nil {
		t.Errorf("BlockByNumber = %v", err)
	}
}

func TestCall_GivesUpOnAnEndpointThatKeepsRefusing(t *testing.T) {
	t.Parallel()

	// Bounded: a wedged endpoint has to become INDETERMINATE inside the Dispute window rather
	// than hang in it.
	node := newFakeNode(20, 15)
	node.refuseFirst, node.refuseWith = 100, http.StatusTooManyRequests

	_, err := readerOver(t, node).BlockByNumber(t.Context(), 5)
	if err == nil {
		t.Fatal("BlockByNumber = nil error, want the refusal to surface")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error loses the status: %v", err)
	}

	node.mu.Lock()
	defer node.mu.Unlock()
	if node.refused != maxAttempts {
		t.Errorf("made %d attempts, want %d", node.refused, maxAttempts)
	}
}

func TestCall_DoesNotRetryWhatWillNotImprove(t *testing.T) {
	t.Parallel()

	// A bad key and an unsupported method fail identically five times over, having cost five
	// times as much. Only "up, but asking us to wait" is worth asking again.
	node := newFakeNode(20, 15)
	node.refuseFirst, node.refuseWith = 100, http.StatusForbidden

	if _, err := readerOver(t, node).BlockByNumber(t.Context(), 5); err == nil {
		t.Fatal("BlockByNumber = nil error, want a refusal")
	}

	node.mu.Lock()
	defer node.mu.Unlock()
	if node.refused != 1 {
		t.Errorf("made %d attempts at a 403, want 1", node.refused)
	}
}

func TestCall_HonoursRetryAfter(t *testing.T) {
	t.Parallel()

	// The endpoint's own instruction beats our guess at one.
	node := newFakeNode(20, 15)
	node.refuseFirst, node.refuseWith, node.retryAfter = 1, http.StatusTooManyRequests, "1"

	start := time.Now()
	if _, err := readerOver(t, node).BlockByNumber(t.Context(), 5); err != nil {
		t.Fatalf("BlockByNumber = %v", err)
	}

	if waited := time.Since(start); waited < time.Second {
		t.Errorf("waited %s, and the endpoint asked for a second", waited)
	}
}

func TestRetryAfter_IgnoresWhatItCannotRead(t *testing.T) {
	t.Parallel()

	// An HTTP-date is not parsed: nothing observed sends one, and guessing wrong about clock
	// skew would be worse than falling back to the backoff.
	for _, header := range []string{"", "Wed, 21 Oct 2026 07:28:00 GMT", "soon", "-5"} {
		resp := &http.Response{Header: http.Header{}}
		if header != "" {
			resp.Header.Set("Retry-After", header)
		}

		if got := retryAfter(resp); got != 0 {
			t.Errorf("retryAfter(%q) = %s, want 0", header, got)
		}
	}

	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "2")
	if got := retryAfter(resp); got != 2*time.Second {
		t.Errorf("retryAfter(2) = %s", got)
	}

	// Capped, so a provider asking for an hour does not hang a Dispute-window run.
	resp.Header.Set("Retry-After", "3600")
	if got := retryAfter(resp); got != retryCap {
		t.Errorf("retryAfter(3600) = %s, want the cap %s", got, retryCap)
	}
}

func TestBackoff_StaysInsideItsWindow(t *testing.T) {
	t.Parallel()

	for attempt := 1; attempt < maxAttempts; attempt++ {
		for range 50 {
			got := backoff(attempt, nil)
			if got < retryBase || got > retryCap+retryBase {
				t.Fatalf("backoff(%d) = %s, outside [%s, %s]", attempt, got, retryBase, retryCap+retryBase)
			}
		}
	}
}

func TestCall_ACancelledContextEndsTheBackoff(t *testing.T) {
	t.Parallel()

	// The Dispute window closes whether or not the endpoint recovers. A caller giving up has
	// to end the wait, not merely the next attempt after it.
	node := newFakeNode(20, 15)
	node.refuseFirst, node.refuseWith, node.retryAfter = 100, http.StatusTooManyRequests, "8"

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := readerOver(t, node).BlockByNumber(ctx, 5)
	if err == nil {
		t.Fatal("BlockByNumber = nil error, want the cancellation to surface")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error is not the cancellation: %v", err)
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("took %s to notice a cancellation during an 8s Retry-After", waited)
	}
}

func TestRetryable_UnwrapsToTheRefusal(t *testing.T) {
	t.Parallel()

	inner := errors.New("http 429 Too Many Requests")
	wrapped := &retryable{status: http.StatusTooManyRequests, err: inner}

	if !errors.Is(wrapped, inner) {
		t.Error("a retryable does not unwrap to the refusal it carries")
	}
	if wrapped.Error() != inner.Error() {
		t.Errorf("Error() = %q, want the refusal's own text", wrapped.Error())
	}
}

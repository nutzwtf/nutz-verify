package chain

import (
	"os"
	"strings"
	"testing"
)

// Live tests read real chain 4663 history over a real endpoint. They are the only place the
// Verifier meets a limit nobody publishes and no fake node can be told to have — the anvil
// harness proves the decoders, and this proves the paging survives contact with a provider.
//
// Opt-in: set NUTZ_VERIFY_LIVE_RPC. Chain 4663's public endpoint needs no key
// (https://rpc.mainnet.chain.robinhood.com), which is the whole point of ADR-0002's "a
// hostile stranger downloads the binary and runs it" — so this costs a contributor nothing
// but a network round trip. NUTZ_VERIFY_LIVE_RPC_2 adds a second, independent provider and
// turns on the cross-check.

// usdgOn4663 is the USDG reward token, from nutz-contracts/script/config/robinhood.json. NUTZ
// does not exist yet and USDG is the busiest thing on the chain, which is exactly what makes
// it the right thing to page over: engineering spec §10 names it for the same reason.
const usdgOn4663 = "0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168"

// liveSpan is the smallest range that reliably crosses the 10,000-result cap. USDG runs at
// about 4.9 Transfer logs per block on 4663, so the opening 2,000-block page is ~9,750 — right
// on the cliff — and 2,500 clears it. Deliberately not wider: this reads a public endpoint
// somebody else pays for, and one narrowing followed by three pages proves the behaviour just
// as well as a sync would.
const liveSpan = 2500

// throttled reports whether a live endpoint refused us rather than failed.
//
// Skipping on this is not the silent non-check the anvil harness was hardened against: that
// harness runs against a node we start, so a skip there means our own setup is missing, while
// here a third party's quota is not something a test can assert about. The reason is logged
// either way.
func throttled(err error) bool {
	message := err.Error()

	return strings.Contains(message, "429") ||
		strings.Contains(message, "403") ||
		strings.Contains(message, "Too Many Requests")
}

func liveReader(t *testing.T, endpoints ...string) *Reader {
	t.Helper()

	token, err := ParseAddress(usdgOn4663)
	if err != nil {
		t.Fatal(err)
	}

	// No Distributor is deployed on 4663 yet, and nothing below calls it. A Reader refuses a
	// zero address rather than letting one through, so this stands in for it.
	r, err := New(Config{Endpoints: endpoints, Token: token, Distributor: token})
	if err != nil {
		t.Fatal(err)
	}

	return r
}

func liveEndpoints(t *testing.T) []string {
	t.Helper()

	primary := os.Getenv("NUTZ_VERIFY_LIVE_RPC")
	if primary == "" {
		t.Skip("live: set NUTZ_VERIFY_LIVE_RPC to a chain 4663 endpoint " +
			"(the public one needs no key: https://rpc.mainnet.chain.robinhood.com)")
	}

	endpoints := []string{primary}
	if second := os.Getenv("NUTZ_VERIFY_LIVE_RPC_2"); second != "" {
		endpoints = append(endpoints, second)
	}

	return endpoints
}

func TestLive_PagingSurvivesAResultCap(t *testing.T) {
	// Engineering spec §4.1 calls 2,000 blocks "the public-RPC cap". It is not: chain 4663's
	// public endpoint accepts a million-block range and refuses on 10,000 *results*. A fixed
	// 2,000-block page therefore fails against a busy token — and it fails intermittently,
	// because whether it fails depends on how busy those particular blocks were. This is the
	// test that would have caught that, and it reads the chain because nothing else can.
	r := liveReader(t, liveEndpoints(t)...)

	tip, err := r.Head(t.Context(), Safe)
	if err != nil {
		t.Fatalf("Head = %v", err)
	}

	// logs rather than Transfers on purpose: Transfers also resolves one block header per
	// block that produced a log, and over a stretch this busy that is one request per block.
	// Paging is what is under test here; that cost is ticket 04's to measure.
	to := tip.Block.Number
	from := to - liveSpan + 1

	logs, err := r.logs(t.Context(), r.token, topicTransfer, from, to)
	if err != nil {
		if throttled(err) {
			t.Skipf("live: the endpoint is throttling us, so this proves nothing either way: %v", err)
		}

		t.Fatalf("logs over %d..%d = %v", from, to, err)
	}

	// The point is that it completed at all. Asserting a count would be asserting how busy
	// the chain was this minute.
	if len(logs) == 0 {
		t.Fatalf("no USDG Transfer logs in %d blocks; the address or the chain is not what this expects", liveSpan)
	}
	t.Logf("%d USDG Transfer logs over blocks %d..%d (%.1f per block)",
		len(logs), from, to, float64(len(logs))/float64(liveSpan))

	for i := 1; i < len(logs); i++ {
		if logs[i].BlockNumber < logs[i-1].BlockNumber {
			t.Fatalf("log %d is from block %d, behind the previous %d — a page was stitched out of order",
				i, logs[i].BlockNumber, logs[i-1].BlockNumber)
		}
	}
	for _, l := range logs {
		if l.BlockNumber < from || l.BlockNumber > to {
			t.Fatalf("a log came back from block %d, outside %d..%d", l.BlockNumber, from, to)
		}
	}
}

func TestLive_TwoProvidersAgree(t *testing.T) {
	// ADR-0002 turns "trust your provider" into "trust that two providers are not colluding",
	// and until this test that claim had only ever been checked against one anvil compared
	// with itself. Two real providers differ in JSON formatting, in log ordering and in which
	// limits they enforce; if any of that reached canonicalLogs, every multi-endpoint run
	// would be INDETERMINATE and the mitigation ADR-0002 leans on would be unusable.
	endpoints := liveEndpoints(t)
	if len(endpoints) < 2 {
		t.Skip("live: set NUTZ_VERIFY_LIVE_RPC_2 to a second, independent chain 4663 endpoint")
	}

	r := liveReader(t, endpoints...)

	id, err := r.ChainID(t.Context())
	if err != nil {
		t.Fatalf("ChainID = %v", err)
	}
	if id != harnessChainID {
		t.Fatalf("chain id = %d, want %d: these endpoints are not both on 4663", id, harnessChainID)
	}

	tip, err := r.Head(t.Context(), Safe)
	if err != nil {
		t.Fatalf("Head = %v", err)
	}
	t.Logf("agreed %s head is block %d, with the slower endpoint %d behind the faster",
		tip.Finality, tip.Block.Number, tip.Lag)

	// Well behind the tip, so the two are compared over history both have settled on rather
	// than over blocks one of them is still catching up to.
	to := tip.Block.Number - 100
	from := to - liveSpan + 1

	logs, err := r.logs(t.Context(), r.token, topicTransfer, from, to)
	if err != nil {
		if throttled(err) {
			t.Skipf("live: an endpoint is throttling us: %v", err)
		}

		// A Disagreement here is the finding, not a flake: it would mean the canonical
		// comparison cannot survive two real providers, and ADR-0002's mitigation with it.
		t.Fatalf("two providers over %d..%d = %v", from, to, err)
	}
	t.Logf("%d USDG Transfer logs, byte-identical across %v", len(logs), r.Endpoints())
}

package cache_test

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nutzwtf/nutz-verify/cache"
	"github.com/nutzwtf/nutz-verify/chain"
)

// The load test. Spec §10: "point ingestion at USDG history on 4663. At ~100 ms blocks the
// real risk is volume, and this is the only way to learn before launch whether a
// from-scratch sync takes minutes or hours."
//
// Opt-in via NUTZ_VERIFY_LIVE_RPC, like chain's live tests, because it reads a
// public endpoint somebody else pays for. NUTZ_VERIFY_LOAD_BLOCKS sets how many blocks of
// history to sync (default loadBlocks); the numbers it prints are what spec §9 records.

// usdgOn4663 is the USDG reward token, the busiest thing on the chain and the pessimistic
// proxy spec §10 names; NUTZ does not exist yet.
const usdgOn4663 = "0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168"

// loadBlocks is the default slice of history: ~5,000 headers and ~35,000 records at the
// tip's density, about five minutes at the public endpoint's pace. Long enough for the rate
// to settle past the endpoint's burst allowance; short enough to run on every nightly.
const loadBlocks = 5000

// blocksPerDay is the unit the extrapolation is in. Measured rather than assumed: chain
// 4663 sealed 63.18M blocks in the 137 days to 2026-09-14, ~460,000 a day, because an Orbit
// chain only seals a block when there is something to seal — not the ~100 ms cadence spec
// §10 assumed.
const blocksPerDay = 460_000

// counting is an http.RoundTripper that counts requests, so the test can say how many the
// endpoint actually saw rather than how many it should have.
type counting struct {
	next     http.RoundTripper
	requests atomic.Int64
}

func (c *counting) RoundTrip(r *http.Request) (*http.Response, error) {
	c.requests.Add(1)

	return c.next.RoundTrip(r)
}

// throttled mirrors chain's helper of the same name (a _test.go cannot be imported):
// the endpoint refused us rather than failed, and a third party's quota is not something a
// test can assert about.
func throttled(err error) bool {
	message := err.Error()

	return strings.Contains(message, "429") ||
		strings.Contains(message, "403") ||
		strings.Contains(message, "Too Many Requests")
}

func TestLive_SyncUSDGHistory(t *testing.T) {
	endpoint := os.Getenv("NUTZ_VERIFY_LIVE_RPC")
	if endpoint == "" {
		t.Skip("live: set NUTZ_VERIFY_LIVE_RPC to a chain 4663 endpoint " +
			"(the public one needs no key: https://rpc.mainnet.chain.robinhood.com)")
	}

	span := uint64(loadBlocks)
	if s := os.Getenv("NUTZ_VERIFY_LOAD_BLOCKS"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || n == 0 {
			t.Fatalf("NUTZ_VERIFY_LOAD_BLOCKS=%q is not a block count", s)
		}

		span = n
	}

	token, err := chain.ParseAddress(usdgOn4663)
	if err != nil {
		t.Fatal(err)
	}

	transport := &counting{next: http.DefaultTransport}
	r, err := chain.New(chain.Config{
		Endpoints:   []string{endpoint},
		Token:       token,
		Distributor: token, // no Distributor on 4663 yet, and nothing here calls it
		HTTPClient:  &http.Client{Transport: transport, Timeout: 30 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}

	tip, err := r.Head(t.Context(), chain.Safe)
	if err != nil {
		t.Fatalf("Head = %v", err)
	}

	from := tip.Block.Number - span + 1
	c := open(t, t.TempDir(), cache.Options{ChainID: 4663, Token: token, Start: from})

	transport.requests.Store(0)
	started := time.Now()

	var lastReport time.Time
	synced, err := c.Sync(t.Context(), r, tip.Block, cache.SyncOptions{
		Chunk: 1000,
		Progress: func(block uint64) {
			if time.Since(lastReport) > 10*time.Second {
				lastReport = time.Now()
				t.Logf("  at block %d of %d (%d records, %d requests, %s)",
					block, tip.Block.Number, c.Len(), transport.requests.Load(), time.Since(started).Round(time.Second))
			}
		},
	})
	elapsed := time.Since(started)
	if err != nil {
		// A refusal is the endpoint's budget, not a Sync bug: chain's live tests
		// skip on the same signal, and the first nightly proved this one has to as well —
		// the public endpoint answered 429 at 15,676 records. The rate up to that point is
		// still the measurement, so it is logged before the skip.
		if throttled(err) {
			t.Skipf("live: the endpoint is throttling us after %s, %d records, %d requests, "+
				"so this proves nothing about Sync either way: %v",
				elapsed.Round(time.Second), c.Len(), transport.requests.Load(), err)
		}

		t.Fatalf("Sync over %d blocks = %v after %s, %d records, %d requests",
			span, err, elapsed.Round(time.Second), c.Len(), transport.requests.Load())
	}

	requests := transport.requests.Load()
	seconds := elapsed.Seconds()

	t.Logf("synced %d blocks of USDG history (%d..%d) in %s",
		span, from, tip.Block.Number, elapsed.Round(time.Millisecond))
	t.Logf("  %d records, %.1f per block; %d requests, %.3f per block, %.1f per second",
		synced.Appended, float64(synced.Appended)/float64(span),
		requests, float64(requests)/float64(span), float64(requests)/seconds)
	t.Logf("  %.0f blocks per second, so one day of history (%d blocks) takes %s",
		float64(span)/seconds, blocksPerDay,
		(time.Duration(float64(blocksPerDay)/float64(span)*seconds) * time.Second).Round(time.Minute))

	if synced.Appended == 0 {
		t.Fatalf("no USDG transfers in %d blocks; the address or the chain is not what this expects", span)
	}

	// The file is what a Recompute replays, so read it back: the cost of the replay is the
	// other half of the answer, and it is the half that does not depend on the endpoint.
	replayStart := time.Now()
	var replayed int64
	if err := c.Each(func(cache.Record) error { replayed++; return nil }); err != nil {
		t.Fatal(err)
	}
	replay := time.Since(replayStart)
	t.Logf("  replayed %d records in %s (%.1f M records/s)",
		replayed, replay.Round(time.Millisecond), float64(replayed)/replay.Seconds()/1e6)
}

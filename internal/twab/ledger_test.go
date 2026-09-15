package twab_test

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// The Ledger's entire correctness claim is that it agrees with Replay, Epoch after Epoch,
// over the same history. Replay is what the Cases pin (ADR-0003); the Ledger is what
// --chain can afford (ticket 07). Every test here compares the two rather than asserting
// a TWAB by hand, so the arithmetic is pinned exactly once, in replay_test.go.

// A history that exercises everything a Holder carries across Epoch boundaries: balances,
// a firstBuyAt set in Epoch 1 and read six Epochs later, a lastSellAt that must survive the
// idle Epochs between, an Exclusion landing mid-way, a burn, a zero-value transfer, and a
// same-block receive-and-forward.
func TestLedger_AgreesWithReplayAcrossEpochs(t *testing.T) {
	t.Parallel()

	relay := repeat(0x9e)

	transfers := []twab.Transfer{
		mint(100, funder, 1_000_000),
		send(3700, funder, alice, 1000),       // Epoch 1: Alice buys
		send(5400, funder, bob, 500),          // Epoch 1: Bob buys
		send(9000, alice, funder, 400),        // Epoch 2: Alice sells some
		send(9000, funder, relay, 300),        // Epoch 2, same block: the relay forwards to Carol
		send(9000, relay, carol, 300),         //   (order within the block is not part of the input)
		send(15000, bob, twab.Address{}, 500), // Epoch 4: Bob burns everything
		send(22000, alice, bob, 0),            // Epoch 6: a zero-value transfer is a no-op, not a sell
		send(30000, funder, bob, 50),          // Epoch 8: Bob is back
		send(36000, funder, alice, 1),         // the instant Epoch 10 opens
	}
	exclusions := []twab.Exclusion{
		{Timestamp: 0, Account: funder},
		{Timestamp: 12_000, Account: carol}, // lands at Epoch 3's minute 20, zeroing all of Epoch 3 on
	}

	ledger := twab.NewLedger(transfers, exclusions)

	for epoch := uint64(0); epoch <= 12; epoch++ {
		want := replay(t, epoch, transfers, exclusions...)

		got, err := ledger.Advance(epoch)
		if err != nil {
			t.Fatalf("Advance(%d): %v", epoch, err)
		}

		requireSameHolders(t, epoch, got, want)
	}
}

// Skipping ahead must land exactly where walking would: the Epochs between are closed and
// nothing of theirs leaks into the target's sub-intervals.
func TestLedger_SkipsEpochsWithoutWalkingThem(t *testing.T) {
	t.Parallel()

	transfers := []twab.Transfer{
		mint(100, funder, 1_000_000),
		send(3700, funder, alice, 1000),
		send(9000, alice, funder, 400), // Epoch 2, which the Ledger is never asked about
		send(18100, funder, bob, 200),  // Epoch 5, before the target's window opens
		send(22000, funder, carol, 7),  // Epoch 6, inside the target
	}

	ledger := twab.NewLedger(transfers, nil)

	if _, err := ledger.Advance(1); err != nil {
		t.Fatalf("Advance(1): %v", err)
	}

	got, err := ledger.Advance(6)
	if err != nil {
		t.Fatalf("Advance(6): %v", err)
	}

	requireSameHolders(t, 6, got, replay(t, 6, transfers))
}

// A wallet that was idle for a long stretch is not on any active list, and the accrual it
// resumes with must start at the window it is touched in — not at the timestamp the Ledger
// last saw it, which would credit it for time it held nothing.
func TestLedger_ResumesAnIdleWalletAtTheWindowNotItsLastTransfer(t *testing.T) {
	t.Parallel()

	transfers := []twab.Transfer{
		mint(100, funder, 1_000_000),
		send(3700, funder, alice, 1000),
		send(7000, alice, funder, 1000), // Epoch 1: Alice is emptied
		send(18100, funder, alice, 500), // Epoch 5: Alice buys again, before the target's window
		send(22000, funder, bob, 1),     // Epoch 6: the target, where Alice has held 500 all along
	}

	ledger := twab.NewLedger(transfers, nil)

	for _, epoch := range []uint64{1, 2, 6} {
		got, err := ledger.Advance(epoch)
		if err != nil {
			t.Fatalf("Advance(%d): %v", epoch, err)
		}

		requireSameHolders(t, epoch, got, replay(t, epoch, transfers))
	}
}

// Agreement with Replay over histories nobody wrote by hand: many wallets, many Epochs,
// buys, sells, burns, forwards and Exclusions in whatever order the seed produces. A
// divergence prints the seed, which reproduces it.
func TestLedger_AgreesWithReplayOnRandomHistories(t *testing.T) {
	t.Parallel()

	const (
		seeds   = 24
		wallets = 12
		epochs  = 40
		count   = 400
	)

	for seed := range uint64(seeds) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()

			transfers, exclusions := randomHistory(seed, wallets, epochs, count)
			ledger := twab.NewLedger(transfers, exclusions)

			for epoch := range uint64(epochs) {
				want, err := twab.Replay(epoch, transfers, exclusions)
				if err != nil {
					t.Fatalf("seed %d: Replay(%d): %v", seed, epoch, err)
				}

				got, err := ledger.Advance(epoch)
				if err != nil {
					t.Fatalf("seed %d: Advance(%d): %v", seed, epoch, err)
				}

				requireSameHolders(t, epoch, got, want)
			}
		})
	}
}

// An Epoch closes once. Asking for one the Ledger has already passed is a programming
// error — the walk is ascending by construction — and is refused rather than answered
// from state that no longer describes it.
func TestLedger_RefusesToGoBackwards(t *testing.T) {
	t.Parallel()

	ledger := twab.NewLedger([]twab.Transfer{mint(100, alice, 1000)}, nil)

	if _, err := ledger.Advance(3); err != nil {
		t.Fatalf("Advance(3): %v", err)
	}
	if _, err := ledger.Advance(3); err == nil {
		t.Error("Advance(3) a second time succeeded; the Epoch has already closed")
	}
	if _, err := ledger.Advance(2); err == nil {
		t.Error("Advance(2) after Advance(3) succeeded; the walk is ascending")
	}
}

// The same refusal Replay makes, for the same reason: a history that drives a balance
// negative is incomplete, and no later Epoch can be answered from it.
func TestLedger_RejectsUnusableHistory(t *testing.T) {
	t.Parallel()

	ledger := twab.NewLedger([]twab.Transfer{
		mint(100, alice, 400),
		send(5000, alice, bob, 500),
	}, nil)

	if _, err := ledger.Advance(0); err != nil {
		t.Fatalf("Advance(0), before the overspend: %v", err)
	}

	_, err := ledger.Advance(1)
	if err == nil {
		t.Fatal("the Ledger accepted a history it cannot compute from")
	}
	if !strings.Contains(err.Error(), "does not start at the token's creation block") {
		t.Errorf("error = %q, want it to say the history is incomplete", err)
	}
}

// --- helpers ---

// requireSameHolders is byte-identity: the same Holders, in the same order, with the same
// TWAB, FirstBuyAt and LastSellAt.
func requireSameHolders(t *testing.T, epoch uint64, got, want []twab.Holder) {
	t.Helper()

	if g, w := fingerprint(got), fingerprint(want); g != w {
		t.Fatalf("Epoch %d: the Ledger's Holders differ from Replay's:\n got %s\nwant %s", epoch, g, w)
	}
}

func fingerprint(holders []twab.Holder) string {
	var b strings.Builder
	for _, h := range holders {
		fmt.Fprintf(&b, "%#x:%s:%d:%d ", h.Account, h.TWAB, h.FirstBuyAt, h.LastSellAt)
	}

	return "[" + strings.TrimSpace(b.String()) + "]"
}

// randomHistory is count transfers among wallets over epochs Epochs, starting from one
// mint. Every sell is affordable in block order, so the history is complete by
// construction; a burn, a forward and a zero-value transfer each appear in proportion.
// Timestamps cluster so that several transfers share a block, and one lands on every
// Epoch boundary the seed picks.
func randomHistory(seed uint64, wallets, epochs, count int) ([]twab.Transfer, []twab.Exclusion) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))

	addresses := make([]twab.Address, wallets)
	for i := range addresses {
		addresses[i] = repeat(byte(0x10 + i))
	}

	balances := make([]*big.Int, wallets)
	for i := range balances {
		balances[i] = new(big.Int)
	}

	// The mint funds wallet 0 with enough for everything.
	transfers := []twab.Transfer{mint(0, addresses[0], 1_000_000_000)}
	balances[0].SetInt64(1_000_000_000)

	span := int64(epochs) * twab.EpochSeconds
	ts := int64(0)
	for range count {
		// Ascending, occasionally repeating, occasionally on a boundary.
		switch rng.IntN(8) {
		case 0:
			// same block
		case 1:
			ts = (ts/twab.EpochSeconds + 1) * twab.EpochSeconds
		default:
			ts += rng.Int64N(span / int64(count) * 3)
		}
		if ts >= span {
			ts = span - 1
		}

		from := rng.IntN(wallets)
		if balances[from].Sign() == 0 {
			from = 0
		}

		to := addresses[rng.IntN(wallets)]
		if rng.IntN(10) == 0 {
			to = twab.Address{} // burn
		}

		value := int64(0)
		if rng.IntN(10) != 0 {
			value = rng.Int64N(balances[from].Int64()/4 + 1)
		}

		transfers = append(transfers, send(ts, addresses[from], to, value))
		balances[from].Sub(balances[from], big.NewInt(value))
		for i, a := range addresses {
			if a == to {
				balances[i].Add(balances[i], big.NewInt(value))
			}
		}
	}

	var exclusions []twab.Exclusion
	for _, a := range addresses {
		if rng.IntN(6) == 0 {
			exclusions = append(exclusions, twab.Exclusion{Timestamp: rng.Int64N(span), Account: a})
		}
	}

	// Log order is not part of the input; hand the history over shuffled.
	rng.Shuffle(len(transfers), func(i, j int) { transfers[i], transfers[j] = transfers[j], transfers[i] })

	return transfers, exclusions
}

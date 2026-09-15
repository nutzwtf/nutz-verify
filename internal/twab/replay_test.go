package twab_test

import (
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// Every expected TWAB below is worked out from spec §5's formula by hand and written in
// the case, never derived the way the code derives it. Epoch 1 is [3600, 7200), so a
// duration of 3600 seconds is a full Epoch and the divisor cancels.
const (
	epoch1      = 1
	epoch1Start = 3600
	epoch1End   = 7200
)

var (
	funder = repeat(0xcc) // stands in for the Converter: the address buyers get NUTZ from
	alice  = repeat(0xa1)
	bob    = repeat(0xb2)
	carol  = repeat(0xc3)
)

func TestReplay_TWAB(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		transfers  []twab.Transfer
		account    twab.Address
		expected   int64
		firstBuyAt int64
		lastSellAt int64
	}{
		{
			name:       "a balance held for the whole Epoch is its own TWAB",
			transfers:  []twab.Transfer{mint(100, alice, 1000)},
			account:    alice,
			expected:   1000, // 1000 × 3600 / 3600
			firstBuyAt: 100,
		},
		{
			name: "buying at minute 59 earns about a sixtieth",
			transfers: []twab.Transfer{
				mint(100, funder, 1_000_000),
				send(7140, funder, alice, 1000),
			},
			account:    alice,
			expected:   16, // floor(1000 × 60 / 3600) = floor(16.66)
			firstBuyAt: 7140,
		},
		{
			name: "selling at minute 1 keeps about a sixtieth",
			transfers: []twab.Transfer{
				mint(100, alice, 1000),
				send(3660, alice, funder, 1000),
			},
			account:    alice,
			expected:   16, // the same 60 seconds, at the other end of the hour
			firstBuyAt: 100,
			lastSellAt: 3660,
		},
		{
			name: "only the balance changes inside the Epoch carve sub-intervals",
			transfers: []twab.Transfer{
				mint(100, alice, 400),
				mint(200, alice, 600), // still before the window: opening balance is 1000
				send(300, alice, funder, 200),
			},
			account:    alice,
			expected:   800, // 800 held throughout
			firstBuyAt: 100,
			lastSellAt: 300,
		},
		{
			name: "any outgoing transfer is a sell, whatever the destination",
			transfers: []twab.Transfer{
				mint(100, alice, 1000),
				send(5400, alice, bob, 1000), // wallet to wallet, not a sale
			},
			account:    alice,
			expected:   500, // 1000 × 1800 / 3600
			firstBuyAt: 100,
			lastSellAt: 5400,
		},
		{
			name: "a transfer in a later Epoch does not reach back into this one",
			transfers: []twab.Transfer{
				mint(100, alice, 1000),
				send(epoch1End, alice, funder, 1000), // the instant Epoch 1 closes
			},
			account:    alice,
			expected:   1000,
			firstBuyAt: 100,
			lastSellAt: 0, // Epoch 1's streak cannot know about it
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := holderOf(t, replay(t, epoch1, tt.transfers), tt.account)

			if h.TWAB.Cmp(big.NewInt(tt.expected)) != 0 {
				t.Errorf("TWAB = %s, want %d", h.TWAB, tt.expected)
			}
			if h.FirstBuyAt != tt.firstBuyAt {
				t.Errorf("FirstBuyAt = %d, want %d", h.FirstBuyAt, tt.firstBuyAt)
			}
			if h.LastSellAt != tt.lastSellAt {
				t.Errorf("LastSellAt = %d, want %d", h.LastSellAt, tt.lastSellAt)
			}
		})
	}
}

// Spec §5's "sum first, divide once". Alice ladders up to the same integral Bob reaches
// by holding still, so the two must score identically: per-term flooring would dock Alice
// 2 for having traded six times.
//
//	Alice: 600s each at 1000, 2000, 3000, 4000, 5000, 6000 → Σ = 12,600,000 → 3500
//	       per-term: 166+333+500+666+833+1000                             = 3498
//	Bob:   3600s at 3500                                   → Σ = 12,600,000 → 3500
func TestReplay_SumsBeforeDividing(t *testing.T) {
	t.Parallel()

	transfers := []twab.Transfer{
		mint(100, funder, 1_000_000),
		send(epoch1Start, funder, bob, 3500),
	}
	for i := range int64(6) {
		transfers = append(transfers, send(epoch1Start+i*600, funder, alice, 1000))
	}

	holders := replay(t, epoch1, transfers)
	aliceTWAB := holderOf(t, holders, alice).TWAB
	bobTWAB := holderOf(t, holders, bob).TWAB

	if aliceTWAB.Cmp(big.NewInt(3500)) != 0 {
		t.Errorf("Alice's TWAB = %s, want 3500 (3498 means the divide happened per term)", aliceTWAB)
	}
	if bobTWAB.Cmp(big.NewInt(3500)) != 0 {
		t.Errorf("Bob's TWAB = %s, want 3500", bobTWAB)
	}
	if aliceTWAB.Cmp(bobTWAB) != 0 {
		t.Errorf("laddering cost Alice %s against Bob's %s; trading more often must not "+
			"change the TWAB of an identical holding", aliceTWAB, bobTWAB)
	}
}

// Spec §5's other boundary resolution: the final sub-interval runs to 3600(e+1), not to
// the last block. Alice's balance is unchanged all hour while the chain's last block
// lands at 7000, so accruing to that block would give floor(1000 × 3400 / 3600) = 944.
func TestReplay_AccruesToTheEpochBoundaryNotTheLastBlock(t *testing.T) {
	t.Parallel()

	transfers := []twab.Transfer{
		mint(100, alice, 1000),
		mint(200, funder, 1_000_000),
		send(7000, funder, carol, 50), // the Epoch's last block, and none of Alice's business
	}

	got := holderOf(t, replay(t, epoch1, transfers), alice).TWAB
	if got.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("TWAB = %s, want 1000 (944 means the window closed at the last block)", got)
	}
}

// Spec §5: the Excluded set of Epoch e is every ExcludedAppended account whose block
// timestamp is < 3600(e+1), and an entry applies to the whole Epoch containing its block —
// not from its block onward. Every case below holds 1000 for all of Epoch 1, so the
// competing reading would show a visible TWAB rather than none at all.
func TestReplay_Exclusions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		at       int64
		eligible bool
	}{
		{name: "the base list, appended at deploy, covers every later Epoch", at: 0},
		{name: "an entry from an earlier Epoch", at: epoch1Start - 1},
		{name: "an entry landing as the Epoch opens", at: epoch1Start},
		{name: "an entry landing at minute 30 zeroes the whole hour", at: epoch1Start + 1800},
		{name: "an entry in the Epoch's last second", at: epoch1End - 1},
		{name: "an entry in the next Epoch does not apply yet", at: epoch1End, eligible: true},
		{name: "an entry well after the Epoch", at: epoch1End + 86400, eligible: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			holders := replay(t, epoch1,
				[]twab.Transfer{mint(100, alice, 1000)},
				twab.Exclusion{Timestamp: tt.at, Account: alice},
			)

			if tt.eligible {
				if got := holderOf(t, holders, alice).TWAB; got.Cmp(big.NewInt(1000)) != 0 {
					t.Errorf("TWAB = %s, want 1000", got)
				}

				return
			}

			for _, h := range holders {
				if h.Account == alice {
					t.Fatalf("an Excluded address has a TWAB of %s; it must hold nothing for "+
						"every purpose (500 means the entry applied from its block onward)", h.TWAB)
				}
			}
		})
	}
}

// Zeroing an Excluded address must not rewrite the history it took part in: the Converter
// is Excluded and is also where every buyer's NUTZ comes from.
func TestReplay_ExcludedAddressesStillMoveBalances(t *testing.T) {
	t.Parallel()

	holders := replay(t, epoch1,
		[]twab.Transfer{
			mint(100, funder, 1_000_000),
			send(epoch1Start, funder, alice, 1000),
		},
		twab.Exclusion{Timestamp: 0, Account: funder},
	)

	if len(holders) != 1 {
		t.Fatalf("got %d Holders, want only Alice", len(holders))
	}

	alice := holderOf(t, holders, alice)
	if alice.TWAB.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("TWAB = %s, want 1000", alice.TWAB)
	}
	if alice.FirstBuyAt != epoch1Start {
		t.Errorf("FirstBuyAt = %d, want %d: buying from the Converter is still a buy",
			alice.FirstBuyAt, epoch1Start)
	}
}

// The mint and burn counterparty is not an account, and a Holder with no TWAB has no
// weight, so neither belongs in the result.
func TestReplay_OmitsNonHolders(t *testing.T) {
	t.Parallel()

	holders := replay(t, epoch1, []twab.Transfer{
		mint(100, alice, 1000),
		send(200, alice, twab.Address{}, 1000), // burned before the Epoch opened
		mint(300, bob, 1000),
		send(7199, bob, carol, 1000), // Carol holds for one second: 1000 × 1 / 3600 floors away
	})

	for _, h := range holders {
		switch h.Account {
		case twab.Address{}:
			t.Error("the zero address is a Holder")
		case alice:
			t.Errorf("Alice burned everything before the Epoch, yet has a TWAB of %s", h.TWAB)
		case carol:
			t.Errorf("Carol's one second of holding floors to zero, yet she has a TWAB of %s", h.TWAB)
		}
	}
}

// A history that does not reach back to the token's creation block produces balances that
// look plausible and are wrong. Better to refuse than to answer confidently.
func TestReplay_RejectsUnusableHistory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		transfers []twab.Transfer
		wantMsg   string
	}{
		{
			name:      "spending more than the history accounts for",
			transfers: []twab.Transfer{mint(100, alice, 400), send(200, alice, bob, 500)},
			wantMsg:   "does not start at the token's creation block",
		},
		{
			name:      "a nil value",
			transfers: []twab.Transfer{{Timestamp: 100, To: alice}},
			wantMsg:   "is nil, which is not the same as zero",
		},
		{
			name:      "a negative value",
			transfers: []twab.Transfer{{Timestamp: 100, To: alice, Value: big.NewInt(-1)}},
			wantMsg:   "is negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := twab.Replay(epoch1, tt.transfers, nil)
			if err == nil {
				t.Fatal("Replay accepted a history it cannot compute from")
			}

			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

// A buy in the very first block must set the streak's start like any other. Tracking
// firstBuyAt by a zero sentinel would discard it and hand Alice the shorter streak of her
// second buy — the kind of wrong answer that never announces itself.
func TestReplay_RecordsAFirstBuyAtTimestampZero(t *testing.T) {
	t.Parallel()

	holders := replay(t, epoch1, []twab.Transfer{
		mint(0, alice, 1000),
		mint(3000, alice, 1000),
	})

	if got := holderOf(t, holders, alice).FirstBuyAt; got != 0 {
		t.Errorf("FirstBuyAt = %d, want 0: the first buy is the one at timestamp 0", got)
	}
}

// A wallet that receives and forwards inside one block passes through a negative balance in
// one of the two orders and not the other. Log order within a block is not part of the
// input — the Cases README promises as much to the private indexer — so neither order may
// change the answer, and neither may be mistaken for a history that is missing logs.
func TestReplay_IsIndifferentToOrderWithinABlock(t *testing.T) {
	t.Parallel()

	relay := repeat(0x9e)

	// The Converter funds a relay, which forwards the lot to Alice in the same block.
	transfers := []twab.Transfer{
		mint(100, funder, 1_000_000),
		send(epoch1Start, funder, relay, 1000),
		send(epoch1Start, relay, alice, 1000),
	}

	forward := replay(t, epoch1, transfers)

	backward := slices.Clone(transfers)
	slices.Reverse(backward)
	reversed := replay(t, epoch1, backward)

	if len(forward) != len(reversed) {
		t.Fatalf("reversing the logs changed the Holder count: %d against %d",
			len(reversed), len(forward))
	}

	for i := range forward {
		if forward[i].Account != reversed[i].Account ||
			forward[i].TWAB.Cmp(reversed[i].TWAB) != 0 {
			t.Errorf("Holder %d differs once the logs are reversed: %#x/%s against %#x/%s",
				i, reversed[i].Account, reversed[i].TWAB, forward[i].Account, forward[i].TWAB)
		}
	}

	// The relay held nothing for any length of time, so it is not a Holder at all.
	if got := holderOf(t, forward, alice).TWAB; got.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Alice's TWAB = %s, want 1000", got)
	}
	for _, h := range forward {
		if h.Account == relay {
			t.Errorf("the relay has a TWAB of %s despite never holding across an interval", h.TWAB)
		}
	}
}

// --- helpers ---

// repeat fills an address with one byte, so Cases read as 0xa1a1…a1.
func repeat(b byte) twab.Address {
	var a twab.Address
	for i := range a {
		a[i] = b
	}

	return a
}

func mint(ts int64, to twab.Address, value int64) twab.Transfer {
	return send(ts, twab.Address{}, to, value)
}

func send(ts int64, from, to twab.Address, value int64) twab.Transfer {
	return twab.Transfer{Timestamp: ts, From: from, To: to, Value: big.NewInt(value)}
}

func replay(t *testing.T, epochID uint64, transfers []twab.Transfer, exclusions ...twab.Exclusion) []twab.Holder {
	t.Helper()

	holders, err := twab.Replay(epochID, transfers, exclusions)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	return holders
}

func holderOf(t *testing.T, holders []twab.Holder, account twab.Address) twab.Holder {
	t.Helper()

	for _, h := range holders {
		if h.Account == account {
			return h
		}
	}

	t.Fatalf("no Holder for %#x among %d Holders", account, len(holders))

	return twab.Holder{}
}

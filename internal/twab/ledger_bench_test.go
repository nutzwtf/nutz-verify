package twab_test

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// BenchmarkChain is ticket 07's measurement: the cost of --chain's walk over E Epochs of an
// N-transfer history among H wallets, as the Ledger does it (once over N) and as Replay
// per Epoch did it (E times over a history growing to N).
//
// The shapes are the ticket's table, the largest its extrapolated ≈ 5.3-hour case, plus
// one day at the density ticket 04's load test measured for USDG on chain 4663 — 7 logs a
// block, ~19,000 blocks an hour, so ~133,000 transfers an Epoch (spec §9). That is the real
// N per Epoch; the load test's own Cache spans 5,000 blocks, less than one Epoch, so a
// walk over it measures nothing.
//
// replay-one-epoch is Replay of the last Epoch alone — the walk's cost over Replay is that
// times E, give or take a half, and running it E times at the larger shapes is the point of
// the ticket. replay-per-epoch runs the actual walk where it is affordable.
//
//	go test ./internal/twab/ -run '^$' -bench Chain -benchtime 1x
func BenchmarkChain(b *testing.B) {
	shapes := []struct {
		transfers, holders, epochs int
		walkReplay                 bool
	}{
		{transfers: 100_000, holders: 10_000, epochs: 100, walkReplay: true},
		{transfers: 1_000_000, holders: 100_000, epochs: 1_000},
		{transfers: 10_000_000, holders: 100_000, epochs: 10_000},
		{transfers: 24 * 133_000, holders: 100_000, epochs: 24, walkReplay: true},
	}

	for _, shape := range shapes {
		name := fmt.Sprintf("transfers=%d,holders=%d,epochs=%d", shape.transfers, shape.holders, shape.epochs)
		b.Run(name, func(b *testing.B) {
			history := syntheticHistory(shape.holders, shape.transfers, shape.epochs)
			last := uint64(shape.epochs - 1)

			b.Run("ledger", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					ledger := twab.NewLedger(history, nil)
					for epoch := range uint64(shape.epochs) {
						if _, err := ledger.Advance(epoch); err != nil {
							b.Fatal(err)
						}
					}
				}
			})

			b.Run("replay-one-epoch", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if _, err := twab.Replay(last, history, nil); err != nil {
						b.Fatal(err)
					}
				}
			})

			if !shape.walkReplay {
				return
			}

			b.Run("replay-per-epoch", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					for epoch := range uint64(shape.epochs) {
						if _, err := twab.Replay(epoch, history, nil); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		})
	}
}

// syntheticHistory is count transfers among holders wallets spread evenly over epochs
// Epochs, in block order, every one affordable: wallet 0 is minted enough for everything
// and funds the rest, so the population of Holders grows to all of them within the first
// Epochs and then trades among itself. Cheap to generate at ten million.
func syntheticHistory(holders, count, epochs int) []twab.Transfer {
	rng := rand.New(rand.NewPCG(7, 11))

	addresses := make([]twab.Address, holders)
	for i := range addresses {
		addresses[i] = twab.Address{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i), 0xee}
	}

	balances := make([]int64, holders)
	balances[0] = 1 << 62

	transfers := make([]twab.Transfer, 0, count+1)
	transfers = append(transfers, twab.Transfer{To: addresses[0], Value: big.NewInt(balances[0])})

	span := int64(epochs) * twab.EpochSeconds
	for i := range count {
		ts := span * int64(i) / int64(count)

		from := rng.IntN(holders)
		if balances[from] == 0 {
			from = 0
		}
		to := rng.IntN(holders)
		value := rng.Int64N(balances[from]/8 + 1)

		transfers = append(transfers, twab.Transfer{
			Timestamp: ts, From: addresses[from], To: addresses[to], Value: big.NewInt(value),
		})
		balances[from] -= value
		balances[to] += value
	}

	return transfers
}

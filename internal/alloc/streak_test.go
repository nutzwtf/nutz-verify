package alloc_test

import (
	"fmt"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/alloc"
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

const day = 86400

// Spec §5: streakDays(a,e) = floor((3600(e+1) − max(lastSellAt, firstBuyAt)) / 86400).
// The offsets are measured back from the Epoch's closing instant, which is the same
// boundary the TWAB window closes at — not the timestamp of the Epoch's last block.
func TestStreakDays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		before   int64 // seconds before the Epoch closes
		expected int64
	}{
		{name: "bought in the closing seconds", before: 0, expected: 0},
		{name: "a second short of a day", before: day - 1, expected: 0},
		{name: "exactly one day", before: day, expected: 1},
		{name: "a second short of the week threshold", before: 7*day - 1, expected: 6},
		{name: "exactly seven days", before: 7 * day, expected: 7},
		{name: "a second short of the month threshold", before: 30*day - 1, expected: 29},
		{name: "exactly thirty days", before: 30 * day, expected: 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held := twab.Holder{FirstBuyAt: window.End - tt.before}
			if got := alloc.StreakDays(window, held); got != tt.expected {
				t.Errorf("StreakDays(%d seconds before the close) = %d, want %d",
					tt.before, got, tt.expected)
			}
		})
	}
}

// The streak runs from whichever came later. Any outgoing transfer resets it, so a
// long-standing Holder who moved coins yesterday is back to 1.00x.
func TestStreakDays_TakesTheLaterOfBuyAndSell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		firstBuyAt, lastSellAt int64
		expected               int64
	}{
		{
			name:       "never sold, so the buy sets the streak",
			firstBuyAt: window.End - 40*day,
			lastSellAt: 0,
			expected:   40,
		},
		{
			name:       "a recent sell overrides an old buy",
			firstBuyAt: window.End - 40*day,
			lastSellAt: window.End - 2*day,
			expected:   2,
		},
		{
			name:       "a sell older than the buy cannot lengthen the streak",
			firstBuyAt: window.End - 3*day,
			lastSellAt: window.End - 40*day,
			expected:   3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held := twab.Holder{FirstBuyAt: tt.firstBuyAt, LastSellAt: tt.lastSellAt}
			if got := alloc.StreakDays(window, held); got != tt.expected {
				t.Errorf("StreakDays = %d, want %d", got, tt.expected)
			}
		})
	}
}

// Engineering spec §4.3's three steps, in basis points: < 7 → 10000, 7 ≤ d < 30 → 12500,
// ≥ 30 → 15000.
func TestMultBps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		streakDays int64
		expected   int64
	}{
		{streakDays: 0, expected: alloc.MultBpsBase},
		{streakDays: 6, expected: alloc.MultBpsBase},
		{streakDays: 7, expected: alloc.MultBpsWeek},
		{streakDays: 29, expected: alloc.MultBpsWeek},
		{streakDays: 30, expected: alloc.MultBpsMonth},
		{streakDays: 3650, expected: alloc.MultBpsMonth},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d days", tt.streakDays), func(t *testing.T) {
			t.Parallel()

			if got := alloc.MultBps(tt.streakDays); got != tt.expected {
				t.Errorf("MultBps(%d) = %d, want %d", tt.streakDays, got, tt.expected)
			}
		})
	}
}

// The bps values are the constants engineering spec §4.3 fixes, not whatever the code says.
func TestMultBpsValues(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		got      int64
		expected int64
	}{
		{name: "1.00x", got: alloc.MultBpsBase, expected: 10000},
		{name: "1.25x", got: alloc.MultBpsWeek, expected: 12500},
		{name: "1.50x", got: alloc.MultBpsMonth, expected: 15000},
	} {
		if tt.got != tt.expected {
			t.Errorf("%s = %d bps, want %d", tt.name, tt.got, tt.expected)
		}
	}
}

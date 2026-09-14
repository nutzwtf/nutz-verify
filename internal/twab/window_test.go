package twab_test

import (
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// Spec §5: Epoch e is [3600e, 3600(e+1)). The bounds below are worked out from that
// formula, not read back from the code.
func TestWindowOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		epochID    uint64
		start, end int64
	}{
		{name: "the first Epoch starts at the unix epoch", epochID: 0, start: 0, end: 3600},
		{name: "the second", epochID: 1, start: 3600, end: 7200},
		{name: "an Epoch around launch", epochID: 494000, start: 1778400000, end: 1778403600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := twab.WindowOf(tt.epochID)
			if w.Start != tt.start || w.End != tt.end {
				t.Errorf("WindowOf(%d) = [%d, %d), want [%d, %d)",
					tt.epochID, w.Start, w.End, tt.start, tt.end)
			}

			if got := w.End - w.Start; got != twab.EpochSeconds {
				t.Errorf("window spans %d seconds, want %d", got, twab.EpochSeconds)
			}
		})
	}
}

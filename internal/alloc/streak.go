package alloc

import "github.com/nutzwtf/nutz-verify/internal/twab"

// secondsPerDay is the streak's unit (engineering spec §4.3).
const secondsPerDay = 86400

// The Multiplier's two thresholds, in whole days of streak.
const (
	StreakWeek  = 7
	StreakMonth = 30
)

// The Multiplier itself, in basis points. Carried as bps rather than a ratio because a
// weight is TWAB × multBps undivided: the 10000 cancels against W (spec §5).
const (
	MultBpsBase  = 10000 // 1.00x, and DEV_WALLET always
	MultBpsWeek  = 12500 // 1.25x, for 7 ≤ streakDays < 30
	MultBpsMonth = 15000 // 1.50x, for streakDays ≥ 30
)

// StreakDays is how many whole days the Holder has held without sending, measured back
// from the instant the Epoch closes:
//
//	streakDays(a,e) = floor((3600(e+1) − max(lastSellAt(a), firstBuyAt(a))) / 86400)
//
// Spec §5 puts the measurement at the same boundary the TWAB window closes at rather than
// at the Epoch's last block, so a Holder's streak does not depend on when the chain
// happened to produce its final block of the hour.
func StreakDays(window twab.Window, holder twab.Holder) int64 {
	since := holder.FirstBuyAt
	if holder.LastSellAt > since {
		// Any outgoing transfer resets the streak, whatever the destination: sells,
		// wallet-to-wallet moves and burns alike (engineering spec §4.3).
		since = holder.LastSellAt
	}

	return (window.End - since) / secondsPerDay
}

// MultBps is the Multiplier in basis points for a streak length.
func MultBps(streakDays int64) int64 {
	switch {
	case streakDays >= StreakMonth:
		return MultBpsMonth
	case streakDays >= StreakWeek:
		return MultBpsWeek
	default:
		return MultBpsBase
	}
}

package accrual

import (
	"context"
	"log"
	"time"
)

// Playtime in Rewards is a cache of the account core, never a second measurement: play_cursor.total_seconds
// is overwritten with the core's per-title total (GetPlayHistory) on every tick, and the activity rows are
// the clamped deltas of that total. Check proves it by comparing the cache with the core, title by title.

// Cursors is the cached side of the comparison.
type Cursors interface {
	PlayCursorTotals(ctx context.Context, acct, ns string) (map[string]int64, error)
}

// Consistency is the result of one comparison. Only counts, never ids.
type Consistency struct {
	Accounts  int   // accounts looked at
	Titles    int   // core history titles compared
	Missing   int   // core titles with no cursor yet (new since the last tick, or beyond the core's 20-title cap)
	Differing int   // cursor total differs from the core total
	MaxDiff   int64 // largest absolute difference in seconds over differing titles
}

// Check compares every account's cached totals with the core's history. Differences while a session is
// running are normal and vanish on the next tick; a persistent count means the cache has drifted.
func Check(ctx context.Context, src Source, cur Cursors, namespaces []string) (Consistency, error) {
	var c Consistency
	after := ""
	for {
		ids, next, err := src.Accounts(ctx, after, 200)
		if err != nil {
			return c, err
		}
		for _, id := range ids {
			c.Accounts++
			for _, ns := range namespaces {
				hist, err := src.History(ctx, id, ns)
				if err != nil {
					continue
				}
				if len(hist) == 0 {
					continue
				}
				have, err := cur.PlayCursorTotals(ctx, id, ns)
				if err != nil {
					return c, err
				}
				for _, h := range hist {
					c.Titles++
					v, ok := have[h.ID]
					if !ok {
						c.Missing++
						continue
					}
					if d := abs(v - h.Seconds); d != 0 {
						c.Differing++
						if d > c.MaxDiff {
							c.MaxDiff = d
						}
					}
				}
			}
		}
		if next == "" {
			return c, nil
		}
		after = next
	}
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// RunCheck compares once at boot and then every interval, logging the counts.
func RunCheck(ctx context.Context, src Source, cur Cursors, namespaces []string, first, every time.Duration) {
	wait := first
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		c, err := Check(ctx, src, cur, namespaces)
		log.Printf("rewards: playtime consistency: accounts=%d titles=%d missing=%d differing=%d max_diff_seconds=%d err=%v",
			c.Accounts, c.Titles, c.Missing, c.Differing, c.MaxDiff, err)
		wait = every
	}
}

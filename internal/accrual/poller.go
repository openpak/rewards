// Package accrual polls the account core for every account's server-side play history and
// hands it to the store, which applies the point rules (internal/rules).
package accrual

import (
	"context"
	"log"
	"time"

	"github.com/openpak/rewards/internal/core"
	"github.com/openpak/rewards/internal/store"
)

// Source is the account core as the poller needs it.
type Source interface {
	Accounts(ctx context.Context, after string, limit int) ([]string, string, error)
	History(ctx context.Context, account, namespace string) ([]core.Title, error)
}

type Poller struct {
	Src        Source
	Store      *store.Store
	Namespaces []string
	Interval   time.Duration
	Now        func() time.Time
}

// Run ticks until ctx ends. Accounts are processed one at a time on purpose: friend
// settlement locks a second account row and relies on there being one accruer.
func (p *Poller) Run(ctx context.Context) {
	for {
		start := time.Now()
		n, pts, err := p.Tick(ctx)
		if err != nil {
			log.Printf("rewards: accrual tick: %v", err)
		} else {
			log.Printf("rewards: accrual tick: %d account(s), %d point(s) credited, %s", n, pts, time.Since(start).Round(time.Millisecond))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.Interval):
		}
	}
}

// Tick walks the directory once. It returns how many accounts it looked at and the points credited.
func (p *Poller) Tick(ctx context.Context) (accounts, points int, err error) {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	after := ""
	for {
		ids, next, err := p.Src.Accounts(ctx, after, 200)
		if err != nil {
			return accounts, points, err
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return accounts, points, ctx.Err()
			}
			accounts++
			for _, ns := range p.Namespaces {
				hist, err := p.Src.History(ctx, id, ns)
				if err != nil {
					log.Printf("rewards: history %s/%s: %v", short(id), ns, err)
					continue
				}
				entries := make([]store.TitleTotal, 0, len(hist))
				for _, h := range hist {
					entries = append(entries, store.TitleTotal{Title: h.ID, Seconds: h.Seconds, Sessions: h.Sessions})
				}
				res, err := p.Store.Accrue(ctx, id, ns, entries, now())
				if err != nil {
					log.Printf("rewards: accrue %s/%s: %v", short(id), ns, err)
					continue
				}
				points += res.Points
			}
		}
		if next == "" {
			return accounts, points, nil
		}
		after = next
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

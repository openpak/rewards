package core

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/openpak/rewards/internal/store"
)

// Announcer is the durable list of game-cosmetic changes waiting to be told to the core.
type Announcer interface {
	PendingAnnouncements(ctx context.Context, limit int) ([]store.Announcement, error)
	AnnouncementDone(ctx context.Context, a store.Announcement) error
}

// Announce drains the outbox onto the account core's event stream until ctx ends. A change is removed only after
// the core accepted it, so a core outage delays the announcement and loses nothing; adapters repair a late event
// with their cache time-out. A change the core refuses for good (unknown account) is dropped.
func (c *Client) Announce(ctx context.Context, st Announcer, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		batch, err := st.PendingAnnouncements(ctx, 100)
		if err != nil {
			log.Printf("rewards: announce: read outbox: %v", err)
			continue
		}
		sent := 0
		for _, a := range batch {
			err := c.EntitlementChanged(ctx, a.Account, a.Game)
			if err != nil {
				if code := status.Code(err); code != codes.InvalidArgument && code != codes.NotFound && code != codes.FailedPrecondition {
					log.Printf("rewards: announce: core unavailable: %v", err)
					break
				}
				log.Printf("rewards: announce: dropped a change the core refused: %v", status.Code(err))
			}
			if err := st.AnnouncementDone(ctx, a); err != nil {
				log.Printf("rewards: announce: %v", err)
				break
			}
			sent++
		}
		if sent > 0 {
			log.Printf("rewards: announced %d entitlement change(s)", sent)
		}
	}
}

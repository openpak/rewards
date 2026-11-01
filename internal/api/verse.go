package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Verse tells OpenVerse (the activity feed) about moments worth a post, so a player who opted in
// there sees "<name> earned the <badge> badge". It is fire and forget: a failure is logged and
// never fails the redemption, claim or grant that caused it. OpenVerse answers "not_opted_in"
// for everyone who has not switched activity posts on, which is most people. See OpenVerse
// docs/activity.md for the endpoint.
type Verse struct {
	URL       string // e.g. http://openpak-nn-juxtaposition-api:20090
	Key       string // OpenVerse PN_MIIVERSE_API_ACTIVITY_KEY
	PublicURL string // where badge art is served from; default https://rewards.openpak.org
	HTTP      *http.Client
	Backoff   []time.Duration // delays between attempts; default 1s, 5s, 30s
}

type verseEvent struct {
	EventID  string `json:"event_id"`
	Account  string `json:"account_id"`
	Category string `json:"category"`
	Subject  string `json:"subject"`
	Link     string `json:"link,omitempty"`
	Image    string `json:"image,omitempty"`
}

func (v *Verse) post(ctx context.Context, ev verseEvent) error {
	body, _ := json.Marshal(ev)
	c := v.HTTP
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	delays := v.Backoff
	if delays == nil {
		delays = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}
	}
	var last error
	for attempt := 0; attempt <= len(delays); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delays[attempt-1]):
			}
		}
		req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(v.URL, "/")+"/internal/v1/activity", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Key", v.Key)
		resp, err := c.Do(req)
		if err != nil {
			last = err
			continue
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch {
		case resp.StatusCode < 300:
			return nil
		case resp.StatusCode >= 500:
			last = fmt.Errorf("status %d", resp.StatusCode)
		default:
			return fmt.Errorf("status %d", resp.StatusCode) // our request is wrong: retrying will not help
		}
	}
	return last
}

// badgeEarned reports a badge an account just received. why makes the event id stable for this
// one grant (a redemption id, a mission period, ...), so a retry or a replay cannot post twice.
func (s *Server) badgeEarned(acct, itemID, why string) {
	v := s.Verse
	if v == nil || v.URL == "" || v.Key == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		it, err := s.Store.GetItem(ctx, itemID)
		if err != nil || it.Kind != "badge" {
			return
		}
		ev := verseEvent{EventID: "badge:" + acct + ":" + itemID + ":" + why, Account: acct, Category: "badge", Subject: it.Name, Link: "https://openpak.org/account/rewards"}
		if it.HasPreview && it.State == "active" {
			pub := v.PublicURL
			if pub == "" {
				pub = "https://rewards.openpak.org"
			}
			ev.Image = strings.TrimRight(pub, "/") + "/v1/items/" + itemID + "/preview"
		}
		if err := v.post(ctx, ev); err != nil {
			log.Printf("rewards: activity post for %s: %v", itemID, err)
		}
	}()
}

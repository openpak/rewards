package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Metric is a count game servers may feed: a sum of deltas, or the number of distinct keys.
type Metric struct {
	ID            string `json:"id"`
	Family        string `json:"family,omitempty"`
	Title         string `json:"title"`
	Mode          string `json:"mode"`
	DailyCap      int64  `json:"daily_cap"`
	MaxDelta      int    `json:"max_delta"`
	RequireFriend bool   `json:"require_friend"`
	Enabled       bool   `json:"enabled"`
}

func (s *Store) Metrics(ctx context.Context) ([]Metric, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, family, title, mode, daily_cap, max_delta, require_friend, enabled FROM metrics ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Metric{}
	for rows.Next() {
		var m Metric
		if err := rows.Scan(&m.ID, &m.Family, &m.Title, &m.Mode, &m.DailyCap, &m.MaxDelta, &m.RequireFriend, &m.Enabled); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMetric changes the cap, delta limit and switch of a metric (admin).
func (s *Store) SetMetric(ctx context.Context, actor, id string, cap int64, maxDelta int, enabled bool) error {
	if cap <= 0 || maxDelta <= 0 || maxDelta > 1000 {
		return invalid("daily_cap and max_delta must be positive (max_delta at most 1000)")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE metrics SET daily_cap=$2, max_delta=$3, enabled=$4 WHERE id=$1`, id, cap, maxDelta, enabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, actor, "metric.set", id, fmt.Sprintf(`{"daily_cap":%d,"max_delta":%d,"enabled":%t}`, cap, maxDelta, enabled))
	})
}

// MyMetrics is one account's counts per metric.
func (s *Store) MyMetrics(ctx context.Context, acct string) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT metric, total FROM metric_counts WHERE account_id=$1`, acct)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Event is one server-to-server observation. EventID is the dedupe key and must be stable for
// the same real-world occurrence (a retry resends the same id).
type Event struct {
	EventID    string
	Source     string
	AccountID  string
	Metric     string
	Delta      int
	Keys       []string
	OccurredAt time.Time
}

// EventResult statuses.
const (
	EvCounted   = "counted"
	EvDuplicate = "duplicate"
	EvHeld      = "held"
	EvIgnored   = "ignored"
)

const maxKeys = 16
const eventMaxAge = 7 * 24 * time.Hour

// Ingest records one event once. friends is the account's friend set, needed only for
// metrics that require a friend; pass nil otherwise.
func (s *Store) Ingest(ctx context.Context, ev Event, friends map[string]bool) (string, error) {
	now := s.Now()
	if ev.EventID == "" || len(ev.EventID) > 200 || ev.Source == "" || len(ev.Source) > 64 {
		return "", invalid("event_id and source are required")
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now
	}
	if ev.OccurredAt.After(now.Add(5*time.Minute)) || ev.OccurredAt.Before(now.Add(-eventMaxAge)) {
		return "", invalid("occurred_at is outside the accepted window")
	}
	if len(ev.Keys) > maxKeys {
		return "", invalid("at most %d keys per event", maxKeys)
	}
	if ev.Keys == nil {
		ev.Keys = []string{}
	}
	var status string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var m Metric
		if err := tx.QueryRow(ctx, `SELECT id, family, title, mode, daily_cap, max_delta, require_friend, enabled FROM metrics WHERE id=$1`, ev.Metric).
			Scan(&m.ID, &m.Family, &m.Title, &m.Mode, &m.DailyCap, &m.MaxDelta, &m.RequireFriend, &m.Enabled); err == pgx.ErrNoRows {
			return invalid("unknown metric")
		} else if err != nil {
			return err
		}
		if !m.Enabled {
			return invalid("metric is switched off")
		}
		if m.Mode == "sum" {
			if ev.Delta < 1 || ev.Delta > m.MaxDelta {
				return invalid("delta must be 1..%d for this metric", m.MaxDelta)
			}
		} else {
			if len(ev.Keys) == 0 {
				return invalid("this metric counts distinct keys; send keys")
			}
			ev.Delta = len(ev.Keys)
		}
		a, err := lock(ctx, tx, ev.AccountID)
		if err != nil {
			return err
		}
		var dup bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE event_id=$1)`, ev.EventID).Scan(&dup); err != nil {
			return err
		}
		if dup {
			status = EvDuplicate
			return nil
		}
		if m.RequireFriend {
			var ok []string
			for _, k := range ev.Keys {
				if friends[strings.ToLower(k)] {
					ok = append(ok, strings.ToLower(k))
				}
			}
			if ok == nil {
				ok = []string{}
			}
			ev.Keys = ok
			ev.Delta = len(ok)
		}
		ins := func(state string) error {
			_, err := tx.Exec(ctx, `INSERT INTO events (event_id, source, account_id, metric, delta, keys, state, occurred_at, received_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				ev.EventID, ev.Source, ev.AccountID, ev.Metric, ev.Delta, ev.Keys, state, ev.OccurredAt, now)
			return err
		}
		if a.Frozen || a.Paused {
			status = EvIgnored
			return ins("ignored")
		}
		if m.Mode == "distinct" && ev.Delta == 0 {
			status = EvCounted
			return ins("counted")
		}
		day := now.UTC().Format("2006-01-02")
		var today int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT counted FROM metric_daily WHERE account_id=$1 AND metric=$2 AND day=$3),0)`, ev.AccountID, ev.Metric, day).Scan(&today); err != nil {
			return err
		}
		if today >= m.DailyCap {
			status = EvHeld
			if err := ins("held"); err != nil {
				return err
			}
			var held int64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(delta),0) FROM events WHERE account_id=$1 AND metric=$2 AND state='held'`, ev.AccountID, ev.Metric).Scan(&held); err != nil {
				return err
			}
			d, _ := json.Marshal(map[string]any{"metric": ev.Metric, "day": day, "daily_cap": m.DailyCap, "counted_today": today, "held": held})
			_, err := tx.Exec(ctx, `INSERT INTO flags (account_id, reason, detail) VALUES ($1,$2,$3::jsonb) ON CONFLICT (account_id, reason) WHERE resolved_at IS NULL DO UPDATE SET detail = EXCLUDED.detail`,
				ev.AccountID, "metric-cap:"+ev.Metric, string(d))
			return err
		}
		if err := ins("counted"); err != nil {
			return err
		}
		status = EvCounted
		if err := s.applyCount(ctx, tx, ev.AccountID, m, ev.Delta, ev.Keys, day); err != nil {
			return err
		}
		return s.refreshMissions(ctx, tx, ev.AccountID, now)
	})
	return status, err
}

// applyCount adds a counted event to the totals. For a distinct metric only keys never seen
// before count, so a replay under a new event id cannot inflate it.
func (s *Store) applyCount(ctx context.Context, tx pgx.Tx, acct string, m Metric, delta int, keys []string, day string) error {
	n := int64(delta)
	if m.Mode == "distinct" {
		n = 0
		for _, k := range keys {
			tag, err := tx.Exec(ctx, `INSERT INTO metric_keys (account_id, metric, key) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, acct, m.ID, k)
			if err != nil {
				return err
			}
			n += tag.RowsAffected()
		}
	}
	if n == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO metric_counts (account_id, metric, total) VALUES ($1,$2,$3)
		ON CONFLICT (account_id, metric) DO UPDATE SET total = metric_counts.total + $3, updated_at = now()`, acct, m.ID, n); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO metric_daily (account_id, metric, day, counted) VALUES ($1,$2,$3,$4)
		ON CONFLICT (account_id, metric, day) DO UPDATE SET counted = metric_daily.counted + $4`, acct, m.ID, day, n)
	return err
}

// ReleaseHeld counts the events a cap held back after an admin reviewed them (audited) and
// closes the open flag.
func (s *Store) ReleaseHeld(ctx context.Context, actor, acct, metric string) (int, error) {
	released := 0
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var m Metric
		if err := tx.QueryRow(ctx, `SELECT id, family, title, mode, daily_cap, max_delta, require_friend, enabled FROM metrics WHERE id=$1`, metric).
			Scan(&m.ID, &m.Family, &m.Title, &m.Mode, &m.DailyCap, &m.MaxDelta, &m.RequireFriend, &m.Enabled); err == pgx.ErrNoRows {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if _, err := lock(ctx, tx, acct); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT event_id, delta, keys FROM events WHERE account_id=$1 AND metric=$2 AND state='held' ORDER BY received_at FOR UPDATE`, acct, metric)
		if err != nil {
			return err
		}
		type h struct {
			id    string
			delta int
			keys  []string
		}
		var hs []h
		for rows.Next() {
			var x h
			if err := rows.Scan(&x.id, &x.delta, &x.keys); err != nil {
				rows.Close()
				return err
			}
			hs = append(hs, x)
		}
		rows.Close()
		now := s.Now()
		for _, x := range hs {
			if err := s.applyCount(ctx, tx, acct, m, x.delta, x.keys, now.UTC().Format("2006-01-02")); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE events SET state='counted' WHERE event_id=$1`, x.id); err != nil {
				return err
			}
			released++
		}
		if _, err := tx.Exec(ctx, `UPDATE flags SET resolved_by=$3, resolved_at=now() WHERE account_id=$1 AND reason=$2 AND resolved_at IS NULL`, acct, "metric-cap:"+metric, actor); err != nil {
			return err
		}
		if err := audit(ctx, tx, actor, "metric.release", acct, fmt.Sprintf(`{"metric":%q,"events":%d}`, metric, released)); err != nil {
			return err
		}
		return s.refreshMissions(ctx, tx, acct, now)
	})
	return released, err
}

// DiscardHeld drops the held events (the admin decided they were abuse), closes the flag.
func (s *Store) DiscardHeld(ctx context.Context, actor, acct, metric string) (int64, error) {
	var n int64
	err := s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE events SET state='ignored' WHERE account_id=$1 AND metric=$2 AND state='held'`, acct, metric)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		if _, err := tx.Exec(ctx, `UPDATE flags SET resolved_by=$3, resolved_at=now() WHERE account_id=$1 AND reason=$2 AND resolved_at IS NULL`, acct, "metric-cap:"+metric, actor); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "metric.discard", acct, fmt.Sprintf(`{"metric":%q,"events":%d}`, metric, n))
	})
	return n, err
}

// eventValue is a mission's progress for an event metric, a metric family (the sum of its
// members) or "family_games:<family>" (how many members are at least 1).
func eventValue(ctx context.Context, d q, acct, metric string) (int64, bool, error) {
	if fam, ok := strings.CutPrefix(metric, "family_games:"); ok {
		var n int64
		err := d.QueryRow(ctx, `SELECT COUNT(*) FROM metric_counts c JOIN metrics x ON x.id=c.metric WHERE c.account_id=$1 AND x.family=$2 AND c.total >= 1`, acct, fam).Scan(&n)
		return n, true, err
	}
	var known bool
	if err := d.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM metrics WHERE id=$1 OR family=$1)`, metric).Scan(&known); err != nil || !known {
		return 0, false, err
	}
	var n int64
	err := d.QueryRow(ctx, `SELECT COALESCE(SUM(c.total),0) FROM metric_counts c JOIN metrics x ON x.id=c.metric WHERE c.account_id=$1 AND (x.id=$2 OR x.family=$2)`, acct, metric).Scan(&n)
	return n, true, err
}

// validMetric reports whether a mission may use this metric name.
func validMetric(ctx context.Context, d q, metric string) bool {
	switch metric {
	case "play_seconds", "play_days", "titles_played", "networks_played", "friends_added", "sessions", "lifetime_play_seconds":
		return true
	}
	if fam, ok := strings.CutPrefix(metric, "family_games:"); ok {
		metric = fam
	}
	var ok bool
	_ = d.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM metrics WHERE id=$1 OR family=$1)`, metric).Scan(&ok)
	return ok
}

// PurgeEvents drops event rows past their dedupe window.
func (s *Store) purgeEvents(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM events WHERE received_at < now() - interval '60 days' AND state <> 'held'`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM metric_daily WHERE day < current_date - 90`)
	return err
}

// GrantResult is one account's outcome in a bulk grant.
type GrantResult struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"` // granted, already_owned
}

// GrantBulk gives an event or supporter item to many accounts (admin), once each. kind is
// "event" or "supporter" and is only recorded in the audit trail.
func (s *Store) GrantBulk(ctx context.Context, actor, kind, itemID, reason string, accts []string) ([]GrantResult, error) {
	if kind != "event" && kind != "supporter" {
		return nil, invalid("kind must be event or supporter")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, invalid("a reason is required")
	}
	if len(accts) == 0 || len(accts) > 500 {
		return nil, invalid("1 to 500 accounts per call")
	}
	var out []GrantResult
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM items WHERE id=$1`, itemID).Scan(&n); err != nil || n == 0 {
			return invalid("item does not exist")
		}
		for _, a := range accts {
			if _, err := lock(ctx, tx, a); err != nil {
				return err
			}
			ok, err := s.grant(ctx, tx, a, itemID, "admin", fmt.Sprintf("%s:%s:%s", kind, itemID, a))
			if err != nil {
				return err
			}
			r := GrantResult{AccountID: a, Status: "already_owned"}
			if ok {
				r.Status = "granted"
				d, _ := json.Marshal(map[string]string{"item": itemID, "reason": reason})
				if err := audit(ctx, tx, actor, "grant."+kind, a, string(d)); err != nil {
					return err
				}
			}
			out = append(out, r)
		}
		return nil
	})
	return out, err
}

// Grants lists who received an item through the grant API, newest first.
func (s *Store) Grants(ctx context.Context, itemID string, limit int) ([]AuditRow, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id, at, actor, action, target, detail FROM audit WHERE action IN ('grant.event','grant.supporter') AND ($1='' OR detail->>'item'=$1) ORDER BY id DESC LIMIT $2`, itemID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.ID, &r.At, &r.Actor, &r.Action, &r.Target, &r.Detail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

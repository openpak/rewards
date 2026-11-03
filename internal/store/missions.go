package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openpak/rewards/internal/rules"
)

type Mission struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Kind         string     `json:"kind"`
	Metric       string     `json:"metric"`
	Target       int64      `json:"target"`
	Network      string     `json:"network,omitempty"`
	TitleID      string     `json:"title_id,omitempty"`
	Requires     *string    `json:"requires,omitempty"`
	RewardPoints int        `json:"reward_points"`
	RewardItem   *string    `json:"reward_item,omitempty"`
	StartsAt     *time.Time `json:"starts_at,omitempty"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
	Active       bool       `json:"active"`
	Sort         int        `json:"sort"`
}

const missionCols = `id, title, description, kind, metric, target, network, title_id, requires, reward_points, reward_item, starts_at, ends_at, active, sort`

func scanMission(r pgx.Row) (Mission, error) {
	var m Mission
	err := r.Scan(&m.ID, &m.Title, &m.Description, &m.Kind, &m.Metric, &m.Target, &m.Network, &m.TitleID, &m.Requires, &m.RewardPoints, &m.RewardItem, &m.StartsAt, &m.EndsAt, &m.Active, &m.Sort)
	return m, err
}

// live reports whether the mission is in its window at t.
func (m Mission) live(t time.Time) bool {
	return m.Active && (m.StartsAt == nil || !t.Before(*m.StartsAt)) && (m.EndsAt == nil || t.Before(*m.EndsAt))
}

func (s *Store) ListMissions(ctx context.Context, all bool) ([]Mission, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+missionCols+` FROM missions ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mission{}
	now := s.Now()
	for rows.Next() {
		m, err := scanMission(rows)
		if err != nil {
			return nil, err
		}
		if all || m.live(now) {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// periodRange is the [from, to) day range a mission counts in; nil bounds mean all time.
func periodRange(kind string, now time.Time) (from, to *time.Time) {
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch kind {
	case "daily":
		e := d.AddDate(0, 0, 1)
		return &d, &e
	case "weekly":
		w := rules.WeekStart(now)
		e := w.AddDate(0, 0, 7)
		return &w, &e
	}
	return nil, nil
}

func (s *Store) metricValue(ctx context.Context, d q, acct string, m Mission, now time.Time) (int64, error) {
	from, to := periodRange(m.Kind, now)
	if m.Metric == "friends_added" {
		// Every accepted friend counts towards the missions (owner, 2026-11-03); only the points paid per
		// friend (payFriend) wait until that friend has played.
		var n int64
		err := d.QueryRow(ctx, `SELECT COUNT(*) FROM friends WHERE account_id=$1 AND ($2::timestamptz IS NULL OR created_at >= $2) AND ($3::timestamptz IS NULL OR created_at < $3)`, acct, from, to).Scan(&n)
		return n, err
	}
	if m.Metric == "lifetime_play_seconds" {
		var n int64
		err := d.QueryRow(ctx, `SELECT lifetime_play_seconds FROM accounts WHERE account_id=$1`, acct).Scan(&n)
		return n, err
	}
	switch m.Metric {
	case "play_seconds", "play_days", "titles_played", "networks_played", "sessions":
	default:
		v, _, err := eventValue(ctx, d, acct, m.Metric)
		return v, err
	}
	var secs, sess, days, titles, nets int64
	err := d.QueryRow(ctx, `SELECT COALESCE(SUM(seconds),0), COALESCE(SUM(sessions),0), COUNT(DISTINCT day), COUNT(DISTINCT title_id), COUNT(DISTINCT network)
		FROM activity WHERE account_id=$1 AND ($2::date IS NULL OR day >= $2) AND ($3::date IS NULL OR day < $3) AND ($4='' OR network=$4) AND ($5='' OR title_id=$5)`,
		acct, from, to, m.Network, m.TitleID).Scan(&secs, &sess, &days, &titles, &nets)
	if err != nil {
		return 0, err
	}
	switch m.Metric {
	case "play_seconds":
		return secs, nil
	case "sessions":
		return sess, nil
	case "play_days":
		return days, nil
	case "titles_played":
		return titles, nil
	case "networks_played":
		return nets, nil
	}
	return 0, nil
}

// refreshMissions recomputes progress for every live mission whose prerequisite is met and
// stamps completion. Progress is derived from the activity and friend rows, never stored from
// a caller, so a mission can only move by server-side play.
func (s *Store) refreshMissions(ctx context.Context, tx pgx.Tx, acct string, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT `+missionCols+` FROM missions WHERE active`)
	if err != nil {
		return err
	}
	var ms []Mission
	for rows.Next() {
		m, err := scanMission(rows)
		if err != nil {
			rows.Close()
			return err
		}
		if m.live(now) {
			ms = append(ms, m)
		}
	}
	rows.Close()
	for _, m := range ms {
		if m.Requires != nil {
			var met bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mission_progress WHERE account_id=$1 AND mission_id=$2 AND claimed_at IS NOT NULL)`, acct, *m.Requires).Scan(&met); err != nil {
				return err
			}
			if !met {
				continue
			}
		}
		v, err := s.metricValue(ctx, tx, acct, m, now)
		if err != nil {
			return err
		}
		if v == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mission_progress (account_id, mission_id, period, progress, completed_at) VALUES ($1,$2,$3,$4, CASE WHEN $4::bigint >= $5::bigint THEN $6::timestamptz END)
			ON CONFLICT (account_id, mission_id, period) DO UPDATE SET progress = EXCLUDED.progress,
			completed_at = COALESCE(mission_progress.completed_at, EXCLUDED.completed_at)`,
			acct, m.ID, rules.Period(m.Kind, now), v, m.Target, now); err != nil {
			return err
		}
	}
	return nil
}

// MissionStatus is a mission with one account's progress in one period.
type MissionStatus struct {
	Mission
	Period      string     `json:"period"`
	Progress    int64      `json:"progress"`
	Completed   bool       `json:"completed"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Claimed     bool       `json:"claimed"`
	Claimable   bool       `json:"claimable"`
	Locked      bool       `json:"locked"` // a series step whose predecessor is not claimed yet
}

// MyMissions lists live missions with this account's progress in the current period, plus
// completed-but-unclaimed rewards of the previous period (daily and weekly).
func (s *Store) MyMissions(ctx context.Context, acct string) ([]MissionStatus, error) {
	ms, err := s.ListMissions(ctx, false)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	out := []MissionStatus{}
	for _, m := range ms {
		st := MissionStatus{Mission: m, Period: rules.Period(m.Kind, now)}
		if m.Requires != nil {
			var met bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mission_progress WHERE account_id=$1 AND mission_id=$2 AND claimed_at IS NOT NULL)`, acct, *m.Requires).Scan(&met); err != nil {
				return nil, err
			}
			st.Locked = !met
		}
		err := s.pool.QueryRow(ctx, `SELECT progress, completed_at, claimed_at IS NOT NULL FROM mission_progress WHERE account_id=$1 AND mission_id=$2 AND period=$3`,
			acct, m.ID, st.Period).Scan(&st.Progress, &st.CompletedAt, &st.Claimed)
		if err != nil && err != pgx.ErrNoRows {
			return nil, err
		}
		st.Completed = st.CompletedAt != nil
		st.Claimable = st.Completed && !st.Claimed && !st.Locked
		out = append(out, st)
		if prev := rules.PreviousPeriod(m.Kind, now); prev != "" {
			p := MissionStatus{Mission: m, Period: prev}
			err := s.pool.QueryRow(ctx, `SELECT progress, completed_at, claimed_at IS NOT NULL FROM mission_progress WHERE account_id=$1 AND mission_id=$2 AND period=$3`,
				acct, m.ID, prev).Scan(&p.Progress, &p.CompletedAt, &p.Claimed)
			if err == nil && p.CompletedAt != nil && !p.Claimed {
				p.Completed, p.Claimable = true, true
				out = append(out, p)
			}
		}
	}
	return out, nil
}

type ClaimResult struct {
	MissionID string `json:"mission_id"`
	Period    string `json:"period"`
	Points    int    `json:"points"`
	Item      string `json:"item,omitempty"`
	Balance   int64  `json:"balance"`
}

// Claim pays a completed mission's reward once per period. period may be the current one or the
// one before it; "" means the current one.
func (s *Store) Claim(ctx context.Context, acct, id, period string) (ClaimResult, error) {
	var res ClaimResult
	err := s.tx(ctx, func(tx pgx.Tx) error {
		a, err := lock(ctx, tx, acct)
		if err != nil {
			return err
		}
		if a.Frozen {
			return ErrFrozen
		}
		m, err := scanMission(tx.QueryRow(ctx, `SELECT `+missionCols+` FROM missions WHERE id=$1`, id))
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.Now()
		cur := rules.Period(m.Kind, now)
		if period == "" {
			period = cur
		}
		if period != cur && period != rules.PreviousPeriod(m.Kind, now) {
			return invalid("that period can no longer be claimed")
		}
		if m.Requires != nil {
			var met bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mission_progress WHERE account_id=$1 AND mission_id=$2 AND claimed_at IS NOT NULL)`, acct, *m.Requires).Scan(&met); err != nil {
				return err
			}
			if !met {
				return invalid("finish the previous mission first")
			}
		}
		var completed bool
		var claimed bool
		err = tx.QueryRow(ctx, `SELECT completed_at IS NOT NULL, claimed_at IS NOT NULL FROM mission_progress WHERE account_id=$1 AND mission_id=$2 AND period=$3 FOR UPDATE`, acct, id, period).Scan(&completed, &claimed)
		if err == pgx.ErrNoRows || (err == nil && !completed) {
			return invalid("mission not completed")
		}
		if err != nil {
			return err
		}
		res = ClaimResult{MissionID: id, Period: period}
		if claimed {
			// Idempotent: answer as the first claim did, change nothing.
			return tx.QueryRow(ctx, `SELECT balance FROM accounts WHERE account_id=$1`, acct).Scan(&res.Balance)
		}
		if _, err := tx.Exec(ctx, `UPDATE mission_progress SET claimed_at=$4 WHERE account_id=$1 AND mission_id=$2 AND period=$3`, acct, id, period, now); err != nil {
			return err
		}
		if m.RewardPoints > 0 {
			if _, err := credit(ctx, tx, acct, int64(m.RewardPoints), "earn", "mission", id+":"+period, "mission:"+id+":"+acct+":"+period, ""); err != nil {
				return err
			}
			res.Points = m.RewardPoints
		}
		if m.RewardItem != nil {
			if granted, err := s.grant(ctx, tx, acct, *m.RewardItem, "mission", "mission:"+id+":"+acct+":"+period); err != nil {
				return err
			} else if granted {
				res.Item = *m.RewardItem
			}
		}
		// The next tier of a ladder may already be earned: show it now.
		if err := s.refreshMissions(ctx, tx, acct, now); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT balance FROM accounts WHERE account_id=$1`, acct).Scan(&res.Balance)
	})
	return res, err
}

// UpsertMission creates or replaces a mission definition (admin).
func (s *Store) UpsertMission(ctx context.Context, actor string, m Mission) error {
	if m.ID == "" || m.Title == "" || m.Target <= 0 {
		return invalid("id, title and a positive target are required")
	}
	if m.RewardPoints < 0 || m.RewardPoints > 1000 {
		return invalid("reward_points must be 0..1000")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if !validMetric(ctx, tx, m.Metric) {
			return invalid("unknown metric")
		}
		if m.RewardItem != nil {
			var n int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM items WHERE id=$1`, *m.RewardItem).Scan(&n); err != nil || n == 0 {
				return invalid("reward_item does not exist")
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO missions (id,title,description,kind,metric,target,network,title_id,requires,reward_points,reward_item,starts_at,ends_at,active,sort)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT (id) DO UPDATE SET title=$2, description=$3, kind=$4, metric=$5, target=$6, network=$7, title_id=$8, requires=$9,
			  reward_points=$10, reward_item=$11, starts_at=$12, ends_at=$13, active=$14, sort=$15`,
			m.ID, m.Title, m.Description, m.Kind, m.Metric, m.Target, m.Network, m.TitleID, m.Requires, m.RewardPoints, m.RewardItem, m.StartsAt, m.EndsAt, m.Active, m.Sort)
		if err != nil {
			return invalid("mission rejected: check kind, metric and requires")
		}
		return audit(ctx, tx, actor, "mission.upsert", m.ID, "")
	})
}

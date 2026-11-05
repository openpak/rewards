package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/openpak/rewards/internal/rules"
)

// TitleTotal is one row of the account core's play history: the cumulative server-side
// session time for a title. It is the only play input the service accepts.
type TitleTotal struct {
	Title    string
	Seconds  int64
	Sessions int
}

type AccrueResult struct {
	Points  int   // points credited by this call
	Counted int64 // seconds counted
	Clamps  int   // deltas cut by the elapsed-time rule
	Skipped bool  // account frozen or paused: cursors moved, nothing earned
}

type cursor struct {
	total, counted, carry int64
	sessions              int
	seen                  time.Time
}

// Accrue turns one account's play history in one namespace into points and mission progress.
// It runs in a single transaction under the account's row lock, so concurrent callers and
// replays cannot overshoot a cap or pay twice.
func (s *Store) Accrue(ctx context.Context, acct, ns string, entries []TitleTotal, now time.Time) (AccrueResult, error) {
	var res AccrueResult
	network := rules.Network(ns)
	day := rules.Day(now)
	err := s.tx(ctx, func(tx pgx.Tx) error {
		res = AccrueResult{}
		a, err := lock(ctx, tx, acct)
		if err != nil {
			return err
		}
		res.Skipped = a.Frozen || a.Paused
		for _, e := range entries {
			if e.Title == "" || e.Seconds < 0 {
				continue
			}
			var c cursor
			err := tx.QueryRow(ctx, `SELECT total_seconds, sessions, counted_seconds, carry_seconds, seen_at FROM play_cursor WHERE account_id=$1 AND namespace=$2 AND title_id=$3 FOR UPDATE`,
				acct, ns, e.Title).Scan(&c.total, &c.sessions, &c.counted, &c.carry, &c.seen)
			if err == pgx.ErrNoRows {
				// First sight: the whole total is history, paid once by payHistory (not for a frozen or paused account).
				hist, histSess := e.Seconds, e.Sessions
				if res.Skipped {
					hist, histSess = 0, 0
				}
				if _, err := tx.Exec(ctx, `INSERT INTO play_cursor (account_id, namespace, title_id, total_seconds, sessions, seen_at, history_seconds, history_done, history_sessions) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
					acct, ns, e.Title, e.Seconds, e.Sessions, now, hist, res.Skipped, histSess); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			delta := e.Seconds - c.total
			counted, clamped := s.R.Clamp(delta, int64(now.Sub(c.seen).Seconds()))
			sess := e.Sessions - c.sessions
			if sess < 0 {
				sess = 0
			}
			if res.Skipped || counted == 0 {
				if _, err := tx.Exec(ctx, `UPDATE play_cursor SET total_seconds=$4, sessions=$5, seen_at=$6 WHERE account_id=$1 AND namespace=$2 AND title_id=$3`,
					acct, ns, e.Title, e.Seconds, e.Sessions, now); err != nil {
					return err
				}
				continue
			}
			if clamped {
				res.Clamps++
				if _, err := tx.Exec(ctx, `INSERT INTO daily_earn (account_id, day, source, bucket, points) VALUES ($1,$2,'clamp','',1) ON CONFLICT (account_id, day, source, bucket) DO UPDATE SET points = daily_earn.points + 1`, acct, day); err != nil {
					return err
				}
			}
			pts, err := s.applyPlay(ctx, tx, acct, ns, network, day, now, e, c, counted, sess)
			if err != nil {
				return err
			}
			res.Points += pts
			res.Counted += counted
		}
		if _, err := tx.Exec(ctx, `UPDATE accounts SET last_poll_at = $2 WHERE account_id = $1`, acct, now); err != nil {
			return err
		}
		if !res.Skipped {
			hp, err := s.payHistory(ctx, tx, acct)
			if err != nil {
				return err
			}
			res.Points += hp
		}
		if res.Counted > 0 {
			bonus, err := s.afterPlay(ctx, tx, acct, day)
			if err != nil {
				return err
			}
			res.Points += bonus
			if err := s.settleFriends(ctx, tx, acct); err != nil {
				return err
			}
		}
		return s.refreshMissions(ctx, tx, acct, now)
	})
	return res, err
}

// applyPlay credits one title's counted seconds: play points, check-in and new-title bonus.
func (s *Store) applyPlay(ctx context.Context, tx pgx.Tx, acct, ns, network, day string, now time.Time, e TitleTotal, c cursor, counted int64, sess int) (int, error) {
	var titlePts, totalPts int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(points) FILTER (WHERE bucket=$3),0), COALESCE(SUM(points),0) FROM daily_earn WHERE account_id=$1 AND day=$2 AND source='play'`,
		acct, day, e.Title).Scan(&titlePts, &totalPts); err != nil {
		return 0, err
	}
	pr := s.R.PlayPoints(rules.PlayInput{Counted: counted, Carry: c.carry, TitlePointsToday: titlePts, TotalPlayPointsToday: totalPts})
	newCounted := c.counted + counted
	if _, err := tx.Exec(ctx, `UPDATE play_cursor SET total_seconds=$4, sessions=$5, counted_seconds=$6, carry_seconds=$7, seen_at=$8 WHERE account_id=$1 AND namespace=$2 AND title_id=$3`,
		acct, ns, e.Title, e.Seconds, e.Sessions, newCounted, pr.NewCarry, now); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET lifetime_play_seconds = lifetime_play_seconds + $2 WHERE account_id=$1`, acct, counted); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO activity (account_id, day, network, title_id, seconds, sessions) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (account_id, day, network, title_id) DO UPDATE SET seconds = activity.seconds + $5, sessions = activity.sessions + $6`,
		acct, day, network, e.Title, counted, sess); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO daily_earn (account_id, day, source, bucket, seconds) VALUES ($1,$2,'play',$3,$4)
		ON CONFLICT (account_id, day, source, bucket) DO UPDATE SET seconds = daily_earn.seconds + $4`, acct, day, e.Title, counted); err != nil {
		return 0, err
	}
	total := 0
	if pr.Points > 0 {
		ok, err := credit(ctx, tx, acct, int64(pr.Points), "earn", "play", e.Title, fmt.Sprintf("play:%s:%s:%s:%d", acct, ns, e.Title, e.Seconds), "")
		if err != nil {
			return 0, err
		}
		if ok {
			total += pr.Points
			if _, err := tx.Exec(ctx, `UPDATE daily_earn SET points = points + $4 WHERE account_id=$1 AND day=$2 AND source='play' AND bucket=$3`, acct, day, e.Title, pr.Points); err != nil {
				return 0, err
			}
		}
	}
	// New-title bonus.
	var credited bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM titles_credited WHERE account_id=$1 AND title_id=$2)`, acct, e.Title).Scan(&credited); err != nil {
		return 0, err
	}
	var ntToday int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(points),0) FROM daily_earn WHERE account_id=$1 AND day=$2 AND source='new_title'`, acct, day).Scan(&ntToday); err != nil {
		return 0, err
	}
	if nt := s.R.NewTitle(credited, newCounted, ntToday/max(s.R.NewTitleBonus, 1)); nt > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO titles_credited (account_id, title_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, acct, e.Title); err != nil {
			return 0, err
		}
		ok, err := credit(ctx, tx, acct, int64(nt), "earn", "new_title", e.Title, fmt.Sprintf("newtitle:%s:%s", acct, e.Title), "")
		if err != nil {
			return 0, err
		}
		if ok {
			total += nt
			if _, err := tx.Exec(ctx, `INSERT INTO daily_earn (account_id, day, source, bucket, points) VALUES ($1,$2,'new_title','',$3)
				ON CONFLICT (account_id, day, source, bucket) DO UPDATE SET points = daily_earn.points + $3`, acct, day, nt); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

// afterPlay runs once per call that counted time: the daily check-in and the abuse flags.
func (s *Store) afterPlay(ctx context.Context, tx pgx.Tx, acct, day string) (int, error) {
	var secs int64
	var done bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(seconds) FILTER (WHERE source='play'),0), COALESCE(BOOL_OR(source='checkin' AND points>0),false) FROM daily_earn WHERE account_id=$1 AND day=$2`,
		acct, day).Scan(&secs, &done); err != nil {
		return 0, err
	}
	paid := 0
	if pts := s.R.Checkin(secs, done); pts > 0 {
		ok, err := credit(ctx, tx, acct, int64(pts), "earn", "checkin", day, fmt.Sprintf("checkin:%s:%s", acct, day), "")
		if err != nil {
			return 0, err
		}
		if ok {
			paid = pts
			if _, err := tx.Exec(ctx, `INSERT INTO daily_earn (account_id, day, source, bucket, points) VALUES ($1,$2,'checkin','',$3)
				ON CONFLICT (account_id, day, source, bucket) DO UPDATE SET points = daily_earn.points + $3`, acct, day, pts); err != nil {
				return 0, err
			}
		}
	}
	// Abuse evidence: days in a row at the play cap, clamps today.
	var streak, clamps int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(points),0) FROM daily_earn WHERE account_id=$1 AND day=$2 AND source='clamp'`, acct, day).Scan(&clamps); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT day, SUM(points) FROM daily_earn WHERE account_id=$1 AND source='play' AND day <= $2::date GROUP BY day ORDER BY day DESC LIMIT 30`, acct, day)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	want, _ := time.Parse("2006-01-02", day)
	for rows.Next() {
		var d time.Time
		var p int
		if err := rows.Scan(&d, &p); err != nil {
			return 0, err
		}
		if !d.Equal(want) || p < s.R.PlayDailyCap {
			break
		}
		streak++
		want = want.AddDate(0, 0, -1)
	}
	rows.Close()
	if flag, why := s.R.IdleFarm(streak, clamps); flag {
		_, err := tx.Exec(ctx, `INSERT INTO flags (account_id, reason, detail) VALUES ($1,$2,jsonb_build_object('cap_streak_days',$3::int,'clamps_today',$4::int)) ON CONFLICT DO NOTHING`, acct, why, streak, clamps)
		return paid, err
	}
	return paid, nil
}

// FriendAccepted records a new friendship, in both directions, and pays each side whose
// friend has enough play of their own. Friends who have not played enough yet are paid
// later, when settleFriends sees them cross the line.
func (s *Store) FriendAccepted(ctx context.Context, a, b string) error {
	if a == b || a == "" || b == "" {
		return nil
	}
	for _, p := range [][2]string{{a, b}, {b, a}} {
		err := s.tx(ctx, func(tx pgx.Tx) error {
			if _, err := lock(ctx, tx, p[0]); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO friends (account_id, other_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, p[0], p[1]); err != nil {
				return err
			}
			if err := s.payFriend(ctx, tx, p[0], p[1]); err != nil {
				return err
			}
			return s.refreshMissions(ctx, tx, p[0], s.Now())
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// payFriend pays acct for friend other if the rules allow; acct's row is already locked.
func (s *Store) payFriend(ctx context.Context, tx pgx.Tx, acct, other string) error {
	var frozen, paused bool
	if err := tx.QueryRow(ctx, `SELECT frozen, paused FROM accounts WHERE account_id=$1`, acct).Scan(&frozen, &paused); err != nil {
		return err
	}
	var paid bool
	var paidSoFar int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT awarded FROM friends WHERE account_id=$1 AND other_id=$2),false), (SELECT COUNT(*) FROM friends WHERE account_id=$1 AND awarded)`, acct, other).Scan(&paid, &paidSoFar); err != nil {
		return err
	}
	var friendPlay int64
	var friendBlocked bool
	err := tx.QueryRow(ctx, `SELECT lifetime_play_seconds, frozen OR paused FROM accounts WHERE account_id=$1`, other).Scan(&friendPlay, &friendBlocked)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	if friendBlocked { // a frozen or paused friend never pays, whatever the play rule
		return nil
	}
	pts := s.R.Friend(friendPlay, paid, paidSoFar, frozen || paused)
	if pts == 0 {
		return nil
	}
	ok, err := credit(ctx, tx, acct, int64(pts), "earn", "friend", other, fmt.Sprintf("friend:%s:%s", acct, other), "")
	if err != nil || !ok {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE friends SET awarded = true WHERE account_id=$1 AND other_id=$2`, acct, other)
	return err
}

// SettleAllFriends pays every friendship the rules now allow but that was never paid (run at start, so a
// rule change such as dropping the play requirement reaches friendships made before it).
func (s *Store) SettleAllFriends(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT account_id, other_id FROM friends WHERE NOT awarded`)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	for _, p := range pairs {
		err := s.tx(ctx, func(tx pgx.Tx) error {
			if _, err := lock(ctx, tx, p[0]); err != nil {
				return err
			}
			if err := s.payFriend(ctx, tx, p[0], p[1]); err != nil {
				return err
			}
			return s.refreshMissions(ctx, tx, p[0], s.Now())
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// settleFriends pays the people who befriended acct before acct had played enough to count.
// It locks each of them in turn after acct's own row; accrual runs one account at a time, and
// no other path locks two account rows, so the order cannot deadlock.
func (s *Store) settleFriends(ctx context.Context, tx pgx.Tx, acct string) error {
	var play int64
	if err := tx.QueryRow(ctx, `SELECT lifetime_play_seconds FROM accounts WHERE account_id=$1`, acct).Scan(&play); err != nil {
		return err
	}
	if play < s.R.FriendMinSeconds {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT account_id FROM friends WHERE other_id=$1 AND NOT awarded LIMIT 50`, acct)
	if err != nil {
		return err
	}
	var who []string
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			rows.Close()
			return err
		}
		who = append(who, x)
	}
	rows.Close()
	for _, x := range who {
		if _, err := lock(ctx, tx, x); err != nil {
			return err
		}
		if err := s.payFriend(ctx, tx, x, acct); err != nil {
			return err
		}
	}
	return nil
}

// payHistory counts the play from before Rewards saw the account: every cursor row whose history is not
// done adds its seconds to the lifetime play and is marked, and one "history" ledger entry pays
// floor(seconds / SecondsPerPoint) with no daily cap. The caller holds the account's row lock and must not
// call it for a frozen or paused account. Marking the rows in the same transaction makes it pay once.
func (s *Store) payHistory(ctx context.Context, tx pgx.Tx, acct string) (int, error) {
	var secs int64
	var rows int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(history_seconds),0), COUNT(*) FROM play_cursor WHERE account_id=$1 AND NOT history_done`, acct).Scan(&secs, &rows); err != nil {
		return 0, err
	}
	if rows == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE play_cursor SET history_done=true WHERE account_id=$1 AND NOT history_done`, acct); err != nil {
		return 0, err
	}
	if secs == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET lifetime_play_seconds = lifetime_play_seconds + $2 WHERE account_id=$1`, acct, secs); err != nil {
		return 0, err
	}
	pts := int(secs / max(s.R.SecondsPerPoint, 1))
	if pts > 0 {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM ledger WHERE account_id=$1 AND source='history'`, acct).Scan(&n); err != nil {
			return 0, err
		}
		// The first payment is the account's pre-Rewards history. A later one is a catch-up for titles the
		// core's old 20-title cap had hidden (card #131 phase 9), or a first sight in a new namespace; the
		// ledger ref says so.
		ref := ""
		if n > 0 {
			ref = fmt.Sprintf("history catch-up: %d title(s) first seen after the 20-title cap was lifted (card #131)", rows)
		}
		ok, err := credit(ctx, tx, acct, int64(pts), "earn", "history", ref, fmt.Sprintf("history:%s:%d", acct, n), "")
		if err != nil || !ok {
			return 0, err
		}
	}
	return pts, s.settleFriends(ctx, tx, acct)
}

// SettleAllHistory pays the play history of every account that has some unpaid (run at start; idempotent).
// It returns the accounts paid and the points paid.
func (s *Store) SettleAllHistory(ctx context.Context) (accounts, points int, err error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT account_id FROM play_cursor WHERE NOT history_done`)
	if err != nil {
		return 0, 0, err
	}
	var accts []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return 0, 0, err
		}
		accts = append(accts, a)
	}
	rows.Close()
	for _, a := range accts {
		err := s.tx(ctx, func(tx pgx.Tx) error {
			acc, err := lock(ctx, tx, a)
			if err != nil {
				return err
			}
			if acc.Frozen || acc.Paused {
				return nil
			}
			p, err := s.payHistory(ctx, tx, a)
			if err != nil {
				return err
			}
			points += p
			accounts++
			return s.refreshMissions(ctx, tx, a, s.Now())
		})
		if err != nil {
			return accounts, points, err
		}
	}
	return accounts, points, nil
}

// BackfillDays records, once per account, the UTC days it played before Rewards first saw it. fetch asks the
// account core for the distinct days of sessions started before the cutoff (the account's first sight).
// The days go in history_days, never in activity, so daily and weekly missions cannot see them; all-time
// play_days is the union of the two sets. Frozen and paused accounts are left for later.
func (s *Store) BackfillDays(ctx context.Context, acct string, fetch func(ctx context.Context, before time.Time) ([]string, error)) error {
	var done, blocked bool
	var since time.Time
	err := s.pool.QueryRow(ctx, `SELECT history_days_done, frozen OR paused, created_at FROM accounts WHERE account_id=$1`, acct).Scan(&done, &blocked, &since)
	if err == pgx.ErrNoRows || done || blocked {
		return nil
	}
	if err != nil {
		return err
	}
	days, err := fetch(ctx, since)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := lock(ctx, tx, acct); err != nil {
			return err
		}
		var again bool
		if err := tx.QueryRow(ctx, `SELECT history_days_done FROM accounts WHERE account_id=$1`, acct).Scan(&again); err != nil || again {
			return err
		}
		for _, d := range days {
			if _, err := tx.Exec(ctx, `INSERT INTO history_days (account_id, day) VALUES ($1,$2::date) ON CONFLICT DO NOTHING`, acct, d); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE accounts SET history_days_done=true WHERE account_id=$1`, acct); err != nil {
			return err
		}
		return s.refreshMissions(ctx, tx, acct, s.Now())
	})
}

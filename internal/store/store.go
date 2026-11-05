// Package store is the rewards database: accounts, the ledger, play cursors, missions, the
// catalogue and redemptions. Every rule decision is taken in internal/rules; this package
// applies them inside transactions so caps cannot be overshot by concurrent callers.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/openpak/rewards/internal/rules"
	"github.com/openpak/rewards/migrations"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInsufficient = errors.New("not enough points")
	ErrFrozen       = errors.New("account frozen")
	ErrOwned        = errors.New("already owned")
	ErrSoldOut      = errors.New("sold out")
	ErrNotForSale   = errors.New("not for sale")
)

// InvalidError is a request the caller can fix; Msg is safe to show.
type InvalidError struct{ Msg string }

func (e InvalidError) Error() string { return e.Msg }

func invalid(f string, a ...any) error { return InvalidError{fmt.Sprintf(f, a...)} }

type Store struct {
	pool     *pgxpool.Pool
	R        rules.Rules
	Now      func() time.Time
	AssetDir string
}

// q is what a pool and a transaction share.
type q interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func Open(ctx context.Context, url, assetDir string, r rules.Rules) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("rewards: database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("rewards: database: %w", err)
	}
	s := &Store{pool: pool, R: r, Now: time.Now, AssetDir: assetDir}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { s.pool.Close() }

// Pool is for tests that reset the schema.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) migrate(ctx context.Context) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7210001)`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
			return err
		}
		files, err := fs.Glob(migrations.FS, "*.sql")
		if err != nil {
			return err
		}
		sort.Strings(files)
		for _, f := range files {
			var done bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, f).Scan(&done); err != nil {
				return err
			}
			if done {
				continue
			}
			b, err := migrations.FS.ReadFile(f)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(b)); err != nil {
				return fmt.Errorf("rewards: migration %s: %w", f, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, f); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) tx(ctx context.Context, f func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ensure creates the account row on first sight.
func ensure(ctx context.Context, d q, acct string) error {
	_, err := d.Exec(ctx, `INSERT INTO accounts (account_id) VALUES ($1) ON CONFLICT DO NOTHING`, acct)
	return err
}

type accState struct {
	Frozen, Paused bool
	Balance        int64
	LifetimePlay   int64
}

// lock creates (if needed) and locks the account row for the rest of the transaction.
func lock(ctx context.Context, d q, acct string) (accState, error) {
	if err := ensure(ctx, d, acct); err != nil {
		return accState{}, err
	}
	var a accState
	err := d.QueryRow(ctx, `SELECT frozen, paused, balance, lifetime_play_seconds FROM accounts WHERE account_id = $1 FOR UPDATE`, acct).
		Scan(&a.Frozen, &a.Paused, &a.Balance, &a.LifetimePlay)
	return a, err
}

// credit appends a ledger row and moves the cached balance, once per idem key. It reports whether
// the row was new; a spend that would take the balance below zero is ErrInsufficient.
func credit(ctx context.Context, d q, acct string, delta int64, kind, source, ref, idem, actor string) (bool, error) {
	if delta == 0 {
		return false, nil
	}
	tag, err := d.Exec(ctx, `INSERT INTO ledger (account_id, delta, kind, source, ref, idem_key, actor) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (idem_key) DO NOTHING`,
		acct, delta, kind, source, ref, idem, actor)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	earned := int64(0)
	if kind == "earn" && delta > 0 {
		earned = delta
	}
	_, err = d.Exec(ctx, `UPDATE accounts SET balance = balance + $2, lifetime_earned = lifetime_earned + $3 WHERE account_id = $1`, acct, delta, earned)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23514" {
		return false, ErrInsufficient
	}
	return err == nil, err
}

func audit(ctx context.Context, d q, actor, action, target string, detail string) error {
	if detail == "" {
		detail = "{}"
	}
	_, err := d.Exec(ctx, `INSERT INTO audit (actor, action, target, detail) VALUES ($1,$2,$3,$4::jsonb)`, actor, action, target, detail)
	return err
}

// Account is what the user's own /v1/me shows.
type Account struct {
	AccountID      string     `json:"account_id"`
	Balance        int64      `json:"balance"`
	LifetimeEarned int64      `json:"lifetime_earned"`
	LifetimePlay   int64      `json:"lifetime_play_seconds"`
	Frozen         bool       `json:"frozen"`
	Paused         bool       `json:"paused"`
	ShowcasePublic bool       `json:"showcase_public"`
	CreatedAt      time.Time  `json:"created_at"`
	LastPollAt     *time.Time `json:"last_poll_at,omitempty"`
}

func (s *Store) Account(ctx context.Context, acct string) (Account, error) {
	if err := ensure(ctx, s.pool, acct); err != nil {
		return Account{}, err
	}
	a := Account{AccountID: acct}
	err := s.pool.QueryRow(ctx, `SELECT balance, lifetime_earned, lifetime_play_seconds, frozen, paused, showcase_public, created_at, last_poll_at FROM accounts WHERE account_id = $1`, acct).
		Scan(&a.Balance, &a.LifetimeEarned, &a.LifetimePlay, &a.Frozen, &a.Paused, &a.ShowcasePublic, &a.CreatedAt, &a.LastPollAt)
	return a, err
}

// Today is how much of each daily cap an account has used.
type Today struct {
	Day          string `json:"day"`
	PlayPoints   int    `json:"play_points"`
	PlayCap      int    `json:"play_cap"`
	PlaySeconds  int64  `json:"play_seconds"`
	CheckinDone  bool   `json:"checkin_done"`
	NewTitles    int    `json:"new_titles"`
	NewTitlesCap int    `json:"new_titles_cap"`
}

func (s *Store) Today(ctx context.Context, acct string) (Today, error) {
	day := rules.Day(s.Now())
	t := Today{Day: day, PlayCap: s.R.PlayDailyCap, NewTitlesCap: s.R.NewTitleDailyCap}
	rows, err := s.pool.Query(ctx, `SELECT source, COALESCE(SUM(points),0), COALESCE(SUM(seconds),0) FROM daily_earn WHERE account_id=$1 AND day=$2 GROUP BY source`, acct, day)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		var pts int
		var secs int64
		if err := rows.Scan(&src, &pts, &secs); err != nil {
			return t, err
		}
		switch src {
		case "play":
			t.PlayPoints, t.PlaySeconds = pts, secs
		case "checkin":
			t.CheckinDone = pts > 0
		case "new_title":
			t.NewTitles = pts / max(s.R.NewTitleBonus, 1)
		}
	}
	return t, rows.Err()
}

type LedgerRow struct {
	ID        int64     `json:"id"`
	Delta     int64     `json:"delta"`
	Kind      string    `json:"kind"`
	Source    string    `json:"source"`
	Ref       string    `json:"ref"`
	CreatedAt time.Time `json:"created_at"`
}

// Ledger is newest first; before is the id of the last row of the previous page (0 = start).
func (s *Store) Ledger(ctx context.Context, acct string, before int64, limit int) ([]LedgerRow, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.pool.Query(ctx, `SELECT id, delta, kind, source, ref, created_at FROM ledger WHERE account_id=$1 AND id < $2 ORDER BY id DESC LIMIT $3`, acct, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerRow{}
	for rows.Next() {
		var r LedgerRow
		if err := rows.Scan(&r.ID, &r.Delta, &r.Kind, &r.Source, &r.Ref, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SetSettings(ctx context.Context, acct string, showcase, paused *bool) error {
	if err := ensure(ctx, s.pool, acct); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE accounts SET showcase_public = COALESCE($2, showcase_public), paused = COALESCE($3, paused) WHERE account_id = $1`, acct, showcase, paused)
	return err
}

// SetFrozen is the core's ban (actor "core") or an admin's freeze.
func (s *Store) SetFrozen(ctx context.Context, acct string, frozen bool, actor string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := ensure(ctx, tx, acct); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE accounts SET frozen = $2 WHERE account_id = $1`, acct, frozen); err != nil {
			return err
		}
		act := "freeze"
		if !frozen {
			act = "unfreeze"
		}
		return audit(ctx, tx, actor, act, acct, "")
	})
}

var purgeTables = []string{"accounts", "ledger", "play_cursor", "history_days", "daily_earn", "activity", "titles_credited", "mission_progress", "metric_counts", "metric_daily", "metric_keys", "events", "redemptions", "equipped", "flags"}

// Purge erases everything held about an account: the user's own DELETE /v1/me and the core's
// account_deleted event. The audit trail keeps only the admin actions, which name the id.
func (s *Store) Purge(ctx context.Context, acct string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		for _, t := range purgeTables {
			if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE account_id = $1`, acct); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM friends WHERE account_id = $1 OR other_id = $1`, acct)
		return err
	})
}

func (s *Store) CoreCursor(ctx context.Context) (uint64, error) {
	var v int64
	err := s.pool.QueryRow(ctx, `SELECT since_version FROM core_cursor WHERE id = 1`).Scan(&v)
	return uint64(v), err
}

func (s *Store) SetCoreCursor(ctx context.Context, v uint64) error {
	_, err := s.pool.Exec(ctx, `UPDATE core_cursor SET since_version = $1 WHERE id = 1`, int64(v))
	return err
}

// PurgeStale drops cursors and counters past their retention (design section 10).
func (s *Store) PurgeStale(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM play_cursor WHERE seen_at < now() - interval '180 days'`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM daily_earn WHERE day < current_date - 90`); err != nil {
		return err
	}
	return s.purgeEvents(ctx)
}

// PlayCursorTotals is the cached core play total (seconds) per title for one account and namespace.
func (s *Store) PlayCursorTotals(ctx context.Context, acct, ns string) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT title_id, total_seconds FROM play_cursor WHERE account_id=$1 AND namespace=$2`, acct, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var t string
		var n int64
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}

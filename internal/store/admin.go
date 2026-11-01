package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Adjust grants or deducts points by hand. A reason is mandatory, an admin cannot touch their
// own account, and the size is bounded; the ledger row and the audit row name the actor.
func (s *Store) Adjust(ctx context.Context, actor, acct string, delta int64, reason string) (int64, error) {
	max := int64(s.R.MaxSingleGrantAdmin)
	if delta == 0 || delta > max || delta < -max {
		return 0, invalid("delta must be non-zero and within +/-%d", max)
	}
	if reason == "" || len(reason) > 300 {
		return 0, invalid("a reason (300 characters at most) is required")
	}
	if actor == acct {
		return 0, invalid("an admin cannot adjust their own account")
	}
	var bal int64
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := lock(ctx, tx, acct); err != nil {
			return err
		}
		kind := "adjust"
		key := fmt.Sprintf("adjust:%s:%d", acct, s.Now().UnixNano())
		if _, err := credit(ctx, tx, acct, delta, kind, "admin", reason, key, actor); err != nil {
			return err
		}
		b, _ := json.Marshal(map[string]any{"delta": delta, "reason": reason})
		if err := audit(ctx, tx, actor, "points.adjust", acct, string(b)); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT balance FROM accounts WHERE account_id=$1`, acct).Scan(&bal)
	})
	return bal, err
}

type Flag struct {
	ID         int64           `json:"id"`
	AccountID  string          `json:"account_id"`
	Reason     string          `json:"reason"`
	Detail     json.RawMessage `json:"detail"`
	RaisedAt   time.Time       `json:"raised_at"`
	ResolvedBy *string         `json:"resolved_by,omitempty"`
	ResolvedAt *time.Time      `json:"resolved_at,omitempty"`
}

func (s *Store) Flags(ctx context.Context, openOnly bool, limit int) ([]Flag, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id, account_id, reason, detail, raised_at, resolved_by, resolved_at FROM flags WHERE (NOT $1 OR resolved_at IS NULL) ORDER BY id DESC LIMIT $2`, openOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Flag{}
	for rows.Next() {
		var f Flag
		if err := rows.Scan(&f.ID, &f.AccountID, &f.Reason, &f.Detail, &f.RaisedAt, &f.ResolvedBy, &f.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) ResolveFlag(ctx context.Context, actor string, id int64, note string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE flags SET resolved_by=$2, resolved_at=now() WHERE id=$1 AND resolved_at IS NULL`, id, actor)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		b, _ := json.Marshal(map[string]any{"flag": id, "note": note})
		return audit(ctx, tx, actor, "flag.resolve", fmt.Sprint(id), string(b))
	})
}

type AuditRow struct {
	ID     int64           `json:"id"`
	At     time.Time       `json:"at"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Target string          `json:"target"`
	Detail json.RawMessage `json:"detail"`
}

func (s *Store) Audit(ctx context.Context, target string, before int64, limit int) ([]AuditRow, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.pool.Query(ctx, `SELECT id, at, actor, action, target, detail FROM audit WHERE ($1='' OR target=$1) AND id < $2 ORDER BY id DESC LIMIT $3`, target, before, limit)
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

type AccountDetail struct {
	Account     Account           `json:"account"`
	Today       Today             `json:"today"`
	Ledger      []LedgerRow       `json:"ledger"`
	Redemptions []Redemption      `json:"redemptions"`
	Flags       []Flag            `json:"flags"`
	Equipped    map[string]string `json:"equipped"`
}

// AccountDetail is the admin's view of one account. It never creates the account.
func (s *Store) AccountDetail(ctx context.Context, acct string) (AccountDetail, error) {
	var d AccountDetail
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM accounts WHERE account_id=$1`, acct).Scan(&n); err != nil {
		return d, err
	}
	if n == 0 {
		return d, ErrNotFound
	}
	var err error
	if d.Account, err = s.Account(ctx, acct); err != nil {
		return d, err
	}
	if d.Today, err = s.Today(ctx, acct); err != nil {
		return d, err
	}
	if d.Ledger, err = s.Ledger(ctx, acct, 0, 50); err != nil {
		return d, err
	}
	if d.Equipped, err = s.Equipped(ctx, acct); err != nil {
		return d, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, item_id, price, source, state, created_at FROM redemptions WHERE account_id=$1 ORDER BY id DESC LIMIT 100`, acct)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.Redemptions = []Redemption{}
	for rows.Next() {
		var r Redemption
		if err := rows.Scan(&r.ID, &r.ItemID, &r.Price, &r.Source, &r.State, &r.CreatedAt); err != nil {
			return d, err
		}
		d.Redemptions = append(d.Redemptions, r)
	}
	rows.Close()
	d.Flags = []Flag{}
	fr, err := s.pool.Query(ctx, `SELECT id, account_id, reason, detail, raised_at, resolved_by, resolved_at FROM flags WHERE account_id=$1 ORDER BY id DESC LIMIT 20`, acct)
	if err != nil {
		return d, err
	}
	defer fr.Close()
	for fr.Next() {
		var f Flag
		if err := fr.Scan(&f.ID, &f.AccountID, &f.Reason, &f.Detail, &f.RaisedAt, &f.ResolvedBy, &f.ResolvedAt); err != nil {
			return d, err
		}
		d.Flags = append(d.Flags, f)
	}
	return d, fr.Err()
}

type Stats struct {
	Accounts       int64            `json:"accounts"`
	PointsIssued   int64            `json:"points_issued"`
	PointsSpent    int64            `json:"points_spent"`
	IssuedToday    int64            `json:"issued_today"`
	Redemptions    map[string]int64 `json:"redemptions_by_item"`
	OpenFlags      int64            `json:"open_flags"`
	FrozenAccounts int64            `json:"frozen_accounts"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	st := Stats{Redemptions: map[string]int64{}}
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT COUNT(*) FROM accounts),
		(SELECT COALESCE(SUM(delta),0) FROM ledger WHERE delta > 0),
		(SELECT COALESCE(-SUM(delta),0) FROM ledger WHERE kind='spend'),
		(SELECT COALESCE(SUM(delta),0) FROM ledger WHERE delta > 0 AND created_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		(SELECT COUNT(*) FROM flags WHERE resolved_at IS NULL),
		(SELECT COUNT(*) FROM accounts WHERE frozen)`).
		Scan(&st.Accounts, &st.PointsIssued, &st.PointsSpent, &st.IssuedToday, &st.OpenFlags, &st.FrozenAccounts)
	if err != nil {
		return st, err
	}
	rows, err := s.pool.Query(ctx, `SELECT item_id, COUNT(*) FROM redemptions WHERE state='granted' GROUP BY item_id`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return st, err
		}
		st.Redemptions[id] = n
	}
	return st, rows.Err()
}

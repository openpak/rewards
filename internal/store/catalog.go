package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var Platforms = []string{"web", "app", "switch", "wiiu", "3ds", "wii", "ds", "ps3", "xbox360", "xbox"}

var Kinds = []string{"badge", "profile_icon", "theme", "title", "switch_icon", "x360_gamerpic", "x360_theme", "ps3_avatar", "ps3_theme"}

// MaxAssetBytes is the largest asset or preview accepted.
const MaxAssetBytes = 32 << 20

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

type Item struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Price           *int       `json:"price"`
	Platforms       []string   `json:"platforms"`
	Stock           *int       `json:"stock"`
	AvailableFrom   *time.Time `json:"available_from,omitempty"`
	AvailableUntil  *time.Time `json:"available_until,omitempty"`
	LimitPerAccount int        `json:"limit_per_account"`
	License         string     `json:"license"`
	State           string     `json:"state"`
	Limited         bool       `json:"limited"`
	Owned           bool       `json:"owned,omitempty"`
	Affordable      bool       `json:"affordable,omitempty"`
	HasPreview      bool       `json:"has_preview"`
}

const itemCols = `i.id, i.kind, i.name, i.description, i.price, i.platforms, i.stock, i.available_from, i.available_until, i.limit_per_account, i.license, i.state,
 (i.stock IS NOT NULL OR i.available_until IS NOT NULL),
 EXISTS (SELECT 1 FROM item_assets a WHERE a.item_id=i.id AND a.role='preview')`

func scanItem(r pgx.Row) (Item, error) {
	var it Item
	err := r.Scan(&it.ID, &it.Kind, &it.Name, &it.Description, &it.Price, &it.Platforms, &it.Stock, &it.AvailableFrom, &it.AvailableUntil, &it.LimitPerAccount, &it.License, &it.State, &it.Limited, &it.HasPreview)
	return it, err
}

func (it Item) onSale(t time.Time) bool {
	return it.State == "active" && it.Price != nil &&
		(it.AvailableFrom == nil || !t.Before(*it.AvailableFrom)) && (it.AvailableUntil == nil || t.Before(*it.AvailableUntil))
}

// Catalog is the shop: active items on sale now. With acct set it marks owned and affordable.
// all=true (admin) returns every state.
func (s *Store) Catalog(ctx context.Context, acct, kind, platform string, all bool) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+itemCols+` FROM items i WHERE ($1='' OR i.kind=$1) AND ($2='' OR $2 = ANY(i.platforms)) ORDER BY i.price NULLS LAST, i.id`, kind, platform)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	owned := map[string]int{}
	var bal int64
	if acct != "" {
		a, err := s.Account(ctx, acct)
		if err != nil {
			return nil, err
		}
		bal = a.Balance
		r2, err := s.pool.Query(ctx, `SELECT item_id, COUNT(*) FROM redemptions WHERE account_id=$1 AND state='granted' GROUP BY item_id`, acct)
		if err != nil {
			return nil, err
		}
		defer r2.Close()
		for r2.Next() {
			var id string
			var n int
			if err := r2.Scan(&id, &n); err != nil {
				return nil, err
			}
			owned[id] = n
		}
	}
	now := s.Now()
	out := []Item{}
	for _, it := range items {
		// Owned items stay visible even when retired, so the inventory page can render them.
		if !all && !it.onSale(now) && (owned[it.ID] == 0 || it.State == "draft") {
			continue
		}
		it.Owned = owned[it.ID] > 0
		it.Affordable = it.Price != nil && int64(*it.Price) <= bal
		out = append(out, it)
	}
	return out, nil
}

func (s *Store) GetItem(ctx context.Context, id string) (Item, error) {
	it, err := scanItem(s.pool.QueryRow(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id=$1`, id))
	if err == pgx.ErrNoRows {
		return it, ErrNotFound
	}
	return it, err
}

// UpsertItem creates or updates an item's metadata (admin). The state is never set here: use
// SetItemState, which checks the asset rules. The licence is always openpak-original.
func (s *Store) UpsertItem(ctx context.Context, actor string, it Item) error {
	if !idRe.MatchString(it.ID) {
		return invalid("id must be 2-63 characters of a-z, 0-9 and -")
	}
	if !slices.Contains(Kinds, it.Kind) {
		return invalid("unknown kind")
	}
	if it.Name == "" || len(it.Name) > 80 || len(it.Description) > 400 {
		return invalid("a name (80 characters at most) is required")
	}
	if it.Price != nil && (*it.Price < 0 || *it.Price > 100000) {
		return invalid("price out of range")
	}
	if it.Stock != nil && *it.Stock < 0 {
		return invalid("stock must not be negative")
	}
	if it.LimitPerAccount < 1 {
		it.LimitPerAccount = 1
	}
	if it.Platforms == nil {
		it.Platforms = []string{}
	}
	for _, p := range it.Platforms {
		if !slices.Contains(Platforms, p) {
			return invalid("unknown platform %q", p)
		}
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO items (id, kind, name, description, price, platforms, stock, available_from, available_until, limit_per_account)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id) DO UPDATE SET name=$3, description=$4, price=$5, platforms=$6, stock=$7, available_from=$8, available_until=$9, limit_per_account=$10`,
			it.ID, it.Kind, it.Name, it.Description, it.Price, it.Platforms, it.Stock, it.AvailableFrom, it.AvailableUntil, it.LimitPerAccount)
		if err != nil {
			return err
		}
		return audit(ctx, tx, actor, "item.upsert", it.ID, "")
	})
}

// SetItemState moves an item between draft, active and retired. Going active needs an asset
// for every listed platform and a preview, except text titles: nothing goes live without art.
func (s *Store) SetItemState(ctx context.Context, actor, id, state string) error {
	if state != "draft" && state != "active" && state != "retired" {
		return invalid("state must be draft, active or retired")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var kind string
		var plats []string
		if err := tx.QueryRow(ctx, `SELECT kind, platforms FROM items WHERE id=$1 FOR UPDATE`, id).Scan(&kind, &plats); err == pgx.ErrNoRows {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if state == "active" && kind != "title" {
			if len(plats) == 0 {
				return invalid("list at least one platform before activating")
			}
			var prev int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM item_assets WHERE item_id=$1 AND role='preview'`, id).Scan(&prev); err != nil {
				return err
			}
			if prev == 0 {
				return invalid("upload a preview before activating")
			}
			for _, p := range plats {
				var n int
				if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM item_assets WHERE item_id=$1 AND role='asset' AND platform=$2`, id, p).Scan(&n); err != nil {
					return err
				}
				if n == 0 {
					return invalid("platform %s has no asset", p)
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE items SET state=$2 WHERE id=$1`, id, state); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "item.state", id, fmt.Sprintf(`{"state":%q}`, state))
	})
}

// PutAsset stores a file content-addressed under AssetDir and records it.
func (s *Store) PutAsset(ctx context.Context, actor, id, platform, role, contentType string, body io.Reader) (sha string, n int64, err error) {
	if role != "asset" && role != "preview" {
		return "", 0, invalid("role must be asset or preview")
	}
	if platform == "" {
		platform = "web"
	}
	if !slices.Contains(Platforms, platform) {
		return "", 0, invalid("unknown platform")
	}
	if _, err := s.GetItem(ctx, id); err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(s.AssetDir, 0o750); err != nil {
		return "", 0, err
	}
	f, err := os.CreateTemp(s.AssetDir, ".upload-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(body, MaxAssetBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	if n == 0 || n > MaxAssetBytes {
		return "", 0, invalid("asset must be 1 byte to %d MiB", MaxAssetBytes>>20)
	}
	sha = hex.EncodeToString(h.Sum(nil))
	if err := os.Rename(f.Name(), filepath.Join(s.AssetDir, sha)); err != nil {
		return "", 0, err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO item_assets (item_id, platform, role, sha256, bytes, content_type) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (item_id, platform, role) DO UPDATE SET sha256=$4, bytes=$5, content_type=$6`, id, platform, role, sha, n, contentType); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "item.asset", id, fmt.Sprintf(`{"platform":%q,"role":%q,"sha256":%q}`, platform, role, sha))
	})
	return sha, n, err
}

type Asset struct {
	Path        string
	SHA256      string
	Bytes       int64
	ContentType string
}

// OpenAsset finds an item's file. Preview is public for active items; the asset itself is for
// owners (user route) or adapters (internal route), decided by the caller. platform "" picks
// the first platform that has the asset.
func (s *Store) FindAsset(ctx context.Context, id, platform, role string) (Asset, error) {
	var a Asset
	err := s.pool.QueryRow(ctx, `SELECT sha256, bytes, content_type FROM item_assets WHERE item_id=$1 AND role=$3 AND ($2='' OR platform=$2) ORDER BY platform LIMIT 1`, id, platform, role).
		Scan(&a.SHA256, &a.Bytes, &a.ContentType)
	if err == pgx.ErrNoRows {
		return a, ErrNotFound
	}
	a.Path = filepath.Join(s.AssetDir, a.SHA256)
	return a, err
}

// Owns reports whether the account holds a granted copy of the item.
func (s *Store) Owns(ctx context.Context, acct, item string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM redemptions WHERE account_id=$1 AND item_id=$2 AND state='granted')`, acct, item).Scan(&ok)
	return ok, err
}

type Redemption struct {
	ID        int64     `json:"id"`
	ItemID    string    `json:"item_id"`
	Price     int       `json:"price"`
	Source    string    `json:"source"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	Balance   int64     `json:"balance,omitempty"`
}

// Redeem buys an item with points. idem (the client's Idempotency-Key, may be empty) makes a
// retried request return the first result.
func (s *Store) Redeem(ctx context.Context, acct, itemID, idem string) (Redemption, error) {
	var out Redemption
	err := s.tx(ctx, func(tx pgx.Tx) error {
		a, err := lock(ctx, tx, acct)
		if err != nil {
			return err
		}
		if a.Frozen {
			return ErrFrozen
		}
		key := ""
		if idem != "" {
			key = "shop:" + acct + ":" + itemID + ":k:" + idem
			err := tx.QueryRow(ctx, `SELECT id, item_id, price, source, state, created_at FROM redemptions WHERE idem_key=$1`, key).
				Scan(&out.ID, &out.ItemID, &out.Price, &out.Source, &out.State, &out.CreatedAt)
			if err == nil {
				out.Balance = a.Balance
				return nil
			}
			if err != pgx.ErrNoRows {
				return err
			}
		}
		var it Item
		it, err = scanItem(tx.QueryRow(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id=$1 FOR UPDATE OF i`, itemID))
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.Now()
		if !it.onSale(now) {
			return ErrNotForSale
		}
		var have int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM redemptions WHERE account_id=$1 AND item_id=$2 AND state='granted'`, acct, itemID).Scan(&have); err != nil {
			return err
		}
		if have >= it.LimitPerAccount {
			return ErrOwned
		}
		if it.Stock != nil && *it.Stock <= 0 {
			return ErrSoldOut
		}
		if a.Balance < int64(*it.Price) {
			return ErrInsufficient
		}
		if key == "" {
			key = fmt.Sprintf("shop:%s:%s:n:%d", acct, itemID, have+1)
		}
		err = tx.QueryRow(ctx, `INSERT INTO redemptions (account_id, item_id, price, source, idem_key) VALUES ($1,$2,$3,'shop',$4) RETURNING id, item_id, price, source, state, created_at`,
			acct, itemID, *it.Price, key).Scan(&out.ID, &out.ItemID, &out.Price, &out.Source, &out.State, &out.CreatedAt)
		if err != nil {
			return ErrConflict
		}
		if _, err := credit(ctx, tx, acct, -int64(*it.Price), "spend", "redeem", itemID, "redeem:"+strconv.FormatInt(out.ID, 10), ""); err != nil {
			return err
		}
		if it.Stock != nil {
			if _, err := tx.Exec(ctx, `UPDATE items SET stock = stock - 1 WHERE id=$1`, itemID); err != nil {
				return err
			}
		}
		out.Balance = a.Balance - int64(*it.Price)
		return nil
	})
	return out, err
}

// grant gives an item without payment (mission reward, admin gift). It reports whether a new
// copy was handed over; owning the item already is a quiet no-op. acct's row must be locked.
func (s *Store) grant(ctx context.Context, tx pgx.Tx, acct, itemID, source, idem string) (bool, error) {
	var have, lim int
	if err := tx.QueryRow(ctx, `SELECT limit_per_account FROM items WHERE id=$1`, itemID).Scan(&lim); err == pgx.ErrNoRows {
		return false, ErrNotFound
	} else if err != nil {
		return false, err
	}
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM redemptions WHERE account_id=$1 AND item_id=$2 AND state='granted'`, acct, itemID).Scan(&have); err != nil {
		return false, err
	}
	if have >= lim {
		return false, nil
	}
	tag, err := tx.Exec(ctx, `INSERT INTO redemptions (account_id, item_id, price, source, idem_key) VALUES ($1,$2,0,$3,$4) ON CONFLICT (idem_key) DO NOTHING`, acct, itemID, source, "grant:"+idem)
	return err == nil && tag.RowsAffected() == 1, err
}

// AdminGrant gives an item to an account (audited).
func (s *Store) AdminGrant(ctx context.Context, actor, acct, itemID string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := lock(ctx, tx, acct); err != nil {
			return err
		}
		if _, err := s.grant(ctx, tx, acct, itemID, "admin", fmt.Sprintf("admin:%s:%s:%d", acct, itemID, s.Now().UnixNano())); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "item.grant", acct, fmt.Sprintf(`{"item":%q}`, itemID))
	})
}

// Revoke takes a granted item back, clears it from the equipped slots and optionally refunds
// what it cost (admin).
func (s *Store) Revoke(ctx context.Context, actor string, redemption int64, refund bool) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var acct, item, state string
		var price int
		if err := tx.QueryRow(ctx, `SELECT account_id, item_id, price, state FROM redemptions WHERE id=$1 FOR UPDATE`, redemption).Scan(&acct, &item, &price, &state); err == pgx.ErrNoRows {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if state == "revoked" {
			return nil
		}
		if _, err := lock(ctx, tx, acct); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE redemptions SET state='revoked' WHERE id=$1`, redemption); err != nil {
			return err
		}
		var left int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM redemptions WHERE account_id=$1 AND item_id=$2 AND state='granted'`, acct, item).Scan(&left); err != nil {
			return err
		}
		if left == 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM equipped WHERE account_id=$1 AND item_id=$2`, acct, item); err != nil {
				return err
			}
		}
		if refund && price > 0 {
			if _, err := credit(ctx, tx, acct, int64(price), "reverse", "revoke", item, "revoke:"+strconv.FormatInt(redemption, 10), actor); err != nil {
				return err
			}
		}
		return audit(ctx, tx, actor, "redemption.revoke", acct, fmt.Sprintf(`{"redemption":%d,"item":%q,"refund":%t}`, redemption, item, refund))
	})
}

type InventoryItem struct {
	Item
	RedemptionID int64     `json:"redemption_id"`
	Source       string    `json:"source"`
	AcquiredAt   time.Time `json:"acquired_at"`
	Equipped     bool      `json:"equipped"`
}

func (s *Store) Inventory(ctx context.Context, acct string) ([]InventoryItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+itemCols+`, r.id, r.source, r.created_at, EXISTS (SELECT 1 FROM equipped e WHERE e.account_id=r.account_id AND e.item_id=i.id)
		FROM redemptions r JOIN items i ON i.id=r.item_id WHERE r.account_id=$1 AND r.state='granted' ORDER BY r.id DESC`, acct)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InventoryItem{}
	for rows.Next() {
		var x InventoryItem
		if err := rows.Scan(&x.ID, &x.Kind, &x.Name, &x.Description, &x.Price, &x.Platforms, &x.Stock, &x.AvailableFrom, &x.AvailableUntil, &x.LimitPerAccount, &x.License, &x.State, &x.Limited, &x.HasPreview,
			&x.RedemptionID, &x.Source, &x.AcquiredAt, &x.Equipped); err != nil {
			return nil, err
		}
		x.Owned = true
		out = append(out, x)
	}
	return out, rows.Err()
}

// Equip puts an owned item in the slot named after its kind.
func (s *Store) Equip(ctx context.Context, acct, slot, itemID string) error {
	if !slices.Contains(Kinds, slot) {
		return invalid("unknown slot")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		a, err := lock(ctx, tx, acct)
		if err != nil {
			return err
		}
		if a.Frozen {
			return ErrFrozen
		}
		var kind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM items WHERE id=$1`, itemID).Scan(&kind); err == pgx.ErrNoRows {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if kind != slot {
			return invalid("that item does not fit this slot")
		}
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM redemptions WHERE account_id=$1 AND item_id=$2 AND state='granted')`, acct, itemID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `INSERT INTO equipped (account_id, slot, item_id) VALUES ($1,$2,$3) ON CONFLICT (account_id, slot) DO UPDATE SET item_id=$3`, acct, slot, itemID)
		return err
	})
}

func (s *Store) Unequip(ctx context.Context, acct, slot string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM equipped WHERE account_id=$1 AND slot=$2`, acct, slot)
	return err
}

// Equipped is the account's items per slot.
func (s *Store) Equipped(ctx context.Context, acct string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT slot, item_id FROM equipped WHERE account_id=$1`, acct)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// Showcase is what a public profile may show: equipped items, only when the owner opted in.
func (s *Store) Showcase(ctx context.Context, acct string) (map[string]string, error) {
	var pub, frozen bool
	err := s.pool.QueryRow(ctx, `SELECT showcase_public, frozen FROM accounts WHERE account_id=$1`, acct).Scan(&pub, &frozen)
	if err == pgx.ErrNoRows || (err == nil && (!pub || frozen)) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := s.Equipped(ctx, acct)
	if err != nil {
		return nil, err
	}
	// Only presentation slots are public: never a console icon.
	for k := range m {
		if k != "badge" && k != "profile_icon" && k != "title" {
			delete(m, k)
		}
	}
	return m, nil
}

type Entitlement struct {
	Item     Item   `json:"item"`
	Equipped bool   `json:"equipped"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	Content  string `json:"content_type"`
}

// Entitlements is what an adapter asks: the owned items deliverable on its platform, with the
// asset digest it should fetch. A frozen account has none.
func (s *Store) Entitlements(ctx context.Context, acct, platform string) ([]Entitlement, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+itemCols+`, a.sha256, a.bytes, a.content_type, EXISTS (SELECT 1 FROM equipped e WHERE e.account_id=r.account_id AND e.item_id=i.id)
		FROM redemptions r JOIN items i ON i.id=r.item_id JOIN item_assets a ON a.item_id=i.id AND a.role='asset' AND a.platform=$2
		JOIN accounts ac ON ac.account_id=r.account_id AND NOT ac.frozen
		WHERE r.account_id=$1 AND r.state='granted' ORDER BY i.id`, acct, platform)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entitlement{}
	seen := map[string]bool{}
	for rows.Next() {
		var e Entitlement
		it := &e.Item
		if err := rows.Scan(&it.ID, &it.Kind, &it.Name, &it.Description, &it.Price, &it.Platforms, &it.Stock, &it.AvailableFrom, &it.AvailableUntil, &it.LimitPerAccount, &it.License, &it.State, &it.Limited, &it.HasPreview,
			&e.SHA256, &e.Bytes, &e.Content, &e.Equipped); err != nil {
			return nil, err
		}
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		it.Owned = true
		out = append(out, e)
	}
	return out, rows.Err()
}

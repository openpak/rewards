package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Game cosmetics (openpak/prds#141): items of kind game_cosmetic. OpenPak hosts no art for them (licence
// game-native); each stands for one in-game id of one hosted game. Ownership is an ordinary redemption.

const KindGame = "game_cosmetic"

var (
	gameIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}$`)
	gameSlotRe = regexp.MustCompile(`^[a-z0-9_]{1,24}$`)
	// A game slot is its slot name, optionally with a position ("emote:1").
	equipSlotRe = regexp.MustCompile(`^([a-z0-9_]{1,24})(:[1-9])?$`)
	slugRe      = regexp.MustCompile(`[^a-z0-9]+`)

	Rarities = []string{"common", "rare", "epic", "legendary", "event"}
	Origins  = []string{"base", "earned", "store", "event", "promo", "openpak"}
)

// DefaultPrice is the starting cost in points by rarity (plan section 2.5). Event and base items are not sold.
func DefaultPrice(rarity, origin string) *int {
	if origin == "base" || origin == "event" || rarity == "event" {
		return nil
	}
	p := map[string]int{"common": 30, "rare": 80, "epic": 200, "legendary": 400}[rarity]
	if p == 0 {
		return nil
	}
	return &p
}

// GameInfo is the game side of a game cosmetic item.
type GameInfo struct {
	GameID      string     `json:"game_id"`
	GameRef     string     `json:"game_ref"`
	Slot        string     `json:"slot"`
	Rarity      string     `json:"rarity"`
	Origin      string     `json:"origin"`
	Deliverable bool       `json:"deliverable"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
}

// GameItemIn is one row of a game's catalogue import.
type GameItemIn struct {
	GameRef     string   `json:"game_ref"`
	Slot        string   `json:"slot"`
	Name        string   `json:"name"`
	Rarity      string   `json:"rarity"`
	Origin      string   `json:"origin"`
	Deliverable *bool    `json:"deliverable"`
	Price       *int     `json:"price"`
	Platforms   []string `json:"platforms"`
}

type ImportResult struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

// GameItemID derives the item id: <game>-<slot>-<slug of the in-game id>, shortened with a hash when long.
func GameItemID(game, slot, ref string) string {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(ref), "-"), "-")
	id := game + "-" + slot + "-" + slug
	if len(id) > 63 || slug == "" {
		h := sha1.Sum([]byte(game + "\x00" + slot + "\x00" + ref))
		keep := 54
		if len(id) < keep {
			keep = len(id)
		}
		id = strings.TrimRight(id[:keep], "-") + "-" + hex.EncodeToString(h[:4])
	}
	return id
}

// ImportGameItems upserts a game's catalogue (admin). New rows are drafts; existing rows keep their state,
// price and stock unless the import carries a price. Rows missing from a later import are never removed.
func (s *Store) ImportGameItems(ctx context.Context, actor, game, catalogRev string, in []GameItemIn) (ImportResult, error) {
	var res ImportResult
	if !gameIDRe.MatchString(game) {
		return res, invalid("game must be 2-41 characters of a-z, 0-9 and -")
	}
	if len(in) == 0 || len(in) > 5000 {
		return res, invalid("send between 1 and 5000 items")
	}
	seen := map[string]bool{}
	for i, x := range in {
		if x.GameRef == "" || len(x.GameRef) > 120 {
			return res, invalid("item %d: game_ref is required (120 characters at most)", i)
		}
		if !gameSlotRe.MatchString(x.Slot) {
			return res, invalid("item %d: slot must be 1-24 characters of a-z, 0-9 and _", i)
		}
		if x.Rarity == "" {
			in[i].Rarity = "common"
		} else if !slices.Contains(Rarities, x.Rarity) {
			return res, invalid("item %d: unknown rarity %q", i, x.Rarity)
		}
		if x.Origin == "" {
			in[i].Origin = "earned"
		} else if !slices.Contains(Origins, x.Origin) {
			return res, invalid("item %d: unknown origin %q", i, x.Origin)
		}
		if x.Price != nil && (*x.Price < 0 || *x.Price > 100000) {
			return res, invalid("item %d: price out of range", i)
		}
		for _, p := range x.Platforms {
			if !slices.Contains(Platforms, p) {
				return res, invalid("item %d: unknown platform %q", i, p)
			}
		}
		if len(x.Name) > 80 {
			return res, invalid("item %d: name is 80 characters at most", i)
		}
		if k := x.Slot + "\x00" + x.GameRef; seen[k] {
			return res, invalid("item %d: %s/%s is listed twice", i, x.Slot, x.GameRef)
		} else {
			seen[k] = true
		}
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		for _, x := range in {
			x = withDefaults(x)
			var id, curRef string
			err := tx.QueryRow(ctx, `SELECT item_id FROM game_items WHERE game_id=$1 AND game_ref=$2`, game, x.GameRef).Scan(&id)
			if err != nil && err != pgx.ErrNoRows {
				return err
			}
			if err == pgx.ErrNoRows {
				id = GameItemID(game, x.Slot, x.GameRef)
				// Two refs can slug alike ("SKIN1" and "skin1"): the later one gets a hash.
				if e := tx.QueryRow(ctx, `SELECT game_ref FROM game_items WHERE item_id=$1`, id).Scan(&curRef); e == nil && curRef != x.GameRef {
					h := sha1.Sum([]byte(game + "\x00" + x.Slot + "\x00" + x.GameRef))
					id = strings.TrimRight(id[:min(len(id), 54)], "-") + "-" + hex.EncodeToString(h[:4])
				} else if e != nil && e != pgx.ErrNoRows {
					return e
				}
				price := x.Price
				if price == nil {
					price = DefaultPrice(x.Rarity, x.Origin)
				}
				plats := x.Platforms
				if plats == nil {
					plats = []string{}
				}
				name := x.Name
				if name == "" {
					name = x.GameRef
				}
				deliv := x.Deliverable != nil && *x.Deliverable
				if _, err := tx.Exec(ctx, `INSERT INTO items (id, kind, name, price, platforms, license, state) VALUES ($1,$2,$3,$4,$5,'game-native','draft')`,
					id, KindGame, name, price, plats); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO game_items (item_id, game_id, game_ref, game_slot, rarity, origin, deliverable, verified_at, catalog_rev)
					VALUES ($1,$2,$3,$4,$5,$6,$7,CASE WHEN $7 THEN now() END,$8)`, id, game, x.GameRef, x.Slot, x.Rarity, x.Origin, deliv, catalogRev); err != nil {
					return err
				}
				res.Created++
				continue
			}
			// Existing: refresh what the generator knows; keep what an admin set.
			var changed int64
			t, err := tx.Exec(ctx, `UPDATE game_items SET game_slot=$2, rarity=$3, origin=$4, catalog_rev=$5,
				deliverable = COALESCE($6, deliverable), verified_at = CASE WHEN $6 IS TRUE AND NOT deliverable THEN now() ELSE verified_at END
				WHERE item_id=$1 AND (game_slot,rarity,origin,catalog_rev,deliverable) IS DISTINCT FROM ($2,$3,$4,$5,COALESCE($6,deliverable))`,
				id, x.Slot, x.Rarity, x.Origin, catalogRev, x.Deliverable)
			if err != nil {
				return err
			}
			changed += t.RowsAffected()
			if x.Name != "" || x.Price != nil || x.Platforms != nil {
				t, err := tx.Exec(ctx, `UPDATE items SET name=COALESCE(NULLIF($2,''),name), price=CASE WHEN $3::int IS NULL THEN price ELSE $3 END,
					platforms=COALESCE($4,platforms) WHERE id=$1 AND (name,price,platforms) IS DISTINCT FROM
					(COALESCE(NULLIF($2,''),name), CASE WHEN $3::int IS NULL THEN price ELSE $3 END, COALESCE($4,platforms))`, id, x.Name, x.Price, x.Platforms)
				if err != nil {
					return err
				}
				changed += t.RowsAffected()
			}
			if changed > 0 {
				res.Updated++
			} else {
				res.Unchanged++
			}
		}
		return audit(ctx, tx, actor, "game.import", game, fmt.Sprintf(`{"rev":%q,"created":%d,"updated":%d,"unchanged":%d}`, catalogRev, res.Created, res.Updated, res.Unchanged))
	})
	return res, err
}

func withDefaults(x GameItemIn) GameItemIn {
	if x.Rarity == "" {
		x.Rarity = "common"
	}
	if x.Origin == "" {
		x.Origin = "earned"
	}
	return x
}

// SetDeliverable marks whether the game really has the item's asset installed (admin, after a test).
func (s *Store) SetDeliverable(ctx context.Context, actor, itemID string, deliverable bool) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		t, err := tx.Exec(ctx, `UPDATE game_items SET deliverable=$2, verified_at=CASE WHEN $2 THEN now() ELSE NULL END WHERE item_id=$1`, itemID, deliverable)
		if err != nil {
			return err
		}
		if t.RowsAffected() == 0 {
			return ErrNotFound
		}
		// Owners' adapters must notice at once.
		if _, err := tx.Exec(ctx, `UPDATE game_rev SET rev=rev+1 WHERE game_id=(SELECT game_id FROM game_items WHERE item_id=$1)`, itemID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO game_outbox (account_id, game_id) SELECT account_id, game_id FROM game_rev WHERE game_id=(SELECT game_id FROM game_items WHERE item_id=$1)`, itemID); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "game.deliverable", itemID, fmt.Sprintf(`{"deliverable":%t}`, deliverable))
	})
}

// attachGame fills Item.Game for game cosmetics.
func (s *Store) attachGame(ctx context.Context, items []Item) error {
	var ids []string
	idx := map[string][]int{}
	for i, it := range items {
		if it.Kind == KindGame {
			ids = append(ids, it.ID)
			idx[it.ID] = append(idx[it.ID], i)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT item_id, game_id, game_ref, game_slot, rarity, origin, deliverable, verified_at FROM game_items WHERE item_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var g GameInfo
		if err := rows.Scan(&id, &g.GameID, &g.GameRef, &g.Slot, &g.Rarity, &g.Origin, &g.Deliverable, &g.VerifiedAt); err != nil {
			return err
		}
		for _, i := range idx[id] {
			gi := g
			items[i].Game = &gi
		}
	}
	return rows.Err()
}

// bumpItem moves the account's revision for the game an item belongs to (no-op for other kinds).
func bumpItem(ctx context.Context, d q, acct, itemID string) error {
	if _, err := d.Exec(ctx, `INSERT INTO game_rev (account_id, game_id, rev) SELECT $1, game_id, 1 FROM game_items WHERE item_id=$2
		ON CONFLICT (account_id, game_id) DO UPDATE SET rev = game_rev.rev + 1`, acct, itemID); err != nil {
		return err
	}
	_, err := d.Exec(ctx, `INSERT INTO game_outbox (account_id, game_id) SELECT $1, game_id FROM game_items WHERE item_id=$2`, acct, itemID)
	return err
}

// GameRef is one in-game id and the slot it fills.
type GameRef struct {
	GameRef string `json:"game_ref"`
	Slot    string `json:"slot"`
}

type GameOwned struct {
	GameRef    string    `json:"game_ref"`
	Slot       string    `json:"slot"`
	ItemID     string    `json:"item"`
	Source     string    `json:"source"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// GameInventory is what an adapter asks for one account and game.
type GameInventory struct {
	Account  string            `json:"account,omitempty"`
	Balance  int64             `json:"balance"` // the account's points
	Game     string            `json:"game"`
	Rev      int64             `json:"rev"`
	Frozen   bool              `json:"frozen,omitempty"`
	Owned    []GameOwned       `json:"owned"`
	Equipped map[string]string `json:"equipped"`
	Base     []GameRef         `json:"base"`
}

// GameInventory returns the deliverable items the account owns, what it has equipped, and the base set every
// player has. A frozen (banned) account gets the base set only.
func (s *Store) GameInventory(ctx context.Context, acct, game string) (GameInventory, error) {
	out := GameInventory{Game: game, Owned: []GameOwned{}, Equipped: map[string]string{}, Base: []GameRef{}}
	if !gameIDRe.MatchString(game) {
		return out, invalid("unknown game")
	}
	var frozen bool
	err := s.pool.QueryRow(ctx, `SELECT frozen FROM accounts WHERE account_id=$1`, acct).Scan(&frozen)
	if err != nil && err != pgx.ErrNoRows {
		return out, err
	}
	out.Frozen = frozen
	if err := s.pool.QueryRow(ctx, `SELECT balance FROM accounts WHERE account_id=$1`, acct).Scan(&out.Balance); err != nil && err != pgx.ErrNoRows {
		return out, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT rev FROM game_rev WHERE account_id=$1 AND game_id=$2`, acct, game).Scan(&out.Rev); err != nil && err != pgx.ErrNoRows {
		return out, err
	}
	rows, err := s.pool.Query(ctx, `SELECT g.game_ref, g.game_slot FROM game_items g JOIN items i ON i.id=g.item_id
		WHERE g.game_id=$1 AND g.origin='base' AND g.deliverable AND i.state='active' ORDER BY g.game_slot, g.game_ref`, game)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var b GameRef
		if err := rows.Scan(&b.GameRef, &b.Slot); err != nil {
			rows.Close()
			return out, err
		}
		out.Base = append(out.Base, b)
	}
	rows.Close()
	if frozen {
		return out, nil
	}
	rows, err = s.pool.Query(ctx, `SELECT DISTINCT ON (g.item_id) g.game_ref, g.game_slot, g.item_id, r.source, r.created_at
		FROM redemptions r JOIN game_items g ON g.item_id=r.item_id
		WHERE r.account_id=$1 AND r.state='granted' AND g.game_id=$2 AND g.deliverable ORDER BY g.item_id, r.id`, acct, game)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var o GameOwned
		if err := rows.Scan(&o.GameRef, &o.Slot, &o.ItemID, &o.Source, &o.AcquiredAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Owned = append(out.Owned, o)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT e.slot, g.game_ref FROM game_equipped e JOIN game_items g ON g.item_id=e.item_id
		WHERE e.account_id=$1 AND e.game_id=$2 AND g.deliverable`, acct, game)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var slot, ref string
		if err := rows.Scan(&slot, &ref); err != nil {
			return out, err
		}
		out.Equipped[slot] = ref
	}
	return out, rows.Err()
}

// GameEquip records what the player has in a game slot ("skin", "emote:2"). ref "" clears the slot. The item
// must be one the account owns, or a base item of the game; its own slot name must match. The adapter calls this
// when the game client reports an equip; Rewards, not the game, decides what is allowed.
func (s *Store) GameEquip(ctx context.Context, acct, game, slot, ref string) error {
	m := equipSlotRe.FindStringSubmatch(slot)
	if m == nil || !gameIDRe.MatchString(game) {
		return invalid("slot must look like skin or emote:1")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		a, err := lock(ctx, tx, acct)
		if err != nil {
			return err
		}
		if a.Frozen {
			return ErrFrozen
		}
		if ref == "" {
			if _, err := tx.Exec(ctx, `DELETE FROM game_equipped WHERE account_id=$1 AND game_id=$2 AND slot=$3`, acct, game, slot); err != nil {
				return err
			}
			return bumpGame(ctx, tx, acct, game)
		}
		var id, gslot, origin string
		var deliverable bool
		var state string
		err = tx.QueryRow(ctx, `SELECT g.item_id, g.game_slot, g.origin, g.deliverable, i.state FROM game_items g JOIN items i ON i.id=g.item_id
			WHERE g.game_id=$1 AND g.game_ref=$2`, game, ref).Scan(&id, &gslot, &origin, &deliverable, &state)
		if err == pgx.ErrNoRows {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if !deliverable || gslot != m[1] {
			return ErrNotFound
		}
		if !(origin == "base" && state == "active") {
			var own bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM redemptions WHERE account_id=$1 AND item_id=$2 AND state='granted')`, acct, id).Scan(&own); err != nil {
				return err
			}
			if !own {
				return ErrNotFound
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO game_equipped (account_id, game_id, slot, item_id) VALUES ($1,$2,$3,$4)
			ON CONFLICT (account_id, game_id, slot) DO UPDATE SET item_id=$4`, acct, game, slot, id); err != nil {
			return err
		}
		return bumpGame(ctx, tx, acct, game)
	})
}

func bumpGame(ctx context.Context, d q, acct, game string) error {
	if _, err := d.Exec(ctx, `INSERT INTO game_rev (account_id, game_id, rev) VALUES ($1,$2,1)
		ON CONFLICT (account_id, game_id) DO UPDATE SET rev = game_rev.rev + 1`, acct, game); err != nil {
		return err
	}
	_, err := d.Exec(ctx, `INSERT INTO game_outbox (account_id, game_id) VALUES ($1,$2)`, acct, game)
	return err
}

// Announcement is one change waiting to be told to the account core.
type Announcement struct {
	ID      int64
	Account string
	Game    string
}

// PendingAnnouncements returns up to limit waiting changes, one per (account, game), oldest first. Done removes
// every row up to and including the ids it is given for that pair.
func (s *Store) PendingAnnouncements(ctx context.Context, limit int) ([]Announcement, error) {
	rows, err := s.pool.Query(ctx, `SELECT max(id), account_id, game_id FROM game_outbox GROUP BY account_id, game_id ORDER BY max(id) LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Account, &a.Game); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AnnouncementDone drops the waiting rows for the pair up to the id that was announced.
func (s *Store) AnnouncementDone(ctx context.Context, a Announcement) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM game_outbox WHERE account_id=$1 AND game_id=$2 AND id <= $3`, a.Account, a.Game, a.ID)
	return err
}

// GameItemRef finds the item for an in-game id; ErrNotFound when the game has none.
func (s *Store) GameItemRef(ctx context.Context, game, ref string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT item_id FROM game_items WHERE game_id=$1 AND game_ref=$2`, game, ref).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", ErrNotFound
	}
	return id, err
}

// GameShopItem is something a game's own shop screen may list: on sale now, deliverable, with a price in points.
type GameShopItem struct {
	Game    string `json:"game"`
	GameRef string `json:"game_ref"`
	Slot    string `json:"slot"`
	Name    string `json:"name"`
	Price   int    `json:"price"`
	Owned   bool   `json:"owned,omitempty"`
}

// GameShop lists a game's items on sale (every game's when game is empty). With acct set, items the account already owns are marked.
func (s *Store) GameShop(ctx context.Context, acct, game string) ([]GameShopItem, error) {
	items, err := s.Catalog(ctx, acct, KindGame, "", false)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	out := []GameShopItem{}
	for _, it := range items {
		if it.Game == nil || (game != "" && it.Game.GameID != game) || !it.Game.Deliverable || !it.onSale(now) {
			continue
		}
		out = append(out, GameShopItem{Game: it.Game.GameID, GameRef: it.Game.GameRef, Slot: it.Game.Slot, Name: it.Name, Price: *it.Price, Owned: it.Owned})
	}
	return out, nil
}

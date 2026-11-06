package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func yes() *bool { b := true; return &b }

func importDemo(t *testing.T, s *Store) {
	t.Helper()
	res, err := s.ImportGameItems(context.Background(), admin, "stumble-guys", "r1", []GameItemIn{
		{GameRef: "SKIN1", Slot: "skin", Name: "Default", Origin: "base", Deliverable: yes()},
		{GameRef: "SKIN2", Slot: "skin", Name: "Blue", Rarity: "rare", Deliverable: yes()},
		{GameRef: "SKIN3", Slot: "skin", Name: "Remote only", Rarity: "epic"},
		{GameRef: "emote_gg", Slot: "emote", Rarity: "common", Deliverable: yes()},
	})
	if err != nil || res.Created != 4 {
		t.Fatalf("import: %+v %v", res, err)
	}
}

func TestGameImportIsIdempotentAndDerivesIDs(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	importDemo(t, s)
	res, err := s.ImportGameItems(ctx, admin, "stumble-guys", "r1", []GameItemIn{{GameRef: "SKIN2", Slot: "skin", Rarity: "rare", Origin: "earned", Deliverable: yes()}})
	if err != nil || res.Unchanged != 1 || res.Created != 0 {
		t.Fatalf("re-import: %+v %v", res, err)
	}
	it, err := s.GetItem(ctx, "stumble-guys-skin-skin2")
	if err != nil || it.License != "game-native" || it.State != "draft" || it.Price == nil || *it.Price != 80 {
		t.Fatalf("item: %+v %v", it, err)
	}
	if _, err := s.GetItem(ctx, "stumble-guys-emote-emote-gg"); err != nil {
		t.Fatal(err)
	}
	// Refs that slug alike do not collide.
	if _, err := s.ImportGameItems(ctx, admin, "stumble-guys", "r2", []GameItemIn{{GameRef: "skin2", Slot: "skin"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM game_items WHERE game_id='stumble-guys' AND game_slot='skin'`).Scan(&n)
	if n != 4 {
		t.Fatalf("skins = %d, want 4", n)
	}
	if _, err := s.ImportGameItems(ctx, admin, "Bad Game", "r", []GameItemIn{{GameRef: "a", Slot: "skin"}}); err == nil {
		t.Fatal("bad game id accepted")
	}
	if err := s.UpsertItem(ctx, admin, Item{ID: "x-gc", Kind: KindGame, Name: "x"}); err == nil {
		t.Fatal("game cosmetic created outside the import")
	}
}

// withArt gives each item its own picture: a game item cannot go live without one.
func withArt(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, _, err := s.PutAsset(context.Background(), admin, id, "web", "preview", "image/png", strings.NewReader("png-"+id)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGameCosmeticNeedsDeliverableToGoLive(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	importDemo(t, s)
	if err := s.SetItemState(ctx, admin, "stumble-guys-skin-skin3", "active"); err == nil {
		t.Fatal("undeliverable item went live")
	}
	if err := s.SetItemState(ctx, admin, "stumble-guys-skin-skin2", "active"); err == nil {
		t.Fatal("an item without its own picture went live")
	}
	withArt(t, s, "stumble-guys-skin-skin2", "stumble-guys-skin-skin3")
	if err := s.SetItemState(ctx, admin, "stumble-guys-skin-skin2", "active"); err != nil {
		t.Fatalf("deliverable item: %v", err)
	}
	if err := s.SetDeliverable(ctx, admin, "stumble-guys-skin-skin3", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, admin, "stumble-guys-skin-skin3", "active"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeliverable(ctx, admin, "nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
}

func TestGameBuyEquipRevokeAndErase(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	importDemo(t, s)
	withArt(t, s, "stumble-guys-skin-skin1", "stumble-guys-skin-skin2", "stumble-guys-emote-emote-gg")
	for _, id := range []string{"stumble-guys-skin-skin1", "stumble-guys-skin-skin2", "stumble-guys-emote-emote-gg"} {
		if err := s.SetItemState(ctx, admin, id, "active"); err != nil {
			t.Fatal(err)
		}
	}
	inv, err := s.GameInventory(ctx, alice, "stumble-guys")
	if err != nil || len(inv.Owned) != 0 || len(inv.Base) != 1 || inv.Base[0].GameRef != "SKIN1" || inv.Rev != 0 {
		t.Fatalf("start: %+v %v", inv, err)
	}
	// The base item can be equipped by anyone; an unowned one cannot.
	if err := s.GameEquip(ctx, alice, "stumble-guys", "skin", "SKIN1"); err != nil {
		t.Fatalf("base equip: %v", err)
	}
	if err := s.GameEquip(ctx, alice, "stumble-guys", "skin", "SKIN2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unowned equip: %v", err)
	}
	if err := s.GameEquip(ctx, alice, "stumble-guys", "emote:1", "SKIN1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong slot: %v", err)
	}
	// Buy: 80 points.
	if _, err := s.Redeem(ctx, alice, "stumble-guys-skin-skin2", ""); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("poor: %v", err)
	}
	s.Adjust(ctx, admin, alice, 100, "test")
	r, err := s.Redeem(ctx, alice, "stumble-guys-skin-skin2", "")
	if err != nil || r.Balance != 20 {
		t.Fatalf("redeem: %+v %v", r, err)
	}
	if _, err := s.Redeem(ctx, alice, "stumble-guys-skin-skin1", ""); !errors.Is(err, ErrNotForSale) {
		t.Fatalf("base item sold: %v", err)
	}
	inv, _ = s.GameInventory(ctx, alice, "stumble-guys")
	if len(inv.Owned) != 1 || inv.Owned[0].GameRef != "SKIN2" || inv.Owned[0].Source != "shop" || inv.Equipped["skin"] != "SKIN1" {
		t.Fatalf("after buy: %+v", inv)
	}
	rev := inv.Rev
	if err := s.GameEquip(ctx, alice, "stumble-guys", "skin", "SKIN2"); err != nil {
		t.Fatal(err)
	}
	inv, _ = s.GameInventory(ctx, alice, "stumble-guys")
	if inv.Equipped["skin"] != "SKIN2" || inv.Rev <= rev {
		t.Fatalf("equipped: %+v (rev was %d)", inv, rev)
	}
	// Another game's inventory is separate.
	other, _ := s.GameInventory(ctx, alice, "fall-guys")
	if len(other.Owned) != 0 || other.Rev != 0 {
		t.Fatalf("other game: %+v", other)
	}
	// Every change is waiting to be announced to the core, once per (account, game).
	pend, err := s.PendingAnnouncements(ctx, 10)
	if err != nil || len(pend) != 1 || pend[0].Account != alice || pend[0].Game != "stumble-guys" {
		t.Fatalf("outbox: %+v %v", pend, err)
	}
	if err := s.AnnouncementDone(ctx, pend[0]); err != nil {
		t.Fatal(err)
	}
	if pend, _ = s.PendingAnnouncements(ctx, 10); len(pend) != 0 {
		t.Fatalf("outbox after done: %+v", pend)
	}
	// A frozen account gets the base set only.
	s.SetFrozen(ctx, alice, true, "core")
	inv, _ = s.GameInventory(ctx, alice, "stumble-guys")
	if len(inv.Owned) != 0 || len(inv.Equipped) != 0 || len(inv.Base) != 1 || !inv.Frozen {
		t.Fatalf("frozen: %+v", inv)
	}
	if err := s.GameEquip(ctx, alice, "stumble-guys", "skin", "SKIN1"); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen equip: %v", err)
	}
	s.SetFrozen(ctx, alice, false, "core")
	// Revoke with refund clears the equip and moves the revision.
	inv, _ = s.GameInventory(ctx, alice, "stumble-guys")
	rev = inv.Rev
	if err := s.Revoke(ctx, admin, r.ID, true); err != nil {
		t.Fatal(err)
	}
	inv, _ = s.GameInventory(ctx, alice, "stumble-guys")
	if len(inv.Owned) != 0 || inv.Equipped["skin"] != "" || inv.Rev <= rev {
		t.Fatalf("after revoke: %+v", inv)
	}
	if b := balance(t, s, alice); b != 100 {
		t.Fatalf("refund: balance %d", b)
	}
	// An admin grant (as a mission or event reward would) shows up; erasing the account removes everything.
	if err := s.AdminGrant(ctx, admin, alice, "stumble-guys-emote-emote-gg"); err != nil {
		t.Fatal(err)
	}
	if err := s.GameEquip(ctx, alice, "stumble-guys", "emote:2", "emote_gg"); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx, alice); err != nil {
		t.Fatal(err)
	}
	var n int
	s.pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM game_outbox WHERE account_id=$1)+(SELECT COUNT(*) FROM game_equipped WHERE account_id=$1)+(SELECT COUNT(*) FROM game_rev WHERE account_id=$1)+(SELECT COUNT(*) FROM redemptions WHERE account_id=$1)`, alice).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows survived the purge", n)
	}
}

func TestGameDefaultPrice(t *testing.T) {
	for _, c := range []struct {
		rarity, origin string
		want           int
	}{{"common", "earned", 30}, {"rare", "store", 80}, {"epic", "earned", 200}, {"legendary", "earned", 400}, {"event", "event", -1}, {"common", "base", -1}} {
		p := DefaultPrice(c.rarity, c.origin)
		if (c.want < 0) != (p == nil) || (p != nil && *p != c.want) {
			t.Errorf("%s/%s: %v", c.rarity, c.origin, p)
		}
	}
	if id := GameItemID("g", "skin", "A Very Long Reference Name With Many Words That Goes On And On And On Forever"); len(id) > 63 {
		t.Errorf("id too long: %s", id)
	}
}

func TestGameChestNeedsWhatTheGameNeeds(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	res, err := s.ImportGameItems(ctx, admin, "dst", "r1", []GameItemIn{
		{GameRef: "pack_character_wanda", Slot: "pack", Name: "Wanda", Rarity: "epic", Origin: "store", Deliverable: yes(), Grants: []string{"wanda_none", "wanda_formal"}},
		{GameRef: "pack_wanda_nature", Slot: "pack", Name: "Wanda Nature", Rarity: "epic", Origin: "store", Deliverable: yes(), Grants: []string{"wanda_naturep"}, Requires: []string{"wanda_none"}},
		{GameRef: "pack_wilson_x", Slot: "pack", Name: "Wilson X", Rarity: "epic", Origin: "store", Deliverable: yes()},
		{GameRef: "wilson_gold", Slot: "skin", Name: "Wilson Gold", Rarity: "event", Origin: "event", Deliverable: yes()},
	})
	if err != nil || res.Created != 4 {
		t.Fatalf("import: %+v %v", res, err)
	}
	ids := []string{"dst-pack-pack-character-wanda", "dst-pack-pack-wanda-nature", "dst-pack-pack-wilson-x"}
	withArt(t, s, append(ids, "dst-skin-wilson-gold")...)
	for _, id := range ids {
		if err := s.SetItemState(ctx, admin, id, "active"); err != nil {
			t.Fatal(err)
		}
	}
	// What the game hands out by playing is never put on sale.
	if err := s.SetItemState(ctx, admin, "dst-skin-wilson-gold", "active"); err == nil {
		t.Fatal("an event item went live")
	}
	s.Adjust(ctx, admin, alice, 1000, "test")
	if _, err := s.Redeem(ctx, alice, "dst-pack-pack-wanda-nature", ""); !errors.Is(err, ErrRequires) {
		t.Fatalf("Wanda chest without Wanda: %v", err)
	}
	if _, err := s.Redeem(ctx, alice, "dst-pack-pack-wilson-x", ""); err != nil {
		t.Fatalf("a chest with no requirement: %v", err)
	}
	if _, err := s.Redeem(ctx, alice, "dst-pack-pack-character-wanda", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(ctx, alice, "dst-pack-pack-wanda-nature", ""); err != nil {
		t.Fatalf("Wanda chest after Wanda: %v", err)
	}
	// Someone else still cannot.
	s.Adjust(ctx, admin, bob, 1000, "test")
	if _, err := s.Redeem(ctx, bob, "dst-pack-pack-wanda-nature", ""); !errors.Is(err, ErrRequires) {
		t.Fatalf("bob: %v", err)
	}
	shop, err := s.GameShop(ctx, bob, "dst")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range shop {
		if (it.GameRef == "pack_wanda_nature") != (len(it.Locked) == 1 && it.Locked[0] == "wanda_none") {
			t.Fatalf("locked flag wrong on %+v", it)
		}
	}
}

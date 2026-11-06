package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/openpak/rewards/internal/store"
)

func TestGameAdapterRoutes(t *testing.T) {
	srv, _ := setup(t)
	a := map[string]string{"X-Admin-Key": akey, "X-Admin-Actor": admin}
	in := map[string]string{"X-Internal-Key": ikey}
	code, m := do(t, "POST", srv.URL+"/internal/v1/admin/games/stumble-guys/import", a, map[string]any{"catalog_rev": "r1", "items": []map[string]any{
		{"game_ref": "SKIN1", "slot": "skin", "origin": "base", "deliverable": true},
		{"game_ref": "SKIN2", "slot": "skin", "rarity": "rare", "deliverable": true},
	}})
	if code != 200 || m["created"] != float64(2) {
		t.Fatalf("import: %d %v", code, m)
	}
	putArt(t, srv.URL, a, "stumble-guys-skin-skin1", "stumble-guys-skin-skin2")
	do(t, "POST", srv.URL+"/internal/v1/admin/items/stumble-guys-skin-skin1/state", a, map[string]string{"state": "active"})
	if code, _ = do(t, "GET", srv.URL+"/internal/v1/adapter/inventory?account="+alice+"&game=stumble-guys", nil, nil); code != 401 {
		t.Fatalf("no key: %d", code)
	}
	code, m = do(t, "GET", srv.URL+"/internal/v1/adapter/inventory?account="+alice+"&game=stumble-guys", in, nil)
	if code != 200 || len(m["base"].([]any)) != 1 || len(m["owned"].([]any)) != 0 {
		t.Fatalf("inventory: %d %v", code, m)
	}
	// Not owned: 409 with the state; base: fine.
	body := map[string]string{"account": alice, "game": "stumble-guys", "slot": "skin", "game_ref": "SKIN2"}
	if code, m = do(t, "PUT", srv.URL+"/internal/v1/adapter/equipped", in, body); code != http.StatusConflict || m["inventory"] == nil {
		t.Fatalf("unowned: %d %v", code, m)
	}
	body["game_ref"] = "SKIN1"
	if code, m = do(t, "PUT", srv.URL+"/internal/v1/adapter/equipped", in, body); code != 200 || m["equipped"].(map[string]any)["skin"] != "SKIN1" {
		t.Fatalf("base equip: %d %v", code, m)
	}
	// Revalidation by ETag.
	req, _ := http.NewRequest("GET", srv.URL+"/internal/v1/adapter/inventory?account="+alice+"&game=stumble-guys", nil)
	req.Header.Set("X-Internal-Key", ikey)
	resp, _ := http.DefaultClient.Do(req)
	tag := resp.Header.Get("ETag")
	resp.Body.Close()
	req.Header.Set("If-None-Match", tag)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if tag == "" || resp.StatusCode != http.StatusNotModified {
		t.Fatalf("etag %q -> %d", tag, resp.StatusCode)
	}

	// A game server holds the events key and names the player by network identity; an unlinked identity owns nothing.
	ev := map[string]string{"X-Internal-Key": ekey}
	do(t, "POST", srv.URL+"/internal/v1/admin/items/stumble-guys-skin-skin2/state", a, map[string]string{"state": "active"})
	do(t, "POST", srv.URL+"/internal/v1/admin/accounts/"+alice+"/grant", a, map[string]string{"item_id": "stumble-guys-skin-skin2"})
	code, m = do(t, "GET", srv.URL+"/internal/v1/adapter/inventory?namespace=wiiu&subject=1234&game=stumble-guys", ev, nil)
	owned, _ := m["owned"].([]any)
	if code != 200 || m["account"] != alice || len(owned) != 1 || owned[0].(map[string]any)["game_ref"] != "SKIN2" {
		t.Fatalf("by identity: %d %v", code, m)
	}
	code, m = do(t, "GET", srv.URL+"/internal/v1/adapter/inventory?namespace=wiiu&subject=9999&game=stumble-guys", ev, nil)
	owned, _ = m["owned"].([]any)
	if code != 200 || len(owned) != 0 || m["account"] != nil {
		t.Fatalf("unlinked: %d %v", code, m)
	}
	if code, _ = do(t, "GET", srv.URL+"/internal/v1/adapter/inventory?namespace=wiiu&subject=1234&game=stumble-guys", map[string]string{"X-Internal-Key": "wrong-key-wrong-key-wrong-key-wrong"}, nil); code != 401 {
		t.Fatalf("wrong key: %d", code)
	}
}

// A game's own shop lists the items on sale with the player's points, and buys with the same ledger as the website.
func TestGameShopAndRedeem(t *testing.T) {
	srv, st := setup(t)
	a := map[string]string{"X-Admin-Key": akey, "X-Admin-Actor": admin}
	ev := map[string]string{"X-Internal-Key": ekey}
	price := 80
	do(t, "POST", srv.URL+"/internal/v1/admin/games/dst/import", a, map[string]any{"catalog_rev": "r", "items": []map[string]any{
		{"game_ref": "pack_x", "slot": "pack", "name": "X Chest", "rarity": "rare", "deliverable": true, "price": price},
		{"game_ref": "pack_hidden", "slot": "pack", "name": "Not yet", "rarity": "rare", "deliverable": true}}})
	putArt(t, srv.URL, a, "dst-pack-pack-x")
	do(t, "POST", srv.URL+"/internal/v1/admin/items/dst-pack-pack-x/state", a, map[string]string{"state": "active"})
	q := "?namespace=wiiu&subject=1234&game=dst"
	code, m := do(t, "GET", srv.URL+"/internal/v1/adapter/shop"+q, ev, nil)
	items, _ := m["items"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["game_ref"] != "pack_x" || items[0].(map[string]any)["price"] != float64(80) {
		t.Fatalf("shop: %d %v", code, m)
	}
	if code, m = do(t, "GET", srv.URL+"/internal/v1/adapter/shop?namespace=wiiu&subject=1234", ev, nil); code != 200 || len(m["items"].([]any)) != 1 || m["items"].([]any)[0].(map[string]any)["game"] != "dst" {
		t.Fatalf("all games: %d %v", code, m)
	}
	buy := func(extra map[string]any) (int, map[string]any) {
		body := map[string]any{"namespace": "wiiu", "subject": "1234", "game": "dst", "game_ref": "pack_x", "trans_id": "t1"}
		for k, v := range extra {
			body[k] = v
		}
		return do(t, "POST", srv.URL+"/internal/v1/adapter/redeem", ev, body)
	}
	if code, m = buy(nil); code != 402 || m["code"] != "insufficient" || m["balance"] != float64(0) {
		t.Fatalf("poor: %d %v", code, m)
	}
	st.Adjust(context.Background(), admin, alice, 100, "test")
	if code, m = buy(map[string]any{"expected_price": 50}); code != 409 || m["code"] != "price_mismatch" {
		t.Fatalf("price mismatch: %d %v", code, m)
	}
	if code, m = buy(map[string]any{"expected_price": 80}); code != 200 || m["balance"] != float64(20) {
		t.Fatalf("buy: %d %v", code, m)
	}
	if code, m = buy(map[string]any{"trans_id": "t1"}); code != 200 || m["balance"] != float64(20) {
		t.Fatalf("a retried purchase is charged once: %d %v", code, m)
	}
	if code, m = buy(map[string]any{"trans_id": "t2"}); code != 409 || m["code"] != "owned" {
		t.Fatalf("again: %d %v", code, m)
	}
	// One ledger: the game's purchase is in the inventory the game reads back, and the balance is the same points.
	code, m = do(t, "GET", srv.URL+"/internal/v1/adapter/inventory"+q, ev, nil)
	owned, _ := m["owned"].([]any)
	if code != 200 || len(owned) != 1 || m["balance"] != float64(20) {
		t.Fatalf("inventory after: %d %v", code, m)
	}
	if code, m = do(t, "POST", srv.URL+"/internal/v1/adapter/redeem", ev, map[string]any{"namespace": "wiiu", "subject": "9999", "game": "dst", "game_ref": "pack_x"}); code != 404 {
		t.Fatalf("unlinked: %d %v", code, m)
	}
}

// putArt gives each item its own picture: a game item cannot go live without one.
func putArt(t *testing.T, base string, a map[string]string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		h := map[string]string{"Content-Type": "image/png"}
		for k, v := range a {
			h[k] = v
		}
		if code, m := do(t, "PUT", base+"/internal/v1/admin/items/"+id+"/asset?platform=web&role=preview", h, []byte("png")); code != 200 {
			t.Fatalf("art %s: %d %v", id, code, m)
		}
	}
}

// The game's own rule over the adapter: a chest with Wanda skins is refused until a chest that gives Wanda is owned, and the
// buyer's inventory then lists both chests.
func TestGameRedeemFollowsTheGamesRequirements(t *testing.T) {
	srv, st := setup(t)
	a := map[string]string{"X-Admin-Key": akey, "X-Admin-Actor": admin}
	ev := map[string]string{"X-Internal-Key": ekey}
	do(t, "POST", srv.URL+"/internal/v1/admin/games/dst/import", a, map[string]any{"catalog_rev": "r", "items": []map[string]any{
		{"game_ref": "pack_character_wanda", "slot": "pack", "name": "Wanda", "rarity": "epic", "origin": "store", "deliverable": true, "grants": []string{"wanda_none"}},
		{"game_ref": "pack_wanda_x", "slot": "pack", "name": "Wanda X", "rarity": "epic", "origin": "store", "deliverable": true, "requires": []string{"wanda_none"}}}})
	putArt(t, srv.URL, a, "dst-pack-pack-character-wanda", "dst-pack-pack-wanda-x")
	for _, id := range []string{"dst-pack-pack-character-wanda", "dst-pack-pack-wanda-x"} {
		if code, m := do(t, "POST", srv.URL+"/internal/v1/admin/items/"+id+"/state", a, map[string]string{"state": "active"}); code != 204 {
			t.Fatalf("activate %s: %d %v", id, code, m)
		}
	}
	st.Adjust(context.Background(), admin, alice, 1000, "test")
	buy := func(ref, trans string) (int, map[string]any) {
		return do(t, "POST", srv.URL+"/internal/v1/adapter/redeem", ev, map[string]any{"namespace": "wiiu", "subject": "1234", "game": "dst", "game_ref": ref, "trans_id": trans})
	}
	if code, m := buy("pack_wanda_x", "a"); code != 409 || m["code"] != "requires" {
		t.Fatalf("without Wanda: %d %v", code, m)
	}
	code, m := do(t, "GET", srv.URL+"/internal/v1/adapter/shop?namespace=wiiu&subject=1234&game=dst", ev, nil)
	locked := 0
	for _, it := range m["items"].([]any) {
		if l, _ := it.(map[string]any)["locked"].([]any); len(l) == 1 && l[0] == "wanda_none" {
			locked++
		}
	}
	if code != 200 || locked != 1 {
		t.Fatalf("shop should mark the Wanda chest locked once: %d %v", code, m)
	}
	if code, m := buy("pack_character_wanda", "b"); code != 200 {
		t.Fatalf("Wanda: %d %v", code, m)
	}
	if code, m := buy("pack_wanda_x", "c"); code != 200 {
		t.Fatalf("with Wanda: %d %v", code, m)
	}
	code, m = do(t, "GET", srv.URL+"/internal/v1/adapter/inventory?namespace=wiiu&subject=1234&game=dst", ev, nil)
	if owned, _ := m["owned"].([]any); code != 200 || len(owned) != 2 {
		t.Fatalf("inventory: %d %v", code, m)
	}
}

// The whole Don't Starve Together chest catalogue (REWARDS_DST_CATALOG = klei tools/catalog/dst-packs-import.json): every
// chest the game sold can be activated, none of the picture-less gift chests is on sale, the game's rule holds for each
// purchase (a chest with a locked character's skins is refused until a chest giving the character is owned), and once
// everything is bought the player's inventory holds every chest. Skipped when the file is not given.
func TestDSTWholeCatalogueFollowsTheGamesRules(t *testing.T) {
	path := os.Getenv("REWARDS_DST_CATALOG")
	if path == "" {
		t.Skip("REWARDS_DST_CATALOG not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Items []struct {
			GameRef  string   `json:"game_ref"`
			Origin   string   `json:"origin"`
			Grants   []string `json:"grants"`
			Requires []string `json:"requires"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatal(err)
	}
	srv, st := setup(t)
	a := map[string]string{"X-Admin-Key": akey, "X-Admin-Actor": admin}
	ev := map[string]string{"X-Internal-Key": ekey}
	var imp struct {
		Rev   string            `json:"catalog_rev"`
		Items []json.RawMessage `json:"items"`
	}
	json.Unmarshal(raw, &imp)
	for i := 0; i < len(imp.Items); i += 40 { // the admin body limit is 64 KiB
		if code, m := do(t, "POST", srv.URL+"/internal/v1/admin/games/dst/import", a, map[string]any{"catalog_rev": "r", "items": imp.Items[i:min(i+40, len(imp.Items))]}); code != 200 {
			t.Fatalf("import: %d %v", code, m)
		}
	}
	idOf := func(ref string) string { return store.GameItemID("dst", "pack", ref) }
	var sold, gifts []string
	for _, it := range cat.Items {
		if it.Origin == "store" {
			sold = append(sold, it.GameRef)
		} else {
			gifts = append(gifts, it.GameRef)
		}
	}
	for _, ref := range sold {
		putArt(t, srv.URL, a, idOf(ref))
		if code, m := do(t, "POST", srv.URL+"/internal/v1/admin/items/"+idOf(ref)+"/state", a, map[string]string{"state": "active"}); code != 204 {
			t.Fatalf("activate %s: %d %v", ref, code, m)
		}
	}
	for _, ref := range gifts { // no picture, earned: they cannot go on sale
		if code, _ := do(t, "POST", srv.URL+"/internal/v1/admin/items/"+idOf(ref)+"/state", a, map[string]string{"state": "active"}); code == 204 {
			t.Fatalf("gift chest %s went live", ref)
		}
	}
	for i := 0; i < 100; i++ { // an admin adjustment is bounded; the chests cost about 90,000 points in all
		if _, err := st.Adjust(context.Background(), admin, alice, 1000, "test"); err != nil {
			t.Fatal(err)
		}
	}
	held := map[string]bool{} // in-game keys the player holds
	bought := map[string]bool{}
	for pass := 0; pass < 6 && len(bought) < len(sold); pass++ {
		for _, it := range cat.Items {
			if it.Origin != "store" || bought[it.GameRef] {
				continue
			}
			ok := true
			for _, r := range it.Requires {
				ok = ok && held[r]
			}
			code, m := do(t, "POST", srv.URL+"/internal/v1/adapter/redeem", ev, map[string]any{"namespace": "wiiu", "subject": "1234", "game": "dst", "game_ref": it.GameRef, "trans_id": "t-" + it.GameRef})
			if ok && code != 200 {
				t.Fatalf("%s should be buyable: %d %v", it.GameRef, code, m)
			}
			if !ok && (code != 409 || m["code"] != "requires") {
				t.Fatalf("%s needs %v first: %d %v", it.GameRef, it.Requires, code, m)
			}
			if code == 200 {
				bought[it.GameRef] = true
				for _, g := range it.Grants {
					held[g] = true
				}
			}
		}
	}
	if len(bought) != len(sold) {
		t.Fatalf("bought %d of %d chests", len(bought), len(sold))
	}
	code, m := do(t, "GET", srv.URL+"/internal/v1/adapter/inventory?namespace=wiiu&subject=1234&game=dst", ev, nil)
	if owned, _ := m["owned"].([]any); code != 200 || len(owned) != len(sold) {
		t.Fatalf("inventory holds %d of %d chests (%d)", len(owned), len(sold), code)
	}
}

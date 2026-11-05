package api

import (
	"context"
	"net/http"
	"testing"
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
	do(t, "POST", srv.URL+"/internal/v1/admin/items/dst-pack-pack-x/state", a, map[string]string{"state": "active"})
	q := "?namespace=wiiu&subject=1234&game=dst"
	code, m := do(t, "GET", srv.URL+"/internal/v1/adapter/shop"+q, ev, nil)
	items, _ := m["items"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["game_ref"] != "pack_x" || items[0].(map[string]any)["price"] != float64(80) {
		t.Fatalf("shop: %d %v", code, m)
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

package api

import (
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

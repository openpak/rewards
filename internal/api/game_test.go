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
}

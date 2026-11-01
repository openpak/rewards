package api

import (
	"context"
	"testing"

	"github.com/openpak/rewards/internal/core"
)

type fakeDir struct{}

func (fakeDir) Resolve(_ context.Context, ns, sub string) (string, error) {
	switch ns + "/" + sub {
	case "wiiu/1234":
		return alice, nil
	case "wiiu/5678":
		return bob, nil
	}
	return "", core.ErrNoLink
}
func (fakeDir) Friends(_ context.Context, a string) (map[string]bool, error) {
	if a == alice {
		return map[string]bool{bob: true}, nil
	}
	return map[string]bool{}, nil
}
func (fakeDir) ResolveHandle(_ context.Context, h string) (string, error) {
	if h == "bobby" {
		return bob, nil
	}
	return "", core.ErrNoLink
}

func results(body map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range body["results"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

func TestEventsEndpoint(t *testing.T) {
	srv, _ := setup(t)
	u := srv.URL + "/internal/v1/events"
	eh := map[string]string{"X-Internal-Key": ekey}
	ev := map[string]any{"event_id": "e1", "source": "t", "metric": "kart_races_completed.mkwii", "delta": 1, "identity": map[string]string{"namespace": "wiiu", "subject": "1234"}}

	if c, _ := do(t, "POST", u, nil, ev); c != 401 {
		t.Fatalf("no key: %d", c)
	}
	if c, _ := do(t, "POST", u, map[string]string{"X-Internal-Key": "x"}, ev); c != 401 {
		t.Fatalf("bad key: %d", c)
	}
	if c, _ := do(t, "POST", u, map[string]string{"X-Admin-Key": akey}, ev); c != 401 {
		t.Fatalf("admin key is not an event key: %d", c)
	}
	// A user session cannot count anything.
	if c, _ := do(t, "POST", u, map[string]string{"Authorization": "Bearer tok-alice"}, ev); c != 401 {
		t.Fatalf("a user bearer must not post events: %d", c)
	}
	c, body := do(t, "POST", u, eh, ev)
	if c != 200 || results(body)[0]["status"] != "counted" {
		t.Fatalf("%d %v", c, body)
	}
	if _, body = do(t, "POST", u, eh, ev); results(body)[0]["status"] != "duplicate" {
		t.Fatalf("retry: %v", body)
	}
	// The general internal key also works.
	if c, _ := do(t, "POST", u, map[string]string{"X-Internal-Key": ikey}, map[string]any{"event_id": "e2", "source": "t", "metric": "trades_completed", "delta": 1, "account_id": alice}); c != 200 {
		t.Fatalf("internal key %d", c)
	}
	// Batch: one good, one unknown identity, one bad metric. Each answered on its own.
	_, body = do(t, "POST", u, eh, map[string]any{"events": []map[string]any{
		{"event_id": "b1", "source": "t", "metric": "trades_completed", "delta": 1, "account_id": alice},
		{"event_id": "b2", "source": "t", "metric": "trades_completed", "delta": 1, "identity": map[string]string{"namespace": "wiiu", "subject": "9999"}},
		{"event_id": "b3", "source": "t", "metric": "nonsense", "delta": 1, "account_id": alice},
	}})
	r := results(body)
	if r[0]["status"] != "counted" || r[1]["status"] != "unresolved" || r[2]["status"] != "rejected" {
		t.Fatalf("batch: %v", body)
	}
	// Players met: only a real friend counts; identities resolve to accounts.
	_, body = do(t, "POST", u, eh, map[string]any{"event_id": "f1", "source": "t", "metric": "friends_played_with", "account_id": alice,
		"key_identity": []map[string]string{{"namespace": "wiiu", "subject": "5678"}, {"namespace": "wiiu", "subject": "9999"}}})
	if results(body)[0]["status"] != "counted" {
		t.Fatalf("%v", body)
	}
	_, me := do(t, "GET", srv.URL+"/v1/me/metrics", map[string]string{"Authorization": "Bearer tok-alice"}, nil)
	counts := me["counts"].(map[string]any)
	if counts["kart_races_completed.mkwii"] != 1.0 || counts["trades_completed"] != 2.0 || counts["friends_played_with"] != 1.0 {
		t.Fatalf("counts: %v", counts)
	}
}

func TestGrantEndpointIsAdminOnly(t *testing.T) {
	srv, _ := setup(t)
	u := srv.URL
	ah := map[string]string{"X-Admin-Key": akey, "X-Admin-Actor": admin}
	do(t, "POST", u+"/internal/v1/admin/items", ah, map[string]any{"id": "badge-ev", "kind": "badge", "name": "Ev"})
	body := map[string]any{"kind": "event", "item_id": "badge-ev", "reason": "launch", "handles": []string{"@bobby", "ghost"}, "account_ids": []string{alice}}
	if c, _ := do(t, "POST", u+"/internal/v1/admin/grants", map[string]string{"X-Internal-Key": ikey}, body); c != 401 {
		t.Fatalf("website key must not grant: %d", c)
	}
	if c, _ := do(t, "POST", u+"/internal/v1/admin/grants", map[string]string{"X-Internal-Key": ekey}, body); c != 401 {
		t.Fatalf("events key must not grant: %d", c)
	}
	c, got := do(t, "POST", u+"/internal/v1/admin/grants", ah, body)
	if c != 200 || len(results(got)) != 2 || len(got["unknown"].([]any)) != 1 {
		t.Fatalf("%d %v", c, got)
	}
	_, inv := do(t, "GET", u+"/internal/v1/admin/grants?item_id=badge-ev", ah, nil)
	if len(inv["grants"].([]any)) != 2 {
		t.Fatalf("grants list %v", inv)
	}
}

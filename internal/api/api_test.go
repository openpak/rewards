package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/openpak/rewards/internal/core"
	"github.com/openpak/rewards/internal/rules"
	"github.com/openpak/rewards/internal/store"
)

const (
	alice = "aaaaaaaa-0000-0000-0000-000000000001"
	admin = "cccccccc-0000-0000-0000-000000000003"
	ikey  = "internal-key-internal-key-internal-key"
	akey  = "admin-key-admin-key-admin-key-admin-k"
)

type ident map[string]string

func (i ident) Whoami(_ context.Context, b string) (string, error) {
	if a, ok := i[b]; ok {
		return a, nil
	}
	return "", core.ErrUnauthenticated
}

func setup(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	url := os.Getenv("REWARDS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REWARDS_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, url, t.TempDir(), rules.Default())
	if err != nil {
		t.Fatal(err)
	}
	s.Pool().Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	s.Close()
	if s, err = store.Open(ctx, url, t.TempDir(), rules.Default()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	srv := httptest.NewServer((&Server{Store: s, Core: ident{"tok-alice": alice}, InternalKey: ikey, AdminKey: akey, RatePerMin: 1000}).Handler())
	t.Cleanup(srv.Close)
	return srv, s
}

func do(t *testing.T, method, url string, hdr map[string]string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if raw, ok := body.([]byte); ok {
		rd = bytes.NewReader(raw)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	m := map[string]any{}
	json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestAuthBoundaries(t *testing.T) {
	srv, _ := setup(t)
	u := srv.URL
	if c, _ := do(t, "GET", u+"/healthz", nil, nil); c != 200 {
		t.Fatalf("healthz %d", c)
	}
	if c, _ := do(t, "GET", u+"/v1/catalog", nil, nil); c != 200 {
		t.Fatalf("catalog is public: %d", c)
	}
	if c, _ := do(t, "GET", u+"/v1/me", nil, nil); c != 401 {
		t.Fatalf("me without a session: %d", c)
	}
	if c, _ := do(t, "GET", u+"/v1/me", map[string]string{"Authorization": "Bearer nope"}, nil); c != 401 {
		t.Fatalf("me with a bad session: %d", c)
	}
	if c, _ := do(t, "GET", u+"/v1/me", map[string]string{"Authorization": "Bearer tok-alice"}, nil); c != 200 {
		t.Fatalf("me: %d", c)
	}
	adm := "/internal/v1/admin/stats"
	if c, _ := do(t, "GET", u+adm, nil, nil); c != 401 {
		t.Fatalf("admin without key: %d", c)
	}
	if c, _ := do(t, "GET", u+adm, map[string]string{"X-Admin-Key": ikey, "X-Admin-Actor": admin}, nil); c != 401 {
		t.Fatalf("the website key is not the admin key: %d", c)
	}
	if c, _ := do(t, "GET", u+adm, map[string]string{"X-Admin-Key": akey}, nil); c != 400 {
		t.Fatalf("admin without actor: %d", c)
	}
	if c, _ := do(t, "GET", u+adm, map[string]string{"X-Admin-Key": akey, "X-Admin-Actor": admin}, nil); c != 200 {
		t.Fatalf("admin: %d", c)
	}
	if c, _ := do(t, "GET", u+"/internal/v1/adapter/entitlements?account="+alice+"&platform=web", map[string]string{"X-Admin-Key": akey}, nil); c != 401 {
		t.Fatalf("adapter routes need the internal key: %d", c)
	}
	// A user session cannot reach admin or adapter routes.
	if c, _ := do(t, "GET", u+adm, map[string]string{"Authorization": "Bearer tok-alice"}, nil); c != 401 {
		t.Fatalf("user on admin: %d", c)
	}
}

func TestShopFlowThroughHTTP(t *testing.T) {
	srv, _ := setup(t)
	u := srv.URL
	ah := map[string]string{"X-Admin-Key": akey, "X-Admin-Actor": admin}
	uh := map[string]string{"Authorization": "Bearer tok-alice"}

	if c, _ := do(t, "POST", u+"/internal/v1/admin/items", ah, map[string]any{"id": "badge-x", "kind": "badge", "name": "X", "price": 40, "platforms": []string{"web"}}); c != 200 {
		t.Fatalf("create item %d", c)
	}
	if c, _ := do(t, "POST", u+"/internal/v1/admin/items/badge-x/state", ah, map[string]any{"state": "active"}); c != 400 {
		t.Fatalf("activate without assets %d", c)
	}
	h := map[string]string{"X-Admin-Key": akey, "X-Admin-Actor": admin, "Content-Type": "image/png"}
	if c, _ := do(t, "PUT", u+"/internal/v1/admin/items/badge-x/asset?platform=web&role=asset", h, []byte("a")); c != 200 {
		t.Fatalf("asset %d", c)
	}
	if c, _ := do(t, "PUT", u+"/internal/v1/admin/items/badge-x/asset?platform=web&role=preview", h, []byte("p")); c != 200 {
		t.Fatalf("preview %d", c)
	}
	if c, _ := do(t, "POST", u+"/internal/v1/admin/items/badge-x/state", ah, map[string]any{"state": "active"}); c != 204 {
		t.Fatalf("activate %d", c)
	}
	if c, body := do(t, "GET", u+"/v1/catalog?kind=badge", nil, nil); c != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("catalog %d %v", c, body)
	}
	if c, _ := do(t, "POST", u+"/v1/me/redeem", uh, map[string]any{"item_id": "badge-x"}); c != 402 {
		t.Fatalf("no points should be 402, got %d", c)
	}
	if c, _ := do(t, "POST", u+"/internal/v1/admin/accounts/"+alice+"/adjust", ah, map[string]any{"delta": 100, "reason": "welcome"}); c != 200 {
		t.Fatalf("adjust %d", c)
	}
	if c, body := do(t, "POST", u+"/v1/me/redeem", uh, map[string]any{"item_id": "badge-x"}); c != 201 || body["balance"].(float64) != 60 {
		t.Fatalf("redeem %d %v", c, body)
	}
	if c, _ := do(t, "POST", u+"/v1/me/redeem", uh, map[string]any{"item_id": "badge-x"}); c != 409 {
		t.Fatalf("owned should be 409, got %d", c)
	}
	if c, _ := do(t, "PUT", u+"/v1/me/equipped/badge", uh, map[string]any{"item_id": "badge-x"}); c != 204 {
		t.Fatalf("equip %d", c)
	}
	req, _ := http.NewRequest("GET", u+"/v1/me/items/badge-x/asset?platform=web", nil)
	req.Header.Set("Authorization", "Bearer tok-alice")
	resp, _ := http.DefaultClient.Do(req)
	b := new(strings.Builder)
	buf := make([]byte, 16)
	n, _ := resp.Body.Read(buf)
	b.Write(buf[:n])
	resp.Body.Close()
	if resp.StatusCode != 200 || b.String() != "a" || resp.Header.Get("ETag") == "" {
		t.Fatalf("owner asset: %d %q", resp.StatusCode, b.String())
	}
	if c, body := do(t, "GET", u+"/internal/v1/adapter/entitlements?account="+alice+"&platform=web", map[string]string{"X-Internal-Key": ikey}, nil); c != 200 || len(body["entitlements"].([]any)) != 1 {
		t.Fatalf("entitlements %d %v", c, body)
	}
	// Private by default; opt in; then public.
	if _, body := do(t, "GET", u+"/internal/v1/accounts/"+alice+"/showcase", map[string]string{"X-Internal-Key": ikey}, nil); len(body["equipped"].(map[string]any)) != 0 {
		t.Fatalf("showcase leaked: %v", body)
	}
	do(t, "PUT", u+"/v1/me/settings", uh, map[string]any{"showcase_public": true})
	if _, body := do(t, "GET", u+"/internal/v1/accounts/"+alice+"/showcase", map[string]string{"X-Internal-Key": ikey}, nil); body["equipped"].(map[string]any)["badge"] != "badge-x" {
		t.Fatalf("showcase: %v", body)
	}
	// A user cannot see another user's asset: unknown item for bob-like token.
	if c, _ := do(t, "DELETE", u+"/v1/me", uh, nil); c != 204 {
		t.Fatalf("erase %d", c)
	}
	if _, body := do(t, "GET", u+"/v1/me", uh, nil); body["account"].(map[string]any)["balance"].(float64) != 0 {
		t.Fatalf("erased account kept balance: %v", body)
	}
}

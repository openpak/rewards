// Package api is the rewards HTTP surface: user routes (/v1) for the website and the app, and
// internal routes (/internal/v1) for the website's admin and the platform adapters. The
// internal routes are not routed publicly. See docs/api.md.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openpak/rewards/internal/core"
	"github.com/openpak/rewards/internal/store"
)

type Identity interface {
	Whoami(ctx context.Context, bearer string) (string, error)
}

// Directory is the account core's identity side: network identities, friends, handles.
type Directory interface {
	Resolve(ctx context.Context, namespace, subject string) (string, error)
	Friends(ctx context.Context, account string) (map[string]bool, error)
	ResolveHandle(ctx context.Context, handle string) (string, error)
}

type Server struct {
	Store       *store.Store
	Core        Identity
	Dir         Directory
	EventsKey   string   // game servers (X-Internal-Key on POST /internal/v1/events only); the internal key also works
	InternalKey string   // website and adapters (X-Internal-Key)
	AdminKey    string   // admin routes (X-Admin-Key)
	CORS        []string // origins allowed to call /v1 from a browser
	RatePerMin  int

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	n     int
	reset time.Time
}

var idRe = regexp.MustCompile(`^[0-9a-f-]{8,64}$`)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })

	// Anonymous.
	mux.HandleFunc("GET /v1/catalog", s.catalog(false))
	mux.HandleFunc("GET /v1/missions", s.missions)
	mux.HandleFunc("GET /v1/items/{id}/preview", s.preview)

	// The signed-in user.
	u := func(p string, h func(http.ResponseWriter, *http.Request, string)) { mux.HandleFunc(p, s.user(h)) }
	u("GET /v1/me", s.me)
	u("GET /v1/me/ledger", s.ledger)
	u("GET /v1/me/missions", s.myMissions)
	u("POST /v1/me/missions/{id}/claim", s.claim)
	u("GET /v1/me/catalog", s.myCatalog)
	u("POST /v1/me/redeem", s.redeem)
	u("GET /v1/me/inventory", s.inventory)
	u("GET /v1/me/items/{id}/asset", s.myAsset)
	u("PUT /v1/me/equipped/{slot}", s.equip)
	u("DELETE /v1/me/equipped/{slot}", s.unequip)
	u("PUT /v1/me/settings", s.settings)
	u("DELETE /v1/me", s.erase)

	// Website and adapters.
	i := func(p string, h http.HandlerFunc) {
		mux.HandleFunc(p, s.keyed(s.InternalKey, "X-Internal-Key", false, h))
	}
	i("GET /internal/v1/accounts/{id}/showcase", s.showcase)
	i("GET /internal/v1/adapter/entitlements", s.entitlements)
	i("GET /internal/v1/adapter/items/{id}/asset", s.adapterAsset)

	// Game servers: the only way a count moves.
	mux.HandleFunc("POST /internal/v1/events", s.eventsAuth(s.events))
	mux.HandleFunc("GET /v1/metrics", s.metrics)
	u("GET /v1/me/metrics", s.myMetrics)

	// Admin.
	a := func(p string, h http.HandlerFunc) { mux.HandleFunc(p, s.keyed(s.AdminKey, "X-Admin-Key", true, h)) }
	a("GET /internal/v1/admin/items", s.catalog(true))
	a("POST /internal/v1/admin/items", s.adminItem)
	a("PUT /internal/v1/admin/items/{id}", s.adminItem)
	a("PUT /internal/v1/admin/items/{id}/asset", s.adminAsset)
	a("POST /internal/v1/admin/items/{id}/state", s.adminState)
	a("GET /internal/v1/admin/missions", s.adminMissions)
	a("POST /internal/v1/admin/missions", s.adminMission)
	a("PUT /internal/v1/admin/missions/{id}", s.adminMission)
	a("GET /internal/v1/admin/accounts/{id}", s.adminAccount)
	a("POST /internal/v1/admin/accounts/{id}/adjust", s.adminAdjust)
	a("POST /internal/v1/admin/accounts/{id}/freeze", s.adminFreeze(true))
	a("POST /internal/v1/admin/accounts/{id}/unfreeze", s.adminFreeze(false))
	a("POST /internal/v1/admin/accounts/{id}/grant", s.adminGrant)
	a("POST /internal/v1/admin/redemptions/{id}/revoke", s.adminRevoke)
	a("GET /internal/v1/admin/metrics", s.metrics)
	a("PUT /internal/v1/admin/metrics/{id}", s.adminMetric)
	a("POST /internal/v1/admin/accounts/{id}/metrics/{metric}/release", s.adminMetricHeld(true))
	a("POST /internal/v1/admin/accounts/{id}/metrics/{metric}/discard", s.adminMetricHeld(false))
	a("POST /internal/v1/admin/grants", s.adminGrants)
	a("GET /internal/v1/admin/grants", s.adminGrantList)
	a("GET /internal/v1/admin/flags", s.adminFlags)
	a("POST /internal/v1/admin/flags/{id}/resolve", s.adminResolve)
	a("GET /internal/v1/admin/audit", s.adminAudit)
	a("GET /internal/v1/admin/stats", s.adminStats)
	return s.cors(mux)
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && strings.HasPrefix(r.URL.Path, "/v1/") {
			for _, ok := range s.CORS {
				if ok == o {
					w.Header().Set("Access-Control-Allow-Origin", o)
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					break
				}
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func out(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func msg(w http.ResponseWriter, code int, m string) { out(w, code, map[string]any{"error": m}) }

func fail(w http.ResponseWriter, err error) {
	var inv store.InvalidError
	switch {
	case errors.As(err, &inv):
		msg(w, 400, inv.Msg)
	case errors.Is(err, store.ErrNotFound):
		msg(w, 404, "not found")
	case errors.Is(err, store.ErrInsufficient):
		msg(w, 402, "not enough points")
	case errors.Is(err, store.ErrFrozen):
		msg(w, 403, "this account's rewards are frozen")
	case errors.Is(err, store.ErrOwned):
		msg(w, 409, "already owned")
	case errors.Is(err, store.ErrSoldOut):
		msg(w, 409, "sold out")
	case errors.Is(err, store.ErrNotForSale):
		msg(w, 409, "not for sale")
	case errors.Is(err, store.ErrConflict):
		msg(w, 409, "conflict")
	default:
		log.Printf("rewards: %v", err)
		msg(w, 500, "rewards service error")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		msg(w, 400, "body is not the expected JSON object")
		return false
	}
	return true
}

// user resolves the bearer to the caller's account and rate-limits per account.
func (s *Server) user(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || bearer == "" {
			msg(w, 401, "sign in required")
			return
		}
		acct, err := s.Core.Whoami(r.Context(), bearer)
		if errors.Is(err, core.ErrUnauthenticated) {
			msg(w, 401, "sign in required")
			return
		}
		if err != nil {
			log.Printf("rewards: whoami: %v", err)
			msg(w, 502, "account service unavailable")
			return
		}
		acct = strings.ToLower(acct)
		if !s.allow(acct) {
			w.Header().Set("Retry-After", "30")
			msg(w, 429, "slow down")
			return
		}
		h(w, r, acct)
	}
}

func (s *Server) allow(key string) bool {
	limit := s.RatePerMin
	if limit <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buckets == nil {
		s.buckets = map[string]*bucket{}
	}
	now := time.Now()
	b := s.buckets[key]
	if b == nil || now.After(b.reset) {
		if len(s.buckets) > 20000 {
			s.buckets = map[string]*bucket{}
		}
		b = &bucket{reset: now.Add(time.Minute)}
		s.buckets[key] = b
	}
	b.n++
	return b.n <= limit
}

// keyed guards an internal route with a shared key; admin routes also need the acting admin's id.
func (s *Server) keyed(key, header string, needActor bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get(header)), []byte(key)) != 1 {
			msg(w, 401, "key required")
			return
		}
		if needActor && !idRe.MatchString(strings.ToLower(r.Header.Get("X-Admin-Actor"))) {
			msg(w, 400, "X-Admin-Actor (the acting admin's account id) is required")
			return
		}
		h(w, r)
	}
}

func actor(r *http.Request) string { return strings.ToLower(r.Header.Get("X-Admin-Actor")) }

func qint(r *http.Request, name string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return v
	}
	return def
}

// ---- anonymous ----

func (s *Server) catalog(all bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.Store.Catalog(r.Context(), "", r.URL.Query().Get("kind"), r.URL.Query().Get("platform"), all)
		if err != nil {
			fail(w, err)
			return
		}
		out(w, 200, map[string]any{"items": items})
	}
}

func (s *Server) missions(w http.ResponseWriter, r *http.Request) {
	ms, err := s.Store.ListMissions(r.Context(), false)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"missions": ms})
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, a store.Asset) {
	f, err := os.Open(a.Path)
	if err != nil {
		msg(w, 404, "not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("ETag", `"`+a.SHA256+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	it, err := s.Store.GetItem(r.Context(), r.PathValue("id"))
	if err != nil || it.State != "active" {
		msg(w, 404, "not found")
		return
	}
	a, err := s.Store.FindAsset(r.Context(), it.ID, "", "preview")
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	s.serveAsset(w, r, a)
}

// ---- user ----

func (s *Server) me(w http.ResponseWriter, r *http.Request, me string) {
	a, err := s.Store.Account(r.Context(), me)
	if err != nil {
		fail(w, err)
		return
	}
	t, err := s.Store.Today(r.Context(), me)
	if err != nil {
		fail(w, err)
		return
	}
	eq, err := s.Store.Equipped(r.Context(), me)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"account": a, "today": t, "equipped": eq})
}

func (s *Server) ledger(w http.ResponseWriter, r *http.Request, me string) {
	rows, err := s.Store.Ledger(r.Context(), me, int64(qint(r, "before", 0)), qint(r, "limit", 50))
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"entries": rows})
}

func (s *Server) myMissions(w http.ResponseWriter, r *http.Request, me string) {
	ms, err := s.Store.MyMissions(r.Context(), me)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"missions": ms})
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request, me string) {
	var in struct {
		Period string `json:"period"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	res, err := s.Store.Claim(r.Context(), me, r.PathValue("id"), in.Period)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, res)
}

func (s *Server) myCatalog(w http.ResponseWriter, r *http.Request, me string) {
	items, err := s.Store.Catalog(r.Context(), me, r.URL.Query().Get("kind"), r.URL.Query().Get("platform"), false)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"items": items})
}

func (s *Server) redeem(w http.ResponseWriter, r *http.Request, me string) {
	var in struct {
		ItemID string `json:"item_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	idem := r.Header.Get("Idempotency-Key")
	if len(idem) > 64 {
		msg(w, 400, "Idempotency-Key is at most 64 characters")
		return
	}
	res, err := s.Store.Redeem(r.Context(), me, in.ItemID, idem)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 201, res)
}

func (s *Server) inventory(w http.ResponseWriter, r *http.Request, me string) {
	inv, err := s.Store.Inventory(r.Context(), me)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"items": inv})
}

func (s *Server) myAsset(w http.ResponseWriter, r *http.Request, me string) {
	id := r.PathValue("id")
	if ok, err := s.Store.Owns(r.Context(), me, id); err != nil {
		fail(w, err)
		return
	} else if !ok {
		msg(w, 404, "not found")
		return
	}
	a, err := s.Store.FindAsset(r.Context(), id, r.URL.Query().Get("platform"), "asset")
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	s.serveAsset(w, r, a)
}

func (s *Server) equip(w http.ResponseWriter, r *http.Request, me string) {
	var in struct {
		ItemID string `json:"item_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Store.Equip(r.Context(), me, r.PathValue("slot"), in.ItemID); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unequip(w http.ResponseWriter, r *http.Request, me string) {
	if err := s.Store.Unequip(r.Context(), me, r.PathValue("slot")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, me string) {
	var in struct {
		ShowcasePublic *bool `json:"showcase_public"`
		Paused         *bool `json:"paused"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Store.SetSettings(r.Context(), me, in.ShowcasePublic, in.Paused); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) erase(w http.ResponseWriter, r *http.Request, me string) {
	if err := s.Store.Purge(r.Context(), me); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- website and adapters ----

func (s *Server) showcase(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.Showcase(r.Context(), strings.ToLower(r.PathValue("id")))
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"equipped": m})
}

func (s *Server) entitlements(w http.ResponseWriter, r *http.Request) {
	acct, platform := strings.ToLower(r.URL.Query().Get("account")), r.URL.Query().Get("platform")
	if !idRe.MatchString(acct) || platform == "" {
		msg(w, 400, "account and platform are required")
		return
	}
	es, err := s.Store.Entitlements(r.Context(), acct, platform)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"entitlements": es})
}

func (s *Server) adapterAsset(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.FindAsset(r.Context(), r.PathValue("id"), r.URL.Query().Get("platform"), "asset")
	if err != nil {
		fail(w, err)
		return
	}
	s.serveAsset(w, r, a)
}

// ---- admin ----

func (s *Server) adminItem(w http.ResponseWriter, r *http.Request) {
	var it store.Item
	if !decode(w, r, &it) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		it.ID = id
	}
	if err := s.Store.UpsertItem(r.Context(), actor(r), it); err != nil {
		fail(w, err)
		return
	}
	got, err := s.Store.GetItem(r.Context(), it.ID)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, got)
}

func (s *Server) adminAsset(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if ct == "" || len(ct) > 100 {
		msg(w, 400, "Content-Type is required")
		return
	}
	sha, n, err := s.Store.PutAsset(r.Context(), actor(r), r.PathValue("id"), r.URL.Query().Get("platform"), defRole(r.URL.Query().Get("role")), ct,
		io.Reader(http.MaxBytesReader(w, r.Body, store.MaxAssetBytes+1)))
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"sha256": sha, "bytes": n})
}

func defRole(r string) string {
	if r == "" {
		return "asset"
	}
	return r
}

func (s *Server) adminState(w http.ResponseWriter, r *http.Request) {
	var in struct {
		State string `json:"state"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Store.SetItemState(r.Context(), actor(r), r.PathValue("id"), in.State); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminMissions(w http.ResponseWriter, r *http.Request) {
	ms, err := s.Store.ListMissions(r.Context(), true)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"missions": ms})
}

func (s *Server) adminMission(w http.ResponseWriter, r *http.Request) {
	m := store.Mission{Active: true, Sort: 100}
	if !decode(w, r, &m) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		m.ID = id
	}
	if err := s.Store.UpsertMission(r.Context(), actor(r), m); err != nil {
		fail(w, err)
		return
	}
	out(w, 200, m)
}

func (s *Server) adminAccount(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.AccountDetail(r.Context(), strings.ToLower(r.PathValue("id")))
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, d)
}

func (s *Server) adminAdjust(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Delta  int64  `json:"delta"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	bal, err := s.Store.Adjust(r.Context(), actor(r), strings.ToLower(r.PathValue("id")), in.Delta, in.Reason)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"balance": bal})
}

func (s *Server) adminFreeze(frozen bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.SetFrozen(r.Context(), strings.ToLower(r.PathValue("id")), frozen, actor(r)); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) adminGrant(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID string `json:"item_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Store.AdminGrant(r.Context(), actor(r), strings.ToLower(r.PathValue("id")), in.ItemID); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		msg(w, 400, "redemption id must be a number")
		return
	}
	var in struct {
		Refund bool `json:"refund"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	if err := s.Store.Revoke(r.Context(), actor(r), id, in.Refund); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminFlags(w http.ResponseWriter, r *http.Request) {
	fs, err := s.Store.Flags(r.Context(), r.URL.Query().Get("all") != "1", qint(r, "limit", 100))
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"flags": fs})
}

func (s *Server) adminResolve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		msg(w, 400, "flag id must be a number")
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	if err := s.Store.ResolveFlag(r.Context(), actor(r), id, in.Note); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Audit(r.Context(), strings.ToLower(r.URL.Query().Get("target")), int64(qint(r, "before", 0)), qint(r, "limit", 100))
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"entries": rows})
}

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Stats(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, st)
}

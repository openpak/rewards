package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/openpak/rewards/internal/core"
	"github.com/openpak/rewards/internal/store"
)

// Game cosmetics for adapters (openpak/prds#141). A game server asks what an account owns and has equipped, and
// reports the player's equips; ownership is never decided outside Rewards.

// gameInventory: GET /internal/v1/adapter/inventory?account=&game=. The ETag follows the account's revision for
// the game, so an adapter can revalidate cheaply.
func (s *Server) gameInventory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	acct, ok := s.gameAccount(w, r, q.Get("account"), q.Get("namespace"), q.Get("subject"))
	game := q.Get("game")
	if !ok {
		return
	}
	if game == "" {
		msg(w, 400, "game is required")
		return
	}
	var inv store.GameInventory
	var err error
	if acct == "" {
		// A network identity nobody has linked to an OpenPak account owns nothing and has no equipped slots.
		inv, err = s.Store.GameInventory(r.Context(), "unlinked", game)
		inv.Owned, inv.Equipped = []store.GameOwned{}, map[string]string{}
	} else {
		inv, err = s.Store.GameInventory(r.Context(), acct, game)
	}
	if err != nil {
		fail(w, err)
		return
	}
	inv.Account = acct
	tag := fmt.Sprintf(`"%d-%t-%d-%s"`, inv.Rev, inv.Frozen, len(inv.Base), acct)
	w.Header().Set("ETag", tag)
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	out(w, 200, inv)
}

// gameAccount resolves who an adapter means: an account id, or a network identity (namespace and subject, as the events
// API takes them) resolved through the account core. It answers 400 itself and reports false when the request is bad;
// an identity with no link gives "" and true.
func (s *Server) gameAccount(w http.ResponseWriter, r *http.Request, account, namespace, subject string) (string, bool) {
	if account != "" {
		a := strings.ToLower(account)
		if !idRe.MatchString(a) {
			msg(w, 400, "account is not an account id")
			return "", false
		}
		return a, true
	}
	if namespace == "" || subject == "" || s.Dir == nil {
		msg(w, 400, "account, or namespace and subject, is required")
		return "", false
	}
	a, err := s.Dir.Resolve(r.Context(), namespace, subject)
	if errors.Is(err, core.ErrNoLink) {
		return "", true
	}
	if err != nil {
		log.Printf("rewards: resolve %s identity: %v", namespace, err)
		msg(w, 502, "account core unavailable")
		return "", false
	}
	return strings.ToLower(a), true
}

// gameEquip: PUT /internal/v1/adapter/equipped. An item the account does not own answers 409 with the current
// state, so the adapter can correct the client.
func (s *Server) gameEquip(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Account   string `json:"account"`
		Namespace string `json:"namespace"`
		Subject   string `json:"subject"`
		Game      string `json:"game"`
		Slot      string `json:"slot"`
		GameRef   string `json:"game_ref"`
	}
	if !decode(w, r, &in) {
		return
	}
	acct, ok := s.gameAccount(w, r, in.Account, in.Namespace, in.Subject)
	if !ok {
		return
	}
	if acct == "" {
		msg(w, 404, "this identity is not linked to an OpenPak account")
		return
	}
	if in.Game == "" || in.Slot == "" {
		msg(w, 400, "game and slot are required")
		return
	}
	err := s.Store.GameEquip(r.Context(), acct, in.Game, in.Slot, in.GameRef)
	if errors.Is(err, store.ErrNotFound) {
		inv, e := s.Store.GameInventory(r.Context(), acct, in.Game)
		if e != nil {
			fail(w, e)
			return
		}
		out(w, 409, map[string]any{"error": "not owned", "inventory": inv})
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	inv, err := s.Store.GameInventory(r.Context(), acct, in.Game)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, inv)
}

// adminGameImport: POST /internal/v1/admin/games/{game}/import {catalog_rev, items:[...]}.
func (s *Server) adminGameImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CatalogRev string             `json:"catalog_rev"`
		Items      []store.GameItemIn `json:"items"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := s.Store.ImportGameItems(r.Context(), actor(r), r.PathValue("game"), in.CatalogRev, in.Items)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, res)
}

// adminDeliverable: POST /internal/v1/admin/items/{id}/deliverable {deliverable}.
func (s *Server) adminDeliverable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Deliverable bool `json:"deliverable"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Store.SetDeliverable(r.Context(), actor(r), r.PathValue("id"), in.Deliverable); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

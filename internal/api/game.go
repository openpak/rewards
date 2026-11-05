package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openpak/rewards/internal/store"
)

// Game cosmetics for adapters (openpak/prds#141). A game server asks what an account owns and has equipped, and
// reports the player's equips; ownership is never decided outside Rewards.

// gameInventory: GET /internal/v1/adapter/inventory?account=&game=. The ETag follows the account's revision for
// the game, so an adapter can revalidate cheaply.
func (s *Server) gameInventory(w http.ResponseWriter, r *http.Request) {
	acct, game := strings.ToLower(r.URL.Query().Get("account")), r.URL.Query().Get("game")
	if !idRe.MatchString(acct) || game == "" {
		msg(w, 400, "account and game are required")
		return
	}
	inv, err := s.Store.GameInventory(r.Context(), acct, game)
	if err != nil {
		fail(w, err)
		return
	}
	tag := fmt.Sprintf(`"%d-%t-%d"`, inv.Rev, inv.Frozen, len(inv.Base))
	w.Header().Set("ETag", tag)
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	out(w, 200, inv)
}

// gameEquip: PUT /internal/v1/adapter/equipped. An item the account does not own answers 409 with the current
// state, so the adapter can correct the client.
func (s *Server) gameEquip(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Account string `json:"account"`
		Game    string `json:"game"`
		Slot    string `json:"slot"`
		GameRef string `json:"game_ref"`
	}
	if !decode(w, r, &in) {
		return
	}
	acct := strings.ToLower(in.Account)
	if !idRe.MatchString(acct) || in.Game == "" || in.Slot == "" {
		msg(w, 400, "account, game and slot are required")
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

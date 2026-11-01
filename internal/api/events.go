package api

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/openpak/rewards/internal/core"
	"github.com/openpak/rewards/internal/store"
)

// eventsAuth accepts the events key (game servers) or the general internal key.
func (s *Server) eventsAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("X-Internal-Key"))
		okEv := s.EventsKey != "" && subtle.ConstantTimeCompare(got, []byte(s.EventsKey)) == 1
		okIn := s.InternalKey != "" && subtle.ConstantTimeCompare(got, []byte(s.InternalKey)) == 1
		if !okEv && !okIn {
			msg(w, 401, "key required")
			return
		}
		h(w, r)
	}
}

type identity struct {
	Namespace string `json:"namespace"`
	Subject   string `json:"subject"`
}

type eventIn struct {
	EventID     string     `json:"event_id"`
	Source      string     `json:"source"`
	Metric      string     `json:"metric"`
	Delta       int        `json:"delta"`
	AccountID   string     `json:"account_id"`
	Identity    *identity  `json:"identity"`
	Keys        []string   `json:"keys"`         // distinct keys: course ids, tournament ids, other players' account ids
	KeyIdentity []identity `json:"key_identity"` // other players as network identities; resolved to account ids and added to keys
	OccurredAt  *time.Time `json:"occurred_at"`
}

type eventOut struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"` // counted duplicate held ignored unresolved rejected
	Error   string `json:"error,omitempty"`
}

// events takes one event object or {"events":[...]} (at most 100). Each is answered on its
// own: a bad event never fails its neighbours, and retries are safe (event_id dedupes).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	var in struct {
		eventIn
		Events []eventIn `json:"events"`
	}
	if !decode(w, r, &in) {
		return
	}
	evs := in.Events
	if len(evs) == 0 {
		evs = []eventIn{in.eventIn}
	}
	if len(evs) > 100 {
		msg(w, 400, "at most 100 events per request")
		return
	}
	res := make([]eventOut, 0, len(evs))
	retry := false
	for _, e := range evs {
		o := eventOut{EventID: e.EventID}
		st, err := s.oneEvent(r, e)
		var inv store.InvalidError
		switch {
		case err == nil:
			o.Status = st
		case errors.Is(err, core.ErrNoLink):
			o.Status = "unresolved"
		case errors.As(err, &inv):
			o.Status, o.Error = "rejected", inv.Msg
		default:
			log.Printf("rewards: event %s: %v", e.EventID, err)
			retry = true
			o.Status, o.Error = "error", "try again"
		}
		res = append(res, o)
	}
	code := 200
	if retry {
		code = 503 // the emitter retries the whole batch; counted ones answer duplicate
	}
	out(w, code, map[string]any{"results": res})
}

func (s *Server) oneEvent(r *http.Request, e eventIn) (string, error) {
	acct := strings.ToLower(e.AccountID)
	ctx := r.Context()
	if acct == "" {
		if e.Identity == nil || e.Identity.Namespace == "" || e.Identity.Subject == "" {
			return "", store.InvalidError{Msg: "account_id or identity is required"}
		}
		if s.Dir == nil {
			return "", errors.New("no directory")
		}
		a, err := s.Dir.Resolve(ctx, e.Identity.Namespace, e.Identity.Subject)
		if err != nil {
			return "", err
		}
		acct = strings.ToLower(a)
	} else if !idRe.MatchString(acct) {
		return "", store.InvalidError{Msg: "account_id is not an account id"}
	}
	keys := e.Keys
	if s.Dir != nil {
		for _, k := range e.KeyIdentity {
			if a, err := s.Dir.Resolve(ctx, k.Namespace, k.Subject); err == nil {
				keys = append(keys, strings.ToLower(a))
			} else if !errors.Is(err, core.ErrNoLink) {
				return "", err
			}
		}
	}
	var friends map[string]bool
	ms, err := s.metricByID(r, e.Metric)
	if err != nil {
		return "", err
	}
	if ms.RequireFriend && s.Dir != nil {
		if friends, err = s.Dir.Friends(ctx, acct); err != nil {
			return "", err
		}
	}
	if e.Delta == 0 {
		e.Delta = 1 // an event with no delta is one occurrence
	}
	ev := store.Event{EventID: e.EventID, Source: e.Source, AccountID: acct, Metric: e.Metric, Delta: e.Delta, Keys: keys}
	if e.OccurredAt != nil {
		ev.OccurredAt = *e.OccurredAt
	}
	return s.Store.Ingest(ctx, ev, friends)
}

func (s *Server) metricByID(r *http.Request, id string) (store.Metric, error) {
	all, err := s.Store.Metrics(r.Context())
	if err != nil {
		return store.Metric{}, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return store.Metric{}, store.InvalidError{Msg: "unknown metric"}
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	ms, err := s.Store.Metrics(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"metrics": ms})
}

func (s *Server) myMetrics(w http.ResponseWriter, r *http.Request, acct string) {
	m, err := s.Store.MyMetrics(r.Context(), acct)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"counts": m})
}

func (s *Server) adminMetric(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DailyCap int64 `json:"daily_cap"`
		MaxDelta int   `json:"max_delta"`
		Enabled  bool  `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Store.SetMetric(r.Context(), actor(r), r.PathValue("id"), in.DailyCap, in.MaxDelta, in.Enabled); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminMetricHeld(release bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct, metric := strings.ToLower(r.PathValue("id")), r.PathValue("metric")
		var n int64
		var err error
		if release {
			var k int
			k, err = s.Store.ReleaseHeld(r.Context(), actor(r), acct, metric)
			n = int64(k)
		} else {
			n, err = s.Store.DiscardHeld(r.Context(), actor(r), acct, metric)
		}
		if err != nil {
			fail(w, err)
			return
		}
		out(w, 200, map[string]any{"events": n})
	}
}

// adminGrants gives an event or supporter item to many people: by account id or handle.
func (s *Server) adminGrants(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind       string   `json:"kind"`
		ItemID     string   `json:"item_id"`
		Reason     string   `json:"reason"`
		AccountIDs []string `json:"account_ids"`
		Handles    []string `json:"handles"`
	}
	if !decode(w, r, &in) {
		return
	}
	seen := map[string]bool{}
	var accts []string
	unknown := []string{}
	for _, a := range in.AccountIDs {
		a = strings.ToLower(strings.TrimSpace(a))
		if !idRe.MatchString(a) {
			unknown = append(unknown, a)
		} else if !seen[a] {
			seen[a] = true
			accts = append(accts, a)
		}
	}
	for _, h := range in.Handles {
		h = strings.TrimSpace(strings.TrimPrefix(h, "@"))
		if s.Dir == nil {
			msg(w, 400, "handles cannot be resolved here")
			return
		}
		a, err := s.Dir.ResolveHandle(r.Context(), h)
		if errors.Is(err, core.ErrNoLink) {
			unknown = append(unknown, h)
			continue
		}
		if err != nil {
			log.Printf("rewards: resolve handle: %v", err)
			msg(w, 502, "account service unavailable")
			return
		}
		a = strings.ToLower(a)
		if !seen[a] {
			seen[a] = true
			accts = append(accts, a)
		}
	}
	res, err := s.Store.GrantBulk(r.Context(), actor(r), in.Kind, in.ItemID, in.Reason, accts)
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"results": res, "unknown": unknown})
}

func (s *Server) adminGrantList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Grants(r.Context(), r.URL.Query().Get("item_id"), qint(r, "limit", 100))
	if err != nil {
		fail(w, err)
		return
	}
	out(w, 200, map[string]any{"grants": rows})
}

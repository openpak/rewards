package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func ev(id, metric string, acct string) Event {
	e := Event{EventID: id, Source: "test", AccountID: acct, Metric: metric, Delta: 1}
	if strings.Contains(metric, "_sessions") { // a distinct metric: one key per session
		e.Delta, e.Keys = 0, []string{id}
	}
	return e
}

func count(t *testing.T, s *Store, acct, metric string) int64 {
	t.Helper()
	m, err := s.MyMetrics(context.Background(), acct)
	if err != nil {
		t.Fatal(err)
	}
	return m[metric]
}

func activate(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := s.pool.Exec(context.Background(), `UPDATE missions SET active=true WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEventIsIdempotent(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for i, want := range []string{EvCounted, EvDuplicate, EvDuplicate} {
		got, err := s.Ingest(ctx, ev("e1", "kart_sessions.mk8dx", alice), nil)
		if err != nil || got != want {
			t.Fatalf("try %d: %s %v, want %s", i, got, err, want)
		}
	}
	if n := count(t, s, alice, "kart_sessions.mk8dx"); n != 1 {
		t.Fatalf("count %d", n)
	}
}

func TestEventValidation(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	bad := []Event{
		{EventID: "", Source: "x", AccountID: alice, Metric: "trades_completed", Delta: 1},
		{EventID: "a", Source: "x", AccountID: alice, Metric: "nope", Delta: 1},
		{EventID: "b", Source: "x", AccountID: alice, Metric: "trades_completed", Delta: 0},
		{EventID: "c", Source: "x", AccountID: alice, Metric: "trades_completed", Delta: 2},
		{EventID: "d", Source: "x", AccountID: alice, Metric: "maker_courses_played"},
		{EventID: "e", Source: "x", AccountID: alice, Metric: "trades_completed", Delta: 1, OccurredAt: c.now().AddDate(0, 0, -30)},
		{EventID: "f", Source: "x", AccountID: alice, Metric: "trades_completed", Delta: 1, OccurredAt: c.now().AddDate(0, 0, 2)},
	}
	for _, e := range bad {
		if _, err := s.Ingest(ctx, e, nil); err == nil {
			t.Errorf("accepted %+v", e)
		}
	}
	if n := count(t, s, alice, "trades_completed"); n != 0 {
		t.Fatalf("count %d", n)
	}
}

func TestDistinctMetricCountsEachKeyOnce(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	e := Event{EventID: "1", Source: "t", AccountID: alice, Metric: "maker_courses_played", Keys: []string{"c1", "c2"}}
	s.Ingest(ctx, e, nil)
	e2 := Event{EventID: "2", Source: "t", AccountID: alice, Metric: "maker_courses_played", Keys: []string{"c2", "c3"}}
	s.Ingest(ctx, e2, nil)
	if n := count(t, s, alice, "maker_courses_played"); n != 3 {
		t.Fatalf("distinct count %d, want 3", n)
	}
}

func TestFriendsPlayedWithNeedsRealFriends(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	e := Event{EventID: "1", Source: "t", AccountID: alice, Metric: "friends_played_with", Keys: []string{bob, "stranger"}}
	if _, err := s.Ingest(ctx, e, map[string]bool{bob: true}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, alice, "friends_played_with"); n != 1 {
		t.Fatalf("only the friend counts, got %d", n)
	}
}

func TestDailyCapHoldsAndRaisesFlagThenAdminReleases(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.SetMetric(ctx, admin, "trades_completed", 3, 1, true); err != nil {
		t.Fatal(err)
	}
	var held int
	for i := 0; i < 6; i++ {
		st, err := s.Ingest(ctx, ev(fmt.Sprint("t", i), "trades_completed", alice), nil)
		if err != nil {
			t.Fatal(err)
		}
		if st == EvHeld {
			held++
		}
	}
	if held != 3 || count(t, s, alice, "trades_completed") != 3 {
		t.Fatalf("held %d, count %d", held, count(t, s, alice, "trades_completed"))
	}
	fl, _ := s.Flags(ctx, true, 10)
	if len(fl) != 1 || fl[0].Reason != "metric-cap:trades_completed" {
		t.Fatalf("flags %+v", fl)
	}
	n, err := s.ReleaseHeld(ctx, admin, alice, "trades_completed")
	if err != nil || n != 3 || count(t, s, alice, "trades_completed") != 6 {
		t.Fatalf("release %d %v count %d", n, err, count(t, s, alice, "trades_completed"))
	}
	if fl, _ := s.Flags(ctx, true, 10); len(fl) != 0 {
		t.Fatal("flag stays open after release")
	}
}

func TestDiscardHeldCountsNothing(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	s.SetMetric(ctx, admin, "trades_completed", 1, 1, true)
	for i := 0; i < 3; i++ {
		s.Ingest(ctx, ev(fmt.Sprint("d", i), "trades_completed", alice), nil)
	}
	if n, err := s.DiscardHeld(ctx, admin, alice, "trades_completed"); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if count(t, s, alice, "trades_completed") != 1 {
		t.Fatal("discarded events must not count")
	}
}

func TestFrozenAccountCountsNothing(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	s.SetFrozen(ctx, alice, true, "core")
	st, err := s.Ingest(ctx, ev("1", "trades_completed", alice), nil)
	if err != nil || st != EvIgnored || count(t, s, alice, "trades_completed") != 0 {
		t.Fatalf("%s %v", st, err)
	}
}

func claimOK(t *testing.T, s *Store, acct, id string) {
	t.Helper()
	if _, err := s.Claim(context.Background(), acct, id, ""); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
}

func TestLadderTiersUnlockInOrderAndSumTheGames(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	activate(t, s, "kart-races-1", "kart-races-10", "kart-races-25")
	// Inactive until activated: no progress. Then races across two games sum.
	for i := 0; i < 6; i++ {
		s.Ingest(ctx, ev(fmt.Sprint("a", i), "kart_sessions.mkwii", alice), nil)
	}
	for i := 0; i < 5; i++ {
		s.Ingest(ctx, ev(fmt.Sprint("b", i), "kart_sessions.mk8dx", alice), nil)
	}
	ms, _ := s.MyMissions(ctx, alice)
	get := func(id string) MissionStatus {
		for _, m := range ms {
			if m.ID == id {
				return m
			}
		}
		t.Fatalf("mission %s missing", id)
		return MissionStatus{}
	}
	if m := get("kart-races-1"); !m.Claimable {
		t.Fatalf("tier 1 should be claimable: %+v", m)
	}
	if m := get("kart-races-10"); !m.Locked || m.Claimable || m.Progress != 0 {
		t.Fatalf("tier 10 must wait for tier 1: %+v", m)
	}
	if _, err := s.Claim(ctx, alice, "kart-races-10", ""); err == nil {
		t.Fatal("tier 10 claimed before tier 1")
	}
	claimOK(t, s, alice, "kart-races-1")
	ms, _ = s.MyMissions(ctx, alice)
	if m := get("kart-races-10"); m.Progress != 11 || !m.Claimable {
		t.Fatalf("tier 10 after tier 1: %+v", m)
	}
	claimOK(t, s, alice, "kart-races-10")
	if m := get2(t, s, alice, "kart-races-25"); m.Progress != 11 || m.Claimable {
		t.Fatalf("tier 25: %+v", m)
	}
}

func get2(t *testing.T, s *Store, acct, id string) MissionStatus {
	ms, _ := s.MyMissions(context.Background(), acct)
	for _, m := range ms {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("mission %s missing", id)
	return MissionStatus{}
}

func TestInactiveLadderMissionsAreHidden(t *testing.T) {
	s, _ := newStore(t)
	ms, _ := s.MyMissions(context.Background(), alice)
	for _, m := range ms {
		if m.Metric != "" && m.ID[:4] == "kart" {
			t.Fatalf("inactive mission listed: %s", m.ID)
		}
	}
	all, _ := s.ListMissions(context.Background(), true)
	n := 0
	for _, m := range all {
		if m.Metric == "kart_sessions" {
			n++
			if m.Active {
				t.Fatalf("%s seeded active", m.ID)
			}
		}
	}
	if n != 9 {
		t.Fatalf("kart ladder has %d tiers, want 9", n)
	}
}

func TestPerGameFirstFinishAndCollector(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	activate(t, s, "kart-first-mkds", "kart-collector")
	for _, g := range []string{"mkds", "mkwii", "mk7", "mk8"} {
		s.Ingest(ctx, ev("k"+g, "kart_sessions."+g, alice), nil)
	}
	if m := get2(t, s, alice, "kart-collector"); m.Completed || m.Progress != 4 {
		t.Fatalf("collector after 4 games: %+v", m)
	}
	if m := get2(t, s, alice, "kart-first-mkds"); !m.Claimable {
		t.Fatalf("first mkds: %+v", m)
	}
	// Ten races of one game do not make a collector.
	for i := 0; i < 10; i++ {
		s.Ingest(ctx, ev(fmt.Sprint("x", i), "kart_sessions.mk8", alice), nil)
	}
	if m := get2(t, s, alice, "kart-collector"); m.Completed {
		t.Fatal("collector needs every game")
	}
	s.Ingest(ctx, ev("last", "kart_sessions.mk8dx", alice), nil)
	if m := get2(t, s, alice, "kart-collector"); !m.Completed || !m.Claimable {
		t.Fatalf("collector with all five: %+v", m)
	}
}

func TestOnlineSessionsLadderUsesPlaySessions(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	activate(t, s, "online-sessions-1")
	s.Accrue(ctx, alice, "switch", tt("t1", 1000, 1), c.now())
	c.add(0)
	c.add(10 * 60 * 1e9)
	s.Accrue(ctx, alice, "switch", tt("t1", 1300, 2), c.now())
	if m := get2(t, s, alice, "online-sessions-1"); !m.Claimable {
		t.Fatalf("%+v", m)
	}
}

func TestGrantBulkIsIdempotentAndAudited(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	s.pool.Exec(ctx, `INSERT INTO items (id, kind, name, price) VALUES ('badge-launch-event','badge','Launch Event', NULL)`)
	r, err := s.GrantBulk(ctx, admin, "event", "badge-launch-event", "launch weekend", []string{alice, bob})
	if err != nil || len(r) != 2 || r[0].Status != "granted" {
		t.Fatalf("%+v %v", r, err)
	}
	r, _ = s.GrantBulk(ctx, admin, "event", "badge-launch-event", "launch weekend", []string{alice})
	if r[0].Status != "already_owned" {
		t.Fatalf("%+v", r)
	}
	if ok, _ := s.Owns(ctx, alice, "badge-launch-event"); !ok {
		t.Fatal("not owned")
	}
	g, _ := s.Grants(ctx, "badge-launch-event", 10)
	if len(g) != 2 {
		t.Fatalf("audit rows %d", len(g))
	}
	if _, err := s.GrantBulk(ctx, admin, "bogus", "badge-launch-event", "r", []string{alice}); err == nil {
		t.Fatal("bad kind accepted")
	}
	if _, err := s.GrantBulk(ctx, admin, "event", "badge-launch-event", " ", []string{alice}); err == nil {
		t.Fatal("empty reason accepted")
	}
}

func TestPurgeErasesCounts(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	s.Ingest(ctx, ev("1", "trades_completed", alice), nil)
	s.Purge(ctx, alice)
	if count(t, s, alice, "trades_completed") != 0 {
		t.Fatal("counts survived erase")
	}
	var n int
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM events WHERE account_id=$1`, alice).Scan(&n)
	if n != 0 {
		t.Fatal("events survived erase")
	}
}

func TestSessionMissionsAreRepointed(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	all, _ := s.ListMissions(ctx, true)
	by := map[string]Mission{}
	for _, m := range all {
		by[m.ID] = m
	}
	want := map[string]string{
		"kart-races-100": "kart_sessions", "splatoon-matches-10": "turf_sessions", "smash-battles-1": "brawl_sessions",
		"kart-first-mk8dx": "kart_sessions.mk8dx", "splatoon-first-s2": "turf_sessions.s2", "smash-first-3ds": "brawl_sessions.3ds",
		"kart-collector": "family_games:kart_sessions", "smash-collector": "family_games:brawl_sessions",
	}
	for id, metric := range want {
		if by[id].Metric != metric || by[id].Active {
			t.Errorf("%s: metric %q active %v, want %q inactive", id, by[id].Metric, by[id].Active, metric)
		}
	}
	// Splatoon 3 has a session metric (npln); its missions are created by the admin loader, not a migration.
	if ms, _ := s.Metrics(ctx); !func() bool {
		for _, m := range ms {
			if m.ID == "turf_sessions.s3" && m.Family == "turf_sessions" {
				return true
			}
		}
		return false
	}() {
		t.Error("turf_sessions.s3 metric missing")
	}
	if c := by["splatoon-collector"]; c.Metric != "family_games:turf_sessions" || c.Target != 2 {
		t.Errorf("turf collector: %+v", c)
	}
	ms, _ := s.Metrics(ctx)
	for _, m := range ms {
		if strings.Contains(m.ID, "_completed.") {
			t.Errorf("finisher metric %s should be gone", m.ID)
		}
	}
	// Two Splatoon games make the collector.
	activate(t, s, "splatoon-collector")
	s.Ingest(ctx, ev("g1", "turf_sessions.s1", alice), nil)
	s.Ingest(ctx, ev("g2", "turf_sessions.s1", alice), nil)
	if m := get2(t, s, alice, "splatoon-collector"); m.Completed {
		t.Fatal("two sessions of one game must not complete the collector")
	}
	s.Ingest(ctx, ev("g3", "turf_sessions.s2", alice), nil)
	if m := get2(t, s, alice, "splatoon-collector"); !m.Completed {
		t.Fatal("one session in each of the two games completes it")
	}
}

package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/openpak/rewards/internal/rules"
)

const (
	alice = "aaaaaaaa-0000-0000-0000-000000000001"
	bob   = "bbbbbbbb-0000-0000-0000-000000000002"
	admin = "cccccccc-0000-0000-0000-000000000003"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

// newStore needs REWARDS_TEST_DATABASE_URL (a throwaway database: its public schema is dropped).
func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	url := os.Getenv("REWARDS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REWARDS_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, url, t.TempDir(), rules.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, url, t.TempDir(), rules.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := &clock{t: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)} // a Monday
	s.Now = c.now
	return s, c
}

func tt(title string, secs int64, sess int) []TitleTotal {
	return []TitleTotal{{Title: title, Seconds: secs, Sessions: sess}}
}

func balance(t *testing.T, s *Store, acct string) int64 {
	t.Helper()
	a, err := s.Account(context.Background(), acct)
	if err != nil {
		t.Fatal(err)
	}
	return a.Balance
}

func TestFirstSightCountsHistoryOnce(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	res, err := s.Accrue(ctx, alice, "switch", tt("t1", 5*3600, 9), c.now())
	if err != nil || res.Counted != 0 || res.Points != 20 { // 5 h at 900 s per point, no daily cap
		t.Fatalf("first sight: %+v %v", res, err)
	}
	a, _ := s.Account(ctx, alice)
	if a.Balance != 20 || a.LifetimePlay != 5*3600 {
		t.Fatalf("account: %+v", a)
	}
	// Seeing the same total again pays nothing more.
	c.add(time.Minute)
	if res, _ := s.Accrue(ctx, alice, "switch", tt("t1", 5*3600, 9), c.now()); res.Points != 0 {
		t.Fatalf("replay paid %d", res.Points)
	}
}

func TestSettleAllHistoryBackfillsOnce(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	// Two accounts as the old service left them: baselines recorded, history not yet counted.
	for _, a := range []string{alice, bob} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO accounts (account_id) VALUES ($1)`, a); err != nil {
			t.Fatal(err)
		}
	}
	ins := func(a, ns, title string, total, counted, hist int64) {
		if _, err := s.pool.Exec(ctx, `INSERT INTO play_cursor (account_id, namespace, title_id, total_seconds, counted_seconds, seen_at, history_seconds, history_done) VALUES ($1,$2,$3,$4,$5,$6,$7,false)`,
			a, ns, title, total, counted, c.now(), hist); err != nil {
			t.Fatal(err)
		}
	}
	ins(alice, "switch", "t1", 40000, 4000, 36000) // 10 h of history
	ins(alice, "wiiu", "t2", 9000, 0, 9000)        // 2.5 h
	ins(bob, "switch", "t1", 10000, 0, 10000)
	paused := true
	s.SetSettings(ctx, bob, nil, &paused)
	var days0 int64
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM activity`).Scan(&days0)

	n, pts, err := s.SettleAllHistory(ctx)
	if err != nil || n != 1 || pts != 50 { // alice: 45000 s = 50 points; bob is paused
		t.Fatalf("settle: %d %d %v", n, pts, err)
	}
	a, _ := s.Account(ctx, alice)
	if a.Balance != 50 || a.LifetimePlay != 45000 {
		t.Fatalf("alice: %+v", a)
	}
	var lines int
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger WHERE account_id=$1 AND source='history'`, alice).Scan(&lines)
	if lines != 1 {
		t.Fatalf("%d history ledger lines", lines)
	}
	// Missions: hours-10 is met by 12.5 h; titles_played counts both titles; play_days and activity unchanged.
	var prog int64
	var done bool
	s.pool.QueryRow(ctx, `SELECT progress, completed_at IS NOT NULL FROM mission_progress WHERE account_id=$1 AND mission_id='hours-10' AND period='once'`, alice).Scan(&prog, &done)
	if prog != 45000 || !done {
		t.Fatalf("hours-10: %d %v", prog, done)
	}
	var m Mission
	m.Metric = "titles_played"
	if v, _ := s.metricValue(ctx, s.pool, alice, m, c.now()); v != 2 {
		t.Fatalf("titles_played %d", v)
	}
	m.Metric = "play_days"
	if v, _ := s.metricValue(ctx, s.pool, alice, m, c.now()); v != 0 {
		t.Fatalf("play_days %d", v)
	}
	var days1 int64
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM activity`).Scan(&days1)
	if days1 != days0 {
		t.Fatal("history must not write activity")
	}
	// A second run does nothing.
	if n, pts, err := s.SettleAllHistory(ctx); err != nil || n != 0 || pts != 0 {
		t.Fatalf("second run: %d %d %v", n, pts, err)
	}
	if b := balance(t, s, alice); b != 50 {
		t.Fatalf("balance after rerun %d", b)
	}
	// Increments after the backfill still count normally and are not double-counted.
	c.add(20 * time.Minute)
	res, _ := s.Accrue(ctx, alice, "switch", tt("t1", 40000+1200, 2), c.now())
	if res.Counted != 1200 {
		t.Fatalf("increment %+v", res)
	}
	if a, _ := s.Account(ctx, alice); a.LifetimePlay != 45000+1200 {
		t.Fatalf("lifetime %d", a.LifetimePlay)
	}
}

func TestAccrualEarnsCheckinNewTitleAndPlayPoints(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	s.Accrue(ctx, alice, "switch", tt("t1", 1000, 1), c.now())
	c.add(25 * time.Minute)
	// 25 minutes elapsed; the core says 20 more minutes were played.
	res, err := s.Accrue(ctx, alice, "switch", tt("t1", 1000+1200, 2), c.now())
	if err != nil {
		t.Fatal(err)
	}
	// 20 min = 1 play point (+5 min carried) + check-in 1 + new title 2 (>= 10 min).
	if res.Points != 4 || res.Counted != 1200 {
		t.Fatalf("got %+v, want 4 points / 1200 s", res)
	}
	// Replaying the same state earns nothing more.
	c.add(time.Minute)
	if res, _ := s.Accrue(ctx, alice, "switch", tt("t1", 2200, 2), c.now()); res.Points != 0 {
		t.Fatalf("replay paid %d", res.Points)
	}
	// 10 more minutes: carry 5 + 10 = 15 min = one more point; no second check-in or new-title.
	c.add(10 * time.Minute)
	if res, _ := s.Accrue(ctx, alice, "switch", tt("t1", 2200+600, 3), c.now()); res.Points != 1 {
		t.Fatalf("carry should pay one more point, got %d", res.Points)
	}
	if b := balance(t, s, alice); b != 6 { // 5 + 1 for the 1000 s of history
		t.Fatalf("balance %d, want 6", b)
	}
}

func TestPerTitleDailyCapHoldsAcrossPolls(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	total := int64(0)
	s.Accrue(ctx, alice, "switch", tt("t1", total, 0), c.now())
	for i := 0; i < 36; i++ { // three hours in five minute steps
		c.add(5 * time.Minute)
		total += 300
		s.Accrue(ctx, alice, "switch", tt("t1", total, i), c.now())
	}
	today, _ := s.Today(ctx, alice)
	if today.PlayPoints != 4 {
		t.Fatalf("one title may pay 4 points a day, paid %d", today.PlayPoints)
	}
	// 4 play + 1 check-in + 2 new title.
	if b := balance(t, s, alice); b != 7 {
		t.Fatalf("balance %d, want 7", b)
	}
}

func TestDailyCapAcrossTitles(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	mk := func(secs int64) []TitleTotal {
		out := []TitleTotal{}
		for _, id := range []string{"a", "b", "c"} {
			out = append(out, TitleTotal{Title: id, Seconds: secs, Sessions: 1})
		}
		return out
	}
	s.Accrue(ctx, alice, "switch", mk(0), c.now())
	c.add(3 * time.Hour)
	s.Accrue(ctx, alice, "switch", mk(2*3600), c.now()) // 2 h in each of three titles
	today, _ := s.Today(ctx, alice)
	if today.PlayPoints != 8 {
		t.Fatalf("daily play cap is 8, paid %d", today.PlayPoints)
	}
}

func TestInflatedTotalIsClampedAndFlagged(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	s.Accrue(ctx, alice, "switch", tt("t1", 0, 0), c.now())
	var clamps int
	for i := 0; i < 3; i++ {
		c.add(time.Minute)
		res, err := s.Accrue(ctx, alice, "switch", tt("t1", int64(i+1)*100000, i), c.now())
		if err != nil {
			t.Fatal(err)
		}
		clamps += res.Clamps
	}
	if clamps != 3 {
		t.Fatalf("clamps %d", clamps)
	}
	fl, err := s.Flags(ctx, true, 10)
	if err != nil || len(fl) != 1 || fl[0].Reason != "elapsed-clamp" || fl[0].AccountID != alice {
		t.Fatalf("flags %+v %v", fl, err)
	}
	// Three minutes of wall clock can never be more than a few minutes of credit.
	a, _ := s.Account(ctx, alice)
	if a.LifetimePlay > 3*(60+60) {
		t.Fatalf("clamped play too large: %d", a.LifetimePlay)
	}
}

func TestTotalGoingDownRebaselines(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	s.Accrue(ctx, alice, "switch", tt("t1", 10000, 5), c.now())
	c.add(10 * time.Minute)
	res, _ := s.Accrue(ctx, alice, "switch", tt("t1", 100, 1), c.now())
	if res.Counted != 0 {
		t.Fatal("a smaller total counts nothing")
	}
	c.add(10 * time.Minute)
	res, _ = s.Accrue(ctx, alice, "switch", tt("t1", 100+600, 2), c.now())
	if res.Counted != 600 {
		t.Fatalf("counting resumes from the new baseline, got %d", res.Counted)
	}
}

func TestFrozenAndPausedEarnNothingAndDoNotBackfillLater(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	s.Accrue(ctx, alice, "switch", tt("t1", 0, 0), c.now())
	if err := s.SetFrozen(ctx, alice, true, "core"); err != nil {
		t.Fatal(err)
	}
	c.add(30 * time.Minute)
	res, _ := s.Accrue(ctx, alice, "switch", tt("t1", 1800, 1), c.now())
	if !res.Skipped || res.Points != 0 {
		t.Fatalf("frozen: %+v", res)
	}
	s.SetFrozen(ctx, alice, false, "core")
	c.add(5 * time.Minute)
	res, _ = s.Accrue(ctx, alice, "switch", tt("t1", 1800+300, 2), c.now())
	if res.Counted != 300 {
		t.Fatalf("only the time after the thaw counts, got %d", res.Counted)
	}
	paused := true
	s.SetSettings(ctx, bob, nil, &paused)
	s.Accrue(ctx, bob, "switch", tt("t1", 0, 0), c.now())
	c.add(20 * time.Minute)
	if res, _ := s.Accrue(ctx, bob, "switch", tt("t1", 1200, 1), c.now()); !res.Skipped {
		t.Fatal("paused accounts earn nothing")
	}
}

func TestDailyMissionClaimPaysOnce(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	s.Accrue(ctx, alice, "switch", tt("t1", 0, 0), c.now())
	c.add(40 * time.Minute)
	s.Accrue(ctx, alice, "switch", tt("t1", 2400, 1), c.now())
	ms, err := s.MyMissions(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range ms {
		if m.ID == "daily-play-30" {
			found = true
			if !m.Claimable || m.Progress != 2400 {
				t.Fatalf("daily mission: %+v", m)
			}
		}
		if m.ID == "hours-50" && !m.Locked {
			t.Fatal("series step must be locked until hours-10 is claimed")
		}
	}
	if !found {
		t.Fatal("daily-play-30 missing")
	}
	before := balance(t, s, alice)
	r1, err := s.Claim(ctx, alice, "daily-play-30", "")
	if err != nil || r1.Points != 2 {
		t.Fatalf("claim: %+v %v", r1, err)
	}
	r2, err := s.Claim(ctx, alice, "daily-play-30", "")
	if err != nil || r2.Points != 0 {
		t.Fatalf("second claim must pay nothing: %+v %v", r2, err)
	}
	if balance(t, s, alice) != before+2 {
		t.Fatal("claimed twice")
	}
	if _, err := s.Claim(ctx, alice, "weekly-4-days", ""); err == nil {
		t.Fatal("an unfinished mission cannot be claimed")
	}
	// Tomorrow the daily resets: not claimable until played again.
	c.add(24 * time.Hour)
	s.Accrue(ctx, alice, "switch", tt("t1", 2400, 1), c.now())
	ms, _ = s.MyMissions(ctx, alice)
	for _, m := range ms {
		if m.ID == "daily-play-30" && m.Period == rules.Day(c.now()) && m.Claimable {
			t.Fatal("daily progress must reset")
		}
	}
}

func TestSeriesMissionUnlocksAfterPredecessorClaimed(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	s.Accrue(ctx, alice, "switch", tt("t1", 0, 0), c.now())
	secs := int64(0)
	for d := 0; d < 4; d++ { // 11 hours of play over four days, 3 h a day
		c.add(24 * time.Hour)
		secs += 3 * 3600
		s.Accrue(ctx, alice, "switch", tt("t1", secs, d), c.now())
	}
	r, err := s.Claim(ctx, alice, "hours-10", "")
	if err != nil || r.Points != 20 {
		t.Fatalf("hours-10: %+v %v", r, err)
	}
	c.add(time.Hour)
	s.Accrue(ctx, alice, "switch", tt("t1", secs, 9), c.now()) // refresh so the series step is evaluated
	ms, _ := s.MyMissions(ctx, alice)
	for _, m := range ms {
		if m.ID == "hours-50" && m.Locked {
			t.Fatal("hours-50 should be unlocked now")
		}
	}
}

func TestFriendPaysAtOnceAndCountsForMissions(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	b0, bob0 := balance(t, s, alice), balance(t, s, bob)
	if err := s.FriendAccepted(ctx, alice, bob); err != nil { // neither has played
		t.Fatal(err)
	}
	if balance(t, s, alice) != b0+3 || balance(t, s, bob) != bob0+3 {
		t.Fatalf("both sides are paid at once: alice %d bob %d", balance(t, s, alice), balance(t, s, bob))
	}
	s.FriendAccepted(ctx, alice, bob) // the event again
	if balance(t, s, alice) != b0+3 {
		t.Fatal("a pair pays once")
	}
	n, err := s.metricValue(ctx, s.pool, alice, Mission{Kind: "once", Metric: "friends_added"}, c.now())
	if err != nil || n != 1 {
		t.Fatalf("friends_added = %d, %v; want 1", n, err)
	}
	// A friendship from before the rule change (never paid) is settled by the sweep.
	s.pool.Exec(ctx, `INSERT INTO friends (account_id, other_id) VALUES ($1,$2)`, alice, "carol")
	if err := s.SettleAllFriends(ctx); err != nil {
		t.Fatal(err)
	}
	if balance(t, s, alice) != b0+6 {
		t.Fatalf("sweep pays the old friendship once: %d", balance(t, s, alice)-b0)
	}
}

func TestFriendLifetimeCap(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	ids := make([]string, 12)
	for i := range ids {
		ids[i] = fmt.Sprintf("dddddddd-0000-0000-0000-%012d", i)
		s.Accrue(ctx, ids[i], "switch", tt("t1", 0, 0), c.now())
	}
	c.add(time.Hour)
	for _, id := range ids { // every friend has 40 minutes of real play
		s.Accrue(ctx, id, "switch", tt("t1", 2400, 1), c.now())
	}
	for _, id := range ids {
		if err := s.FriendAccepted(ctx, alice, id); err != nil {
			t.Fatal(err)
		}
	}
	var paid int
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM friends WHERE awarded AND account_id=$1`, alice).Scan(&paid)
	if paid != 10 {
		t.Fatalf("alice is paid for at most 10 friends, got %d", paid)
	}
}

func itemFixture(t *testing.T, s *Store, id string, price int) {
	t.Helper()
	ctx := context.Background()
	p := price
	if err := s.UpsertItem(ctx, admin, Item{ID: id, Kind: "badge", Name: "Test " + id, Price: &p, Platforms: []string{"web"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, admin, id, "active"); err == nil {
		t.Fatal("an item without assets must not go live")
	}
	if _, _, err := s.PutAsset(ctx, admin, id, "web", "asset", "image/png", bytes.NewReader([]byte("png-bytes"))); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, admin, id, "active"); err == nil {
		t.Fatal("an item without a preview must not go live")
	}
	if _, _, err := s.PutAsset(ctx, admin, id, "web", "preview", "image/png", bytes.NewReader([]byte("preview-bytes"))); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, admin, id, "active"); err != nil {
		t.Fatal(err)
	}
}

func TestRedeemEquipRevoke(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	itemFixture(t, s, "badge-one", 50)
	if _, err := s.Redeem(ctx, alice, "badge-one", ""); err != ErrInsufficient {
		t.Fatalf("no points: %v", err)
	}
	if _, err := s.Adjust(ctx, admin, alice, 80, "test grant"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Redeem(ctx, alice, "badge-one", "k1")
	if err != nil || r.Balance != 30 {
		t.Fatalf("redeem: %+v %v", r, err)
	}
	if again, err := s.Redeem(ctx, alice, "badge-one", "k1"); err != nil || again.ID != r.ID {
		t.Fatalf("same Idempotency-Key must return the first redemption: %+v %v", again, err)
	}
	if balance(t, s, alice) != 30 {
		t.Fatal("charged twice")
	}
	if _, err := s.Redeem(ctx, alice, "badge-one", "k2"); err != ErrOwned {
		t.Fatalf("limit one per account: %v", err)
	}
	if err := s.Equip(ctx, alice, "badge", "badge-one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Equip(ctx, bob, "badge", "badge-one"); err == nil {
		t.Fatal("equipping something you do not own")
	}
	if err := s.Equip(ctx, alice, "theme", "badge-one"); err == nil {
		t.Fatal("wrong slot")
	}
	// Showcase is private until the owner opts in.
	if m, _ := s.Showcase(ctx, alice); len(m) != 0 {
		t.Fatal("showcase must default to private")
	}
	yes := true
	s.SetSettings(ctx, alice, &yes, nil)
	if m, _ := s.Showcase(ctx, alice); m["badge"] != "badge-one" {
		t.Fatalf("showcase: %v", m)
	}
	ents, err := s.Entitlements(ctx, alice, "web")
	if err != nil || len(ents) != 1 || !ents[0].Equipped {
		t.Fatalf("entitlements: %+v %v", ents, err)
	}
	if err := s.Revoke(ctx, admin, r.ID, true); err != nil {
		t.Fatal(err)
	}
	if balance(t, s, alice) != 80 {
		t.Fatalf("refund: %d", balance(t, s, alice))
	}
	if eq, _ := s.Equipped(ctx, alice); len(eq) != 0 {
		t.Fatal("revoked item must leave the slot")
	}
	if ents, _ := s.Entitlements(ctx, alice, "web"); len(ents) != 0 {
		t.Fatal("revoked item must not be delivered")
	}
}

func TestStockNeverOversells(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	itemFixture(t, s, "limited", 10)
	one := 1
	p := 10
	if err := s.UpsertItem(ctx, admin, Item{ID: "limited", Kind: "badge", Name: "L", Price: &p, Platforms: []string{"web"}, Stock: &one}); err != nil {
		t.Fatal(err)
	}
	var accts []string
	for i := 0; i < 8; i++ {
		a := "eeeeeeee-0000-0000-0000-00000000000" + string(rune('0'+i))
		accts = append(accts, a)
		s.Adjust(ctx, admin, a, 100, "test")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for _, a := range accts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Redeem(ctx, a, "limited", ""); err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("stock 1 sold %d times", won)
	}
}

func TestAdjustRules(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if _, err := s.Adjust(ctx, admin, admin, 10, "self"); err == nil {
		t.Fatal("no self-adjust")
	}
	if _, err := s.Adjust(ctx, admin, alice, 10, ""); err == nil {
		t.Fatal("reason required")
	}
	if _, err := s.Adjust(ctx, admin, alice, 1000000, "x"); err == nil {
		t.Fatal("bounded")
	}
	if _, err := s.Adjust(ctx, admin, alice, -5, "negative below zero"); err != ErrInsufficient {
		t.Fatalf("balance cannot go negative: %v", err)
	}
	if rows, _ := s.Audit(ctx, alice, 0, 10); len(rows) != 0 {
		t.Fatalf("failed adjustments are not audited as done: %d", len(rows))
	}
}

func TestPurgeErasesEverything(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	s.Accrue(ctx, alice, "switch", tt("t1", 0, 0), c.now())
	c.add(30 * time.Minute)
	s.Accrue(ctx, alice, "switch", tt("t1", 1800, 1), c.now())
	if err := s.Purge(ctx, alice); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"accounts", "ledger", "play_cursor", "daily_earn", "activity", "titles_credited", "mission_progress"} {
		var n int
		s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+tbl+` WHERE account_id=$1`, alice).Scan(&n)
		if n != 0 {
			t.Fatalf("%s kept %d row(s)", tbl, n)
		}
	}
}

func TestMissionRewardItemGranted(t *testing.T) {
	s, c := newStore(t)
	ctx := context.Background()
	itemFixture(t, s, "founder", 0)
	item := "founder"
	err := s.UpsertMission(ctx, admin, Mission{ID: "item-mission", Title: "Item", Kind: "once", Metric: "play_seconds", Target: 60, RewardItem: &item, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Accrue(ctx, alice, "switch", tt("t1", 0, 0), c.now())
	c.add(5 * time.Minute)
	s.Accrue(ctx, alice, "switch", tt("t1", 300, 1), c.now())
	r, err := s.Claim(ctx, alice, "item-mission", "")
	if err != nil || r.Item != "founder" {
		t.Fatalf("claim: %+v %v", r, err)
	}
	if ok, _ := s.Owns(ctx, alice, "founder"); !ok {
		t.Fatal("reward item not granted")
	}
}

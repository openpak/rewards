package rules

import (
	"testing"
	"time"
)

func TestClampBoundsDeltaByElapsedTime(t *testing.T) {
	r := Default()
	if got, c := r.Clamp(600, 600); got != 600 || c {
		t.Fatalf("honest delta cut: %d %v", got, c)
	}
	if got, c := r.Clamp(7200, 300); got != 360 || !c {
		t.Fatalf("a two hour jump in five minutes must be clamped to elapsed+slack, got %d %v", got, c)
	}
	if got, _ := r.Clamp(-50, 300); got != 0 {
		t.Fatalf("a total that went down counts nothing, got %d", got)
	}
	if got, c := r.Clamp(100, -5); got != 60 || !c {
		t.Fatalf("negative elapsed treated as zero, got %d %v", got, c)
	}
}

func TestPlayPointsOnePerFifteenMinutesWithCarry(t *testing.T) {
	r := Default()
	res := r.PlayPoints(PlayInput{Counted: 600})
	if res.Points != 0 || res.NewCarry != 600 {
		t.Fatalf("10 min earns nothing yet: %+v", res)
	}
	res = r.PlayPoints(PlayInput{Counted: 600, Carry: 600})
	if res.Points != 1 || res.NewCarry != 300 {
		t.Fatalf("20 min is 1 point and 5 min carried: %+v", res)
	}
}

func TestPlayPointsPerTitleCap(t *testing.T) {
	r := Default()
	// 3 hours in one title: 12 points wanted, the title cap is 4.
	res := r.PlayPoints(PlayInput{Counted: 3 * 3600})
	if res.Points != 4 || !res.Capped || res.NewCarry != 0 {
		t.Fatalf("per-title cap: %+v", res)
	}
	res = r.PlayPoints(PlayInput{Counted: 3600, TitlePointsToday: 4})
	if res.Points != 0 || !res.Capped {
		t.Fatalf("title at cap earns nothing: %+v", res)
	}
}

func TestPlayPointsDailyCapAcrossTitles(t *testing.T) {
	r := Default()
	res := r.PlayPoints(PlayInput{Counted: 3600, TotalPlayPointsToday: 6})
	if res.Points != 2 || !res.Capped {
		t.Fatalf("only 2 points of room left in the day: %+v", res)
	}
	res = r.PlayPoints(PlayInput{Counted: 900, TotalPlayPointsToday: 8})
	if res.Points != 0 || res.NewCarry != 0 {
		t.Fatalf("capped seconds are dropped, not banked: %+v", res)
	}
}

func TestIdleConsoleEarnsTheCapOnce(t *testing.T) {
	r := Default()
	// A console idling for 24 h in one title: whatever the poll cadence, the day pays the title cap.
	pts, title, total, carry := 0, 0, 0, int64(0)
	for i := 0; i < 288; i++ { // 5 minute polls
		res := r.PlayPoints(PlayInput{Counted: 300, Carry: carry, TitlePointsToday: title, TotalPlayPointsToday: total})
		pts += res.Points
		title += res.Points
		total += res.Points
		carry = res.NewCarry
	}
	if pts != r.PlayTitleDailyCap {
		t.Fatalf("idle day paid %d, want the per-title cap %d", pts, r.PlayTitleDailyCap)
	}
}

func TestCheckin(t *testing.T) {
	r := Default()
	if r.Checkin(299, false) != 0 || r.Checkin(300, false) != 1 || r.Checkin(9999, true) != 0 {
		t.Fatal("check-in needs 5 counted minutes and pays once a day")
	}
}

func TestNewTitle(t *testing.T) {
	r := Default()
	if r.NewTitle(false, 599, 0) != 0 {
		t.Fatal("under 10 minutes pays nothing")
	}
	if r.NewTitle(false, 600, 0) != 2 {
		t.Fatal("10 minutes pays the bonus")
	}
	if r.NewTitle(true, 5000, 0) != 0 {
		t.Fatal("a title pays once ever")
	}
	if r.NewTitle(false, 5000, 3) != 0 {
		t.Fatal("three new titles a day at most")
	}
}

func TestFriend(t *testing.T) {
	r := Default()
	if r.Friend(1800, false, 0, false) != 3 {
		t.Fatal("friend with 30 minutes pays")
	}
	if r.Friend(1799, false, 0, false) != 0 {
		t.Fatal("a friend with no real play pays nothing (alt farming)")
	}
	if r.Friend(9999, true, 0, false) != 0 {
		t.Fatal("a pair pays once")
	}
	if r.Friend(9999, false, 10, false) != 0 {
		t.Fatal("lifetime cap of paid friends")
	}
	if r.Friend(9999, false, 0, true) != 0 {
		t.Fatal("frozen accounts earn nothing")
	}
}

func TestIdleFarm(t *testing.T) {
	r := Default()
	if f, _ := r.IdleFarm(6, 2); f {
		t.Fatal("below thresholds")
	}
	if f, why := r.IdleFarm(7, 0); !f || why != "play-cap-streak" {
		t.Fatal("seven days at the cap raises a flag")
	}
	if f, why := r.IdleFarm(0, 3); !f || why != "elapsed-clamp" {
		t.Fatal("three clamps in a day raise a flag")
	}
}

func TestPeriods(t *testing.T) {
	d := time.Date(2026, 11, 1, 23, 59, 0, 0, time.UTC) // Sunday
	if Day(d) != "2026-11-01" || Week(d) != "2026-W44" {
		t.Fatalf("keys: %s %s", Day(d), Week(d))
	}
	if Period("once", d) != "once" || Period("weekly", d) != "2026-W44" || PreviousPeriod("weekly", d) != "2026-W43" {
		t.Fatal("period keys")
	}
	if WeekStart(d).Format("2006-01-02") != "2026-10-26" {
		t.Fatalf("week starts on Monday, got %s", WeekStart(d))
	}
	// ISO week-year boundary: 2026-12-31 is in 2026-W53.
	if Week(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)) != "2026-W53" {
		t.Fatal("iso week 53")
	}
	if Week(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) != "2026-W53" {
		t.Fatal("iso week-year follows the ISO year, not the calendar year")
	}
}

func TestNetworkMap(t *testing.T) {
	for ns, want := range map[string]string{"switch": "switch", "wiiu": "wiiu-3ds", "3ds": "wiiu-3ds", "wfc": "wii-ds", "np": "ps3", "x360": "xbox360", "xbox360": "xbox360", "xbox": "xbox", "Other": "other"} {
		if got := Network(ns); got != want {
			t.Fatalf("%s -> %s, want %s", ns, got, want)
		}
	}
}

// Package rules holds the point rules and every anti-abuse limit as pure functions
// (docs/design.md section 3). Nothing here touches the network or a database, so each
// rule is unit-tested on its own; the store only applies what these functions decide.
package rules

import (
	"fmt"
	"strings"
	"time"
)

// Rules are the defaults of the design doc; Default() returns them.
type Rules struct {
	SecondsPerPoint     int64 // counted seconds that earn one play point
	PlayDailyCap        int   // play points per UTC day, all titles
	PlayTitleDailyCap   int   // play points per UTC day, one title
	ClampSlackSeconds   int64 // grace over the wall-clock time since the previous poll
	CheckinBonus        int
	CheckinMinSeconds   int64 // counted seconds in a day that make it a check-in day
	NewTitleBonus       int
	NewTitleMinSeconds  int64 // counted seconds in a title before its bonus
	NewTitleDailyCap    int   // new-title bonuses per UTC day
	FriendBonus         int
	FriendMinSeconds    int64 // the friend's own lifetime counted play
	FriendLifetimeCap   int   // friend bonuses per account, ever
	FlagCapStreakDays   int   // consecutive days at the play cap before an idle-farm flag
	FlagClampsPerDay    int   // elapsed-time clamps in one day before a flag
	MaxSingleGrantAdmin int   // largest manual adjustment either way
}

func Default() Rules {
	return Rules{
		SecondsPerPoint: 900, PlayDailyCap: 8, PlayTitleDailyCap: 4, ClampSlackSeconds: 60,
		CheckinBonus: 1, CheckinMinSeconds: 300,
		NewTitleBonus: 2, NewTitleMinSeconds: 600, NewTitleDailyCap: 3,
		FriendBonus: 3, FriendMinSeconds: 1800, FriendLifetimeCap: 10,
		FlagCapStreakDays: 7, FlagClampsPerDay: 3, MaxSingleGrantAdmin: 5000,
	}
}

// Clamp bounds a counted delta by the wall-clock time since the previous poll of the same
// cursor, plus slack: the core's total can never legitimately grow faster than time passes.
// A negative delta (the core pruned or reset its totals) counts nothing. clamped reports that
// the delta had to be cut, which is evidence for an abuse flag.
func (r Rules) Clamp(delta, elapsed int64) (counted int64, clamped bool) {
	if delta <= 0 {
		return 0, false
	}
	if elapsed < 0 {
		elapsed = 0
	}
	limit := elapsed + r.ClampSlackSeconds
	if delta > limit {
		return limit, true
	}
	return delta, false
}

// PlayInput is one cursor's state when a counted delta arrives.
type PlayInput struct {
	Counted              int64 // seconds, already clamped
	Carry                int64 // sub-point remainder from earlier polls
	TitlePointsToday     int   // play points this title earned today
	TotalPlayPointsToday int   // play points all titles earned today
}

type PlayResult struct {
	Points   int
	NewCarry int64
	Capped   bool // some points were refused by a cap
}

// PlayPoints turns counted seconds into points under the per-title and daily caps.
// Seconds that hit a cap are dropped, never banked for tomorrow, and the carry stays
// strictly below one point, so an idle console earns each day's cap once.
func (r Rules) PlayPoints(in PlayInput) PlayResult {
	avail := in.Carry + in.Counted
	if avail < 0 {
		avail = 0
	}
	want := int(avail / r.SecondsPerPoint)
	room := r.PlayTitleDailyCap - in.TitlePointsToday
	if d := r.PlayDailyCap - in.TotalPlayPointsToday; d < room {
		room = d
	}
	if room < 0 {
		room = 0
	}
	if want > room {
		return PlayResult{Points: room, NewCarry: 0, Capped: true}
	}
	carry := avail % r.SecondsPerPoint
	if want == room && room == 0 {
		// At the cap with less than a point pending: nothing to bank either.
		carry = 0
	}
	return PlayResult{Points: want, NewCarry: carry}
}

// Checkin is the daily check-in bonus: once per day, once the day holds enough counted play.
func (r Rules) Checkin(countedToday int64, alreadyToday bool) int {
	if alreadyToday || countedToday < r.CheckinMinSeconds {
		return 0
	}
	return r.CheckinBonus
}

// NewTitle is the first-time-in-a-title bonus: once per (account, title), after enough play
// in that title, at most NewTitleDailyCap a day.
func (r Rules) NewTitle(creditedBefore bool, titleCounted int64, awardedToday int) int {
	if creditedBefore || titleCounted < r.NewTitleMinSeconds || awardedToday >= r.NewTitleDailyCap {
		return 0
	}
	return r.NewTitleBonus
}

// Friend is the friend-added bonus. The friend must have earned real play of their own, the pair
// is paid once, and an account has a lifetime number of paid friends.
func (r Rules) Friend(friendCounted int64, pairAlreadyPaid bool, paidSoFar int, selfFrozen bool) int {
	if selfFrozen || pairAlreadyPaid || paidSoFar >= r.FriendLifetimeCap || friendCounted < r.FriendMinSeconds {
		return 0
	}
	return r.FriendBonus
}

// IdleFarm says whether an account's recent pattern deserves a human look: it sat at the play
// cap for capStreakDays days running, or was clamped clampsToday times in a day.
func (r Rules) IdleFarm(capStreakDays, clampsToday int) (flag bool, reason string) {
	switch {
	case capStreakDays >= r.FlagCapStreakDays:
		return true, "play-cap-streak"
	case clampsToday >= r.FlagClampsPerDay:
		return true, "elapsed-clamp"
	}
	return false, ""
}

// Day is the UTC calendar day key of t.
func Day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// Week is the ISO week key of t, for example 2026-W44.
func Week(t time.Time) string {
	y, w := t.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// WeekStart is the Monday 00:00 UTC of the ISO week containing t.
func WeekStart(t time.Time) time.Time {
	t = t.UTC()
	d := int(t.Weekday()+6) % 7 // Monday = 0
	return time.Date(t.Year(), t.Month(), t.Day()-d, 0, 0, 0, 0, time.UTC)
}

// Period is the key a mission's progress is stored under at time t.
func Period(kind string, t time.Time) string {
	switch kind {
	case "daily":
		return Day(t)
	case "weekly":
		return Week(t)
	}
	return "once"
}

// PreviousPeriod is the period before the current one, "" for missions that never reset.
func PreviousPeriod(kind string, t time.Time) string {
	switch kind {
	case "daily":
		return Day(t.AddDate(0, 0, -1))
	case "weekly":
		return Week(t.AddDate(0, 0, -7))
	}
	return ""
}

// Network maps an adapter namespace to the network a mission and the UI name.
// Unknown namespaces are their own network, so nothing is silently dropped.
func Network(namespace string) string {
	switch strings.ToLower(namespace) {
	case "switch", "nx":
		return "switch"
	case "wiiu", "3ds", "wiiu-3ds":
		return "wiiu-3ds"
	case "wfc", "wii", "ds", "wii-ds":
		return "wii-ds"
	case "np", "ps3":
		return "ps3"
	case "xbox360", "x360":
		return "xbox360"
	case "xbox":
		return "xbox"
	}
	return strings.ToLower(namespace)
}

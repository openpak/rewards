package accrual

import (
	"context"
	"testing"

	"github.com/openpak/rewards/internal/core"
)

type fakeCur map[string]map[string]int64

func (f fakeCur) PlayCursorTotals(_ context.Context, a, ns string) (map[string]int64, error) {
	return f[a+"|"+ns], nil
}

func TestCheckCountsDriftAndMissing(t *testing.T) {
	src := &fake{accts: []string{"a", "b"}, hist: map[string][]core.Title{
		"a|switch": {{ID: "t1", Seconds: 100}, {ID: "t2", Seconds: 50}, {ID: "t3", Seconds: 9}},
		"b|switch": {{ID: "t1", Seconds: 7}},
	}}
	cur := fakeCur{"a|switch": {"t1": 100, "t2": 20}, "b|switch": {"t1": 7}}
	c, err := Check(context.Background(), src, cur, []string{"switch", "wiiu"})
	if err != nil {
		t.Fatal(err)
	}
	want := Consistency{Accounts: 2, Titles: 4, Missing: 1, Differing: 1, MaxDiff: 30}
	if c != want {
		t.Fatalf("got %+v want %+v", c, want)
	}
}

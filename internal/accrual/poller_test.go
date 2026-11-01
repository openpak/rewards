package accrual

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/openpak/rewards/internal/core"
	"github.com/openpak/rewards/internal/rules"
	"github.com/openpak/rewards/internal/store"
)

type fake struct {
	accts []string
	hist  map[string][]core.Title // key account|namespace
}

func (f *fake) Accounts(_ context.Context, after string, _ int) ([]string, string, error) {
	if after != "" {
		return nil, "", nil
	}
	return f.accts, "", nil
}
func (f *fake) History(_ context.Context, a, ns string) ([]core.Title, error) {
	return f.hist[a+"|"+ns], nil
}

func TestTickReadsHistoryFromTheCoreOnly(t *testing.T) {
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
	defer s.Close()
	const a = "aaaaaaaa-0000-0000-0000-000000000001"
	f := &fake{accts: []string{a}, hist: map[string][]core.Title{}}
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	p := &Poller{Src: f, Store: s, Namespaces: []string{"switch", "xbox360"}, Now: func() time.Time { return now }}
	s.Now = p.Now

	f.hist[a+"|switch"] = []core.Title{{ID: "t1", Seconds: 100, Sessions: 1}}
	if n, pts, err := p.Tick(ctx); err != nil || n != 1 || pts != 0 {
		t.Fatalf("baseline tick: %d %d %v", n, pts, err)
	}
	now = now.Add(30 * time.Minute)
	f.hist[a+"|switch"] = []core.Title{{ID: "t1", Seconds: 100 + 1500, Sessions: 2}}
	f.hist[a+"|xbox360"] = []core.Title{{ID: "x1", Seconds: 50, Sessions: 1}} // first sight on a second network
	_, pts, err := p.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 25 min: 1 play + check-in 1 + new title 2.
	if pts != 4 {
		t.Fatalf("points %d, want 4", pts)
	}
	acct, _ := s.Account(ctx, a)
	if acct.Balance != 4 || acct.LifetimePlay != 1500 {
		t.Fatalf("account %+v", acct)
	}
	// The same history again pays nothing: no way to double count.
	if _, pts, _ := p.Tick(ctx); pts != 0 {
		t.Fatalf("re-reading identical history paid %d", pts)
	}
}

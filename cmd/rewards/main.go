// Rewards: OpenPak's points, missions and reward shop (docs/design.md in the research repo,
// docs/ here). Points come only from the account core's server-side play sessions. Port block
// 20180: 20180 HTTP.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/openpak/rewards/internal/accrual"
	"github.com/openpak/rewards/internal/api"
	"github.com/openpak/rewards/internal/core"
	"github.com/openpak/rewards/internal/rules"
	"github.com/openpak/rewards/internal/store"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// secret reads NAME, or the file named by NAME_FILE (preferred: nothing secret in the unit).
func secret(name string) (string, error) {
	if f := os.Getenv(name + "_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", name, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return os.Getenv(name), nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// rulesFromEnv starts from the defaults and lets the operator tune the economy.
func rulesFromEnv() rules.Rules {
	r := rules.Default()
	for k, p := range map[string]*int{
		"REWARDS_PLAY_DAILY_CAP": &r.PlayDailyCap, "REWARDS_PLAY_TITLE_DAILY_CAP": &r.PlayTitleDailyCap,
		"REWARDS_NEW_TITLE_DAILY_CAP": &r.NewTitleDailyCap, "REWARDS_FRIEND_LIFETIME_CAP": &r.FriendLifetimeCap,
	} {
		if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v >= 0 {
			*p = v
		}
	}
	if v, err := strconv.ParseInt(os.Getenv("REWARDS_SECONDS_PER_POINT"), 10, 64); err == nil && v >= 60 {
		r.SecondsPerPoint = v
	}
	return r
}

func run() error {
	dbURL, err := secret("REWARDS_DATABASE_URL")
	if err != nil || dbURL == "" {
		return fmt.Errorf("rewards: REWARDS_DATABASE_URL (or _FILE) is required: %v", err)
	}
	coreKey, err := secret("REWARDS_CORE_INTERNAL_KEY")
	if err != nil || coreKey == "" {
		return fmt.Errorf("rewards: the account core credential is required (REWARDS_CORE_INTERNAL_KEY or _FILE): %v", err)
	}
	webKey, err := secret("REWARDS_INTERNAL_KEY")
	if err != nil || len(webKey) < 32 {
		return fmt.Errorf("rewards: REWARDS_INTERNAL_KEY (or _FILE) of at least 32 characters is required: %v", err)
	}
	adminKey, err := secret("REWARDS_ADMIN_KEY")
	if err != nil || len(adminKey) < 32 || adminKey == webKey {
		return errors.New("rewards: REWARDS_ADMIN_KEY (or _FILE) of at least 32 characters, different from the internal key, is required")
	}
	eventsKey, err := secret("REWARDS_EVENTS_KEY")
	if err != nil || (eventsKey != "" && (len(eventsKey) < 32 || eventsKey == webKey || eventsKey == adminKey)) {
		return errors.New("rewards: REWARDS_EVENTS_KEY (or _FILE), when set, must be at least 32 characters and differ from the other keys")
	}
	interval, err := time.ParseDuration(env("REWARDS_POLL_INTERVAL", "5m"))
	if err != nil || interval < 30*time.Second {
		return errors.New("rewards: REWARDS_POLL_INTERVAL must be a Go duration of at least 30s")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	st, err := store.Open(ctx, dbURL, env("REWARDS_ASSET_DIR", "/data/assets"), rulesFromEnv())
	if err != nil {
		return err
	}
	defer st.Close()

	cc, err := core.Dial(env("REWARDS_CORE_ADDRESS", "127.0.0.1:20001"), env("REWARDS_CORE_HTTP_URL", "http://127.0.0.1:20000"), coreKey)
	if err != nil {
		return err
	}
	go cc.Follow(ctx, st)
	go func() { // friendships the current rules pay but nothing has paid yet
		if err := st.SettleAllFriends(ctx); err != nil {
			log.Printf("rewards: settle friends: %v", err)
		}
	}()

	if env("REWARDS_ACCRUAL", "on") != "off" {
		p := &accrual.Poller{Src: cc, Store: st, Interval: interval,
			Namespaces: strings.Split(env("REWARDS_NAMESPACES", "switch,wiiu,3ds,wfc,np,xbox360,xbox"), ",")}
		go p.Run(ctx)
	}
	go func() {
		for {
			if err := st.PurgeStale(ctx); err != nil {
				log.Printf("rewards: retention: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(6 * time.Hour):
			}
		}
	}()

	var origins []string
	for _, o := range strings.Split(env("REWARDS_CORS_ORIGINS", "https://openpak.org"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	var verse *api.Verse
	if u := env("REWARDS_VERSE_URL", ""); u != "" {
		vk, err := secret("REWARDS_VERSE_KEY")
		if err != nil {
			return err
		}
		verse = &api.Verse{URL: u, Key: vk, PublicURL: env("REWARDS_PUBLIC_URL", "https://rewards.openpak.org")}
	}
	srv := &http.Server{
		Addr:              env("REWARDS_HTTP_ADDR", "127.0.0.1:20180"),
		Handler:           (&api.Server{Store: st, Core: cc, Dir: cc, EventsKey: eventsKey, InternalKey: webKey, AdminKey: adminKey, CORS: origins, RatePerMin: 120, Verse: verse}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	go func() {
		log.Printf("rewards: http on %s (poll every %s)", srv.Addr, interval)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("rewards: http: %v", err)
			stop()
		}
	}()
	<-ctx.Done()
	sd, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(sd)
}

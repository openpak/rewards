# Changelog — rewards

`git log` stays the source of truth; this file is the readable summary.

## v0.19.0 — 2026-11-05
- Card #131 phase 9, owner decision: the 20-title cap is lifted. History asks the core for every title (`limit = -1`, account v0.23.0). Titles that were hidden
  by the cap get a play_cursor row on first sight and are paid once through the normal history rule (`payHistory`); titles already counted keep their cursor
  and are not paid again. A second or later `history` payment carries a ledger `ref` naming it a catch-up. No ledger row is written by hand.
- Cursors stay fresh because every title is now returned each tick, so the 180-day stale purge cannot drop a counted title and re-pay it.

## v0.18.0 — 2026-11-05
- Card #131 phase 9 (playtime exists once): documented that `play_cursor`/`activity` are a cache of the core, not a second measurement
  (`total_seconds` is overwritten from the core's GetPlayHistory every tick; no other writer). New consistency check at boot (+10 min) and
  every 24 h: logs `playtime consistency: accounts titles missing differing max_diff_seconds` (counts only). New test that Purge covers every
  table with an account_id (audit excepted). Known gap, not changed: the core caps GetPlayHistory at 20 titles per namespace, so older titles
  never reach the cache (counted as `missing`).

## v0.17.0 — 2026-11-05
- Migration 0015 (card #132): `game_sessions.call-of-duty-modern-warfare-3` (PS3, reported by demonware under the `np` namespace; the existing
  `np` network mapping and link resolution already cover that namespace).

## v0.16.0 — 2026-11-04
- Card #85 (database only): reward artwork bytes live in Postgres (`asset_blobs`, migration 0014, keyed by sha256); serving reads from the database.
  Start-up imports any file in `REWARDS_ASSET_DIR` not yet in the table (counts logged, files never deleted).

## v0.15.0 — 2026-11-04
- Migration 0013: `leaderboard_new_best.<title>` for the 33 PS3 boards (29 titles plus the EU/JP regions of Demon's Souls and Armored Core V), the 30 Xbox 360 titles
  (`x360:<title id>`) and `mario-kart-wii`.

## v0.14.0 — 2026-11-04
- Migration 0012 (card #63): `leaderboard_new_best.<title>` for the 24 switch-nex Ranking/Ranking2 titles (Castle Crashers Remastered, Monopoly, TMNT Shredder's Revenge,
  Pac-Man 99, Mario Party Superstars, Mario Golf Super Rush, ARMS, SMB35, Mario Tennis Aces, Animal Crossing NH, Luigi's Mansion 3, Mario Strikers, Minecraft, Splatoon 2,
  Super Mario Maker 2, Super Mario Odyssey, Smash Ultimate, 3D World + Bowser's Fury, Kirby's Dream Buffet, Clubhouse Games 51, Risk of Rain 1/Returns/2, Don't Starve Together).

## v0.13.0 — 2026-11-04
- Migration 0011 (card #67): metric families `leaderboard_new_best` and `achievement_unlocked`, one `<family>.<title>` metric per
  title (mode sum, delta 1) as titles start emitting, matching what the core leaderboards and achievements services send.
  First rows: `leaderboard_new_best.terraria`, `leaderboard_new_best.mario-kart-8-deluxe`, `achievement_unlocked.terraria`.
  Add a row per title in a new migration; an undefined title is rejected as an unknown metric.

## v0.12.0 — 2026-11-03
- Per-request logging: one slog line per HTTP request (method, path, status, ms) on stderr via internal/httplog. Query strings, headers and bodies are never logged; /healthz and /metrics are skipped.

## 0.11.0

- Migration 0010 (card #80): three metric families for ladders that span games. `sports_sessions` (ARMS, Mario Tennis Aces,
  Mario Strikers: Battle League, Mario Golf: Super Rush), `royale_sessions` (Super Mario Bros. 35, Pac-Man 99) and
  `hunt_sessions` (the Monster Hunter games on Wii U, 3DS and Switch). Members keep their `game_sessions.<game>` ids, only
  the family moves. New metric `game_sessions.monster-hunter-generations-ultimate`.

## 0.10.0

- Migration 0009: `game_sessions.<game>` for the vendor-hosted Switch titles (Outbound, Moving Out 1 and 2, We Were
  Here, Stumble Guys, Among Us, Fall Guys, Diablo II: Resurrected, Diablo III, Crash Team Racing Nitro-Fueled,
  DOOM + DOOM II, Northgard, TMNT Shredder's Revenge, PvZ Battle for Neighborville, Brawlhalla, Unrailed!, Super
  Animal Royale, Grounded, Hello Kitty Island Adventure, Black Ice; Core Keeper, Don't Starve Together and Gang
  Beasts already exist), plus `spelunky2_daily_runs` and `spelunky2_ranked_runs` (card #78).

## 0.9.0

- Migration 0008: 16 new `game_sessions.<game>` metrics for the Switch NPLN titles (npln 0.11.0): Stardew Valley,
  Super Mario Bros. Wonder, Human: Fall Flat, PlateUp!, Dinkum, Core Keeper, Go-Go Town!, Bayonetta 3, Pokemon
  Scarlet/Violet, Pokemon Legends: Arceus, Super Mario Party Jamboree, Monster Hunter Rise, Hamster Playground,
  Wavecade, Endless Ocean Luminous, MGS Peace Walker. Terraria reuses the existing id.

## 0.8.0

- Migration 0007. History also counts online sessions (`play_cursor.history_sessions`, the cursor's sessions that no
  activity row holds) and play days (`history_days`, filled once per account from the new account core RPC
  `Sessions.GetPlayDays`, account-api v0.1.1, with the account's first sight as the cutoff). The all-time `sessions`
  and `play_days` metrics include them (days are the union with activity, so none counts twice); daily and weekly
  periods never read them. No extra points.

## 0.7.0

- Play from before Rewards first saw an account now counts, once (owner, 2026-11-03). Migration 0006 keeps each
  cursor's history (`history_seconds`, `history_done`). It adds to lifetime play (hours missions) and to the
  all-time titles/networks/play-seconds metrics, and pays `floor(seconds / SecondsPerPoint)` as ONE ledger entry
  (source `history`, no daily cap). It writes no `activity` rows, so play days and daily/weekly missions are untouched.
- Runs at start for existing accounts (`SettleAllHistory`, idempotent, skips frozen and paused) and at first sight
  for new ones, through the same `payHistory`.

## 0.6.0

- Migration 0005: 65 `game_sessions.<game>` metrics (family `game_sessions`, distinct, cap 40) for every title whose
  server reports online sessions: the Switch NEX titles (switch-nex 0.10.0) and the Wii U/3DS NEX titles (nex 1.5.0).
  No mission uses them yet.

## 0.5.0

- Migration 0004: metric `turf_sessions.s3` (Splatoon 3 online sessions, family `turf_sessions`), sent by npln.
- Docs: Mario Kart Wii and DS sessions have a source (`wfc-qr2`); WFC identities are console user ids.

## 0.4.1

- Friends missions count every accepted friend, not only friends who have played.
- A friend pays its points at once, with no play needed (`FriendMinSeconds` is now 0); the lifetime cap of 10 paid
  friends stays, and a frozen or paused friend still pays nothing.
- At start the service settles every friendship the rules now pay but nothing has paid yet.

## 0.4.0

- `GET /v1/catalog?upcoming=1` (and `/v1/me/catalog?upcoming=1`, which leaves out what the account owns) lists
  active, priced items whose sale starts in the future, soonest first, with `available_from`. An item whose
  `available_until` has passed is never listed. Buying them stays closed until their date.

## 0.3.1

- Kart, Turf and Brawl ladders, per-game firsts and Collectors now count online sessions per game
  (`kart_sessions.*`, `turf_sessions.*`, `brawl_sessions.*`, distinct by session id) instead of finished
  races. The `*_completed.*` metrics are removed. Splatoon 3 has no metric; the Splatoon Collector needs two games.

## 0.3.0

- OpenVerse activity posts: when an account receives a badge (shop purchase, mission reward, admin or event grant),
  the service tells OpenVerse (`POST /internal/v1/activity`), fire and forget with retries. OpenVerse only posts for
  players who opted in. Off unless `REWARDS_VERSE_URL` and `REWARDS_VERSE_KEY` are set.

## 0.2.1

- An event with no `delta` counts as one.

## 0.2.0

- Ladder badges: a metric registry and per-account counts fed only by game servers through
  `POST /internal/v1/events` (idempotent by event id, batched, network identities resolved through the
  account core's links), `sum` and `distinct` metrics, per-game metrics that sum into a family, and
  `family_games:` for Collector badges.
- Daily cap per metric and account: events over it are held, a `metric-cap:` flag is raised, an admin
  releases or discards them.
- 91 ladder, per-game and Collector missions seeded **inactive** (docs/metrics.md).
- Admin grant API for event and supporter items (account ids or handles), audited and idempotent.
- `GET /v1/metrics`, `GET /v1/me/metrics`; a claim now refreshes the next ladder tier.
- Optional `REWARDS_EVENTS_KEY` so game servers do not hold the website key.

## 0.1.0

- First release: point accrual from the account core's server-side play history with daily,
  per-title and elapsed-time limits; check-in, new-title and friend bonuses; missions (daily,
  weekly, once, series) with explicit claims; the catalogue, redemption, inventory, equip and
  showcase; adapter entitlement reads; internal admin API (items, assets, missions, adjust,
  freeze, revoke, flags, audit, stats); erase and pause for privacy.

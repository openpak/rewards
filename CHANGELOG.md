# Changelog — rewards

`git log` stays the source of truth; this file is the readable summary.

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

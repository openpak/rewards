# Metrics, events and ladders

Ladder badges count things game servers saw. A client (console, emulator, app, browser) can never
raise a count: the only route that does is `POST /internal/v1/events`, on the private network,
with a key that only game servers hold.

## Event API

`POST /internal/v1/events` with `X-Internal-Key: <REWARDS_EVENTS_KEY>` (the website's internal key
also works). One event object, or `{"events":[...]}` with at most 100.

```json
{ "event_id": "mk8:tour:41:3:1234:entered", "source": "nex-mario-kart-8",
  "metric": "kart_tournaments_entered", "delta": 1,
  "account_id": "…",                                   // the core account, or
  "identity": { "namespace": "wiiu", "subject": "1234" },
  "keys": ["41"],                                      // distinct metrics: what is being counted
  "key_identity": [ { "namespace": "wiiu", "subject": "5678" } ],  // other players, as identities
  "occurred_at": "2026-11-02T10:00:00Z" }
```

Answer `200 {"results":[{"event_id","status","error"}]}`, one per event. `status`:

| Status | Meaning | Emitter should |
| --- | --- | --- |
| `counted` | added to the account's count | nothing |
| `duplicate` | `event_id` seen before (retries are safe) | nothing |
| `held` | over the metric's daily cap; not counted, an admin flag was raised | nothing |
| `ignored` | account frozen, banned or paused | nothing |
| `unresolved` | the identity has no active account link | nothing (no retry) |
| `rejected` | invalid (unknown metric, bad delta, `occurred_at` older than 7 days); `error` says why | fix the code |
| `error` | the service failed; the whole request answers 503 | retry (the Go emitter does) |

- `event_id` is the dedupe key (kept 60 days). Make it stable for the real occurrence
  (`<game>:<thing>:<ids>`), never random, so a retry or restart cannot count twice.
- Identity: the account core's link `namespace` and `subject`: `wiiu` and `3ds` use the PID in decimal
  (the sibling namespace is tried too), `switch` the 16-hex BAAS user id, `wfc` the console user id (the website links it from a friend code),
  `np` PSN, `xbox360`/`xbox` the XUID. Resolved through the core's `Links.GetLinkBySubject`, cached 10 minutes.
- `sum` metrics take `delta` 1..`max_delta`. `distinct` metrics take `keys` (up to 16) and count each key
  once per account, ever.
- Rate cap: each metric has a `daily_cap` per account per UTC day. Events past it are stored as `held`,
  not counted, and one open flag `metric-cap:<metric>` is raised (`GET /internal/v1/admin/flags`).
  An admin releases (counts them) or discards them, see below.
- Accounts that are frozen, banned or paused count nothing.

The shared Go client is `github.com/openpak/rewards-emit` (async, buffered, retried, never blocks).
Env: `REWARDS_EVENTS_URL=http://openpak-rewards:20180`, `REWARDS_EVENTS_KEY[_FILE]`. Unset = off.

## Metrics

`GET /v1/metrics` lists them; `GET /v1/me/metrics` (signed in) returns the caller's counts as
`{"counts":{"<metric>":n}}`.

Per-game session metrics, with the family they sum into (a mission whose `metric` is the family name sums
its members; `family_games:<family>` is how many members are at least 1, used by the Collector badges). They are
`distinct` metrics: the key is the session (gathering) id, so one session counts once per account.

| Metric | Family |
| --- | --- |
| `kart_sessions.mkds` `.mkwii` `.mk7` `.mk8` `.mk8dx` | `kart_sessions` |
| `turf_sessions.s1` `.s2` `.s3` | `turf_sessions` |
| `brawl_sessions.3ds` `.wiiu` `.ult` | `brawl_sessions` |

Card #80 families (members keep their `game_sessions.<game>` ids, migration 0010): `sports_sessions` (ARMS, Mario Tennis Aces,
Mario Strikers, Mario Golf), `royale_sessions` (Super Mario Bros. 35, Pac-Man 99), `hunt_sessions` (Monster Hunter 3 Ultimate, 4,
4 Ultimate, Generations, XX, Stories, Generations Ultimate, Rise). Ladders `sports-sessions-N`, `royale-sessions-N`,
`hunt-sessions-N`, firsts `sports-first-*`, `royale-first-*`, `hunt-first-*`, Collector `hunt-collector`.

Every other game with a server-side matchmaking store sends `game_sessions.<game>` (family `game_sessions`, same rules,
one per title: `game_sessions.arms`, `.mario-tennis-aces`, `.tetris-axis` ... see migration 0005).

Vendor hosts (Photon, Klei, Team17, EOS, Battle.net, Among Us, Demonware, PlayFab, UGS, Shiro; migration 0009) send the
same `game_sessions.<game>` from their room or lobby membership. Spelunky 2 has no rooms: its server sends
`spelunky2_daily_runs` (key: the day) and `spelunky2_ranked_runs` (key: the run id) for results it accepted.

A **session** is: the player was in a server-side matchmaking gathering together with at least one other player
for 3 continuous minutes (`REWARDS_SESSION_MIN`, default 3m). A lobby alone, or one that ends before 3 minutes,
counts nothing. It proves the player was in an online match or race lobby, not that a race finished.

Standalone: `friends_played_with` (distinct; keys are other players, only accepted friends count, checked
against the core's friend list), `maker_courses_played` (distinct course ids), `maker_courses_uploaded`
(distinct), `trades_completed` (sum), `splatfests_joined` (distinct festival ids),
`kart_tournaments_entered` (distinct tournament ids). Online sessions use the existing `sessions` metric (the core's
server-side session count), no event source needed.

"Completed" means a race or match that finished, never a lobby join. Where a server cannot see a finish, the
per-game metric has no source and its missions stay inactive.

## Ladder missions

Migration 0002 seeds every mission **inactive**. Tiers are `series` missions chained with `requires`,
so tier N+1 starts counting when tier N is claimed:

| Ladder (id prefix) | Metric | Tiers |
| --- | --- | --- |
| `kart-races-N` | `kart_sessions` | 1 10 25 50 100 500 1000 2500 10000 |
| `splatoon-matches-N` | `turf_sessions` | same |
| `smash-battles-N` | `brawl_sessions` | same |
| `online-sessions-N` | `sessions` | same |
| `friends-played-N` | `friends_played_with` | 1 5 10 25 50 100 250 |
| `maker-played-N` | `maker_courses_played` | 1 10 25 50 100 500 1000 2500 |
| `maker-uploaded-N` | `maker_courses_uploaded` | 1 5 10 25 50 100 250 500 |
| `trades-N` | `trades_completed` | 1 10 25 50 100 500 1000 |
| `splatfests-N` | `splatfests_joined` | 1 5 10 25 50 |
| `kart-tournaments-N` | `kart_tournaments_entered` | 1 5 10 25 50 100 |

Per game: `kart-first-<mkds|mkwii|mk7|mk8|mk8dx>`, `splatoon-first-<s1|s2|s3>`, `smash-first-<3ds|wiiu|ult>`
(first online session, `once`); Collectors `kart-collector` (5 games), `splatoon-collector` (3),
`smash-collector` (3), metric `family_games:<family>`. The ids keep their old names; the titles say Kart, Turf
and Brawl Sessions.

Rewards are points only; the badge art attaches with `PUT /internal/v1/admin/missions/{id}` setting
`reward_item`. Activate a mission once its source is verified: `PUT` it with `"active": true`. Progress for
a newly activated mission catches up at the next event or poll (within five minutes).

## Where each count comes from

A server emits only from the code path that proved the action. Per metric, what exists today:

| Metric | Signal | Honest limits |
| --- | --- | --- |
| `kart_sessions.mk7`, `.mk8` (Wii U), `turf_sessions.s1`, `brawl_sessions.3ds`, `.wiiu` | `nex` titles: a watcher reads the title's own `matchmaking.gatherings` table every 20 s and emits once a player has shared a gathering with another player for 3 minutes | Lobby presence, not a finished race. Only ever true for gatherings the server itself holds. |
| `kart_sessions.mk8dx`, `turf_sessions.s2`, `brawl_sessions.ult` | `switch-nex`: the same watcher over the title's in-memory gatherings | Same. |
| `kart_sessions.mkwii`, `.mkds` | `wfc-qr2`: a watcher over its matchmaking groups (rooms) emits once a player has shared a group with another player for 3 minutes | Lobby presence, not a finished race. The identity is `wfc` plus the console user id; only consoles linked on the website count (an unlinked console answers `unresolved`). |
| `turf_sessions.s3` | `npln` (Splatoon 3): a watcher over the game sessions (rooms, private battles, matchmaker pools) emits once a player has shared one with another player for 3 minutes | Lobby presence, not a finished match. The identity is `switch` plus the BAAS user id. |
| `sessions` (Online Sessions) | the account core's session history | already live, no event source. |
| `friends_played_with` | none wired | needs a co-session signal from matchmaking. |
| `splatfests_joined` | Splatoon Splatfest match upload, distinct festival ids | |
| `kart_tournaments_entered` | MK8 (Wii U) competition score upload, distinct tournament ids | |
| `maker_courses_uploaded` | SMM1: thumbnail attach complete (`CompleteAttachFile`, ends a course upload). SMM2: `CompleteCourse` | distinct course ids. |
| `maker_courses_played` | SMM1: course record stored for a course that is not your own (a clear). SMM2: course body download for a course that is not your own | SMM2 has no clear signal; a download is the proxy. |
| `trades_completed` | `pokemon-global-link` (taker when the trade is accepted, owner when the traded listing is collected) and `pokemon-home` (GTS taker, Wonder Box collect, both sides of a room trade) | GTS ids are shared, so a trade seen by both services counts once. Gen 4/5 trainers have no `wfc` core links, so they count once WFC accounts are linked. |

## Admin: held events, metrics, grants

| Route | What |
| --- | --- |
| `GET /internal/v1/admin/metrics` | the metric list with caps |
| `PUT /internal/v1/admin/metrics/{id}` | `{"daily_cap":200,"max_delta":1,"enabled":true}`; `204` |
| `POST /internal/v1/admin/accounts/{id}/metrics/{metric}/release` | count the held events; closes the flag; `200 {"events":n}` |
| `POST /internal/v1/admin/accounts/{id}/metrics/{metric}/discard` | drop them (abuse); closes the flag |
| `POST /internal/v1/admin/grants` | grant an event or supporter item, below |
| `GET /internal/v1/admin/grants?item_id=&limit=` | who received it: `{"grants":[{id,at,actor,action,target,detail}]}` |

### Grant API for events and supporters (website `/admin`)

Event-participation and supporter badges cannot be earned; an admin hands them out. Create the item first
(`POST /internal/v1/admin/items`, kind `badge`, no price), then:

```
POST /internal/v1/admin/grants        X-Admin-Key + X-Admin-Actor (the signed-in admin's account id)
{ "kind": "event" | "supporter",
  "item_id": "badge-launch-weekend",
  "reason": "Launch weekend 2026-11-07",            // required, written to the audit trail
  "account_ids": ["…"],                              // and/or
  "handles": ["@someone"] }                          // resolved through the account core; up to 500 in all
```

`200 {"results":[{"account_id","status":"granted"|"already_owned"}], "unknown":["ghost"]}`: `unknown` are
handles or ids that do not exist. Idempotent: granting again is `already_owned`. Each new grant is audited
(`grant.event` / `grant.supporter`). The website key and the events key cannot call it. Revoking is the
existing `POST /internal/v1/admin/redemptions/{id}/revoke`.

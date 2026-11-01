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
  (the sibling namespace is tried too), `switch` the 16-hex BAAS user id, `wfc` the WFC profile id,
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

Per-game "completed" metrics, with the family they sum into (a mission whose `metric` is the family name sums
its members; `family_games:<family>` is how many members are at least 1, used by the Collector badges):

| Metric | Family |
| --- | --- |
| `kart_races_completed.mkds` `.mkwii` `.mk7` `.mk8` `.mk8dx` | `kart_races_completed` |
| `splatoon_matches_completed.s1` `.s2` `.s3` | `splatoon_matches_completed` |
| `smash_battles_completed.3ds` `.wiiu` `.ult` | `smash_battles_completed` |

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
| `kart-races-N` | `kart_races_completed` | 1 10 25 50 100 500 1000 2500 10000 |
| `splatoon-matches-N` | `splatoon_matches_completed` | same |
| `smash-battles-N` | `smash_battles_completed` | same |
| `online-sessions-N` | `sessions` | same |
| `friends-played-N` | `friends_played_with` | 1 5 10 25 50 100 250 |
| `maker-played-N` | `maker_courses_played` | 1 10 25 50 100 500 1000 2500 |
| `maker-uploaded-N` | `maker_courses_uploaded` | 1 5 10 25 50 100 250 500 |
| `trades-N` | `trades_completed` | 1 10 25 50 100 500 1000 |
| `splatfests-N` | `splatfests_joined` | 1 5 10 25 50 |
| `kart-tournaments-N` | `kart_tournaments_entered` | 1 5 10 25 50 100 |

Per game: `kart-first-<mkds|mkwii|mk7|mk8|mk8dx>`, `splatoon-first-<s1|s2|s3>`, `smash-first-<3ds|wiiu|ult>`
(first finished game, `once`); Collectors `kart-collector` (5 games), `splatoon-collector` (3),
`smash-collector` (3), metric `family_games:<family>`.

Rewards are points only; the badge art attaches with `PUT /internal/v1/admin/missions/{id}` setting
`reward_item`. Activate a mission once its source is verified: `PUT` it with `"active": true`. Progress for
a newly activated mission catches up at the next event or poll (within five minutes).

## Where each count comes from

A server emits only from the code path that proved the action. Per metric, what exists today:

| Metric | Signal | Honest limits |
| --- | --- | --- |
| `kart_races_completed.mkwii`, `.mkds` | none | online races are peer to peer; `wfc-race` serves the leaderboard read only (no score submission yet), so no finish is observed. No source. |
| `kart_races_completed.mk7` | none | matchmaking only. No source. |
| `kart_races_completed.mk8` (Wii U) | NEX ranking competition score upload (`nex` mario-kart-8) | counts one finished tournament race per (tournament, season); not an online multiplayer race. A regular online race leaves no trace. |
| `kart_races_completed.mk8dx` | none wired | |
| `splatoon_matches_completed.s1` | Splatfest match upload (Ranking method 0x12, `nex` splatoon) | Splatfest matches only; ordinary Turf War and Ranked matches are not reported to the server. |
| `splatoon_matches_completed.s2`, `.s3` | none | Splatoon 2 only exposes Splatfest ranking reads; Splatoon 3 is not hosted. |
| `smash_battles_completed.*` | none | matchmaking and datastore only. |
| `sessions` (Online Sessions) | the account core's session history | already live, no event source. |
| `friends_played_with` | none wired | needs a co-session signal from matchmaking. |
| `splatfests_joined` | same upload as `splatoon_matches_completed.s1` | distinct festival ids. |
| `kart_tournaments_entered` | same upload as `kart_races_completed.mk8` | distinct tournament ids. |
| `maker_courses_uploaded` | SMM1: thumbnail attach complete (`CompleteAttachFile`, ends a course upload). SMM2: `CompleteCourse` | distinct course ids. |
| `maker_courses_played` | SMM1: course record stored for a course that is not your own (a clear). SMM2: course body download for a course that is not your own | SMM2 has no clear signal; a download is the proxy. |
| `trades_completed` | `pokemon-global-link` (taker when the trade is accepted, owner when the traded listing is collected) and `pokemon-home` (GTS taker, Wonder Box collect, both sides of a room trade) | GTS ids are shared (`gts:<listing>:<side>`), so a trade seen by both services counts once. Global Link identifies trainers by PID: Gen 6-7 as `3ds`, Gen 8+ as `switch`, Gen 4-5 as `wfc`, which counts once WFC accounts are linked in the core. |

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

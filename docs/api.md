# API reference: rewards

Base URL `https://rewards.openpak.org` (public, `/v1` only). The website reaches the same service on the
private network at `http://openpak-rewards:20180`, where `/internal/v1` also exists. JSON, UTF-8.
Every JSON response carries `Cache-Control: no-store`. Errors are `{"error": "..."}`.

## Conventions

**User routes** (`/v1/me...`) need the signed-in person's account-core session:

    Authorization: Bearer <session token>

The service asks the account core's `GET /v1/whoami` who that is (active accounts only); the caller is
always that account, there is no way to name another. Missing or rejected session: `401`.
The account core being unreachable: `502`. More than 120 requests a minute for one account: `429`
with `Retry-After`.

**Anonymous routes**: the catalogue, mission definitions and item previews.

**Internal routes** (`/internal/v1...`) need `X-Internal-Key` (website, adapters). **Admin routes**
(`/internal/v1/admin...`) need `X-Admin-Key` and `X-Admin-Actor: <admin's account id>` (the signed-in
admin, checked by the website against its admin list); the actor is written to the audit trail.

| Status | Meaning |
| --- | --- |
| 400 | invalid request; `error` says what |
| 401 | no or bad session / key |
| 402 | not enough points |
| 403 | rewards frozen for this account |
| 404 | not found (also: you do not own that item) |
| 409 | already owned, sold out, not for sale |
| 429 | rate limited |

Browsers on `https://openpak.org` may call `/v1` directly (CORS allow-list `REWARDS_CORS_ORIGINS`);
the website may also proxy it. Times are RFC 3339 UTC. Points are integers. Days and weeks are UTC.

## Objects

**Item**

```json
{ "id": "badge-first-steps", "kind": "badge", "name": "First Steps", "description": "",
  "price": 50, "platforms": ["web", "app"], "stock": null, "available_from": null,
  "available_until": null, "limit_per_account": 1, "license": "openpak-original",
  "state": "active", "limited": false, "has_preview": true,
  "owned": false, "affordable": true }
```

`kind` is one of `badge profile_icon theme title switch_icon x360_gamerpic x360_theme ps3_avatar ps3_theme`;
it is also the equip slot. `price` is `null` for items that are only granted (mission rewards, gifts).
`owned` and `affordable` appear only on `/v1/me/catalog`. `limited` is true when stock or an end date applies.
Preview image: `GET /v1/items/{id}/preview`.

**Mission**

```json
{ "id": "daily-play-30", "title": "Play for 30 minutes", "description": "...", "kind": "daily",
  "metric": "play_seconds", "target": 1800, "network": "", "title_id": "", "requires": null,
  "reward_points": 2, "reward_item": null, "starts_at": null, "ends_at": null, "active": true, "sort": 10 }
```

`kind`: `daily` `weekly` `once` `series` (a step unlocked when `requires` is claimed). `metric` and `target`:
`play_seconds` (seconds), `play_days`, `titles_played`, `networks_played`, `friends_added`, `sessions`,
`lifetime_play_seconds`, or a server-fed count (`kart_races_completed`, `trades_completed`, ...; see
[metrics.md](metrics.md)). Seconds are for the website to show as minutes or hours. Ladder missions are
`series` steps; the next tier shows `locked: true` until the previous one is claimed. Show a ladder as its
list of tiers (`id` prefix, `target`); `GET /v1/me/metrics` gives the running count.

## Anonymous

| Route | Answer |
| --- | --- |
| `GET /healthz` | `ok` |
| `GET /v1/metrics` | `{"metrics":[{id,family,title,mode,daily_cap,max_delta,require_friend,enabled}]}` |
| `GET /v1/catalog?kind=&platform=` | `{"items":[Item]}`: active items on sale now (no ownership info) |
| `GET /v1/missions` | `{"missions":[Mission]}`: active missions in their window |
| `GET /v1/items/{id}/preview` | the preview image of an active item (`Content-Type` as uploaded, cacheable 1 h); 404 otherwise |

## Signed-in user

### `GET /v1/me`: the points page header

```json
{ "account": { "account_id": "…", "balance": 42, "lifetime_earned": 130, "lifetime_play_seconds": 54000,
               "frozen": false, "paused": false, "showcase_public": false,
               "created_at": "…", "last_poll_at": "…" },
  "today": { "day": "2026-11-02", "play_points": 3, "play_cap": 8, "play_seconds": 3000,
             "checkin_done": true, "new_titles": 1, "new_titles_cap": 3 },
  "equipped": { "badge": "badge-first-steps" } }
```

`last_poll_at` is when the service last read this account's play time (about every 5 minutes), so a
fresh session can take up to one interval to show up. `frozen` means the account earns and redeems
nothing (banned or frozen by an admin). `paused` is the person's own opt-out.

### `GET /v1/me/metrics`

`{"counts":{"kart_races_completed.mk8":3,"trades_completed":12}}`: the caller's server-observed counts.

### `GET /v1/me/ledger?before=<id>&limit=<1-200>`

`{"entries":[{"id":101,"delta":4,"kind":"earn","source":"play","ref":"<title>","created_at":"…"}]}`,
newest first. Page by passing the last `id` as `before`. `kind`: `earn spend adjust reverse`;
`source`: `play checkin new_title friend mission redeem admin revoke`. `ref` is a title id, a mission
id and period, an item id or an admin's reason.

### `GET /v1/me/missions`: the missions page

```json
{ "missions": [ { "…Mission fields…": "…", "period": "2026-11-02", "progress": 1500, "completed": false,
                  "completed_at": null, "claimed": false, "claimable": false, "locked": false } ] }
```

One entry per live mission for the current period (`daily` day, `weekly` ISO week like `2026-W44`,
`once`), plus, for daily and weekly, the previous period's entry when it was completed and is still
unclaimed. Show a progress bar from `progress / target`; `claimable` enables the Claim button;
`locked` marks a series step whose `requires` is not claimed yet.

### `POST /v1/me/missions/{id}/claim`

Body optional: `{"period": "2026-W43"}` (current or previous period; default current).
`200 {"mission_id","period","points","item","balance"}`. Claiming twice returns the same shape with
`points: 0`; it never pays twice. `400` if not completed. `403` if frozen.

### `GET /v1/me/catalog?kind=&platform=`: the shop

Like `/v1/catalog` with `owned` and `affordable`. Retired items you own are still listed.

### `POST /v1/me/redeem`

Body `{"item_id":"badge-first-steps"}`, optional header `Idempotency-Key` (up to 64 characters; a retry
with the same key returns the first redemption and charges once). `201`:

```json
{ "id": 17, "item_id": "badge-first-steps", "price": 50, "source": "shop", "state": "granted",
  "created_at": "…", "balance": 92 }
```

`402` not enough points, `409` already owned / sold out / not for sale, `404` unknown item, `403` frozen.

### `GET /v1/me/inventory`

`{"items":[Item + {"redemption_id","source","acquired_at","equipped"}]}`; `source` is `shop`, `mission`
or `admin`.

### `GET /v1/me/items/{id}/asset?platform=web`

The reward file itself, for the owner only (`404` for anyone else). `ETag` is the SHA-256. `platform`
selects which export (`web`, `app`, `switch`, …); default is the first available.

### `PUT /v1/me/equipped/{slot}`, `DELETE /v1/me/equipped/{slot}`

`PUT` body `{"item_id":"…"}`; the slot must equal the item's `kind` and the item must be owned. `204`.
`DELETE` clears the slot. Slots are the item kinds.

### `PUT /v1/me/settings`

`{"showcase_public": true, "paused": false}`, either or both. `204`. `showcase_public` lets the person's
equipped badge, profile icon and title appear on their public profile (default off). `paused: true` stops
earning and hides the showcase.

### `DELETE /v1/me`

Erases everything the service holds about the account (balance, ledger, inventory, progress, cursors). `204`.
Irreversible; confirm in the UI.

## Internal (website and adapters, `X-Internal-Key`)

### `GET /internal/v1/accounts/{account_id}/showcase`

`{"equipped":{"badge":"…","profile_icon":"…","title":"…"}}`: only when the account opted in, otherwise
`{"equipped":{}}`. For rendering a public profile; resolve names and images from the catalogue.

### `GET /internal/v1/adapter/entitlements?account=<id>&platform=<p>`

What a platform adapter (nx-baas, x360-profile, np-account, …) may deliver to that account on `platform`
(`switch`, `xbox360`, `ps3`, …):

```json
{ "entitlements": [ { "item": { "…Item…": "…" }, "equipped": true, "sha256": "…", "bytes": 5120,
                      "content_type": "image/jpeg" } ] }
```

Empty for a frozen account. Cache at most 60 s; if this service is down, show the default.

### `GET /internal/v1/adapter/items/{id}/asset?platform=<p>`

The asset bytes for that platform. `ETag` is the SHA-256 in the entitlement.

## Admin (`X-Admin-Key` + `X-Admin-Actor`)

For the website's `/admin/rewards` page.

| Route | What |
| --- | --- |
| `GET /internal/v1/admin/stats` | `{accounts, points_issued, points_spent, issued_today, redemptions_by_item, open_flags, frozen_accounts}` |
| `GET /internal/v1/admin/flags?all=1&limit=` | abuse queue (open only unless `all=1`): `{flags:[{id, account_id, reason, detail, raised_at, resolved_by, resolved_at}]}`; reasons `play-cap-streak`, `elapsed-clamp`, `metric-cap:<metric>` (a daily event cap was hit; release or discard the held events) |
| `POST /internal/v1/admin/flags/{id}/resolve` | body `{"note":"…"}`; `204` |
| `GET /internal/v1/admin/accounts/{account_id}` | `{account, today, ledger, redemptions, flags, equipped}`; `404` if the account has never been seen |
| `POST /internal/v1/admin/accounts/{account_id}/adjust` | `{"delta": 25, "reason": "event prize"}` (non-zero, within +/-5000, reason required, an admin cannot adjust themselves, never below zero); `200 {"balance"}` |
| `POST /internal/v1/admin/accounts/{account_id}/freeze`, `.../unfreeze` | `204` |
| `POST /internal/v1/admin/accounts/{account_id}/grant` | `{"item_id":"…"}`: gift an item; `204` |
| `POST /internal/v1/admin/redemptions/{id}/revoke` | `{"refund": true}` optional; clears the slot; `204` |
| `GET /internal/v1/admin/audit?target=&before=&limit=` | `{entries:[{id, at, actor, action, target, detail}]}` newest first |
| `GET /internal/v1/admin/items` | every item in every state |
| `POST /internal/v1/admin/items`, `PUT /internal/v1/admin/items/{id}` | create or update metadata (`id`, `kind`, `name`, `description`, `price`, `platforms`, `stock`, `available_from`, `available_until`, `limit_per_account`). State is not set here; `license` is always `openpak-original` |
| `PUT /internal/v1/admin/items/{id}/asset?platform=web&role=asset` (or `role=preview`) | raw body, `Content-Type` required, at most 32 MiB; `{sha256, bytes}` |
| `POST /internal/v1/admin/items/{id}/state` | `{"state":"draft"}`, `"active"` or `"retired"`. Going `active` needs a preview and an asset for every listed platform (text `title` items excepted). `400` otherwise |
| `GET /internal/v1/admin/missions` | all missions |
| `GET /internal/v1/admin/metrics`, `PUT .../metrics/{id}`, `POST .../accounts/{id}/metrics/{metric}/release` or `/discard`, `POST /internal/v1/admin/grants`, `GET /internal/v1/admin/grants` | ladder metrics, held events and event/supporter grants: see [metrics.md](metrics.md) |
| `POST /internal/v1/admin/missions`, `PUT /internal/v1/admin/missions/{id}` | create or replace a mission (Mission fields; `reward_points` 0-1000; `reward_item` must exist) |

Item art review (admin checklist before activating): original OpenPak artwork, no Nintendo, Sony or
Microsoft art, logos, characters or console imitations; correct size for each platform.

## Suggested pages

- **Points** (`/account/rewards`): `GET /v1/me` (balance, today's caps), `GET /v1/me/ledger`.
- **Missions**: `GET /v1/me/missions`, claim button `POST …/claim`.
- **Shop**: `GET /v1/me/catalog`, previews from `/v1/items/{id}/preview`, `POST /v1/me/redeem`.
- **Inventory**: `GET /v1/me/inventory`, equip with `PUT /v1/me/equipped/{slot}`, settings toggle for the showcase.
- **Admin**: the table above; a flags queue first, account lookup second, catalogue and missions third.

## OpenVerse activity posts

If `REWARDS_VERSE_URL` and `REWARDS_VERSE_KEY[_FILE]` are set, a badge that reaches an account (redeemed in the shop, claimed
as a mission reward, or granted by an admin or the event/supporter grant API) is reported to OpenVerse's
`POST /internal/v1/activity` with category `badge`, the badge name, a link to `https://openpak.org/account/rewards` and the badge
preview (`REWARDS_PUBLIC_URL`/v1/items/{id}/preview). The `event_id` is `badge:<account>:<item>:<why>` where why is the
redemption id, the mission and period, or `g` for a grant, so a retry never posts twice. Nothing is posted unless the player
turned activity posts on in OpenVerse. A failure is logged and never fails the request. Items of other kinds are not reported.

## Game cosmetics (adapters and admin)

Keys as elsewhere: `X-Internal-Key` for adapters, `X-Admin-Key` plus `X-Admin-Actor` for admin. See design section 5a.

| Route | What |
| --- | --- |
| `GET /internal/v1/adapter/inventory?account=&game=` (or `namespace=&subject=` instead of `account`; events key or internal key) | `{game, rev, frozen?, owned:[{game_ref, slot, item, source, acquired_at}], equipped:{slot:game_ref}, base:[{game_ref, slot}]}`. `ETag` follows the revision; `If-None-Match` answers 304 |
| `PUT /internal/v1/adapter/equipped` `{account, game, slot, game_ref}` | equip (`game_ref` empty clears). 200 with the inventory; 409 `{error, inventory}` when not owned or the wrong slot; 403 frozen |
| `POST /internal/v1/admin/games/{game}/import` `{catalog_rev, items:[{game_ref, slot, name?, rarity?, origin?, deliverable?, grants?, requires?, price?, platforms?}]}` | upsert, up to 5000 items; answers `{created, updated, unchanged}` |
| `POST /internal/v1/admin/items/{id}/deliverable` `{deliverable}` | mark whether the game has the asset installed |

Catalogue and inventory items of this kind carry `game: {game_id, game_ref, slot, rarity, origin, deliverable}`.

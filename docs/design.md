# OpenPak Rewards: design

Status: approved 2026-11-01; this service is `openpak/rewards`.
Style: My Nintendo / Club Nintendo. Missions earn and play earns points; points buy cosmetic rewards.
Scope: every network OpenPak serves (Switch, Wii U/3DS, Wii/DS, PS3, Xbox 360, Xbox).

The policy choices below are defaults; the
backend is built on those defaults and every one of them is a config value or a catalogue row, so
changing one is not a rewrite.

## 1. Principles

1. **Server-side truth only.** Points come from what OpenPak's own servers observed. Nothing a
   console, emulator, app or browser *says* it did is ever worth a point.
2. **Cosmetic only.** Rewards never change gameplay, matchmaking, quotas, bans or access. No
   pay-to-win, no cash value, not transferable, not purchasable with money.
3. **OpenPak-made assets only.** No Nintendo, Sony or Microsoft art, logos, character likenesses,
   fonts, console UI screenshots or trademarked names in any reward. See section 5.
4. **One ledger, no hidden state.** Every balance change is a row in an append-only ledger with a
   reason and a unique idempotency key. The balance is a cache of the ledger and can be rebuilt.
5. **Boring and cheap.** One Go service, one Postgres database, polling the account core. No new
   client code on any console is needed to earn.

## 2. Where the data comes from

The account core (the `account` repository) owns accounts and live-session leases. An adapter
(nx-baas, nn-account, wfc, np-account, x360-*, xb-*) registers a session lease when a person is
online in a title and heartbeats it; the core closes it on expiry or explicit teardown and keeps
the finished sessions (`play_sessions`). The core exposes this over the `Sessions` gRPC service:

| Call | Used for |
| --- | --- |
| `Sessions.GetPlayHistory(account, namespace)` | per title: sessions, total seconds, first/last played; finished sessions plus the live one. **The accrual input.** |
| `Sessions.GetPresence` | not used for points (a live lease is not yet "played"); reserved for "playing now" badges |
| `Identity.ListAccounts`, `Identity.GetAccount` | who to poll; account status (active/banned) |
| `Events.SubscribeAccountEvents` | `friend_accepted` (friend points), `account_banned`/`account_unbanned` (freeze), `account_deleted` (erase) |
| `GET /v1/whoami` (HTTP) | bearer token to account id for the user routes |

**Parental usage events are not an accrual input.** `parental` stores usage reports that the
console-side adapter relays from the console; that is a client claim in the sense of principle 1.
Parental is used for one thing only: a future "guardian turned rewards off for this member"
switch (section 10). Session leases come from servers that the player must really be connected to,
so they are the only play source.

Namespaces differ per adapter (`switch`, `wiiu`, `3ds`, `wfc`, `np`, `xbox360`, `xbox`; the core
stores whatever the adapter registers). The service maps each to a *network* for missions and
display: `switch`; `wiiu-3ds` (wiiu, 3ds); `wii-ds` (wfc, wii, ds); `ps3` (np, ps3);
`xbox360` (xbox360, x360); `xbox`. The list and the map are config (`REWARDS_NAMESPACES`).

### Accrual loop

Every `REWARDS_POLL_INTERVAL` (default 5 min) the service walks active accounts (`ListAccounts`,
paged) and, for each configured namespace, reads `GetPlayHistory`. It keeps a **cursor per
(account, namespace, title)**: the last `total_seconds` it saw and when. The counted delta is the
growth of `total_seconds` since the cursor. The first time it sees a title it only records the
baseline (no backfill, see Q3). A total that goes *down* (core pruned or reset) re-baselines and
awards nothing.

It runs from the account core only; the service has no public path that can write a play time.

## 3. Point sources and anti-abuse limits

All numbers are defaults in one `Rules` struct (`internal/rules`), unit-tested, and overridable by
environment; days are UTC.

| Source | Points | Conditions | Limits |
| --- | --- | --- | --- |
| Online play | 1 per 15 min of counted time (sub-15-min remainder carries) | server-side session time from the core | 4 pts/day per title; 8 pts/day total (2 h of credited play a day) |
| Daily check-in | 1 | at least 5 counted minutes that day | 1/day |
| New title | 2 | at least 10 counted minutes in a title never credited to this account before | once per (account, title); 3/day |
| Friend added | 3 | `friend_accepted` from the core; the friend is an active account with at least 30 counted minutes of its own | once per pair; 10 awards lifetime (30 pts) |
| Missions | per mission | section 4 | each mission once per period; claimed explicitly |
| Admin grant | any | audited, actor recorded | admin only |

Overall ceiling for the four automatic sources: **8 + 1 + 6 + friends**, about 15 points a day,
and no source scales with how many consoles or accounts a person owns.

Anti-abuse rules (each has a test):

- **Elapsed-time clamp.** A delta can never exceed the wall-clock time since the previous poll of
  that cursor (plus 60 s slack). A corrupted or replayed total cannot mint hours.
- **Daily caps** per source and per title, as above, enforced in the same transaction that inserts
  the ledger row (`daily_earn` counters, `ON CONFLICT` guarded), so two pollers cannot overshoot.
- **No banking.** The sub-point remainder carries but is capped below one point, and counted
  seconds that hit a cap are dropped, not saved for tomorrow. Leaving a console idle overnight
  earns today's cap, once.
- **Idempotency.** Every ledger row has a unique key derived from its cause
  (`play:<acct>:<title>:<day>:<n>`, `mission:<id>:<period>`, `friend:<a>:<b>`,
  `redeem:<id>`). Replaying a poll, an event or an API call is a no-op.
- **Frozen accounts** (banned in the core, or frozen by an admin) neither earn nor redeem; points
  are kept. Unbanning restores.
- **Idle-farm flag.** An account that hits the play cap seven days running, or whose deltas are
  clamped by the elapsed-time rule more than 3 times in a day, raises a *flag* for the admin
  queue. Flags do not take points; a human decides (section 8).
- **Friend ring guard.** Friend points need the friend's own earned play (30 min), a lifetime cap of
  10, and one award per pair, so alt accounts cost real play time to farm.
- **Spend guard.** Redemption locks the balance row, checks balance, stock and per-account limit
  in one transaction; the balance can never go negative (`CHECK (balance >= 0)`).
- **No client-supplied numbers** exist anywhere in the user API. The only write routes for users are
  redeem, claim, equip and settings.

## 4. Missions

A mission is a row (`missions`), editable in the admin without a deploy.

| Field | Meaning |
| --- | --- |
| `kind` | `daily` (period = the UTC day), `weekly` (ISO week), `once` (never resets), `series` (a `once` mission in a chain, unlocked by `requires`) |
| `metric` | what is counted: `play_seconds`, `play_days`, `titles_played`, `networks_played`, `friends_added`, `sessions`, `lifetime_play_seconds` |
| `target` | goal in the metric's unit |
| `network` / `title_id` | optional filter: only that network or title counts |
| `reward_points`, `reward_item` | what claiming gives; a mission can grant points, an item, or both |
| `starts_at`, `ends_at`, `active`, `sort` | scheduling and display order |

Progress is updated by the accrual loop from the same counted deltas that earn points, so a mission
can only be advanced by server-side play. Completion is automatic; the reward is **claimed** by the
user (Club Nintendo style), `POST /v1/me/missions/{id}/claim`, idempotent per period. Unclaimed weekly
missions stay claimable until the next period closes.

Seed set:

| Id | Kind | Goal | Reward |
| --- | --- | --- | --- |
| `daily-play-30` | daily | 30 min of online play | 2 pts |
| `weekly-4-days` | weekly | play on 4 different days | 5 pts |
| `weekly-3-titles` | weekly | play 3 different titles | 5 pts |
| `weekly-2-networks` | weekly | play on 2 networks | 5 pts |
| `first-friend` | once | add a friend | 5 pts |
| `first-online` | once | first counted online play | 3 pts |
| `hours-10` | once | 10 h lifetime play | 20 pts |
| `hours-50` | series (after `hours-10`) | 50 h lifetime play | 50 pts |
| `all-networks` | once | play on 3 networks | 30 pts |

Launch-limited missions (a title week, an event) are added through the admin with `starts_at`/`ends_at`.

## 5. Reward catalogue

Every item is an OpenPak original, designed by OpenPak. An item row records `license`
(`openpak-original`; the admin refuses anything else), the SHA-256 of its asset, and the platforms
it can be delivered on. Hard rules, checked in review before an item goes `active`:

- no Nintendo, Sony, Microsoft or third-party game art, characters, logos, wordmarks, hardware
  silhouettes, fonts or UI screenshots; no names that imitate a console or title;
- drawn by us (or commissioned with a written transfer); no AI-image-model output of existing IP;
- legible and tasteful at the console's smallest size (Switch icons 256x256, 360 gamerpic 64x64,
  PS3 avatar 128x128).

| Kind | What | Platforms | Typical price |
| --- | --- | --- | --- |
| `badge` | small emblem shown next to a name on the website and app | web, app | 50-150 |
| `profile_icon` | avatar for the website/app profile | web, app | 150 |
| `theme` | website/app colour theme (design-token set) | web, app | 400 |
| `switch_icon` | Switch user icon | switch | 150 |
| `x360_gamerpic` | Xbox 360 gamer picture | xbox360 | 150 |
| `x360_theme` | Xbox 360 dashboard theme | xbox360 | 500 |
| `ps3_avatar` | PS3 profile avatar | ps3 | 150 |
| `ps3_theme` | PS3 XMB theme | ps3 | 500 |
| `title` (text) | a profile "title" line | web, app | 100 |

A single item can list several platforms (the same OpenPak artwork exported to each size). Items can be
**limited**: `stock`, `available_from`/`available_until`, and "mission reward only" (price null).
Owning an item is permanent; an item taken out of the shop stays in inventories.

Starter catalogue: the migration seeds missions only. Items are created `draft` by the admin once
the art exists; no item goes live without an asset and a licence line.

## 6. Delivery per platform

The rewards service is the **entitlement authority**. A platform adapter never decides who owns
what; it asks. Two adapter-facing reads (internal key):

- `GET /internal/v1/adapter/entitlements?account=<id>&platform=<p>`: the owned items deliverable on
  that platform, plus the equipped one per slot.
- `GET /internal/v1/adapter/items/{id}/asset`: the bytes (sha256 in `ETag`).

| Platform | Item | How it reaches the player |
| --- | --- | --- |
| Website and app | badge, profile_icon, theme, title | The website/app call the user API (`/v1/me/inventory`, `PUT /v1/me/equipped/{slot}`), render the asset from `GET /v1/me/items/{id}/asset` or, for previews, `GET /v1/items/{id}/preview`. The public profile reads the **showcase** (equipped badges and icon) only when the owner opted in. |
| Switch | `switch_icon` | nx-baas already serves the user icons (avatar CDN, `*.nintendo.*` names the console is redirected to). It lists the account's owned `switch_icon` entitlements as extra choices on the account's icon list and serves the equipped one's bytes. The console picks through the normal firmware user-icon flow; nx-baas calls the adapter entitlements read. Change lives in nx-baas, not here. |
| Xbox 360 | `x360_gamerpic`, `x360_theme` | `x360-profile` returns the equipped gamerpic for the gamertag (its existing profile/gamerpic path) and `x360-store` lists the owned themes as downloadable STFS theme packages in the dashboard's marketplace, gated by the entitlement. Packaging an item as STFS is the asset pipeline's job (section 12), not the service's. |
| PS3 | `ps3_avatar`, `ps3_theme` | `np-account` returns the equipped avatar URL in the profile it already serves; `np-community` (or the planned store surface) offers owned `.p3t` themes for download. |
| Wii U / 3DS | none in v1 | earns points and uses website/app items. A 3DS theme path via the custom-firmware route is a later option (Q8). |
| Wii / DS | none in v1 | same |
| Xbox (original) | none in v1 | same |

An item is *deliverable* on a platform if the platform is in its `platforms` list and an asset
exists for it (`item_assets` is per platform). Adapters cache the entitlement answer briefly (60 s
suggested) and fail soft: if rewards is down the console simply shows the default icon.

## 7. Data model

One Postgres database `rewards`, one role. Account ids are the core's UUIDs (no names or emails
here). Migrations are embedded and applied at start (`migrations/`).

| Table | Purpose |
| --- | --- |
| `accounts` | per account: `frozen`, `paused` (privacy opt-out), `showcase_public`, `created_at`, `balance` (>= 0), `lifetime_earned`, `lifetime_play_seconds`, `last_poll_at` |
| `ledger` | append-only: `id`, `account_id`, `delta`, `kind` (earn/spend/adjust/reverse), `source` (play, checkin, new_title, friend, mission, redeem, admin), `ref`, `idem_key` UNIQUE, `actor`, `created_at` |
| `play_cursor` | PK (account, namespace, title): last `total_seconds`, `sessions`, `seen_at`, `carry_seconds` |
| `daily_earn` | PK (account, day, source, bucket): points awarded and seconds counted that day. The cap counter |
| `titles_credited` | PK (account, title): the new-title bonus has been given |
| `activity` | PK (account, day, network, title): counted seconds and sessions per day. Every play-based mission metric is derived from it |
| `friends` | PK (account, other): an accepted friendship, and `awarded` once the friend bonus was paid |
| `missions` | definitions (section 4), `requires` for chains |
| `mission_progress` | PK (account, mission, period): `progress`, `completed_at`, `claimed_at` |
| `items` | catalogue: `id`, `kind`, `name`, `description`, `price` (null = not for sale), `platforms[]`, `stock`, `available_from/until`, `limit_per_account`, `license`, `state` (draft/active/retired) |
| `item_assets` | PK (item, platform, role[asset/preview]): `sha256`, `bytes`, `content_type` |
| `redemptions` | `id`, `account_id`, `item_id`, `price`, `source` (shop/mission/admin), `state` (granted/revoked), `idem_key` UNIQUE, `created_at` |
| `equipped` | PK (account, slot): `item_id` |
| `flags` | abuse queue: `account_id`, `reason`, `detail`, `raised_at`, `resolved_by`, `resolved_at` |
| `audit` | every admin action: actor, action, target, detail, `at` |
| `core_cursor` | position in the core's event stream |

Assets are files in `REWARDS_ASSET_DIR` (a volume), content-addressed by SHA-256; only metadata is in
Postgres. Max 32 MiB per asset.

## 8. API

Base `https://rewards.openpak.org`, JSON, UTF-8. **User routes** (`/v1/me/...`) need the user's
account-core session as `Authorization: Bearer`; the service resolves it with the core's
`GET /v1/whoami` (active accounts only) and the caller is always that account. Reads of other people
never exist except the opted-in showcase. Anonymous routes are the catalogue and the preview image.
**Internal routes** (`/internal/v1/...`) exist only on the private network (the public Traefik rule
does not route them) and need a key: `X-Internal-Key` (the website, adapters) or `X-Admin-Key` (admin
routes) plus `X-Admin-Actor: <account id>`.

Full request/response reference for the website and app is in [api.md](api.md). Summary:

| Route | Who | What |
| --- | --- | --- |
| `GET /healthz` | open | `ok` |
| `GET /v1/catalog?kind=&platform=` | open | active items (no assets), price, stock, `owned` is absent |
| `GET /v1/items/{id}/preview` | open | preview image of an active item |
| `GET /v1/missions` | open | active mission definitions |
| `GET /v1/me` | user | balance, lifetime, today's earn vs caps, flags state (frozen/paused), settings |
| `GET /v1/me/ledger?before=&limit=` | user | points history, newest first |
| `GET /v1/me/missions` | user | each active mission with progress for the current period, completed, claimed |
| `POST /v1/me/missions/{id}/claim` | user | claim a completed mission; idempotent per period |
| `GET /v1/me/catalog?kind=&platform=` | user | catalogue with `owned`, `affordable` |
| `POST /v1/me/redeem` `{item_id}` | user | spend points; header `Idempotency-Key` optional; 409 on already owned / out of stock, 402 on not enough points |
| `GET /v1/me/inventory` | user | owned items with equipped state |
| `GET /v1/me/items/{id}/asset?platform=` | user (owner) | the asset bytes |
| `PUT /v1/me/equipped/{slot}` `{item_id}` / `DELETE` | user | equip or clear a slot (`badge`, `profile_icon`, `theme`, `title`, `switch_icon`, `x360_gamerpic`, `ps3_avatar`) |
| `PUT /v1/me/settings` `{showcase_public, paused}` | user | privacy settings |
| `DELETE /v1/me` | user | erase all rewards data for this account |
| `GET /internal/v1/accounts/{id}/showcase` | website key | equipped items, only if `showcase_public` |
| `GET /internal/v1/adapter/entitlements?account=&platform=` | internal key | adapter read (section 6) |
| `GET /internal/v1/adapter/items/{id}/asset?platform=` | internal key | asset bytes |
| `GET/POST/PUT /internal/v1/admin/items`, `PUT .../items/{id}/asset?platform=&role=`, `POST .../items/{id}/state` | admin key | catalogue management |
| `GET/POST/PUT /internal/v1/admin/missions` | admin key | mission management |
| `GET /internal/v1/admin/accounts/{id}` | admin key | balance, caps used, ledger, redemptions, flags |
| `POST /internal/v1/admin/accounts/{id}/adjust` `{delta, reason}` | admin key | grant or deduct (audited, reason mandatory) |
| `POST /internal/v1/admin/accounts/{id}/freeze` / `unfreeze` | admin key | |
| `POST /internal/v1/admin/redemptions/{id}/revoke` | admin key | revoke an item; optional refund |
| `GET /internal/v1/admin/flags`, `POST .../flags/{id}/resolve` | admin key | abuse queue |
| `GET /internal/v1/admin/audit` | admin key | audit trail |
| `GET /internal/v1/admin/stats` | admin key | points issued/spent today and total, redemptions by item, flags open |

Errors are `{"error": "..."}` with 400, 401, 402, 404, 409, 429, 502 (account core unavailable).
User routes are rate-limited per bearer (60 requests per minute default).

## 9. Moderation and admin (website `/admin`)

The website already hosts an admin area for administrators. A new **Rewards** page there
talks to the admin routes; the website supplies `X-Admin-Actor` (the signed-in admin's account id)
so the audit trail names a person. The page needs, in order:

1. **Flags queue**: open flags with the evidence (daily cap streak, clamp count), resolve or freeze.
2. **Account lookup**: by account id or handle (the website resolves handle to id): balance, today's
   caps, ledger, redemptions; adjust (reason required), freeze/unfreeze, revoke an item.
3. **Catalogue**: create items as draft, upload assets and previews, set price/stock/window, review
   checklist (OpenPak-original, size, licence), activate or retire.
4. **Missions**: create, schedule, enable/disable.
5. **Stats and audit**.

Moderation rules: an admin cannot adjust their own account (the service refuses actor == target);
every admin write is audited with the reason; revoking an item also clears it from `equipped`;
retired items disappear from the shop, not from inventories; an item found to infringe is *revoked
for everyone* with the audit entry saying so.

## 10. Privacy

- **Minimal data.** The service stores the account UUID, counted seconds per title (cursors),
  points and what the person owns. No names, emails, birthdays, IPs, console ids or friend lists (the
  friend marker is a pair of ids needed to award once).
- **No profile leakage.** Balances, ledgers and inventories are visible to their owner only. The only
  public surface is the **showcase** (equipped badge/icon/title), **off by default**, opt-in.
- **No leaderboards** of points or play time. Nobody can see another person's activity through this service.
- **Pause and erase.** `paused=true` stops accrual and hides the showcase; `DELETE /v1/me` removes all rows
  and assets pointers for the account (ledger included). The core's `account_deleted` event does the same
  automatically.
- **Retention.** Cursor rows for titles not played for 180 days are dropped; `daily_earn` older than 90 days
  is dropped; the ledger is kept as long as the account (it is the balance's proof).
- **Children.** Under-13 members can earn and spend (it is cosmetic) unless a guardian switches rewards
  off in parental controls (Q4). The service reads no birthday itself.
- **Logging.** Info logs carry counts and short ids only; never bearers or keys.

## 11. Defaults still to be confirmed

The numbers and policies above are defaults. Points never expire, never have cash value and cannot be bought or
transferred; every account earns automatically with `paused` as the opt-out; the showcase is private until opted
in; reward artwork is OpenPak property for personal use of the owner of the item and is not part of this
repository. Prices and caps can be changed without a release (`REWARDS_*` settings, catalogue rows).

## 12. Build plan

| Phase | What | Status |
| --- | --- | --- |
| 1 | This design | done |
| 2 | Backend `openpak/rewards`: accrual, missions, catalogue, redemption, internal admin API, deploy | built and deployed (v0.1.0) |
| 3 | Website and app: points balance, missions page, shop, equip; `/admin/rewards` (other agent, from `docs/api.md`) | next |
| 4 | Art and asset pipeline: first catalogue batch; exporters (Switch JPEG, 360 gamerpic + STFS theme, PS3 `.p3t`) | artist |
| 5 | Adapters read entitlements: nx-baas (icons), x360-profile/x360-store, np-account/np-community | after 4 |
| 6 | Parental switch (Q4); event-driven accrual instead of polling when account count grows | later |

Scaling note: polling touches `accounts x namespaces` calls per interval. That is fine at hundreds of
accounts; the later optimisation is to poll only accounts the core's `presence_changed` stream reported
active since the previous tick.

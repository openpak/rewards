# Changelog — rewards

`git log` stays the source of truth; this file is the readable summary.

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

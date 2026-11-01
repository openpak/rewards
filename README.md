# Rewards Server

OpenPak's points, missions and reward shop, in the style of My Nintendo / Club Nintendo.

| | |
| --- | --- |
| Network | Console-neutral: Switch, Wii U/3DS, Wii/DS, PS3, Xbox 360, Xbox |
| Role | Point accrual from server-side play, missions, reward catalogue, redemption, entitlements for platform adapters |
| Depends on | [`account`](https://github.com/openpak/account) (gRPC and HTTP), PostgreSQL |
| Image | `ghcr.io/openpak/rewards` |
| Licence | AGPL-3.0-only (code); reward artwork is not part of this repository |

## What it serves

| Endpoint | What |
| --- | --- |
| HTTP, port 20180, `/v1` | Website and app: balance, missions, shop, inventory, equip. A signed-in user presents their account-core session as `Authorization: Bearer`; the catalogue and previews are anonymous |
| HTTP, port 20180, `/internal/v1` | Private network only: adapter entitlement reads, showcase, and the admin API for the website's `/admin`. Not routed publicly |
| `GET /healthz` | open |

Play time is read only from the account core's own session records. Nothing a console, an app or a
browser claims is ever worth a point. See [Design](docs/design.md) for the rules and limits.

## Run

```sh
cp example.env .env   # fill in; secrets as *_FILE
go run ./cmd/rewards
```

## Docs

- [API](docs/api.md): the reference for the website and app
- [Design](docs/design.md): rules, anti-abuse limits, data model, privacy
- [Deploy](docs/deploy.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).

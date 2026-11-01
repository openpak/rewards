# Deploy: rewards

## Run

```sh
cp example.env .env   # fill in; secrets as *_FILE
go run ./cmd/rewards
```

Needs Postgres (database `rewards`, its own role) and the account core (gRPC 20001, HTTP 20000).
Port block 20180: 20180 HTTP. Not yet in the public port plan.

## Production

- Image `ghcr.io/openpak/rewards:latest`, quadlet `openpak-rewards.container` on `openpak.network`
  (pin the address with `pin-ips.sh --apply`), volume `openpak-rewards-data:/data` for reward files.
- Env in the shared deploy `.env`: `REWARDS_DATABASE_URL`, `REWARDS_CORE_INTERNAL_KEY`,
  `REWARDS_INTERNAL_KEY`, `REWARDS_ADMIN_KEY`, and `REWARDS_EVENTS_KEY` (the key game servers send to
  `POST /internal/v1/events`; at least 32 characters, different from the other two; unset = only the
  internal key can post events). Game servers need `REWARDS_EVENTS_URL=http://openpak-rewards:20180` and
  the same key as `REWARDS_EVENTS_KEY` (see [metrics.md](metrics.md)).
- Traefik routes `Host(rewards.openpak.org) && (PathPrefix(/v1) || Path(/healthz))` to the
  container. `/internal` is deliberately not routed: the website reaches it by container name
  (`http://openpak-rewards:20180`).
- The public hostname also needs a Cloudflare tunnel hostname (or DNS record) for
  `rewards.openpak.org`, the same as the other `*.openpak.org` hosts.

## Tests

```sh
podman run -d --name openpak-rewards-test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=rewards_test \
  -p 127.0.0.1:54381:5432 docker.io/library/postgres:16-alpine
REWARDS_TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:54381/rewards_test?sslmode=disable' \
  GOWORK=off go test -p 1 -race -count=1 ./...
```

The store, API and poller tests drop and recreate the `public` schema of that database; run the
packages one at a time (`-p 1`). Without the variable they skip; the rules tests (every point
rule and anti-abuse limit) always run.

## Repository

Module `github.com/openpak/rewards`. Release by pushing a `vX.Y.Z` tag
(`.github/workflows/release.yml`; tags and manual dispatch only).

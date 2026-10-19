# Relay and self-hosting

`owp-relay` is the Open Watch Party relay: rooms, their playback state machine, limits, persistence and the endpoints of [the protocol](../spec/SPEC.md), in one Go binary. Rooms live in memory; a state directory makes them survive restarts.

## Run it locally

In `relay/`, `go run ./cmd/owp-relay --addr 127.0.0.1:8080` prints `listening 127.0.0.1:8080`. Apps use `ws://127.0.0.1:8080/v1/ws`; an SDK reaches it with `Relays.at("http://127.0.0.1:8080")`. Try `curl http://127.0.0.1:8080/health` and `curl http://127.0.0.1:9090/metrics`.

## Endpoints

On `--addr`: `GET /v1/ws` (the WebSocket, any `Origin`), `GET /v1/info` (JSON `protocol` 1, `id`, `limits`, `policy`; CORS `*`, `Cache-Control: no-cache`) and `GET /health` (200 `ok`). On `--admin-addr`: `GET /metrics` (Prometheus text) and `GET /admin/usage` (`{"id":…,"days":{"YYYY-MM-DD":{"<app>":count}}}`).

The admin listener is plain HTTP and unauthenticated, so keep it local. TLS wraps only `--addr`. Close codes and HTTP refusals at upgrade are in [SPEC.md](../spec/SPEC.md) Appendix B.

## Configuration

Every flag has an environment variable, which replaces the default; a flag on the command line wins. Booleans take Go `strconv.ParseBool` values (`true`, `false`, `1`, `0`, …). Exit 0: `--help` or a clean shutdown. Exit 2: a flag, argument or variable error. Exit 1: a refused configuration or a failed listener. Each limit must be at least 1 and each rate positive. `/v1/info` reports the limits and policy in force.

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--addr` | `OWP_ADDR` | `:8080` | Public listener |
| `--admin-addr` | `OWP_ADMIN_ADDR` | `127.0.0.1:9090` | Admin listener; empty disables it |
| `--id` | `OWP_ID` | `local` | Relay ID: 1-32 of `a-z 0-9 -`, starting with a letter or digit |
| `--prefixes` | `OWP_PREFIXES` | all 31 | First characters of codes it creates; 1-31 distinct code symbols |
| `--denylist` | `OWP_DENYLIST` | none | Refused app names file, re-read on change |
| `--state-dir` | `OWP_STATE_DIR` | none | Snapshots, usage counts, ACME certificates |
| `--trust-proxy` | `OWP_TRUST_PROXY` | `false` | Client IP from the first `X-Forwarded-For` hop |
| `--max-connections` | `OWP_MAX_CONNECTIONS` | `10000` | Connections in total; more get 503 |
| `--max-participants` | `OWP_MAX_PARTICIPANTS` | `8` | Per room, disconnected ones included; 1-20 |
| `--max-connections-per-ip` | `OWP_MAX_CONNECTIONS_PER_IP` | `10` | Per IPv4 or IPv6 /64; more get 429. With `--tls-domains` also the sockets not upgraded yet; more are closed at once |
| `--max-rooms` | `OWP_MAX_ROOMS` | `10000` | Rooms held; `create` beyond it gets `relay_full` |
| `--max-rooms-per-ip` | `OWP_MAX_ROOMS_PER_IP` | `3` | Rooms one IPv4 or IPv6 /64 created and it still holds |
| `--ipv6-factor-56` | `OWP_IPV6_FACTOR_56` | `4` | Per-IP multiplier for an IPv6 /56 |
| `--ipv6-factor-48` | `OWP_IPV6_FACTOR_48` | `16` | Per-IP multiplier for an IPv6 /48 |
| `--message-burst` | `OWP_MESSAGE_BURST` | `30` | Per-connection message bucket |
| `--messages-per-second` | `OWP_MESSAGES_PER_SECOND` | `10` | Refill; a message with no token gets `rate_limited` |
| `--join-burst` | `OWP_JOIN_BURST` | `20` | Per-IP `join` bucket |
| `--joins-per-second` | `OWP_JOINS_PER_SECOND` | `1` | Per-IP `join` refill |
| `--join-burst-total` | `OWP_JOIN_BURST_TOTAL` | `2000` | Relay-wide `join` bucket |
| `--joins-per-second-total` | `OWP_JOINS_PER_SECOND_TOTAL` | `200` | Relay-wide `join` refill |
| `--timer-scale` | `OWP_TIMER_SCALE` | `1` | Test mode: multiplies the scaled policy durations (SPEC.md §13.3); positive |
| `--tls-domains` | `OWP_TLS_DOMAINS` | none | Hostnames for ACME TLS on `--addr`; needs `--state-dir` |
| `--acme-directory` | `OWP_ACME_DIRECTORY` | `https://acme-v02.api.letsencrypt.org/directory` | ACME directory; must be `https://` |
| `--acme-email` | `OWP_ACME_EMAIL` | none | ACME account contact |

Per-IP limits count an IPv4 alone and an IPv6 in its /64 (×1), /56 and /48 (× the factors); a request must fit every group. Only `join` uses the join buckets.

## Deny list file

The deny list holds one app name per line — the part of `client` before the first `/` — matched ASCII case-insensitively, trimmed, with blank lines and `#` lines ignored:

```
# Apps this relay refuses, one per line
ExampleApp
```

`create`, `join` and `resume` from a listed app get `client_blocked`; participants already in rooms stay. The file is re-read on change. A missing file is an empty list; an unreadable one keeps the previous list and is logged.

## State and restarts

`--state-dir` is created mode 0700. `rooms.json` holds all rooms — tokens as SHA-256 hashes only, no IPs, but names, clients and descriptors are there. It is rewritten within 4 s of a change and every 4 s while a room plays, so a crash loses at most 5 s. `usage.json` holds the daily counts.

SIGTERM/SIGINT: stop accepting, save, close all with 1001, wait up to 2 s for them to close, exit 0. A restart restores rooms under 24 h old (counted from creation): with media `paused` at the saved position, without `idle`, participants disconnected but resumable. A room is deleted once its last participant is gone, so a restored room lasts until its participants resume or their reservations run out. Without `--state-dir`, nothing survives.

## Metrics, usage and logs

Gauges: `owp_build_info{version}` (always 1), `owp_connections`, `owp_rooms`, `owp_participants{connected}`, `owp_participants_by_client{client}` (the day's top 50 names, rest `(other)`). Counters: `owp_rooms_created_total`, `owp_joins_total`, `owp_resumes_total`, `owp_messages_total`, `owp_rejected_connections_total{reason}`, `owp_errors_total{code}`.

Usage counts accepted creates, joins and resumes per UTC day and app, kept 400 days. No IPs, names, codes or titles.

`listening` to stdout, rest to stderr (UTC). Codes, tokens, names, clients, descriptors, message content and client addresses are never logged, nor failed TLS handshakes.

## TLS and proxies

With `--tls-domains`, `--addr` serves TLS itself: no port 80, no redirect, plain HTTP unanswered. Names must be ASCII DNS names with two or more labels, no wildcard, trailing dot or IP (`localhost` refused). Certificates come over [TLS-ALPN-01](https://www.rfc-editor.org/rfc/rfc8737) on TCP 443, so the name must resolve here and 443 must reach `--addr`; they live in `<state-dir>/acme`. It logs each certificate's expiry after start and daily; renewal is automatic. Test via `--acme-directory` ([staging](https://letsencrypt.org/docs/staging-environment/), [Pebble](https://github.com/letsencrypt/pebble)). At most 1024 sockets wait for a handshake or a request at once, each address group holding no more than its `--max-connections-per-ip` share; a request that doesn't upgrade ends its connection.

Behind a proxy, leave `--tls-domains` unset: the proxy terminates TLS and forwards `/v1/ws` (with upgrade), `/v1/info`, `/health`, keeping the CORS header; 30 s pings fit nginx's 60 s default timeout. Use `--trust-proxy` only if it overwrites `X-Forwarded-For` with the client's address (nginx: `proxy_set_header X-Forwarded-For $remote_addr;`, plus the [standard upgrade headers](https://nginx.org/en/docs/http/websocket.html)); an unparsable first value gets 400.

Proxies must not log IPs either (§15). In Docker behind a proxy: volume at `/state`, `OWP_STATE_DIR=/state`; the image runs as UID 65532.

## Self-hosting

`relay/compose.yaml` runs one relay from the published image `ghcr.io/edde746/owp-relay`, terminating TLS itself with a Let's Encrypt certificate it gets and renews. You need Docker Compose, an A record for your hostname (AAAA if IPv6) and TCP 443 open; 80 can stay closed. Add a `.env` beside it (else Compose stops: `set OWP_DOMAIN in .env`):

```
OWP_DOMAIN=relay.example.com
```

Then `docker compose up -d`. It listens on container 8443, published as host 443. Apps use it for everything with `Relays.at("https://relay.example.com")` ([SDK.md](../spec/SDK.md) §3), in Kotlin `Relays.at(http, "https://relay.example.com")`, in Swift `try Relays.at("https://relay.example.com")`. Its rooms are separate from the public relays; it serves any app.

Host checks: `curl http://127.0.0.1:9090/metrics`, `docker compose logs relay` (5×10 MB). The file is sized for the default 10000 connections at about 60 KiB each; to allow more, set `OWP_MAX_CONNECTIONS` and raise `mem_limit` (1 GiB) and `GOMEMLIMIT` in proportion.

Tag `1` tracks the latest `1.x.y`: `docker compose pull && docker compose up -d` updates, rooms returning from state. Keep the `relay-state` volume across updates: a new one orders a new certificate, and [Let's Encrypt allows 5 per exact set of names per 7 days](https://letsencrypt.org/docs/rate-limits/). Do not back it up — snapshots carry names and descriptors that should vanish with their room; losing it loses live rooms, usage days and the ACME account key.

Without Docker, run the binary as a user who may bind port 443: `owp-relay --addr :443 --tls-domains relay.example.com --state-dir DIR`.

## Build and test

Go 1.26+. In `relay/`: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=1.0.0" -o bin/owp-relay ./cmd/owp-relay` (static; version defaults `dev`). Image from the root: `docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=1.0.0 -t ghcr.io/edde746/owp-relay relay` (distroless, user 65532).

```sh
cd relay
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

`gofmt -l .` must print nothing. For the full protocol check, see the [conformance suite](../conformance/relay/README.md); also the [integration guide](../docs/integration.md) and [SECURITY.md](../SECURITY.md).

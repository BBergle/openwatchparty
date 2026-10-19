# Relay conformance

`owp-conform` checks a relay against [SPEC.md](../../spec/SPEC.md) (`run`) and measures one under load (`load`). It is its own Go module in this directory. The suite has 68 scenarios in 9 groups named by SPEC.md section, from `§3 Endpoints and transport` to `§15 Privacy`. `--only NAME` runs those whose name contains `NAME`.

```sh
cd conformance/relay
go build -o bin/owp-conform ./cmd/owp-conform
./bin/owp-conform run --list
```

With no arguments it prints the usage and exits 2:

```
usage:
  owp-conform run --relay-bin PATH [--timer-scale 0.1] [--ports 18150-18199] [--examples DIR] [--only NAME]
  owp-conform run --url ws[s]://HOST:PORT/v1/ws [--ca-file PEM] [--connect HOST:PORT] [--timer-scale 0.1]
                  [--trust-proxy] [--examples DIR] [--only NAME]
  owp-conform run --list
  owp-conform load --url ws[s]://HOST:PORT/v1/ws [--ca-file PEM] [--connect HOST:PORT] [--pidfile DIR/relay.pid]
                   [--ssh USER@HOST] [--clients 2000] [--room-size 4] [--ramp 500] [--duration 60s]
                   [--action-interval 10s] [--label X]
```

`run -h` and `load -h` print each mode's flags and exit 0. Exactly one of `--url` and `--relay-bin` is given to `run`.

## Checking a relay binary

```sh
cd relay && go build -o /tmp/owp-relay ./cmd/owp-relay
cd ../conformance/relay && ./bin/owp-conform run --relay-bin /tmp/owp-relay --timer-scale 0.1
```

The suite starts the relay itself, with persistence, a deny list and `--trust-proxy`, in a temporary directory on the first free port of `--ports`. A relay is ready when its first stdout line is `listening <addr>` within 10 s; it is stopped with SIGTERM and must exit within 10 s.

This mode also covers what needs process control: restart and on-disk survival, token-free state, deny-list reloads, and nothing identifying in log or state. `run --list` marks the scenarios that need it. Some start extra relays, 7 ports in all.

After a clean run the files are removed; on failure they are kept (`relay files kept in DIR`). A full run takes ~3.5 minutes (`keepalive` ~1 minute; pings never scale). CI runs it this way.

## Checking a running relay

```sh
owp-relay --addr 127.0.0.1:8080 --timer-scale 0.1 --trust-proxy
./bin/owp-conform run --url ws://127.0.0.1:8080/v1/ws --timer-scale 0.1 --trust-proxy
```

Start the relay with the same `--timer-scale` you pass the suite. Scenarios that need process control are skipped. Every suite client always sends its own `X-Forwarded-For`; `--trust-proxy` tells the suite the relay uses it, so the per-IP scenarios run, and without it they are skipped.

With a `wss://` URL the suite reads `/v1/info` and `/health` over `https://`. `--ca-file PEM` trusts the PEM root certificates in the file besides the system roots (a test CA such as [Pebble](https://github.com/letsencrypt/pebble)); nothing skips verification. `--connect HOST:PORT` dials that address for the `--url` host, which stays the TLS server name and `Host` header.

## Timing, examples and standing checks

The suite reads limits and policy from `/v1/info` once and times every scenario by them. With `--relay-bin`, `--timer-scale` is the scale the suite starts the relay with.

The `examples` scenario sends the `spec/examples` files byte for byte, each `descriptor` example as the `media` of a `setMedia`. Every client message and descriptor example in `invalid` must get the refusal SPEC.md §6.2 gives it, or close 1007 when it is not UTF-8 and 1009 when it is longer than `maxMessageBytes`; every `create`, `setMedia`, `descriptor` and `status` example in `valid` must be accepted, stored and forwarded. It looks for `spec/examples` in the working directory and the directories above it; `--examples DIR` names another, and with none the scenario is skipped.

Throughout, snapshot `rev` ordering is checked on every connection.

## Output and exit status

```
relay /tmp/owp-relay on ws://127.0.0.1:18385/v1/ws, files in /tmp/owp-conform-551494347

§5 Relays, room codes and routing
  PASS relay_list                  2.03s
  PASS relay_list_invalid          4.13s
  PASS relay_list_default           30ms
  PASS relay_list_self_url          30ms

owp-conform: 4 passed, 0 failed, 0 skipped
```

Exit 0 means nothing failed (skips are allowed), 1 means a scenario failed or the relay did not shut down cleanly, 2 means a usage error or the relay did not start, and 130 means Ctrl-C.

## Load mode

```sh
./bin/owp-conform load --url ws://127.0.0.1:8080/v1/ws --clients 8 --room-size 4 --ramp 50 --duration 3s --action-interval 1s
```

`load` fills rooms of `--room-size` with `--clients` participants at `--ramp` new connections per second: hosts create, others join, media shared. Hosts cycle `seek`/`pause`/`seek`/`play` about every `--action-interval` (±20 %); clients ping every 5 s and send `status` every 15 s. After `--duration` of steady state it reports join, ping and fan-out latencies and message rates (lines like `join 8 ok, 0 failed … p50=0.2ms …`), then one `RESULT {json}` line (see `--label`). Clients send their own `X-Forwarded-For`: use a `--trust-proxy` relay or raise its per-IP limits. Runs serialize on `/tmp/owp-bench.lock`. `--pidfile PATH --ssh USER@HOST --ssh-key KEY` samples relay CPU/memory via SSH `/proc`. Exit 1 on a failed join or unexpected disconnect.

See [the relay reference](../../relay/README.md) for the flags used here, and [the vector tables](../vectors/README.md) for the client-side counterpart.

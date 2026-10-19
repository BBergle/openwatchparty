# Conformance vectors

These eleven JSON tables pin the deterministic parts of [SDK.md](../../spec/SDK.md): same input, same result in every SDK, and every SDK runs every table. A Go reference in `tools/` generates them (`ref.go` and `check.go` hold the rules, `cases.go` the cases, `cases_party*.go` the traces of `party.json`). Rules a table cannot express are in each SDK's tests (SDK.md §17).

## Generating and checking

In `conformance/vectors/tools` (Go 1.26):

```
go run .            # rewrite every table
go run . -check     # exit 1 if any table differs from the generator's output
go test ./...       # boundary tests of the reference implementation and the trace checks
```

`-dir <path>` names the vectors directory (default `..`). `-check` reports, sorted, under the header ``vectors are stale (run `go run .` in conformance/vectors/tools):``:

```
  extra.json: not produced by the generator
  names.json: missing
  timeline.json: differs from the generator's output
```

`ref_test.go` pins hand-worked boundaries; it also rejects duplicate case names, nondeterministic output, invalid JSON and non-printable-ASCII bytes. `party_check_test.go` pins the traces the trace check refuses. CI: format, vet, tests, `-check`.

## Adding a case or a table

Cases are added in `cases.go` (helpers such as `add(...)`; `value(...)` adds a case both ways); expectations come from `ref.go` and `check.go`, never by hand. The one exception is `party.json`: `cases_party.go` and the `cases_party_<area>.go` files beside it write its outputs by hand from SDK.md and SPEC.md, with each trace's sections in a comment, and `party_check.go` checks their shape, not their values. Rule changes go in those files and SDK.md together — they must agree — then `go run .` and commit the tables. A new table needs `xxxTable()`, a `tables()` entry and a runner per SDK (every SDK fails until then). The generator refuses encoder-dependent or mislabelled size cases and inputs holding U+FDD0.

## File format

`{"table": <name>, "cases": [...]}` in that order; `table` is the file name minus `.json`. Cases are `{"name","input","expect"}`, one per line, compact printable-ASCII JSON (non-ASCII as `\uXXXX`, no HTML escaping), names unique per table. Times are integer ms.

```json
{
  "table": "timeline",
  "cases": [
    {"name":"idle holds positionMs","input":{"room":{"phase":"idle","positionMs":0,"atMs":1000,"rate":1},"durationMs":3487000,"serverNowMs":50000},"expect":{"positionMs":0,"running":false}},
    …
  ]
}
```

Decode with the platform decoder: inputs hold unpaired surrogate escapes (`"Anna\ud800"`), and expectations are computed on the strings as the SDK platforms hold them. An int may be written with a fraction or exponent — any integral number within ±(2^53−1) counts ([SPEC.md](../../spec/SPEC.md) §2). All `matching`/`same-title` descriptors pass the receive checks, so parse them strictly.

## Comparing results

Compare whole `expect` objects: member order ignored, but absent ≠ `null` (only in `code-routing` and `relay-list` is an error expect just `{"error":…}`; actions carry only their own members) — Kotlin's runner cannot distinguish those. Round `setRate` rates to 6 decimals first. `same-title` is symmetric; TypeScript/Kotlin/Swift also test swapped. `party.json` compares step by step instead, as its section says.

## Runners in the SDKs

| SDK | Runner | Tables | Run |
|---|---|---|---|
| TypeScript | `sdk/typescript/test/unit/vectors.test.ts` | Every `*.json` in `conformance/vectors/` but `party.json`, which `party-traces.test.ts` runs; `every vector table has a runner` fails on any other | `npm run test:unit` in `sdk/typescript` |
| Dart | `sdk/dart/test/vectors_test.dart` | Each table it names, read from `../../conformance/vectors/`, and `party.json` through `party_traces_test.dart`; `every table has a runner` fails on any other | `dart test` in `sdk/dart` |
| Kotlin | `sdk/kotlin/src/commonTest/kotlin/party/openwatch/VectorTest.kt` | Each table it names, from test data generated out of `conformance/vectors/`, and `party.json` through `PartyTraces.kt`; `everyTableRuns` fails on any other | `./gradlew check` in `sdk/kotlin` |
| Swift | `sdk/swift/Tests/OpenWatchPartyTests/VectorTests.swift` | Each table it names, read from `conformance/vectors/`, but `party.json`, which `PartyTraceTests.swift` runs; `everyTableRuns` fails on any other | `swift test --filter VectorTests --filter PartyTraceTests` at the repository root |

For `matching`, TypeScript runs a real party on mocked timers, Kotlin runs the walk under `runTest`, Swift runs the walk on a virtual clock, and Dart calls the ranking and walk functions directly.

Swift decodes the tables with the SDK's own JSON reader, not the platform decoder: a Swift `String` can't hold an unpaired surrogate, so the reader turns each unpaired surrogate escape into U+FFFD and marks the string, and the checks still see it.

## The tables

The constants in play are SDK.md §2's. Each table below gives its input and expect members; the rule itself is the section in its heading.

### `timeline.json` (SDK.md §8)

In: `room{phase,positionMs,atMs,rate}`, `durationMs`, `serverNowMs`. Out: `positionMs`, `running`.

### `clock-sync.json` (SDK.md §5)

In: `events` — `{"ping":id,"atMs":t}` or `{"pong":id,"serverTime":s,"atMs":t}` (local times). Out: `pongs[]`, per pong `result` (`accepted`/`stepped`=window cleared then kept/`ignored`=unknown or round trip outside 0..1000) plus best `offsetMs`/`rttMs` (null if none).

### `reconcile.json` (SDK.md §11)

In: `room`, `durationMs`, `serverNowMs`, `offsetMs`, `player{positionMs,paused,buffering,durationMs|null,canSetRate,rate,seekAccuracyMs?}` (`seekAccuracyMs` an int ≥ 0, absent = 0), `nudging`, `sinceSeekMs` (null = cooled down). Out: `actions` ordered pause/setRate/seek/play (`{"do":…}` plus `rate` or `positionMs` only), `nudging`, `playInMs` (ceil wait for a scheduled local start — also set when a negative local position delays the start — else null). Only a playing seek gets the +250 lead, clamped; a held seek goes to `clamp(positionMs + offsetMs)`.

### `matching.json` (SDK.md §9.4, §9.5)

In: `media`, `candidates[]{id,durationMs?,sizeBytes?,oshash?,name?,probe?{durationMs|null,takesMs}}` (`probe` = simulated answer; null = didn't open). Out: `order`, `candidates[]{id,deltaMs|null}`, `probed`, `result` (`matched|mismatch|unavailable`), `choice`, `deltaMs`.

### `same-title.json` (SDK.md §9.3)

In: descriptors `a`, `b`. Out: `same`.

### `code-routing.json` (SDK.md §4)

In: `code`, `relays[]{id,prefixes}`. Out: normalized `code` + `relay` (first owner of symbol 1, else null), or only `{"error":"invalid_code"}`.

### `relay-list.json` (SDK.md §3.2)

In: `list` (any JSON, a public list document). Out: `relays` as given, or only `{"error":"invalid_list"}`.

### `names.json` (SDK.md §7.1)

In: `field` (`name|client`), `value`. Out: `valid` (false ⇒ constructor throws `invalid_message`). `name`: 1-32 code points. `client`: 1-64, `<app>/<version>`, both non-empty, app ends at first `/`.

### `media-check.json` (SDK.md §9.7)

In: `on` (`receive|send`, also the case-name suffix), `media`. Out: `valid`. Shape/value always; sizes (4096, `ext` 1024) on send only, on the SDK's compact encoding of known members (ints integral, UTF-8). Shape cases are receive-only. Run the receive check on receive, `checkMedia` on send.

### `room-check.json` (SDK.md §8)

In: `room`, the members of a `room` message but `type` and `media`: the snapshot fields and an optional `action`. Out: `valid` (the SDK accepts the `room`). `action`, when present, is an object with `kind` a non-empty string and `by` null or a peer ID.

### `party.json` (SDK.md §3–§15)

Traces of the party state machine. A driver runs a real `WatchParty` against a scripted relay, scripted adapters and a scripted resolver in virtual time, runs step i, settles, and compares what the party did with `outputs[i]`. The drivers are `sdk/typescript/test/unit/party-traces.test.ts`, `sdk/dart/test/party_traces_test.dart`, `sdk/kotlin/src/commonTest/kotlin/party/openwatch/PartyTraces.kt` (run by `VectorTest.party()`) and `sdk/swift/Tests/OpenWatchPartyTests/PartyTraceTests.swift`.

In: `setup`, `steps[]`. Out: `outputs[]`, one per step.

`setup`: `startMs` (local time at the start: the `nowMs` seam and the timers' clock); `relayOffsetMs` (relay time − local time); `rttMs` (the starting round trip `rtt` of every URL: each `ping` gets its `pong` `rtt` later, `serverTime` = local send time + floor(`rtt` / 2) + `relayOffsetMs`); `random` (the draws `u` the randomness seam yields, in order, each a multiple of 2^-53 in [0, 1): a backoff waits floor(base × (0.5 + 0.5`u`)), SDK.md §14); `base` (`Relays.at(base)`, one relay that never fetches, or null for `Relays.public()`); `list` (null with a `base`; else a valid public list, the document every GET of `https://openwatch.party/relays.json` answers, as JSON text); `name`, `client`; `resolver` (true: the scripted resolver is passed). `Relays.public()` starts from the SDK's embedded list, so a trace that routes before a refresh succeeded uses the repository's `relays.json`, which the generator reads; the other traces on the public list call `refresh` first.

A step has one kind:

- `{"call": api, "id"?, …args}` calls the API: `create`, `join{code}`, `leave`, `end`, `close`, `setMedia{media,ref,positionMs?}`, `clearMedia`, `play`, `pause`, `seek{ms}`, `setRate{rate}`, `setOffset{ms}`, `setControl{mode}`, `lock{locked}`, `kick{peerId}`, `transferHost{peerId}`, `choose{candidate}`, `acceptMismatch`, `declineMismatch`, `resolveAgain`, `attachPlayer{player,ref}`, `detachPlayer{player?}`, `playerChanged`, and `refresh` (`Relays.refresh()`). `id` names the call for its `done` output; calls that return a result or a completion (`create`, `join`, `close`, `choose`, `detachPlayer`, `refresh`) need one. A call without `id` must not throw. `setMedia` may carry a descriptor failing the send checks (SDK.md §9.7) only with an `id`.
- `on`: `recv`, `recvText`, `drop` take `"on": url`, the newest open connection to that URL; without it, the newest open connection. `nextOpen` and `rtt` take it too, for that URL's connections only.
- `{"recv": msg, "malformed"?: true}`: the relay sends `msg`. A `joined`, `room`, `error` or `closed` passes SDK.md §8's check of its type, or fails it when marked `malformed`; for a `room` that is the `room-check.json` rule, and its `media` member is outside it (the party checks `media` only on a `room` of a new epoch). A `pong` is only ever a malformed one (`serverTime` not an int ≥ 0), since the driver answers pings. Other types aren't checked and can't be marked.
- `{"recvText": text}`: the relay sends `text` as it is (not JSON, an array, …).
- `{"drop": code|null}`: the relay closes the connection with `code` (null: none).
- `{"nextOpen": "fail"|"hang"}`: the next connection (to `on`, else to any URL; one scripted for its URL goes first) fails without opening, or never opens. Every other connection opens in the settle after the connect.
- `{"rtt": ms|null}`: pings the party sends from now on, to `on`'s connections or (without `on`) every URL's, are answered `ms` ≥ 0 later, or never. Pings already sent keep their round trip.
- `{"nextGet": doc|"fail"|"hang"}`: the next GET answers `doc` (an object) instead of `list`, fails, or never answers.
- `{"advance": ms}`: local time moves `ms` ≥ 1 ahead.
- `{"player": id, positionMs?, paused?, buffering?, durationMs?, firstFrameShown?, canSetRate?, seekAccuracyMs?, playThrows?}` creates adapter `id` on first use (0, true, false, null, true, false, 0, false) and sets the members given. While not paused and not buffering its position moves at the rate last set: the position last set + floor(elapsed × rate). `canSetRate` false means no `setRate`. A command takes effect when it finishes (`play` unpauses, `pause` pauses, `seek` sets the position, `setRate` the rate) and finishes at once unless held; with `playThrows`, `play` throws and changes nothing.
- `{"hold": id}`: adapter `id`'s next command stays running. `{"finish": id, "throws"?}`: it finishes, or throws with no effect.
- `{"answer": [candidate]}` / `{"answerThrows": true}`: the pending `resolve` returns the candidates, or throws. A candidate is `{id, ref?, durationMs?, sizeBytes?, oshash?, name?, probe?}`, `ref` a non-empty string (absent: `id`); `"probe": true` gives it a scripted probe.
- `{"probeAnswer": id, "durationMs": int|null}` / `{"probeThrows": id}`: candidate `id`'s running probe returns, or throws.

Settling runs everything ready without moving time: continuations, connections opening, timers due now (`pong`s with a round trip of 0 included), commands finishing, again until nothing is ready. `advance` takes each timer due by the end in turn, earliest first and equal times in the order they were set, sets the clock to its time and settles; then it sets the clock to the end and settles. No wall-clock wait anywhere.

An output is `{send?, command?, event?, resolver?, net?, done?, state?}`. An absent channel means nothing happened on it; a present one is non-empty and compared exactly, in order. Order across channels isn't pinned.

- `send`: each message the party sent, but `ping`s, as JSON, or `{on: url, msg}` where the trace also pins the connection it went on.
- `command`: `{player, do}` plus `positionMs` (`seek`) or `rate` (`setRate`, rounded to 6 decimals), when the SDK calls the adapter.
- `event`: `{type:"action", kind, byPeerId, by, byMe, room}` (`by` the entry's `peerId` or null, `room` the snapshot's `rev`), `{type:"participantJoined"|"participantLeft", peerId}`, `{type:"error", code, re}`, `{type:"resolveFailed", timeout}` (true: the resolve timeout, false: the resolver threw), `{type:"playBlocked"}`, `{type:"ended", reason, error}` (`error` its code or null).
- `resolver`: `{resolve: title}` / `{resolveCancelled: title}` as the party calls or cancels the resolver; `{probe: id}` / `{probeCancelled: id}` likewise for probes.
- `net`: `{get: url}` per GET, `{open: url}` per connect, `{close: url, code}` when the party closes a connection that had opened (`code` null when it gave none). Compared per URL: each URL's entries in order, the order between URLs not pinned.
- `done`: `{id}`, `{id, value}` or `{id, error: code, re?, message?}` once call `id` completes: `value` is `create`'s code, `choose`'s or `refresh`'s boolean; `error` the `PartyError` code. `re` (string or null) and `message` are compared only where the trace writes them; `message` only for a relay's refusal. The order of a step's `done` entries isn't pinned: compare them by `id`.
- `state`: only the members listed, read after the settle: `connection`, `peerId`, `room` (`rev` or null), `media` (`title` or null), `match` (`{state, deltaMs?}` or null), `choice` (`{id, ref}` or null), `mismatch` (`{id, deltaMs}` or null), `candidates` (`[{id, deltaMs}]`), `offsetMs`, `player`, `endReason`, `isHost`, `canControl` and `roomPositionMs` (its value then).

A trace fails also when, after its last step, a `random` value wasn't drawn (or one more was), a `resolve` or probe is unanswered, an adapter still runs a held command, or a call with `id` never reported `done`. The driver then closes the party without checking more. The generator refuses unknown step kinds, calls, channels, members and state fields, a count of outputs other than steps, commands of unknown adapters, `done` of unknown or finished calls, relay input that passes or fails its check against its `malformed` mark, draws that aren't multiples of 2^-53, and duplicate names.

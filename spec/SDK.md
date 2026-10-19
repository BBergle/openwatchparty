# Open Watch Party SDKs

The client behaviour every SDK implements (§3–§15) and the shared Kotlin/Dart/TypeScript/Swift API (§16). `SPEC.md` owns the wire and wins on anything crossing it. `conformance/vectors/` pins the deterministic parts (§17).

The key words MUST, MUST NOT, REQUIRED, SHOULD, SHOULD NOT, RECOMMENDED, MAY and OPTIONAL in this document are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## Contents

1. Scope
2. Conventions and constants
3. Relay list
4. Room codes and routing
5. Clock sync
6. Choosing a relay for `create`
7. Session lifecycle
8. Room state
9. Matching
10. Status
11. Following the room
12. Local commands
13. Offset
14. Reconnect and resume
15. Events
16. API
17. Conformance

## 1. Scope

An SDK does everything between app and relay: relay list (§3), routing (§4), relay choice (§6), WebSocket, clock sync (§5), resume (§14), room state (§8), events (§15), matching via the app's resolver (§9), `status` (§10), following (§11) and commands (§12). The app supplies a player adapter, UI and, to match other participants' media, a resolver (§16.7).

An SDK MUST implement §3–§15 and pass every vector table (§17). This document defines the algorithms and values `SPEC.md` §16 leaves to it.

Packages: npm/Dart `openwatchparty`, Maven `party.openwatch:openwatchparty` (Kotlin package `party.openwatch`), SwiftPM package `OpenWatchParty` from `https://github.com/edde746/openwatchparty`. Only WebSocket, HTTP GET and JSON are needed; the app may supply the first two (§16.8). Kotlin: kotlinx-coroutines, kotlinx-serialization-json, the app's Ktor `HttpClient` with WebSockets. Dart: pure Dart, no Flutter, Dart ≥ 3.4 < 4; deps `web_socket` and `web`; without `dart:io`/JS interop the default `httpGet` throws `UnsupportedError` — pass one. TypeScript: global `WebSocket`/`fetch` (Node ≥ 22), no runtime deps. Swift: tools ≥ 6.0, Swift 6 language mode; iOS 16, macOS 13, tvOS 16, watchOS 9, visionOS 1; Foundation only (`FoundationNetworking` on Linux), no other deps; the defaults use `URLSessionWebSocketTask` and `URLSession`.

## 2. Conventions and constants

Times/durations are integer ms.

Integers sent are integral, within ±(2^53 − 1) (`SPEC.md` §2) and their field bounds, written without fraction/exponent (`1000`, not `1000.0`/`1e3`); Dart sends `int`, not `double`. The SDK checks app-passed and computed wire ints before any local effect or send; failure throws `invalid_message`, changing nothing: `seek` (§12), `pause`'s computed position, `setMedia` position (0..2^53 − 1), descriptor ints (§9.7), `setOffset` (±604800000, §13).

Delays are 64-bit integer ms, negative = 0. Native timers get ≤ 2147483647 ms each; a clamped timer recomputes the remainder on firing. No unbounded ms becomes a native duration unclamped.

Local time is monotonic, never wall clock (Kotlin `TimeSource.Monotonic`, Dart `Stopwatch`, TS `Math.floor(performance.now())`, Swift `ContinuousClock`; app may pass its own, §16.8). Relay time now = local + clock offset (§5), unknown before the first sample.

Integer halving rounds down (operands ≥ 0). Doubles are IEEE 754, each operation rounded separately — compute every product and quotient into its own variable where `a × b + c` may fuse. Optional "equal" = both present and equal; "ignoring case" adds non-empty and ASCII `A`–`Z` → `a`–`z` only.

These values are constants, not settings:

|constant|value|section|
|---|---|---|
|ping burst|3 pings, 500 ms apart, at connection open|§5, §6|
|ping interval|5000 ms after the burst's first ping|§5|
|clock window|8 samples|§5|
|largest usable round trip|1000 ms|§5, §6|
|clock-step slack|100 ms|§5|
|connect timeout|10000 ms|§6, §7.1, §14|
|resolve timeout|30000 ms|§9.2|
|HTTP GET|aborted after 5000 ms; body at most 65536 bytes; no redirects|§3.4|
|public list source|`https://openwatch.party/relays.json`|§3|
|list refresh|on `joined`, when this object has had no successful refresh yet or the last one is 24 h old, and no background refresh is running|§3|
|match tolerance|1500 ms|§9|
|probes|4 at most, 60000 ms in total|§9|
|status heartbeat|15000 ms|§10|
|status spacing|250 ms|§10|
|tick|500 ms|§11|
|deadband|350 ms|§11|
|nudge|× 0.96 ahead, × 1.04 behind, until under 150 ms|§11|
|seek threshold|2000 ms with rate control, 1000 ms without; the adapter's seek accuracy when larger|§11|
|held seek threshold|500 ms; the adapter's seek accuracy when larger|§10, §11|
|seek lead|250 ms|§11|
|seek cooldown|2000 ms|§11|
|end margin|1000 ms|§11|
|rate tolerance|0.001|§11|
|command hold|2000 ms|§12|
|silence timeout|15000 ms|§14|
|reconnect backoff|500 ms doubling to 8000 ms, × a random factor 0.5 + 0.5u (§16.8)|§14|
|resume window|300000 ms|§14|
|largest timer delay|2147483647 ms|§2|
|media duration|1 to 604800000 ms (7 days)|§9.7|
|offset|−604800000 to 604800000 ms|§13|

## 3. Relay list

### 3.1 Sources

A `Relays` object holds the relays a party uses. One object serves every party; its list and refresh schedule belong to it.

`Relays.public()` holds the public list (`SPEC.md` §5.2). It starts from the SDK's compiled copy of the repo `relays.json`, and each valid refresh from `https://openwatch.party/relays.json` replaces it, in memory only.

`Relays.at(baseUrl)` is one self-hosted relay for everything: `create` uses it alone (§6), `join` and resume go there whatever the code's first symbol, and it fetches nothing. `baseUrl` is the relay's `https://` or `http://` base (`https://relay.example.com`, `http://192.168.1.5:8080/owp`), trailing `/`s removed; after that trimming the constructor throws `invalid_message` unless it is `https://` or `http://` in lower case followed by at least one character before the first `/`, `?` or `#`. The relay URL is the base with `https://` → `wss://` or `http://` → `ws://`, plus `/v1/ws`; the constructor also throws `invalid_message` unless that URL passes §3.2's `url` rule. Its `list` is one entry: `id` `"custom"`, `name` the trimmed base URL, `url` the relay URL, `prefixes` all 31 symbols in alphabet order. `relayFor(code)` returns that entry for every valid code; `refresh()` does nothing and returns true.

### 3.2 Validation

A public list, fetched or embedded, is used only when valid by `SPEC.md` §5.2; an invalid one is rejected whole and the current list stays. An object with `relays` an array of ≥ 1 objects, each:

- `id`: 1–32 of `a`–`z`/`0`–`9`/`-`, not leading `-`, unique;
- `name`: 1–64 code points, no forbidden character (`SPEC.md` §2), no unpaired surrogate;
- `url`: lower-case `wss://`/`ws://`, non-empty between `://` and next `/`, ends `/v1/ws`, rest printable ASCII U+0021–U+007E except `?`/`#`;
- `prefixes`: non-empty, symbols of `23456789ABCDEFGHJKMNPQRSTUVWXYZ`, none twice in the list.

Every `url` of a list is `wss://`; `ws://` passes only for `Relays.at` with an `http://` base. Unknown members dropped (only the four kept). Vectors: `relay-list.json`.

### 3.3 Refresh

A refresh is one GET of `https://openwatch.party/relays.json`, nothing else; failure or an invalid list changes nothing. Only `Relays.public()` refreshes.

On `joined`, if this object has had no successful refresh yet or its last success is ≥ 24 h old, one starts in the background (failures don't count), unless a background refresh is already running. It never delays `create`/`join`. Routing may force one (§4.2), awaited. `refresh()` forces one, returning validity.

A room stays on its relay for life; new lists affect later `create`/`join` only.

### 3.4 HTTP

Every SDK GET (refreshes only), own or app-supplied (§16.8):

- aborted at 5 s (body included), connection closed; an app fetcher MUST stop on its cancel signal, and the SDK gives up then regardless;
- body ≤ 65536 bytes, longer aborts/fails — also for app fetchers, as UTF-8 bytes of returned text;
- no redirects (3xx fails; app fetchers MUST NOT follow);
- only status 200 with a UTF-8 JSON body succeeds.

## 4. Room codes and routing

### 4.1 Normalizing a code

`join` normalizes first, to route by the first symbol; `relayFor`/`formatCode` share the normalizer, which applies the relay's rule (`SPEC.md` §5.1): remove separators (exactly U+0020, U+0009, U+000A, U+000D, U+00A0, U+2007, U+202F, U+002D, U+2010–U+2014, U+2212), map ASCII `a`–`z` → `A`–`Z` only. The result MUST be 6 symbols of `23456789ABCDEFGHJKMNPQRSTUVWXYZ`, else `invalid_code` before any network use; `join` sends the normalized code.

`formatCode(code)`: normalized, grouped `K7M-Q2X`; throws `invalid_code`. Vectors: `code-routing.json`.

### 4.2 Routing

The relay whose `prefixes` hold the first symbol (unique, §3.2). None: force a refresh (§3.3), route again (on the old list if it failed); still none: `room_not_found`.

A `join` answered `room_not_found`, no refresh yet forced in this call: force one; if the symbol now maps to another `url` (by URL, not id), join there once, answer final; else fail with the first `room_not_found`. At most one forced refresh per `join`. With `Relays.at` the one relay owns every symbol and nothing refreshes, so its `room_not_found` is final.

## 5. Clock sync

One clock per connection, from WebSocket open, never inherited; while reconnecting the dropped clock stays in use (§14). It implements `SPEC.md` §16.2.

Ping `id`s 1, 2, 3, … at open, +500, +1000 (burst), then +5000, +10000, every 5000, computed from open; send times recorded locally.

Unknown/answered `pong` `id`s are ignored; first pong clears the ping. `rtt = now − sentAt`; ignore if < 0 or > 1000 (1000 kept). Else `offset = serverTime − (sentAt + rtt / 2)`.

Step: non-empty window and `|offset − best.offset| > (rtt + best.rtt) / 2 + 100` clears it first (equal is kept; ignored pongs never step). Append, keep 8 newest (oldest evicted even if best). Best = lowest rtt, oldest on ties. `joined.serverTime` unused. Vectors: `clock-sync.json` (`accepted`/`stepped`/`ignored` per pong, with best after it).

## 6. Choosing a relay for `create`

One relay (always so for `Relays.at`): connect, send `create` at once, no pong wait; a failed connection is `unreachable`.

Several: at t0 the SDK connects to every relay at once, each connection with its own clock and burst from its open (§5). The first relay whose connection yields an accepted clock sample (a `pong` with 0 ≤ rtt ≤ 1000, §5) wins: the SDK sends `create` on that connection, closes every other connection and attempt with 1000, and the winner's clock continues as the room's. Pongs are handled in arrival order; the first accepted one wins. No winner within the connect timeout (§2) from t0, or every connection failed first (refused, HTTP 429/503 included, closed or errored): `create` fails with `unreachable`.

On `relay_full`: close that connection (1000) and try the first untried relay in list order: connect and send `create` at once, no pong wait. When every relay has answered `relay_full`, `create` fails with the last `relay_full` error (its `message` and `re`). Any other refusal ends `create` with that code; a lost connection or no answer within the connect timeout is `unreachable`, with no more tries.

## 7. Session lifecycle

### 7.1 Connections

Open within the connect timeout (§2) or the attempt failed. `joined` or an `error` with matching `re` must answer within the connect timeout of sending (other `error`s ignored while waiting; early close = failed); for `resume` that covers `joined` plus a first clock sample (§14). A failed `create` or `join` = `unreachable`; a failed reconnect attempt retries (§14).

Unknown message `type`s/members are ignored. The constructor validates `name` (1–32 code points, no forbidden char/unpaired surrogate) and `client` (1–64, same, and contains a `/` whose first occurrence is neither its first nor its last character), throwing `invalid_message`. Vectors: `names.json`.

All messages are valid UTF-8 (`SPEC.md` §3.1); unpaired surrogates in descriptor strings are refused pre-send (`invalid_message`, §9.7). Sent objects never repeat a member name (`SPEC.md` §4). Commands are checked pre-send (§12).

### 7.2 Create and join

`create()`: choose a relay (§6), send `{type: create, name, client}`, await `joined`. `join(code)`: normalize first (`invalid_code` before `already_in_room`, before any change), route (§4), connect, send `{type: join, code, name, client}`, await `joined` per §4.2.

Failures carry the relay's code (open set: `room_not_found`, `room_full`, `room_locked`, `rate_limited`, `client_blocked`, `relay_full` for create) or SDK `invalid_code`/`unreachable`/`invalid_message` (malformed `joined`, §8). `already_in_room` while attempting or `connected`/`reconnecting` (not `idle`/`ended`). An attempt clears `endReason` and away (§10.2), resets state, publishes `connecting`; failure closes connections (1000), returns to `idle`, no `Ended`. Neither an attempt nor its failure or cancellation releases the adapter: one attached while `idle` or `ended` stays attached, with its `ref`, into the session the attempt starts, since only §11.2's releases let an adapter go.

`leave()`/`close()` mid-attempt cancels: back to `idle`, no `Ended`, pending call `cancelled`. Connections close (1000) except one that already sent `create`/`join`, kept to answer/timeout — `joined` gets `leave` then close, `error` close (`SPEC.md` §11.5). Kotlin: cancelling the caller's coroutine cancels likewise. Swift: cancelling the calling task cancels likewise, and the call throws `CancellationError`.

### 7.3 On `joined`

In order: keep what a resume needs (relay URL, code, peer ID, token; §14); accept the snapshot at any `rev` as the `room` baseline (§8); handle media/epoch (§8–§9; a reconnect only if changed, §14); send `status` (§10). A reconnect also diffs participants vs pre-drop (§15). Messages arriving before the switch to this connection are then handled in order.

### 7.4 Ending

|reason|cause|
|---|---|
|`left`|the app called `leave()`: when connected, the SDK sends `leave` and closes the connection with status 1000|
|`ended`|`closed` with reason `ended`, or with a reason the SDK doesn't know|
|`kicked`|`closed` with reason `kicked`|
|`expired`|`closed` with reason `expired`|
|`replaced`|the relay closed the connection with status 4000: a resume elsewhere took over. Never resumed|
|`lost`|the party can't go on: resume became impossible (§14) or a `room` starting a new epoch had no valid `media` (§8); the error is attached|

Reconnecting `leave()` ends `left` unsent; idle/ended it no-ops. `end()` (§12) ends via the relay's `closed`. After `closed`, close 1000 (relay leaves it open). `lost` errors: `unreachable` (window), `resume_failed`/`client_blocked` (relay), `invalid_message` (new epoch without valid `media`, after `leave`).

Ending: release adapter (§11.2), close connection/attempts, stop timers, cancel matching, reset room fields, publish `ended` + `endReason`, then emit `Ended`. An ended party may start over.

`close()` = `leave()` then release all; idempotent (TS clears listeners; Dart closes streams after queued `Ended`; Kotlin cancels its scope after answering attempts/rate restore; Swift finishes the `states()` and `events()` streams after queued `Ended`, and later calls return finished streams). It returns the release completion (§11.2). Afterwards `create`/`join` throw the state error, not a party error: Kotlin `IllegalStateException`, Dart `StateError`, TypeScript `Error`, Swift `PartyClosedError`.

## 8. Room state

All relay input is checked before changing state; failures change nothing. Non-objects, non-string `type`, unknown `type`s: ignored.

A snapshot (`room`, `joined.room`) is malformed if it fails `snapshot.schema.json`: not an object; any of `rev code epoch phase positionMs atMs rate control locked waitingOn participants` absent (nullables must be present); or a rule broken. "Int" = a JSON number integral as a double within ±(2^53 − 1) (`42.0` yes; strings/bools/null/`1e400` no). Rules (`SPEC.md` §8.2, §9.1, §13.1):

- `rev` int ≥ 1; `code` `^[23456789ABCDEFGHJKMNPQRSTUVWXYZ]{6}$`; `epoch`/`atMs` int ≥ 0; `phase` `idle`/`paused`/`waiting`/`playing`; `positionMs` int 0..604800000; `rate` number 0.5–2.0; `control` `host`/`everyone`; `locked` bool; `idle` needs `positionMs` 0.
- `waitingOn`: distinct `^[A-Za-z0-9_-]{1,16}$` strings, non-empty only in `waiting`.
- `action` (`room` only): absent, or an object with `kind` a non-empty string (open) and `by` present, null or a peer ID.
- `participants`: non-empty, exactly one `host`. Each: `peerId` per pattern; `name` 1–32 code points, no forbidden char/unpaired surrogate; `client` 1–64, same, both sides of first `/` non-empty; `host`/`connected` bools; `player` `idle`/`loading`/`ready`/`buffering`; `match` present: null or {`state` `searching`/`matched`/`mismatched`/`unavailable`, `deltaMs` absent/int}; `offsetMs` int ±604800000.

Unknown members ignored. Unchecked: peer ID uniqueness, whether `waitingOn` and `action.by` name participants, `positionMs` vs duration, `code` vs own code, and a `room`'s `media` (checked only when its epoch is new, below).

Malformed or non-increasing-`rev` (per connection) `room`s drop; accepted ones replace the snapshot whole.

An accepted `room` whose `epoch` differs from the party's epoch must carry `media`, null or valid by §9.7; otherwise the SDK sends `leave`, closes 1000 and ends `lost` with `invalid_message`. With such a `media`, in order, the SDK replaces the snapshot, sets media and epoch, and starts the new epoch (§9.1); only then does it emit the snapshot's events (§15) and step (§11.1), as for `joined` (§7.3). A `media` member on a `room` of the party's own epoch is ignored, unchecked.

Malformed `joined` (bad `code`; `you` not an object; bad `you.peerId`; `you.token` not `^[A-Za-z0-9_-]{43}$`; `serverTime` not int ≥ 0; bad `room`/`media`): for create/join — `leave`, close 1000, call fails `invalid_message`, no fallback; for a reconnect's `resume` — no `leave`, close 1000, the attempt failed (§14).

Other schema failures are ignored: `pong` (`serverTime` int ≥ 0), `error` (`code`/`message` strings, `code` non-empty, `re` present null/string), `closed` (`reason` non-empty). A bad `pong` is no sample (§5–§6).

The party's epoch is the `epoch` of the last `joined` or accepted `room` processed; epochs are compared only for equality, never for order.

Own entry = first `participants` match on peer ID (maybe absent). Host: entry with `host` true. Controller: host, or entry present and `control` = `everyone`.

Position `P(t)` (`SPEC.md` §12.2, current `durationMs`): playing → `min(durationMs, floor(positionMs + max(0, t − atMs) × rate))` (product its own double, floor of sum); else `positionMs` uncapped. Running: `playing` and `t ≥ atMs`. `roomPositionMs()` = `P(now)`, no offset; null without media, snapshot or clock sample. Vectors: `timeline.json`, `room-check.json`.

## 9. Matching

### 9.1 When matching runs

Matching runs on every new epoch: an accepted `room` of another epoch (§8), a `joined` answering `create` or `join`, and a reconnect's `joined` only when its epoch differs (§14). Each new epoch first cancels any matching (resolver call and probes in flight, results ignored), sets the offset 0 (§13), `choice`/`mismatch` null, `candidates` empty. Then:

- Media null: `match` null; adapter unbound and, when attached, paused once (§10.2). Unbinding doesn't discard queued commands; the pause queues behind them.
- Own media — the descriptor equals, as parsed JSON (unknown members outside `ext` dropped, order ignored), the one this party last sent in `setMedia`: `match` = `{"state": "matched", "deltaMs": 0}`; `choice` = candidate `id` `""` with that `setMedia`'s `ref` and the descriptor's `durationMs`; the attached adapter binds at once when its ref equals that `ref` (§10.2); no pause, no resolver call. "Last sent" is recorded at the call, kept if the relay refuses it, survives `clearMedia` and others' media, resets only at the party's end; equality is by value, so another host's identical descriptor is own media again. The choice persists through the epoch.
- Other media: adapter unbound and paused once when attached, `match` = `{"state": "searching"}`, then resolver and walk (§9.4, §9.5).

`resolveAgain()` searches again for the current epoch; it does nothing without media or when the current epoch is the party's own media (decided when the epoch starts). It cancels matching in flight (resolver call and probes) and never touches the offset. With `choice` null it reruns the other-media steps as on a new epoch, minus the offset reset and minus any adapter action (nothing is bound): `match` `searching`, `mismatch` null, `candidates` empty until the resolver answers, then the walk. With `choice` set it runs only the resolver and ranking (§9.4) and replaces `candidates` with the result, `deltaMs` from a counting `durationMs` only, else null; no probes, no walk. `choice`, `match`, `mismatch`, binding and the adapter stay unchanged, and the app offers the new list for `choose`. `ResolveFailed` and the resolve timeout apply to both; a failed call leaves `candidates` empty.

### 9.2 Resolver and candidates

`resolve(media)` returns the app's candidates in its order of preference, once per search, cancelled per §9.1. A throw or a call longer than the resolve timeout (§2) means no candidates (on timeout the SDK also cancels), and the SDK emits `ResolveFailed` with the thrown error or the language's timeout error (§16.8); a cancelled call emits none. In TypeScript a non-array answer is no candidates, without `ResolveFailed`. A party without a resolver (§16.2: none passed) never searches: for any other participant's media its `candidates` is empty and its `match` is `unavailable` from the start of the epoch, so its first `status` there already says `unavailable` (§9.6, §10.3).

A candidate (§16.5) has a unique `id`; optional `durationMs`; optional hints `sizeBytes`, `oshash`, `name`; optional async `probe` (opens the file, returns its duration or nothing, closes it); and `ref`, handed back with `choice`. `probe` runs only without a counting `durationMs`; the SDK cancels unneeded probes, which then release what they opened; a throwing probe returned nothing, also when what it throws is a cancellation of its own, such as the probe's own timeout, while the SDK hasn't cancelled it.

A duration counts only as an integer 1..604800000; any other value is none (a bad `durationMs` probes if it can; a bad probe answer is a returned nothing). No duration and no `probe` is never chosen.

Identity is the resolver's job for every kind: it returns the files it takes for this title, and `sameTitle` (§9.3) helps it decide. The SDK never applies a same-title or same-file rule while matching; ranking orders the candidates (§9.4) and the walk's duration tolerance decides (§9.5). An own-name `media.ext` member MAY shortcut the search: a member naming a known server and item is looked up there (§9.7).

### 9.3 Same title

`sameTitle(a, b)` implements this rule for resolvers comparing descriptors; it is symmetric, and `SPEC.md` §16.5 points here. Same title only when `kind`s are equal:

- `movie`: some namespace of `imdb`, `tmdb`, `tvdb`, `plex`, `anilist`, `anidb`, `kitsu`, `mal` has the same non-empty value in both `ids` (same namespace only, exact compare, others don't count).
- `episode`: as `movie` on own `ids` (a shared episode ID beats differing numbers); otherwise, unless some namespace of that list has different non-empty values in the two own `ids` (differing episode IDs beat equal numbers), both have `series` sharing an ID as `movie` and `season`/`episode` present in both and equal.
- `video`: both have `file` and `oshash`es equal ignoring ASCII case, or `sizeBytes` present and equal in both and `name`s equal ignoring ASCII case. IDs don't count.

Vectors: `same-title.json`.

### 9.4 Ranking

Sort the candidates stably by: `oshash` equal ignoring ASCII case; `sizeBytes` equal; `name` equal ignoring ASCII case; counting `durationMs` before not; then smaller `|durationMs − media.durationMs|`. Full ties keep resolver order. The keys apply to every kind and drop no candidate; without `file` all tie on the first three.

### 9.5 The walk

In ranked order: a counting `durationMs` is the duration; else probe unless 4 probes have counted or one has missed its deadline. Probes run one at a time, sharing 60000 ms; a probe's deadline is the budget left at its start (inclusive), measured in local time and spent from the budget; it is cancelled at budget-left + 1 ms. A missed deadline: cancelled, not counted, no delta, no further probes. A failed, empty or non-counting probe skips the candidate and counts toward the 4. Counting `durationMs` candidates are still checked after probes run out.

`delta = duration − media.durationMs`; `|delta| ≤ 1500` matches and stops the walk — first in ranked order wins, even over a closer later one. Else keep the closest so far, earlier on ties.

Published `candidates`: empty until the resolver answers, then all ranked as `Ranked {candidate, deltaMs}` (counting durations from the walk's start, probed ones on a timely counting answer, else null). The list survives the outcome and `acceptMismatch`/`declineMismatch`/`choose`; only a new epoch or `resolveAgain()` replaces it (§9.1). Vectors: `matching.json`.

### 9.6 Outcomes

Matched: `choice` set, `match` = `{"state": "matched", "deltaMs": delta}`, `mismatch` null. None matched, some had a duration: `mismatch` = `{candidate: closest, deltaMs: delta}`, `match` = `{"state": "searching", "deltaMs": delta}`, `choice` null. None had a duration: `match` = `{"state": "unavailable"}`, both null.

`acceptMismatch()` sets `choice` to the offer, `match` = `{"state": "mismatched", "deltaMs": delta}`, clears `mismatch`, leaves offset. `declineMismatch()` sets `match` = `{"state": "unavailable", "deltaMs": delta}`, clears both. Both no-op without a pending `mismatch`. Binding follows §10.2: an outcome binds an attached adapter whose ref equals the new `choice.ref`.

`choose(candidate)` takes a file from `candidates` or anywhere else the app finds one (any matching state, media present incl. own; without media, false). Its duration comes from a counting `durationMs` (§9.2) or one probe outside the walk budget/count, with no deadline. False, changing nothing, when: neither; probe fails/empty/non-counting; superseded or matching cancelled. Latest `choose` wins, cancelling earlier probes (answers ignored); cancelling matching does the same. With a duration and still current: cancel walk and prompt, set `choice` to the given candidate, also when it has the current choice's `id` (a refreshed `ref` replaces the old one), `match` = `matched` (|delta| ≤ 1500) or `mismatched`, with `deltaMs`, offset unchanged, return true. Binding follows §10.2: an attached adapter whose ref differs from the new `choice.ref` unbinds, without a pause.

### 9.7 Descriptors are untrusted

A descriptor comes from another participant via a relay; every string and all of `ext` is untrusted.

Senders MUST NOT put anything that locates or fetches media in any field, `ext` included (`SPEC.md` §10); nobody checks this. Resolvers MUST treat descriptors as search terms only — never a path, URL, host, shell/SQL fragment or format string. `file.name` is only compared with names the app found itself, never opened. Any `ext` member can be forged: use it only to look up the app's own library/catalogue, never to choose where to connect or what to fetch. Apps show titles and names as plain text only.

The SDK checks descriptors it sends (`checkMedia`/`setMedia`) and receives (`room` and `joined`):

- a JSON object; `kind` exactly `movie`/`episode`/`video`; `series` present for `episode` (optional, checked, otherwise);
- `title` (required) and `series.title` (required within `series`): ≥ 1 code point, no forbidden character, no unpaired surrogate;
- `year`/`series.year` ints within ±(2^53 − 1); `season`/`episode`/`file.sizeBytes` ints ≥ 0; `durationMs` (required) int 1..604800000;
- `ids`/`series.ids`: objects with non-empty string values, any member names;
- `file.oshash` matches `^[0-9a-f]{16}$`; `file.name`: ≥ 1 code point, no forbidden character or unpaired surrogate, no `/`/`\`, not `.`/`..`;
- `ext`: object, member names matching `^[a-z0-9-]+(\.[a-z0-9-]+)+$`, values any JSON, nested names free apart from surrogates;
- `series`/`file` when present are objects; wrong type (null included for non-nullables) fails;
- no unpaired surrogate in `ids`/`series.ids` names and values, or any `ext` string incl. member names;
- unknown members outside `ext` ignored (unchecked, unkept, unsent).

Send only: descriptor ≤ 4096 bytes, `ext` ≤ 1024, on the SDK's compact JSON of its `Media` (known members only, absent omitted, ints integral, no whitespace, non-ASCII as UTF-8; `ext` measured alone) — the exact text `setMedia` sends, so a passing descriptor stays within the relay's as-received limit. Empty `ids`/`series.ids` may be omitted when sending; receivers treat absent and empty alike. Receipt skips sizes (the relay measured as received). Send failure: `checkMedia` returns the first rule broken, `setMedia` throws `invalid_message`, sends nothing. Receive failure makes a `joined` malformed and ends the party on a new epoch's `room` (§8). Vectors: `media-check.json`.

## 10. Status

### 10.1 Fields

|field|value|
|---|---|
|`epoch`|the epoch (§8)|
|`player`|§10.2|
|`match`|§9; null while the room has no media|
|`offsetMs`|the offset (§13)|
|`rttMs`|the clock's best round trip; absent before the first sample|

`rttMs` is read at send time and never compared: a change of `rttMs` alone never sends a `status`.

### 10.2 Player state

Four facts decide `player`: attached (an adapter is attached), away (set by `detachPlayer`, unless it names an adapter that isn't attached; cleared by `attachPlayer`; false from the start of every `create` or `join` attempt (§7.2) and after the end; kept across a reconnect), bound and blocked (below).

`attachPlayer(player, ref)` attaches `player` as the player of `ref`: the `ref` of the file the player has open, normally `state.choice.ref` exactly as published or, as host, the `ref` passed to `setMedia`. Another attached adapter is released first (§11.2); the adapter already attached keeps its lane and queued commands and takes the new `ref`. It clears away, re-evaluates binding and steps (§11.1). It works while `idle` or `ended` too, and an adapter attached then stays attached into the next session (§7.2). The app attaches once per file it opens, and again after a release while the player still has that file open: the party's end releases the adapter (§7.4), so an app that keeps its player open after `Ended` attaches it again, before or after the next `create` or `join`. `detachPlayer(player?)` without an argument releases the attached adapter (§11.2), unbinds and sets away; it works with no adapter attached and doesn't pause the adapter. With an argument it does so only when that adapter is the one attached, else it changes nothing. Both return the release completion (§11.2).

Bound is a condition, re-evaluated whenever one of its inputs changes: an adapter is attached; `choice` is non-null and its `ref` equals the attached ref (Kotlin and Dart `==`, Swift `==` of `Ref: Equatable`, TypeScript `===`); `match.state` is `matched` or `mismatched` for the current epoch; and no `setMedia` of this party is pending. Every new epoch unbinds first, then re-evaluates (§9.1). When the condition turns true (binding), nudging is off, the applied rate becomes the room rate, and when the adapter can set rates one `setRate` of that rate is queued; afterwards the SDK tracks the rate it last set. When it turns false (unbinding), the play timer stops (§11.2) and queued commands stay. So the SDK never commands a player that holds another file: an open that finished late for a superseded choice attaches without binding, and an attached player already playing `choice.ref` binds as soon as matching publishes that choice.

`setMedia` is pending from the call until an accepted `room` with a new epoch, a `joined` with a new epoch, or an `error` with `re` `setMedia`; the call unbinds without a pause. A reconnect's `joined` with the same epoch sends it again or ends it (§14). When it stops being pending, binding is re-evaluated and the SDK steps. So a host may call `setMedia(media, ref)` and `attachPlayer(player, ref)` in either order.

Blocked means the player refused to start: it turns true when a `play` the SDK issued on the bound adapter throws and the adapter then reads `paused`, as a browser does when its autoplay policy wants a user gesture. It turns false when the adapter reads not `paused`, on unbinding, and when the room's `phase` isn't `playing`. When it turns true the SDK emits `PlayBlocked` (§15). The step still issues `play` (§11.2); the app asks the user to start the player with the player's own control, which a browser counts as a gesture.

`player` is the first that applies:

1. no media → `idle`;
2. `match.state` is `unavailable` → `idle`;
3. away → `idle`;
4. not bound → `loading`;
5. bound and the first frame isn't shown, or a seek the SDK issued is queued or running → `loading`;
6. bound, the room is `playing` and relay time is unknown → `loading`;
7. held (§11.2) and `|position − holdAt| > max(500, seek accuracy)` → `loading`;
8. blocked → `loading`;
9. buffering → `buffering` when the room is running (§8) and not held, else `loading`;
10. otherwise `ready`.

An app that takes part without playing calls `detachPlayer()` after each join. `ready` here means able to play at once and close enough that following needs no seek (`SPEC.md` §7.5, §16.4).

### 10.3 When to send

The SDK sends `status` right after every `joined`, with the last-sent values cleared; when `epoch`, `player`, `match` or `offsetMs` differs from the last `status` sent on this connection; and 15000 ms after the last `status`, the heartbeat restarted by every send. At most one `status` goes out per 250 ms: a change inside the spacing goes out when the 250 ms are up, with the values of that moment, if they still differ. A `status` whose `epoch` differs from the last one sent skips the spacing. No `status` is sent while reconnecting, and its timers stop at the drop. The SDK reads the adapter on every tick (§11.1), on `playerChanged()` and once the attached adapter's lane is empty.

The first `status` of a new epoch decides this participant's part in the room's wait: `idle` takes it out, `loading` keeps the room waiting, and `ready` stops it holding the room. A user who won't open the new media is taken out of the wait by `detachPlayer()` or `declineMismatch()` as soon as the app knows.

## 11. Following the room

### 11.1 When the SDK steps

A step runs only while an adapter is bound, media is present and relay time is known. It runs every 500 ms — the tick starts at the first `joined` and runs until the party ends, also while reconnecting — and at once after an accepted snapshot, `attachPlayer`, the attached adapter becoming bound (§10.2: a match, `choose`, `acceptMismatch` or the own media's `room` setting the choice to its `ref`), `setOffset`, `playerChanged()`, the end of a command hold, and when the play timer fires.

Each adapter has its own command lane: its commands run one at a time, in order, each awaited, and a failing command is ignored since the next step corrects it. Lanes of different adapters never wait for each other. No step starts while a command on the attached adapter's lane is queued or running; a step requested meanwhile is remembered as one flag and runs once that lane's last queued command finishes; several requests become one step. No step runs during a command hold (§12); its end runs one.

The step's inputs are the room's `phase`, `positionMs`, `atMs` and `rate`; the media's `durationMs`; relay time now; the offset (§13); the adapter's position, `paused`, `buffering`, `durationMs`, seek accuracy and whether it can set rates; the applied rate; whether the SDK is nudging; and the milliseconds since the SDK last issued a seek on this adapter, on the local clock, null when never.

### 11.2 The step

```
if phase is idle: no action; nudging = false; stop.
a      = the adapter's seek accuracy
cooled = the SDK never seeked on this adapter, or issued its last seek ≥ 2000 ms ago
end    = adapter.durationMs − 1000 (at least 0) when the adapter knows a duration > 0, else none
clamp(x) = max(0, x), and at most end when end exists
anchor = positionMs + offsetMs                        # local position at atMs

held = phase is paused or waiting
if phase is playing:
    startAt = atMs + (anchor < 0 ? −anchor / rate : 0) # relay time when local playback starts (double)
    if now < startAt: held = true; playIn = ceil(startAt − now)

if held:
    holdAt = clamp(anchor)
    pause                        if the adapter isn't paused
    setRate(rate)                if it can set rates and |applied − rate| > 0.001
    seek(holdAt)                 if |position − holdAt| > max(500, a) and cooled
    nudging = false; start the play timer for playIn when set; stop.

target   = max(0, P(now) + offsetMs)
nudging  = nudging and the adapter can set rates
if end exists and target ≥ end and position ≥ end:    # both at the local file's end
    setRate(rate) if it can set rates and |applied − rate| > 0.001; nudging = false; stop.
if the adapter is buffering: no action; stop.

drift = position − target
seek  = false
with rate control:
    |drift| > max(2000, a):  nudging = false; seek = cooled
    else if nudging:         nudging = (|drift| ≥ 150)
    else if |drift| > 350:   nudging = true
without rate control:
    |drift| > max(1000, a):  seek = cooled
want = nudging ? rate × (drift > 0 ? 0.96 : 1.04) : rate
setRate(want)                if it can set rates and |applied − want| > 0.001
seek(clamp(target + 250))    if seek
play                         if the adapter is paused
```

Here `P(now)` is the room position of §8 and `rate` the room's. Each product and quotient is its own double (§2). A `setRate` action's rate is rounded to 6 decimals, and the applied rate becomes that rounded value. Actions run in the order pause, setRate, seek, play, each queued as §11.1 says; issuing a seek records its time (for `cooled`), and it counts as in flight from then until it finishes (§10.2). Step seeks never go past the end margin.

`a` is the adapter's `seekAccuracyMs` (§16.4), read at every step: how far from the requested position a seek may land, such as a player's keyframe distance. A value that isn't an integer ≥ 0 counts as 0. It raises every seek threshold, here and in §10.2; the nudge thresholds (350, 150) and the seek lead don't change. A player that can't seek at all declares a very large value and is followed by rate only.

A step that holds with `playIn` starts a one-shot play timer for `playIn` ms that runs a step; each step replaces or cancels it, unbinding cancels it, and delays are clamped as §2 says.

Releasing an adapter — `detachPlayer`, `attachPlayer` with another adapter, or the party's end — drops its commands that haven't started; a running one finishes on its lane with its outcome ignored; if the SDK was nudging, one `setRate` of the room rate, captured at that moment, is queued on the released lane and runs, also after `close()`; nothing else reaches that adapter. `detachPlayer` and `close()` return a completion (§16.8) that settles, never failing, once no released adapter has a command running or queued, at once when none has. An adapter command must finish or throw; until it does, the completion waits. An app awaits it before disposing a player. Vectors: `reconcile.json`.

## 12. Local commands

The user's controls go through the party; a change made behind the party's back is undone by the next step. Positions the app passes and reads from its adapter are in its own file's timeline; the SDK subtracts the offset (§13) before sending. Commands don't wait for the relay: a refusal arrives as an `Error` event (§15), the SDK doesn't undo local effects, and the next step does. `setOffset` (§13) is a local setting, not a command.

|method|checks before sending|local effect|sends|
|---|---|---|---|
|`play()`|member, media, controller|none: the room starts everyone (§11)|`play`|
|`pause()`|member, media, controller|the adapter pauses|`pause`, `positionMs` = max(0, adapter position − offset) when bound, else absent|
|`seek(ms)`|member, media, controller, `ms` an int ≥ 0, `ms − offset` and the sent position ints (§2)|the adapter seeks to `ms`|`seek`, `positionMs` = max(0, `ms` − offset)|
|`setRate(rate)`|member, media, controller, 0.5 ≤ rate ≤ 2.0|the adapter takes `rate` when it can|`rate`|
|`setMedia(media, ref, positionMs = 0)`|member, host, descriptor, `positionMs ≥ 0`|marks the descriptor as the party's own, with `ref` as its file (§9.1); unbinds the adapter until its epoch arrives (§10.2)|`setMedia`|
|`clearMedia()`|member, host|none|`clearMedia`|
|`setControl(mode)`|member, host|none|`setControl`|
|`transferHost(peerId)`|member, host, a participant with that peer ID|none|`transferHost`|
|`kick(peerId)`|member, host, a participant with that peer ID|none|`kick`|
|`lock(locked)`|member, host|none|`lock`|
|`end()`|member, host|none|`end`|

The check order is member, then media, then controller, then values; and member, then host, then the descriptor, value or participant. Local effects touch the adapter only while bound. A `seek` command's local seek is unclamped and counts as an SDK seek for the cooldown and `loading`; `pause` also checks its computed `positionMs` (§2); `setRate` rejects NaN; `setMedia` sends `positionMs` as given, since the new media's timeline is the host's own file. A participant check accepts connected and disconnected entries and doesn't refuse the party itself. A failed check throws without sending and before any local effect: `not_in_room` when not connected, reconnecting included; `forbidden` when not host or not a controller (§8); and `invalid_message` for no media, a value out of range or not an int, a computed wire value that isn't a valid int, a peer ID not in the room, or a descriptor failing §9.7.

After `pause`, `seek` or `setRate`, bound or not, no step runs until the next accepted `room` carrying `action` (§8), an `error` with `re` `pause`, `seek` or `rate` respectively, or 2000 ms pass, so snapshots already in flight don't pull the player back. A new command replaces the hold, and its end runs a step. There is never a hold after `play`, `setMedia` or the host commands.

## 13. Offset

Local position = room position + offset (`SPEC.md` §16.4). A positive offset means the user's file is ahead, with extra footage before the same scene, such as a longer intro.

`setOffset(ms)` takes an integer from −604800000 to 604800000 inclusive; anything else throws `invalid_message` and changes nothing. It needs neither a room nor media and works while idle or reconnecting, but a value set outside a room is reset by the next `joined`. The offset becomes 0 on every new epoch (§9.1); a resumed `joined` with the same epoch and `resolveAgain()` keep it.

Apps that allow mismatched files SHOULD offer the offset control, with steps of 100 ms and 1 s, a reset, and the mismatch's `deltaMs` as a hint.

## 14. Reconnect and resume

The member connection closing or failing is a drop: every close status other than 4000 counts (1001, 1007 and 1009 among them, `SPEC.md` Appendix B), and so does a close without a status. After `closed` and after `leave()` the SDK closed the link itself, so no drop is seen, and status 4000 ends the party as `replaced` (§7.4). When no message has arrived for 15 s — every received message, `pong` included, re-arms the timer — the SDK closes the connection with status 1000 and treats it as a drop with no status.

On the drop, `connection` becomes `reconnecting`, the status timers stop, the window timer starts and attempt 1 is scheduled. Attempt n waits floor(min(8000, 500 × 2^(n−1)) × (0.5 + 0.5u)) ms, with u one draw from the injectable source (§16.8): attempt 1 waits from the drop and attempt n from the failure of attempt n−1. Each attempt connects to the URL the party joined on, starts a new clock and sends `{type: resume, code, peerId, token}`. A failed connection (HTTP 429/503 included), an open timeout, no answer in time (§7.1), the connection closing before the switch, a malformed `joined` (§8), or any `error` other than `resume_failed` or `client_blocked` is a failed attempt: the SDK closes that connection with 1000 and schedules the next attempt.

The window is 300000 ms (§2), measured from the drop; when it passes the party ends with `lost` and error `unreachable`. On `joined` the party is connected again and §7.3 applies, the media handled as a new epoch only when its epoch changed. On `resume_failed` or `client_blocked` the party ends with `lost` carrying that error and never joins again on its own.

A `setMedia` still pending (§10.2) when a reconnect's `joined` has the epoch the party had before the drop never reached the room. If that snapshot lists this party as host, the SDK sends the same `setMedia` again right after the `status` of §7.3 and it stays pending; otherwise it stops being pending.

While reconnecting the SDK keeps stepping against the last snapshot and the old clock; it switches to the new connection's snapshot and clock together once that connection has both `joined` and a clock sample. Commands throw `not_in_room` (§12). When the party ends while a resume attempt is under way, a connection that already sent `resume` is kept until its answer or the connect timeout, and a `joined` is answered with `leave`, as §7.2 treats a cancelled `create` or `join`.

## 15. Events

SDK 1.x has seven events: `Action`, `ParticipantJoined`, `ParticipantLeft`, `Error`, `ResolveFailed`, `PlayBlocked` and `Ended` (§16.8 for their names per language). A later 1.x may add events: apps handle `PartyEvent` non-exhaustively, with an `else`/`default` branch that ignores unknown events, and TypeScript listeners ignore unknown `type`s. The wire enums `Phase`, `Control`, `PlayerState`, `MatchState` and `MediaKind` are fixed for protocol v1, and `Connection` and `EndReason` for SDK 1.x. Every subscriber gets every event emitted while it is subscribed, in order, never dropped or merged: TypeScript calls listeners synchronously from a copy of the set and rethrows a throwing listener asynchronously while the others still run; Dart delivers asynchronously and a paused subscription buffers; in Swift each `events()` call is its own `AsyncStream`, which buffers without limit, so a slow reader still gets every event in order.

`Action` fires after an accepted `room` carrying `action` (§8); `joined` never produces one. It carries `kind` (a string, an open enum), `byPeerId` (null for the relay), `by` (that peer's entry in this snapshot, null when absent), `byMe` (`byPeerId` is this party's peer ID), and the accepted snapshot as `room`.

`ParticipantJoined` and `ParticipantLeft` report a peer ID appearing in or disappearing from `participants` between two accepted snapshots, including a reconnect's `joined` against the pre-drop snapshot but not a `joined` answering `create` or `join`; `ParticipantLeft` carries the last known entry. For one snapshot the order is all `ParticipantJoined` in new-snapshot order, then all `ParticipantLeft` in old-snapshot order, then `Action`.

`Error` carries every relay `error` on the member connection: a refused command (§12), `status` or `ping`, or one with `re` null. Answers to `create`, `join` and `resume` don't produce it; those calls or attempts fail instead. `ResolveFailed` reports a resolver that threw or timed out (§9.2). `PlayBlocked` reports that the player refused to start (§10.2) and carries the error the adapter's `play` threw; the app asks the user to press play on the player itself. `Ended` carries its reason (§7.4) and an error that is non-null only for `lost`.

The rest is state, not events: being host is `state.isHost`, reconnecting is `state.connection`, and a kick is `Ended` with `kicked`.

Every SDK error carries `code`, `message` and `re`. For a relay refusal, `message` is the relay's `error.message` and `re` its `re`; the `relay_full` that ends a `create` (§6) is the last relay's error, `message` and `re` included. An error the SDK raises itself, before or instead of sending — `room_not_found` after routing, `already_in_room`, `not_in_room`, `forbidden`, `invalid_message`, `invalid_code`, `unreachable` and `cancelled` — carries SDK English text and `re` null. Apps never show `message`; they map `code` to their own text, with a generic text for unknown codes.

A `seek` toast shows `positionMs + offset`, the user's own timeline (§13).

|`kind`|`by`|what happened|example toast|
|---|---|---|---|
|`play`|the presser|playback starts once everyone is ready|"Anna pressed play"|
|`pause`|the presser|the room paused|"Anna paused"|
|`seek`|the presser|the room moved to `positionMs`|"Anna jumped to 12:34"|
|`rate`|the presser|the room plays at `rate`|"Anna set the speed to 1.25×"|
|`media`|the host|new media (`state.media` holds it) or none (`clearMedia`)|"Anna started Pilot" / "Anna stopped the video"|
|`stall`|the stalled participant|the room waits for them|"Waiting for Anna"|
|`ended`|none|the media reached its end; the room paused there|"Finished"|
|`host`|the old host, or none|the participant with `host` true is the new host|"Ben is now the host"|
|other|any|an action this SDK doesn't describe|nothing|

## 16. API

### 16.1 Concepts and names

Each concept has one name in every SDK; only idioms differ (§16.8).

|concept|name|what it is|
|---|---|---|
|party|`WatchParty`|one participant's session: connection, room state, matching, status, following|
|relay list|`Relays`, `Relay`|a list source (§3) and one entry (`id`, `name`, `url`, `prefixes`)|
|player adapter|`PlayerAdapter`|the app's player, read and commanded by the SDK (§16.4)|
|resolver|`Resolver`|the app's search: `resolve(media)` → candidates|
|candidate|`Candidate`|one file the user could play (§9.2), with the app's `ref` of type `T`|
|ranked candidate|`Ranked`|a candidate of the walk and its `deltaMs` once known (§9.5)|
|descriptor|`Media`, `Series`, `MediaFile`, `MediaKind`|the media descriptor of `SPEC.md` §10|
|party state|`PartyState`|everything the UI shows, as one value (§16.6)|
|room|`Room`|the snapshot of `SPEC.md` §9 as types|
|participant|`Participant`|one entry of `participants`|
|match|`Match`, `MatchState`|`{state, deltaMs}` as on the wire|
|offered file|`Mismatch`|the closest candidate and its delta while the user decides|
|enums|`Phase`, `Control`, `PlayerState`, `Connection`, `EndReason`|wire values, fixed for protocol v1; `Connection` and `EndReason` are the SDK's, fixed for SDK 1.x (§15)|
|event|`PartyEvent`|`Action`, `ParticipantJoined`, `ParticipantLeft`, `Error`, `ResolveFailed`, `PlayBlocked`, `Ended`; a later 1.x may add more (§15)|
|error|`PartyException` (Kotlin, Dart), `PartyError` (TypeScript, Swift)|`code`, `message`, `re`|
|cancellation|the language's own (§16.8)|how the SDK stops a resolver call or a probe|
|transport, clock, randomness|the language's own (§16.8)|where sockets, HTTP GETs, local time and random numbers come from|

Error codes are the relay's (`SPEC.md` Appendix A, an open set) plus `invalid_code`, `unreachable` and `cancelled`; they are strings, so a newer relay's code passes through.

### 16.2 `WatchParty`

Shown in Kotlin; §16.8 maps it to Dart, TypeScript and Swift.

```kotlin
class WatchParty<T>(
    relays: Relays,
    client: String,          // "Example/1.0", checked by SPEC.md §6.3; the constructor throws invalid_message
    name: String,            // the user's display name, checked by SPEC.md §6.3
    resolver: Resolver<T>? = null,      // without one, others' media is unavailable at once (§9.2)
    scope: CoroutineScope,   // the party runs in a child SupervisorJob of it, on its dispatcher
    clock: MonotonicClock = MonotonicClock.System,
    random: Random = Random.Default,
)

val state: StateFlow<PartyState<T>>
val events: SharedFlow<PartyEvent>

suspend fun create(): String              // the room code
suspend fun join(code: String)

fun attachPlayer(player: PlayerAdapter, ref: T)    // ref: the file the player has open (§10.2)
fun detachPlayer(player: PlayerAdapter? = null): Job   // completes once released adapters are done (§11.2)
fun playerChanged()                       // optional nudge: the player's state changed

fun acceptMismatch()
fun declineMismatch()
suspend fun choose(candidate: Candidate<T>): Boolean
fun resolveAgain()

fun play()
fun pause()
fun seek(positionMs: Long)
fun setRate(rate: Double)
fun setOffset(offsetMs: Long)

fun setMedia(media: Media, ref: T, positionMs: Long = 0)
fun clearMedia()
fun setControl(mode: Control)
fun transferHost(peerId: String)
fun kick(peerId: String)
fun lock(locked: Boolean)
fun end()

fun leave()
fun roomPositionMs(): Long?
fun close(): Job                          // completes as detachPlayer's does
```

`create()` returns the room code, 6 symbols unformatted. `choose` is §9.6.

Top-level helpers:

- `formatCode(code)` (§4.1);
- `sameTitle(a, b)` (§9.3);
- `checkMedia(media)`: null when the descriptor passes the send checks of §9.7, else the first rule it breaks. An app trims `file.name` or `ext` and checks again instead of getting `invalid_message`;
- `parseMedia(json)`: the one descriptor decoder. It applies §9.7's receive checks (not the send-only sizes), returns a `Media` (TypeScript: a fresh frozen copy) and throws `invalid_message` on failure.

### 16.3 `Relays`

```kotlin
class Relays {
    companion object {
        fun public(http: HttpClient): Relays
        fun at(http: HttpClient, baseUrl: String): Relays   // throws invalid_message for a bad base URL (§3.1)
    }
    val list: List<Relay>                 // the list in use (§3.1)
    fun relayFor(code: String): Relay?    // normalizes (§4.1); null for an invalid code or a symbol nobody owns
    suspend fun refresh(): Boolean        // a forced refresh (§3.3); false when no valid list came back
}
data class Relay(val id: String, val name: String, val url: String, val prefixes: String)
```

`relayFor` never waits, never refreshes and never throws.

### 16.4 `PlayerAdapter`

```kotlin
interface PlayerAdapter {
    val positionMs: Long              // current position in the app's file
    val durationMs: Long?             // the file's duration; null when unknown
    val paused: Boolean
    val buffering: Boolean            // stalled while trying to play
    val firstFrameShown: Boolean      // the file is open and shows a frame
    val canSetRate: Boolean get() = false
    val seekAccuracyMs: Long get() = 0   // how far from its target a seek may land (§11.2)
    suspend fun play()                   // throws when the player refuses to start (§10.2)
    suspend fun pause()
    suspend fun seek(positionMs: Long)   // returns once the seek has landed
    suspend fun setRate(rate: Double) {}
}
```

Reads are cheap and synchronous; the SDK reads them on every tick (§11). Commands return when the player has done them; a `seek` that returns early makes the SDK measure mid-seek positions. A command must finish or throw: a hung one holds its lane and the release completion (§11.2). `play` throws when the player refuses to start, such as a browser blocking playback without a user gesture; the SDK then reports `loading` and emits `PlayBlocked` (§10.2). `canSetRate` may change while attached. `firstFrameShown` may stay false until the app's own start-up work is done; the SDK reports `loading` until then. `seekAccuracyMs` is how far from the requested position a seek may land: a player that seeks to keyframes declares its keyframe distance, and one that can't seek declares a very large value (§11.2). A 1.x release may add adapter members with defaults; §16.8 says how each language keeps that from breaking apps.

### 16.5 `Resolver` and `Candidate`

```kotlin
fun interface Resolver<T> {
    suspend fun resolve(media: Media): List<Candidate<T>>   // cancelled as §9.1 says
}

class Candidate<T>(
    val id: String,
    val ref: T,
    val durationMs: Long? = null,
    val sizeBytes: Long? = null,
    val oshash: String? = null,
    val name: String? = null,
    val probe: (suspend () -> Long?)? = null,
)

data class Ranked<T>(val candidate: Candidate<T>, val deltaMs: Long?)
```

The field rules are §9.2's.

### 16.6 `PartyState` and the room types

```kotlin
data class PartyState<T>(
    val connection: Connection,         // idle, connecting, connected, reconnecting, ended
    val peerId: String?,                // own peer ID while in a room
    val room: Room?,                    // the last accepted snapshot
    val media: Media?,                  // the current descriptor
    val match: Match?,                  // as reported in status
    val choice: Candidate<T>?,          // the file to open for the current media
    val mismatch: Mismatch<T>?,         // the file offered while the user decides
    val candidates: List<Ranked<T>>,    // the walk's ranked candidates (§9.5)
    val offsetMs: Long,
    val player: PlayerState,            // as reported in status
    val endReason: EndReason?,
) {
    val me: Participant?                // own entry of room.participants
    val isHost: Boolean
    val canControl: Boolean             // host, or own entry present and control is everyone
}

data class Room(
    val rev: Long, val code: String, val epoch: Long, val phase: Phase, val positionMs: Long, val atMs: Long,
    val rate: Double, val control: Control, val locked: Boolean, val waitingOn: List<String>,
    val participants: List<Participant>,
)
data class Participant(
    val peerId: String, val name: String, val client: String, val host: Boolean, val connected: Boolean,
    val player: PlayerState, val match: Match?, val offsetMs: Long,
)
data class Match(val state: MatchState, val deltaMs: Long?)
data class Mismatch<T>(val candidate: Candidate<T>, val deltaMs: Long)
```

State changes when a snapshot, the media, matching, the player state, the offset or the connection changes, and equal states aren't republished. Positions are read on demand through `roomPositionMs()` or the adapter.

Published state is read-only. In TypeScript every published type (`PartyState`, `Room`, `Participant`, `Match`, `Mismatch`, `Ranked`, `Media`, `Series`, `MediaFile`, `Relay`, `Relays.list`) is deeply `readonly`, and the SDK deep-freezes every object and array it creates and publishes, once at creation: the snapshot, participants, media it parsed, match, mismatch, the ranked list and relay lists. It never freezes app objects (candidates, refs, adapters). In Kotlin and Dart the SDK keeps its own deep copy of every descriptor, list and map the app passes in, so changing the app's object afterwards changes nothing in the party, and the collections it publishes are unmodifiable. In Swift `state` is `private(set)`, and `Media` and every published type but `Candidate` (a class of constants) are value types, so neither side's later change reaches the other.

`Media` mirrors `SPEC.md` §10 field for field: `kind`, `title`, `year`, `ids`, `series`, `season`, `episode`, `durationMs`, `file`, `ext`. In Kotlin and Dart `Media` is an immutable value with `toJson()`; in TypeScript it is a plain object; in Swift it is a `struct` with `toJSON()`, which returns a `JSONValue`. `parseMedia` (§16.2) decodes; decoding and encoding keep unknown `ids` namespaces and every `ext` member unchanged, and drop unknown members outside `ext`, including inside `series` and `file`.

### 16.7 What apps must do

Required:

- construct the party with a `Relays`, a true `client` name, the user's name and, to match other participants' media, a resolver (§16.2);
- call `create()` or `join(code)`, show the code, and handle `Ended` (§15);
- handle `PartyEvent` non-exhaustively, ignoring events it doesn't know (§15);
- write a `PlayerAdapter` with the reads and `play`, `pause` and `seek`; in Dart, extend `PlayerAdapter` (§16.4, §16.8);
- open `state.choice` whenever it changes and call `attachPlayer(player, ref)` once per opened file, with the `ref` of the file it opened (§10.2); a player already playing `choice.ref` isn't reopened, nor attached again while it is still attached;
- after `Ended`, which releases the adapter, attach a player that still has its file open again, before or after the next `create` or `join` (§7.4, §10.2);
- as host, call `setMedia(media, ref)` with the `ref` that reopens the shared file, and `attachPlayer(player, ref)` with that `ref` once it's open, in either order (§10.2);
- call `detachPlayer()` when the user leaves the player or the app goes to the background;
- await the completion of `detachPlayer` or `close()` before disposing a player (§11.2);
- answer `state.mismatch` with `acceptMismatch()`, `declineMismatch()` or `choose(candidate)` (§9.6);
- route the user's play, pause, seek and speed controls to the party (§12);
- call `close()` when done with the party (§7.4).

Optional:

- `setRate` and `canSetRate` on the adapter: without them the SDK follows by seeking only;
- `seekAccuracyMs` on the adapter, for a player that seeks to keyframes or can't seek (§11.2);
- `durationMs` may be null: then the SDK doesn't protect the end of a shorter file;
- `playerChanged()`: without it, changes reach the relay on the next tick;
- probes: candidates may carry durations instead (§9.2);
- the offset control (§13);
- toasts from `Action`, participant events, host controls, `choose`, `resolveAgain`, `relayFor`, `formatCode`, `sameTitle`, `checkMedia`, `parseMedia`, and the transport, clock and randomness parameters.

### 16.8 Per-language idioms

|concept|Kotlin|Dart|TypeScript|Swift|
|---|---|---|---|---|
|constructor|parameters as in §16.2; without a resolver, `scope` is passed by name|named parameters: `relays`, `client`, `name` required; `resolver`, `connect`, `nowMs`, `random` optional|`new WatchParty({relays, client, name, resolver?, connect?, nowMs?, random?})`|`init(relays:client:name:resolver:connect:clock:random:) throws`, `Ref: Equatable & Sendable`; the parameters after `name` have defaults|
|`Relays`|`Relays.public(http)`, `Relays.at(http, baseUrl)`|`Relays.public({httpGet})`, `Relays.at(baseUrl)`|`Relays.public({httpGet?})`, `Relays.at(baseUrl)`|`Relays.public(httpGet:clock:)`, whose parameters have defaults; `Relays.at(_:) throws`|
|transport|the app's Ktor `HttpClient` with WebSockets installed, given to `Relays`; the party opens its sockets with it, and the SDK makes its GETs through a copy configured with `followRedirects = false`, reading the body as a stream up to the limit|`connect: Future<WebSocket> Function(Uri)` (package `web_socket`) on the party; `httpGet: Future<String> Function(Uri, Cancellation)` on `Relays.public`; defaults: `dart:io` `HttpClient` with `followRedirects = false`, or the browser's `fetch` with `redirect: "error"`; elsewhere pass `httpGet`|`connect: (url: string) => WebSocket` on the party; `httpGet: (url: string, signal: AbortSignal) => Promise<string>` on `Relays.public`; the default uses `fetch` with `redirect: "error"` and the signal|`connect: WebSocketConnect` (`@MainActor (String) async throws -> any WebSocketConnection`, a text WebSocket with `receive`, `closeCode`, `send` and `close(code:)`) on the party; `httpGet: HTTPGet` (`@MainActor (String) async throws -> String`) on `Relays.public`; defaults: `urlSessionConnect` (`URLSessionWebSocketTask`) and `urlSessionGet` (`URLSession` without redirects, status 200 only, at most 65536 bytes); both stop when their task is cancelled|
|clock|`clock: MonotonicClock` (`fun interface { fun nowMs(): Long }`); timers run on the scope's dispatcher, so a test dispatcher drives them|`nowMs: int Function()`; timers are `dart:async` `Timer`s, so `fake_async` drives them|`nowMs: () => number`; timers are `setTimeout`, so fake timers drive them|`clock: any PartyClock` on the party and on `Relays.public` (`@MainActor` protocol: `nowMs() -> Int64` and `schedule(afterMs:_:)`, which returns a cancel function); default `SystemClock` (`ContinuousClock` and `Task.sleep`); every SDK timer goes through it, so a virtual clock drives them|
|randomness: each draw is one u in [0, 1), and the reconnect backoff (§14) is the only draw|`random: kotlin.random.Random`, u = `nextDouble()`|`random: Random` (`dart:math`), u = `nextDouble()`|`random: () => number` in [0, 1), u = `random()`|`random: any RandomNumberGenerator` (default `SystemRandomNumberGenerator()`), u = `Double.random(in: 0..<1, using:)`|
|state|`state: StateFlow<PartyState<T>>`|`PartyState<T> get state` and `Stream<PartyState<T>> get states` (broadcast, emits on change)|`get state(): PartyState<T>` and `on("state", listener)`|`private(set) var state: PartyState<Ref>` and `states() -> AsyncStream<PartyState<Ref>>` (a new stream per call with every state published after it, in order)|
|events|`events: SharedFlow<PartyEvent>` with an unbounded buffer: it never drops and never suspends the party|`Stream<PartyEvent> get events` (broadcast)|`on("event", listener)`; `on` returns an unsubscribe function|`events() -> AsyncStream<any PartyEvent>`: a new stream per call that buffers without limit, so it never drops and never suspends the party|
|async work|`suspend` functions|`Future`s|`Promise`s|`async` functions; `create` and `join` are `async throws`|
|player calls|`attachPlayer(player: PlayerAdapter, ref: T)`; `detachPlayer(player: PlayerAdapter? = null): Job`; `close(): Job`|`void attachPlayer(PlayerAdapter player, T ref)`; `Future<void> detachPlayer([PlayerAdapter? player])`; `Future<void> close()`|`attachPlayer(player: PlayerAdapter, ref: T): void`; `detachPlayer(player?: PlayerAdapter): Promise<void>`; `close(): Promise<void>`|`attachPlayer(_ player: any PlayerAdapter, ref: Ref)`; `detachPlayer(_ player: (any PlayerAdapter)? = nil) -> Task<Void, Never>`; `close() -> Task<Void, Never>`; the last two `@discardableResult`|
|`PlayerAdapter`|`interface`; optional members have defaults; apps implement it|`abstract base class`; optional members have defaults; apps `extend` it, with `base` or `final` subclasses|an object type with optional members|`@MainActor` class-bound `protocol`; optional members have defaults in a protocol extension; apps implement it with a class|
|`Resolver`|`fun interface Resolver<T>`|`abstract interface class Resolver<T>` with `resolve(media, cancel)`|an object `{ resolve(media, signal) }`|`struct Resolver<Ref>` made from a closure: `Resolver { media in … }`|
|cancellation|coroutine cancellation of `resolve` and `probe`|`resolve(Media media, Cancellation cancel)` and `probe(Cancellation cancel)`; `Cancellation` has `bool isCancelled` and `Future<void> whenCancelled`|`resolve(media, signal: AbortSignal)` and `probe(signal: AbortSignal)`|Task cancellation of the task running the resolver closure or `probe` (`@MainActor @Sendable () async throws -> Int64?`)|
|`PartyEvent`|non-sealed `interface` with nested `PartyEvent.Action`, `PartyEvent.ParticipantJoined`, …|`abstract final class` (not exhaustive, not implementable outside the library); subclasses take the names plus `Event` (`ActionEvent`, `ErrorEvent`, …)|union discriminated by `type`: `"action"`, `"participantJoined"`, `"participantLeft"`, `"error"`, `"resolveFailed"`, `"playBlocked"`, `"ended"`; listeners ignore other `type`s|`protocol PartyEvent: Sendable`; structs take the names plus `Event` (`ActionEvent`, `ErrorEvent`, …); apps match them with `as` casts and a `default:` branch|
|enums|enum classes|enums|string literal unions|enums; the wire enums have `String` raw values|
|errors|`PartyException`|`PartyException implements Exception`|`PartyError extends Error`|`struct PartyError: Error`; `code` is a `PartyError.Code`, a string wrapper with constants such as `.unreachable`|
|integers|`Long`|`int`|`number` (integers up to 2^53 − 1)|`Int64`|
|optional adapter rate control|`canSetRate` + `setRate` with defaults|`canSetRate` + `setRate` with defaults|optional `setRate?` method; present means it can set rates|`canSetRate` + `setRate` with defaults|
|seek accuracy|`val seekAccuracyMs: Long get() = 0`|`int get seekAccuracyMs => 0`|optional `readonly seekAccuracyMs?: number`|`var seekAccuracyMs: Int64 { get }`, `0` by default|
|published collections|read-only views not backed by a list the SDK still mutates|`List.unmodifiable`, `Map.unmodifiable`|deeply `readonly` types, frozen with `Object.freeze`|value types (`Array`, `Dictionary`): the app's copy never changes the party's|
|`ext`|`JsonObject?`|`Map<String, Object?>?`|`Record<string, unknown>`|`[String: JSONValue]?`|
|`setMedia`|`setMedia(media, ref, positionMs = 0)`|`setMedia(media, ref, {positionMs = 0})`|`setMedia(media, ref, positionMs = 0)`|`setMedia(_ media: Media, ref: Ref, positionMs: Int64 = 0) throws`|
|`Media` JSON|`parseMedia(json: JsonElement): Media`; `toJson()`|`Media parseMedia(Object? json)`; `toJson()`|plain objects; `parseMedia(value: unknown): Media`|`parseMedia(_ json: JSONValue) throws -> Media`; `toJSON() -> JSONValue`; `JSONValue(parsing:)` reads JSON text|
|threads|pass a single-threaded scope (Android: a Main scope) and call the party from that thread; the party, adapter calls and resolver calls run on its dispatcher|one isolate|one event loop|`@MainActor`: the party and `Relays` run on the main actor and call the adapter, resolver, clock and transport there|

Each adapter type lets a 1.x release add a member with a default without breaking apps: Kotlin's interface members have defaults, Dart apps extend the base class, TypeScript members are optional, and Swift protocols get the default in a protocol extension. On a resolver timeout (§9.2) `ResolveFailed` carries Kotlin's `TimeoutCancellationException`, Dart's `TimeoutException`, Swift's `TimeoutError`, or in TypeScript a `DOMException` named `TimeoutError`. TypeScript also accepts plain values where a Promise is expected from adapter commands and the resolver.

A Swift `String` can't hold an unpaired surrogate, so `name`, `client` and descriptors from a Swift app never carry one, and the refusal of §7.1 and §9.7 before sending holds by the type. The SDK reads relay messages with its own JSON reader, which turns an unpaired surrogate escape into U+FFFD and marks it, so the receive checks still refuse it; `JSONValue(parsing:)` throws `invalid_message` on one.

### 16.9 Test seams

Each SDK exposes, at least to its own tests, the deterministic parts as functions: the room position and running check, the clock (fed pings and pongs with explicit times), the reconciliation step, ranking and the matching walk with simulated probe times, `sameTitle`, code normalization and routing, relay list validation, the `name` and `client` checks, the snapshot check and the receive and send checks of descriptors. A runner may instead drive a rule through a party in virtual time (TypeScript runs `matching.json` that way) or through the constructor (`names.json` in Kotlin, TypeScript and Swift). Tests run a whole party against a fake relay in virtual time through the clock and randomness parameters of §16.8 and a socket seam: Dart, TypeScript and Swift `connect`, and in Kotlin an internal transport.

## 17. Conformance

Every SDK runs every table in `conformance/vectors/` in its own suite, passes all of them, and fails on a table it has no runner for. `conformance/vectors/README.md` documents each table's format.

|table|section|
|---|---|
|`timeline.json`|§8|
|`clock-sync.json`|§5|
|`reconcile.json`|§11, §13|
|`matching.json`|§9.4, §9.5|
|`same-title.json`|§9.3|
|`code-routing.json`|§4|
|`relay-list.json`|§3.2|
|`names.json`|§7.1|
|`media-check.json`|§9.7|
|`room-check.json`|§8|
|`party.json`|§3–§15|

`party.json` holds traces of a whole party in virtual time: each step is an API call, a relay message, a player, resolver or HTTP answer, or time passing, and lists what the party must then have sent, opened, fetched, published and emitted. A rule a trace pins needs no test of its own.

Some rules can't be a trace or a table. Each SDK also has its own tests that:

- its embedded relay list equals the repository's `relays.json`;
- it accepts a relay message of 16384 bytes and more (`SPEC.md` §3.1);
- it writes every outgoing int without a fraction or exponent, also when the value came from arithmetic (§2);
- it refuses an integer API input or a computed wire value outside its bounds before any local effect (§2, §12, §13);
- a timer longer than 2147483647 ms doesn't run its action early and doesn't re-arm in a tight loop (§2);
- it refuses unpaired surrogates in `name`, `client` and descriptors before sending (§7.1, §9.7);
- in TypeScript, changing published state throws in strict mode; in Kotlin and Dart, changing a map or list the app passed to `setMedia` afterwards changes nothing in the party; Swift needs neither test, since `state` is `private(set)` and `Media` is a value type (§16.6);
- `parseMedia` applies the receive checks and fails with `invalid_message` (§16.2);
- a probe that throws a cancellation of its own, such as its own timeout, counts as returning nothing and the walk goes on (§9.2);
- its HTTP GETs are aborted after 5 s even when the fetcher ignores the signal, a body over 65536 bytes fails, redirects aren't followed and only status 200 succeeds (§3.4).

Each SDK runs exactly four scenarios against a real relay on `127.0.0.1`: (1) `create`, and `join` with a typed code (lower case, with a separator); (2) `setMedia`: the other party's resolver matches, both report `ready`, the room plays and both players follow; (3) `pause`, `seek` and `setRate` reach the other player; (4) a dropped connection is resumed (§14). TypeScript also restarts the relay with `--state-dir`, and both parties resume into the saved room. A test of an SDK's own transport against a local fake server, such as 16384-byte messages, close codes or HTTP limits, is not a scenario.

Swift runs its suites with swift-testing through `swift test` at the repository root, where its `Package.swift` sits. `VectorTests` (`sdk/swift/Tests/OpenWatchPartyTests/VectorTests.swift`) runs every table but `party.json` against the SDK's internal functions through `@testable import OpenWatchParty`, the matching walk of `matching.json` on a `VirtualClock`, and its `everyTableRuns` fails on a table it doesn't run; `PartyTraceTests` runs `party.json`. The party tests the traces can't replace (`SessionTests`, `MatchingTests` and `PlayerTests`, with the helpers in `Harness.swift`) run a `WatchParty` against `FakeRelay` in virtual time: a `VirtualClock` as `clock`, which runs timers only when a test calls `run()`, `advance(by:)` or `runUntil`; `FakeRelay.connect` as `connect`; a seeded `RandomNumberGenerator` (`SeededRandom`) as `random`; and `Relays.public` with an `httpGet` that fails (`notFoundRelays`). `RelayListTests` drives `Relays` through `httpGet` on a `VirtualClock`, `DefaultHTTPGetTests` runs the default `urlSessionGet` against a local HTTP server, and `WireTests` covers outgoing ints and timers longer than 2147483647 ms. `IntegrationTests` (`IntegrationTests.swift`, with `RelayHarness.swift`) runs the four scenarios with the default `URLSession` transport against a real relay: a fresh one per test on `127.0.0.1` with `--timer-scale 0.1` and raised per-IP and rate limits, as Kotlin's harness runs it. The relay is `OWP_RELAY_BIN` when set, else `relay/` built with `go build` for the suite. `TcpProxy` cuts the connection in the resume scenario. The suite builds only on macOS and Linux (`#if os(macOS) || os(Linux)`), which can start the relay.

# Open Watch Party protocol v1

This document defines the Open Watch Party (OWP) v1 wire protocol and relay behaviour. The JSON Schemas in `schema/` define message shapes; this document defines meaning and wins where they disagree. Client behaviour beyond the wire is in `SDK.md`. A relay built from this document, and a client built from it and `SDK.md`, interoperate with every conforming implementation.

The key words MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT, RECOMMENDED, MAY and OPTIONAL are to be interpreted as described in RFC 2119 and RFC 8174 when, and only when, they appear in all capitals.

## Contents

1. Overview (non-normative)
2. Conventions
3. Endpoints and transport
4. Versioning and extension
5. Relays, room codes and routing
6. Messages: common rules
7. Client → relay messages
8. Relay → client messages
9. Room snapshot
10. Media descriptor
11. Rooms and roles
12. Playback state machine
13. Limits and policy
14. Persistence and restart
15. Privacy
16. Client behaviour
- Appendix A. Error codes
- Appendix B. WebSocket close codes and HTTP refusals

## 1. Overview (non-normative)

People in a room watch one title together; each plays their own copy in their own app; the room keeps the copies in sync. The relay owns the timeline, the readiness barrier and the roles; clients follow, so a faulty client desyncs only its own user. One WebSocket per participant carries JSON text frames. The relay's clock is the room's clock, estimated by clients from `ping`/`pong`; every change sends the whole room, with the descriptor only when it changes.

The descriptor names a title, never a source; each client finds its own file. Positions are in the reference file's timeline, with any local offset applied only by the client. There are no accounts. A room lives on one relay for its whole life, its code's first character names that relay.

## 2. Conventions

A relay implements §3–§15; a client is an app connecting to one; a connection is one WebSocket. A participant is a person in a room, identified by peer ID, *connected* while a connection is attached and otherwise *disconnected*. A member is a connection attached to a participant; any other connection is *outside a room*. The host has host rights and a controller may send `play`, `pause`, `seek` and `rate` (§11.3).

An int is a JSON number with integral value, magnitude at most 2^53 − 1 (9007199254740991). Senders MUST write ints without fraction or exponent; receivers MUST accept any integral number in range (`3.0` is 3). A number is any JSON number. String length counts Unicode code points. Bytes are the UTF-8 bytes of the JSON text as received or sent; every message is valid UTF-8 (§3.1).

A forbidden character has general category Cc (U+0000–U+001F, U+007F–U+009F) or the Bidi_Control property (U+061C, U+200E, U+200F, U+202A–U+202E, U+2066–U+2069), and is allowed in no `name`, `client`, relay `name`, descriptor `title`, `series.title` or `file.name`.

Times and durations are int milliseconds. Relay time is the relay's monotonic clock from an origin it chooses, typically process start, meaningless across restarts; `serverTime` and `atMs` are relay times. Media time (`positionMs`, `durationMs`) is a position in the reference file.

Optional fields are marked `?`: absent, or a value of the field's type. `null` is a value only where the type lists it; elsewhere it is a wrong type. Policy values are written as the `/v1/info` key followed by the public value in parentheses: "the stall grace (`stallGraceMs`, 500)".

## 3. Endpoints and transport

### 3.1 The WebSocket: `GET /v1/ws`

The protocol runs over WebSocket (RFC 6455) at path `/v1/ws`; public relays use `wss://`. A relay MUST NOT refuse a connection because of its `Origin` header.

Each message is one JSON object, in UTF-8, in a text message; fragments are reassembled first and limits apply to the whole. Clients MUST send valid UTF-8 (RFC 3629): a text message invalid anywhere, pass-through `media` and `ping` `id` included, MUST make the relay close with 1007 and no `error`. A binary message gets `error` `invalid_message`, `re` null. A message over `maxMessageBytes` (8192) MUST make the relay close with 1009 and no `error`. Clients MUST accept relay messages of at least 16384 bytes; each is one JSON object in one text message.

Each connection has a token bucket, starting full at `messageBurst` (30), refilling `messagesPerSecond` (10) per second. Every data message takes one token, valid or not; control frames take none. With no token, the message is dropped and answered `error` `rate_limited`, `re` as §6.2 gives it; the connection stays open.

The relay sends a WebSocket ping frame every 30 s on every connection and closes a connection from which no frame of any kind, data, ping or pong, arrived for 60 s. Both are protocol constants and are never scaled (§13.3). Client WebSocket stacks answer ping frames themselves.

Connections over two limits are refused at upgrade, without upgrading: when a group of the client IP already holds `maxConnectionsPerIp` (10) × its group factor open connections, HTTP 429; when the relay already holds `maxConnections` (10000) open connections, HTTP 503. When both apply, the answer is 503.

The client IP is the TCP peer address, or behind a proxy the operator configures as trusted, the first `X-Forwarded-For` address, falling back to the peer address without that header. A relay MUST NOT trust `X-Forwarded-For` unless so configured. Behind a trusted proxy, a first value that is not an IP address or carries a zone gets HTTP 400 at upgrade. An IPv4-mapped IPv6 address is the IPv4 address.

Per-IP limits (connections, `join` lookups §7.2, rooms §11.10) are kept per address group: an IPv4 address is one group, factor 1; an IPv6 address is its /64 (factor 1), /56 (`ipv6Factor56`, 4) and /48 (`ipv6Factor48`, 16). A group's limit is the per-IP value × factor, multiplying bucket size and refill alike. A request counts in every group only when within the limit in all of them; a refused request counts in none.

On graceful shutdown the relay writes its persisted state, if any (§14), then closes every connection with status 1001.

### 3.2 `GET /v1/info`

`GET /v1/info` returns 200 with `Content-Type: application/json` and the body of §5.3, and MUST carry `Access-Control-Allow-Origin: *`.

### 3.3 `GET /health`

`GET /health` returns 200 with `Content-Type: text/plain` and body `ok` while the relay accepts connections, and MUST carry `Access-Control-Allow-Origin: *`.

## 4. Versioning and extension

The major version is in the path (`/v1/ws`, `/v1/info`). A change a v1 peer cannot safely ignore is a new major version, served beside v1 by the same relay at `/v2/…`.

Within v1, changes are additive: new optional fields, message types, values of the open enums `error.code`, `closed.reason` and `action.kind`, keys in `/v1/info`, `limits` and `policy`, and descriptor ID namespaces. Every receiver MUST ignore unknown members at every depth. A client MUST ignore a relay message of unknown `type`, MUST treat an unknown `error.code` as a refusal of the message in `re`, an unknown `closed.reason` as the end of its membership and an unknown `action.kind` as an action it does not describe. The relay MUST answer an unknown client `type` with `unsupported` (§6.2). Other enums (`phase`, `player`, `match.state`, `control`, media `kind`) are closed; a new value is v2.

Senders MUST NOT send two members with the same name in one object. Senders MUST NOT send a string escape yielding an unpaired surrogate: a `\uXXXX` escape in `\uD800`–`\uDFFF` is valid only as a high surrogate (`\uD800`–`\uDBFF`) directly followed by a low-surrogate escape (`\uDC00`–`\uDFFF`). The relay refuses a message breaking either rule with `invalid_message` (§6.2).

## 5. Relays, room codes and routing

### 5.1 Room codes

A room code is 6 symbols from `23456789ABCDEFGHJKMNPQRSTUVWXYZ`: 31 symbols, with no 0, 1, I, L or O. The relay always sends codes in upper case, and clients MAY display them in groups (`K7M-Q2X`). A client MAY send a code in `join` or `resume` as the user typed it. The relay removes the separators U+0020, U+0009, U+000A, U+000D, U+00A0, U+2007, U+202F, U+002D, U+2010–U+2014 and U+2212 from a received code, converts ASCII `a`–`z` to upper case, and looks the result up exactly; a result that is not 6 alphabet symbols names no room.

At creation the first symbol is drawn uniformly from the creation prefixes (§5.2), the rest uniformly from the alphabet, by CSPRNG. The code MUST differ from every held room, restored included (§14); collisions are redrawn. A relay MUST answer `join` and `resume` for every room it holds, whatever the first symbol.

### 5.2 The relay list

The relay list names the public relays. It is `relays.json` at the repository root, served unchanged at `https://openwatch.party/relays.json`; its schema is `relays.schema.json`. Relays publish no list: `/v1/info` describes only the relay that serves it (§5.3). This example list of three relays leaves `QRSTUVWXYZ` to no relay:

```json
{
  "relays": [
    { "id": "a", "name": "Relay A", "url": "wss://a.relay.example/v1/ws", "prefixes": "2345678" },
    { "id": "b", "name": "Relay B", "url": "wss://b.relay.example/v1/ws", "prefixes": "9ABCDEF" },
    { "id": "c", "name": "Relay C", "url": "wss://c.relay.example/v1/ws", "prefixes": "GHJKMNP" }
  ]
}
```

|field|type|rule|
|---|---|---|
|`relays`|array of relay entries|1 or more entries, in the order clients try them for `create` (§5.4)|
|`relays[].id`|string|1–32 characters of `a`–`z`, `0`–`9` and `-`, starting with a letter or digit; unique in the list; never reused for another relay|
|`relays[].name`|string|1–64 characters, no forbidden characters (§2); for display|
|`relays[].url`|string|absolute `wss://` URL of the relay's `/v1/ws` endpoint, ending in `/v1/ws`, without query or fragment; printable ASCII only (U+0021–U+007E: no spaces, control or non-ASCII characters; a non-ASCII host name is written in its `xn--` form); no `/` in the host and no `?` or `#` anywhere|
|`relays[].prefixes`|string|1–31 distinct upper-case symbols of the code alphabet: the first symbols this relay owns|

A `url` may carry a path prefix before `/v1/ws`, as in `wss://h/owp/v1/ws`. No symbol may appear in the `prefixes` of two relays. A list may leave symbols unassigned: no relay creates codes with them, and a later list may give them to a new relay. A code whose first symbol no relay of the list owns cannot be joined through the list.

A relay creates codes only with its creation prefixes: distinct symbols of the code alphabet, set by its operator, all 31 by default. A public relay's operator MUST keep its creation prefixes within its entry's `prefixes`; the relay doesn't check this. Creation prefixes differ from `prefixes` only while symbols move between relays: the old relay first stops creating codes with them, and at least `maxRoomAgeMs` (86400000) later the list moves them.

A relay's HTTP endpoints derive from its WebSocket URL: replace `wss://` with `https://`, or `ws://` with `http://`, and the trailing `/v1/ws` with `/v1/info` or `/health`.

### 5.3 `/v1/info` body

The schema is `info.schema.json`.

```json
{
  "protocol": 1,
  "id": "b",
  "limits": {
    "maxParticipants": 8, "maxMessageBytes": 8192, "maxMediaBytes": 4096, "maxExtBytes": 1024,
    "maxNameLength": 32, "maxClientLength": 64, "minRate": 0.5, "maxRate": 2.0,
    "maxConnections": 10000, "maxConnectionsPerIp": 10, "maxRooms": 10000, "maxRoomsPerIp": 3,
    "ipv6Factor56": 4, "ipv6Factor48": 16, "messageBurst": 30, "messagesPerSecond": 10,
    "joinBurst": 20, "joinsPerSecond": 1, "joinBurstTotal": 2000, "joinsPerSecondTotal": 200
  },
  "policy": {
    "stallGraceMs": 500, "stallReleaseMs": 400, "waitMs": 15000, "mediaWaitMs": 30000, "exclusionMs": 60000,
    "startDelayMinMs": 750, "startDelayMaxMs": 2000, "startDelayRttFactor": 1.5, "defaultRttMs": 500,
    "seekCoalesceMs": 200, "pauseToleranceMs": 2000, "hostPromotionMs": 30000, "resumeMs": 300000,
    "maxRoomAgeMs": 86400000
  }
}
```

|field|type|rule|
|---|---|---|
|`protocol`|int|`1`|
|`id`|string|this relay's ID, set by its operator; a public relay's is its `id` in the relay list (§5.2)|
|`limits`|object|every key of §13.1 and §13.2 with this relay's value|
|`policy`|object|every key of §13.3 with this relay's value|

A relay MUST report every key listed in §13. `policy` values are the durations in effect, after any test-mode scaling (§13.3). `/v1/info` serves operators and test suites; clients read nothing from it (§13).

### 5.4 Choosing the relay (client)

A client takes its relays from the public list or from a custom source. With the public list, the client ships a copy of the repository's `relays.json` and refreshes it only from `https://openwatch.party/relays.json`, using a fetched list only when it is valid by §5.2 and otherwise keeping the one it has. *Refreshing* is reading that URL again; `SDK.md` §3 defines when, and the HTTP limits. A custom source is one self-hosted relay's base URL, such as `https://relay.example.com`: the client uses that relay alone, at the base URL with `https://` changed to `wss://`, or `http://` to `ws://`, plus `/v1/ws`, for every `create`, `join` and `resume`, and fetches no list.

To create with several relays, the client connects to all of them at once and creates on the first one to answer its clock sync (`SDK.md` §6). On `relay_full`, it creates on the first relay it has not tried, in list order, and fails when every relay answered `relay_full`.

To join, the client connects to the relay whose `prefixes` contain the code's first symbol, case-insensitively; to resume, it goes back to the relay the party joined on. If no relay owns it, the client refreshes the list, at most once per join; if still none owns it, joining fails as `room_not_found` would. On `room_not_found` answering a `join`, the client refreshes the list within that same allowance and, if the first symbol now belongs to a relay with another `url`, retries the `join` there once. A custom source's relay gets every `join` whatever the first symbol, and its `room_not_found` is final.

## 6. Messages: common rules

### 6.1 Envelope

Every message, in both directions, is a JSON object with a string member `type` naming the message; `type` values are case-sensitive. Each message type in §7 and §8 has a schema `schema/<type>.schema.json`; shared definitions are in `common.schema.json`.

### 6.2 Validation and refusals

Size and UTF-8 are already checked (§3.1). The relay then checks, in order, stopping at the first refusal:

1. Rate limit (§3.1): no token → `rate_limited`.
2. Envelope: not valid JSON, not an object, no string `type`, more than one `type` member, or a `type` value with an unpaired surrogate escape (§4) → `invalid_message`.
3. Type: a `type` not defined for client → relay → `unsupported`. Relay → client type names count as unknown.
4. Shape: a missing required field, a wrong type, a value out of range, a string breaking its rule, an unknown value of a closed enum, an object at any depth, `media` and `ext` included, with two members of the same name (§4), or a string anywhere else, member names included, with an unpaired surrogate escape (§4) → `invalid_message`. Member names are compared after decoding escapes, so `"a"` and `"\u0061"` are the same name. Unknown members are ignored, never refused.
5. Membership: a room command, every type except `create`, `join`, `resume` and `ping`, from a connection outside a room → `not_in_room`; `create`, `join` or `resume` from a member → `already_in_room`.
6. The message-specific checks of §7, in the order each message's section lists them: access, per-IP allowances, permission (`forbidden`), room state.

A refused message changes no room state and gets exactly one `error` (§8.5); `re` is the message's `type` when its envelope passes step 2, else null, for every refusal including `rate_limited`. A refusal never closes the connection. An accepted message gets no acknowledgement; its effects are the reply: `joined`, `pong`, `room` or `closed`, per §7. `leave` gets no reply.

### 6.3 `name` and `client`

`name` is a display name of 1–32 characters (`maxNameLength`) with no forbidden characters (§2). It need not be unique, and the relay stores it unchanged.

`client` names the app and version as `<app>/<version>`, as in `Example/1.31.0`: 1–64 characters in all (`maxClientLength`), no forbidden characters. `<app>` is everything before the first `/`, at least 1 character; `<version>` is everything after it, at least 1 character, and may contain `/`. A `client` without `/`, or with an empty `<app>` or `<version>`, gets `invalid_message`. `<app>` MUST name the real app sending the message. Every participant sees `client`, and the relay's deny list (§11.9), its usage counts (§15) and the terms of use are keyed on `<app>`.

## 7. Client → relay messages

Every field is required unless marked `?`. Each section lists the fields and refusals in check order after §6.2 steps 1–5, and the effect; the snapshot each message causes follows §9.2.

|type|fields|allowed for|
|---|---|---|
|`create`|`name`, `client`|a connection outside a room|
|`join`|`code`, `name`, `client`|a connection outside a room|
|`resume`|`code`, `peerId`, `token`|a connection outside a room, for the participant it names|
|`ping`|`id`|any connection|
|`status`|`epoch`, `player`, `match`, `offsetMs`, `rttMs`?|members|
|`play`|—|controllers|
|`pause`|`positionMs`?|controllers|
|`seek`|`positionMs`|controllers|
|`rate`|`rate`|controllers|
|`setMedia`|`media`, `positionMs`?|the host|
|`clearMedia`|—|the host|
|`setControl`|`mode`|the host|
|`transferHost`|`peerId`|the host|
|`kick`|`peerId`|the host|
|`lock`|`locked`|the host|
|`leave`|—|members|
|`end`|—|the host|

### 7.1 `create`

|field|type|rule|
|---|---|---|
|`name`|string|§6.3|
|`client`|string|§6.3|

Refusals, in order: `client_blocked` (§11.9); `rate_limited` when a group of the client IP is the creator of its limit of rooms (§11.10); `relay_full` when the relay holds `maxRooms` (10000) rooms (§11.10). Effect: a new room with a new code (§5.1) and the defaults of §9.3; the sender becomes its first participant (§11.1) and host. The relay sends `joined` to the sender with the first snapshot, `rev` 1.

### 7.2 `join`

|field|type|rule|
|---|---|---|
|`code`|string|any string; matched as §5.1 says|
|`name`|string|§6.3|
|`client`|string|§6.3|

Refusals, in order:

1. `client_blocked` (§11.9).
2. `rate_limited` when the client IP or the relay has no join allowance left. Each address group has a bucket of `joinBurst` (20) tokens refilled at `joinsPerSecond` (1) per second, times its group factor; the relay has one bucket of `joinBurstTotal` (2000) tokens refilled at `joinsPerSecondTotal` (200) per second. A `join` reaching this step takes one token from each bucket when every one has a token, whether or not the code exists; otherwise it takes none and is refused.
3. `room_not_found`: no room with this code.
4. `room_full`: the room has `maxParticipants` (8) participants, disconnected ones included.
5. `room_locked`: the room is locked.

Effect: the sender becomes a new participant at the end of the join order (§11.1), and host promotion runs (§11.4). The relay sends `joined` to the sender and a snapshot to the other connected members.

### 7.3 `resume`

|field|type|rule|
|---|---|---|
|`code`|string|matched as §5.1 says|
|`peerId`|string|any string|
|`token`|string|any string|

Refusals, in order: `resume_failed` when there is no room with this code, no participant with this `peerId` (it never existed, or it left, was kicked or was removed), or `token` is not its token; tokens are compared in constant time, and the refusal is the same in each case. Then `client_blocked` when the participant's stored `client` is refused (§11.9). `resume` never takes a join token (§7.2).

Effect: if the participant is connected, the relay closes its old connection with status 4000 and sends it nothing else. The sender's connection becomes the participant's, and the participant keeps its peer ID, token, name, client, place in the join order, host flag, `player`, `match`, `offsetMs`, reported `rttMs`, exclusion and status epoch. Its reservation timer stops (§11.5); if it is the host, the host's absence ends (§11.4), otherwise host promotion runs. `waitingOn` is not recomputed; the resumer's next `status` does that. The relay sends `joined` to the sender and a snapshot to the other connected members, even when no snapshot field changed.

### 7.4 `ping`

|field|type|rule|
|---|---|---|
|`id`|string or number|chosen by the client; echoed unchanged|

`ping` is allowed on any connection, in a room or not, with no refusals beyond §6.2 steps 1–4, and has no effect on the room. The relay sends `pong` (§8.4) to the sender at once, with the same `id` value, its bytes echoed, and `serverTime` read as late as possible before sending.

### 7.5 `status`

|field|type|rule|
|---|---|---|
|`epoch`|int|≥ 0: the room epoch this report is about|
|`player`|string|`idle`, `loading`, `ready` or `buffering`|
|`match`|object or null|null while the client hasn't tried to match; otherwise the match object below|
|`match.state`|string|`searching`, `matched`, `mismatched` or `unavailable`|
|`match.deltaMs`?|int|own file's duration − `durationMs` of the media; positive means longer|
|`offsetMs`|int|−604800000 ≤ offsetMs ≤ 604800000 (± 7 days, §13.1): the local offset this participant plays with (§16.4); any sign|
|`rttMs`?|int|≥ 0: the client's current best round trip to the relay (§16.2)|

`player` values: `idle` is not in the player, because there is no media or the user is not playing it; `loading` is opening, searching, seeking, waiting on the user's choice after a mismatch, or waiting for the user to start a player that refused to play; `ready` is close enough to the room's position to follow it without seeking (§16.4) and able to play at once; `buffering` was playing and is now stalled.

`match.state` values: `searching` while candidates are tried or the user has not decided about a mismatch; `matched` when the chosen file's duration is within 1500 ms of `durationMs`; `mismatched` when the user plays a file outside that margin, with an offset; `unavailable` when no file plays, because none was found or the user declined. `deltaMs` may accompany any state and gives the difference for the file chosen or offered.

Effect: the relay stores `player`, `match` and `offsetMs` and `epoch` as the participant's status epoch; it stores `rttMs` when present and otherwise keeps the last one. A participant has *reported unavailable for the current epoch* when its latest status has `epoch` equal to the room's and `match.state` `unavailable`. The peer-event rules then run (§12.5), and stall handling follows §12.9. The relay sends a snapshot when any snapshot field changed, whether this participant's `player`, `match` or `offsetMs`, or a room field changed by §12.5; a status changing only `epoch` or `rttMs` sends nothing.

### 7.6 `play`

`play` has no fields. Refusals: `forbidden` when the sender is not a controller; `invalid_message` when the room has no media (`phase` `idle`). In `paused`, the relay sets the action `play` by the sender (§9.1) and the room enters waiting at `positionMs` with timeout `waitMs` (15000) (§12.4). In `playing` or `waiting`, nothing happens.

### 7.7 `pause`

|field|type|rule|
|---|---|---|
|`positionMs`?|int|≥ 0: where the sender's player stopped, in media time|

Refusals: `forbidden` when the sender is not a controller; `invalid_message` when the room has no media. In `playing` or `waiting`, the room pauses (§12.1) and the relay sets the action `pause` by the sender. The new `positionMs` is `min(positionMs, durationMs)` when the field is present and `|positionMs − P(now)| ≤ pauseToleranceMs` (2000), and `P(now)` otherwise. In `paused`, nothing happens.

### 7.8 `seek`

|field|type|rule|
|---|---|---|
|`positionMs`|int|≥ 0: the target in media time|

Refusals: `forbidden` when the sender is not a controller; `invalid_message` when the room has no media. The target is `min(positionMs, durationMs)`, applied now or held under §12.8. Applying a seek sets the action `seek` by its sender, then, in `paused`, sets `positionMs` to the target and stays paused, and in `playing` or `waiting`, enters waiting at the target with timeout `waitMs` (15000). The snapshot goes out at the moment the seek is applied.

### 7.9 `rate`

|field|type|rule|
|---|---|---|
|`rate`|number|`minRate` (0.5) ≤ rate ≤ `maxRate` (2.0)|

Refusals: `forbidden` when the sender is not a controller; `invalid_message` when the room has no media. Effect: the relay sets the action `rate` by the sender. In `playing` with `now ≥ atMs`, it first re-anchors: `positionMs` = `P(now)` and `atMs` = `now`. It then sets `rate` to the new rate and, in `playing`, reschedules the end-of-media timer (§12.11). In `paused` and `waiting` only `rate` changes, and the rate applies when playback starts.

### 7.10 `setMedia`

|field|type|rule|
|---|---|---|
|`media`|object|a media descriptor (§10), at most `maxMediaBytes` (4096) bytes as received|
|`positionMs`?|int|≥ 0: where the new media starts; default 0|

Refusal: `forbidden` when the sender is not the host. Effect, in any phase, in order: `epoch` increases by 1 and the relay stores `media`; a held seek is dropped (§12.8) and every exclusion is cleared (§12.7); `positionMs` becomes `min(positionMs or 0, durationMs)`; the relay sets the action `media` by the sender; the room enters waiting at that position with timeout `mediaWaitMs` (30000), and every connected participant counts as `loading` until it reports `status` with the new epoch (§12.3). The `room` this publishes carries the new `media` (§8.3).

### 7.11 `clearMedia`

`clearMedia` has no fields. Refusal: `forbidden` when the sender is not the host. With no media, nothing happens: no error and no snapshot. Otherwise, in order: `epoch` increases by 1 and the media becomes null; every playback timer stops (§12.12), a held seek is dropped and every exclusion is cleared; `phase` becomes `idle`, `positionMs` 0 and `waitingOn` empty, with `rate` and `atMs` unchanged; the relay sets the action `media` by the sender. The `room` this publishes carries `media` null (§8.3).

### 7.12 `setControl`

|field|type|rule|
|---|---|---|
|`mode`|string|`host` or `everyone`|

Refusal: `forbidden` when the sender is not the host. Effect: `control` becomes `mode` (§11.3).

### 7.13 `transferHost`

|field|type|rule|
|---|---|---|
|`peerId`|string|a participant of the room, connected or not|

Refusals: `forbidden` when the sender is not the host; `invalid_message` when no participant has this peer ID. Naming the current host changes nothing; otherwise the named participant becomes host, the relay sets the action `host` by the sender, and a disconnected new host's absence starts now (§11.4).

### 7.14 `kick`

|field|type|rule|
|---|---|---|
|`peerId`|string|a participant of the room, connected or not; the host itself included|

Refusals: `forbidden` when the sender is not the host; `invalid_message` when no participant has this peer ID. Effect: a connected target gets `closed` with reason `kicked` and its connection stays open outside any room; the target is then removed (§11.6), so its peer ID and token stop working. The relay sends `closed` to the target and a snapshot to the remaining connected members, unless the room was deleted.

### 7.15 `lock`

|field|type|rule|
|---|---|---|
|`locked`|boolean|`true` refuses new `join`s; `false` allows them|

Refusal: `forbidden` when the sender is not the host. Effect: `locked` becomes the value (§11.7).

### 7.16 `leave`

`leave` has no fields and no refusals beyond §6.2. The sender is removed (§11.6); its connection stays open outside any room, and the relay sends it nothing about the room again. The relay sends a snapshot to the remaining connected members, unless the room was deleted, and nothing to the sender.

### 7.17 `end`

`end` has no fields. Refusal: `forbidden` when the sender is not the host. Effect: every connected member, the sender included, gets `closed` with reason `ended`, the room is deleted (§11.8), and every connection stays open outside any room.

## 8. Relay → client messages

The relay sends the messages to one connection in the order it produced them. For one event (§9.2), each connection gets at most one snapshot.

|type|fields|
|---|---|
|`joined`|`code`, `you` (`peerId`, `token`), `room`, `media`, `serverTime`|
|`room`|the snapshot fields (§9), `action`?, `media`?|
|`pong`|`id`, `serverTime`|
|`error`|`code`, `message`, `re`|
|`closed`|`reason`|

### 8.1 `joined`

|field|type|rule|
|---|---|---|
|`code`|string|the room code, upper case|
|`you`|object|this connection's participant|
|`you.peerId`|string|§11.1|
|`you.token`|string|§11.1; the client keeps it to `resume`|
|`room`|object|the snapshot (§9.1): no `type`, `action` or `media`|
|`media`|object or null|the room's media descriptor exactly as the host sent it, or null without media|
|`serverTime`|int|relay time when the message was sent|

`joined` answers an accepted `create`, `join` or `resume` and starts the connection's membership.

### 8.2 `room`

`room` is the snapshot: `type` `room` plus every field of §9.1, and two optional fields.

|field|type|rule|
|---|---|---|
|`action`?|object|the action the event that published this snapshot set (§9.1); absent when it set none|
|`media`?|object or null|present only on the snapshot that starts a new epoch (§8.3)|

### 8.3 Media in `room`

`media` is present exactly on the snapshot that `setMedia` or an effective `clearMedia` publishes, the one whose `epoch` is new: the descriptor exactly as the host sent it, or null after `clearMedia`. Every other `room` leaves it out. A participant disconnected at that moment gets the current media in `joined` when it resumes. "Exactly as the host sent it", here and in `joined`, means the same JSON value: the same members and values, nested and unknown members included. A relay SHOULD forward the bytes it received.

### 8.4 `pong`

|field|type|rule|
|---|---|---|
|`id`|string or number|the `id` of the `ping`, unchanged|
|`serverTime`|int|relay time when the message was sent|

### 8.5 `error`

|field|type|rule|
|---|---|---|
|`code`|string|an error code of Appendix A (an open enum, §4)|
|`message`|string|English text for logs and developers; clients MUST NOT parse it|
|`re`|string or null|the `type` of the refused message, or null when its envelope was invalid (§6.2 step 2) or it was binary|

### 8.6 `closed`

|field|type|rule|
|---|---|---|
|`reason`|string|`ended` (the host ended the room), `kicked` (the host removed this participant) or `expired` (§11.8)|

After `closed`, the connection is outside any room and stays open; the relay sends it nothing more about that room, and it may `create`, `join` or `resume` again.

## 9. Room snapshot

### 9.1 Fields

The schemas are `snapshot.schema.json`, for the fields as they appear in `joined.room`, and `room.schema.json`, for the `room` message.

```json
{
  "type": "room",
  "rev": 42,
  "code": "K7MQ2X",
  "epoch": 3,
  "phase": "paused",
  "positionMs": 61234,
  "atMs": 918273645,
  "rate": 1.0,
  "control": "host",
  "locked": false,
  "waitingOn": [],
  "participants": [
    {
      "peerId": "p1", "name": "Edde", "client": "Example/1.31.0", "host": true, "connected": true,
      "player": "ready", "match": { "state": "matched", "deltaMs": -120 }, "offsetMs": 0
    }
  ],
  "action": { "kind": "pause", "by": "p1" }
}
```

Every field below is always present; `room` adds `action` and `media` (§8.2).

|field|type|rule|
|---|---|---|
|`rev`|int|≥ 1; see §9.2|
|`code`|string|the room code, upper case|
|`epoch`|int|≥ 0; + 1 on every `setMedia` and every effective `clearMedia`|
|`phase`|string|`idle` (no media), `paused`, `waiting` (will play once the barrier releases) or `playing`|
|`positionMs`|int|0 ≤ positionMs ≤ the media's `durationMs`, so at most 604800000; 0 in `idle`|
|`atMs`|int|≥ 0; relay time; in `playing`, the moment playback runs from `positionMs`; clients ignore it in other phases|
|`rate`|number|`minRate` ≤ rate ≤ `maxRate`|
|`control`|string|`host` or `everyone`|
|`locked`|boolean||
|`waitingOn`|array of strings|peer IDs the barrier waits for, in join order; empty except in `waiting`|
|`participants`|array|every participant, connected or not, in join order; never empty|
|`participants[].peerId`|string|§11.1|
|`participants[].name`|string|§6.3|
|`participants[].client`|string|§6.3|
|`participants[].host`|boolean|true for exactly one participant|
|`participants[].connected`|boolean|whether a connection is attached|
|`participants[].player`|string|as last reported in `status`; `idle` until the first|
|`participants[].match`|object or null|as last reported; `deltaMs` is absent when the status didn't carry it; null until the first status|
|`participants[].offsetMs`|int|as last reported, so within ± 604800000; 0 until the first status|

An action says who caused a change, for toasts. An event *sets an action* as the table below says, and the `room` it publishes carries that action as `action`:

|field|type|rule|
|---|---|---|
|`action.kind`|string|`play`, `pause`, `seek`, `rate`, `media`, `stall`, `ended` or `host` (an open enum, §4)|
|`action.by`|string or null|the peer ID that caused it, or null for the relay; may name a participant no longer in the room|

|kind|set by|`by`|
|---|---|---|
|`play`|`play` from `paused`|the sender|
|`pause`|`pause` from `playing` or `waiting`|the sender|
|`seek`|an applied `seek`|the seek's sender|
|`rate`|`rate`|the sender|
|`media`|`setMedia`, effective `clearMedia`|the sender|
|`stall`|a stall grace that ran out (§12.9)|the stalled participant|
|`ended`|end of media (§12.11)|null|
|`host`|`transferHost`|the sender|
|`host`|host promotion or a removed host's replacement (§11.4, §11.6)|null|

Nothing else sets an action. An event that sets an action always publishes, even when no snapshot field changed, and an event that sets several carries the last. Each `room` carrying `action` is a new action; `joined.room` never carries one, and the relay neither persists nor repeats an action.

### 9.2 When snapshots are sent and `rev`

An event is one inbound message, one connection closing, or one timer firing. After every event that changes any snapshot field, the relay publishes one snapshot: `rev` increases by 1, and every connected member gets it, the actor included. One event changing several fields publishes one snapshot. An event changing no snapshot field publishes none, except `resume`, which always publishes (§7.3), and an event that sets an action (§9.1). `create`, `join` and `resume` deliver the snapshot to the sender inside `joined`; the other connected members get `room`.

`rev` starts at 1 at creation and increases by exactly 1 per published snapshot, so on one connection each `room` has a higher `rev` than the snapshot before it. A client drops a `room` whose `rev` is not higher than the last snapshot it accepted on that connection. `joined` resets the comparison: the client accepts its `room` whatever its `rev` and compares later `room` messages with it, which covers a relay restart that makes `rev` go back (§14).

### 9.3 A new room

A new room has `rev` 1, `epoch` 0, `phase` `idle`, `positionMs` 0, `atMs` equal to the relay time of creation, `rate` 1, `control` `host`, `locked` false and `waitingOn` empty. It has one participant, the creator, with `host` true, `connected` true, `player` `idle`, `match` null and `offsetMs` 0.

## 10. Media descriptor

The room plays a title described by a media descriptor, and each client resolves it to its own file (§16.5). The schema is `descriptor.schema.json`.

```json
{
  "kind": "episode",
  "title": "Pilot",
  "ids": { "imdb": "tt0959621", "tvdb": "349232" },
  "series": { "title": "Breaking Bad", "year": 2008, "ids": { "imdb": "tt0903747", "tmdb": "1396", "tvdb": "81189" } },
  "season": 1,
  "episode": 1,
  "durationMs": 3487000,
  "file": { "sizeBytes": 2147483648, "oshash": "8e245d9679d31e12", "name": "Breaking.Bad.S01E01.1080p.mkv" },
  "ext": { "com.example": { "server": "1a2b3c", "item": "4711" } }
}
```

|field|type|rule|
|---|---|---|
|`kind`|string|`movie`, `episode` or `video` (a file with no catalogue identity: a home video, a recording, a clip)|
|`title`|string|1 or more characters, no forbidden characters (§2): the movie's or episode's title, or the video's|
|`year`?|int|release year|
|`ids`?|object|namespace → ID (below); for an episode, the episode's own IDs|
|`series`?|object|REQUIRED when `kind` is `episode`|
|`series.title`|string|1 or more characters, no forbidden characters (§2)|
|`series.year`?|int|first-air year|
|`series.ids`?|object|namespace → the series' IDs|
|`season`?|int|≥ 0, as the setter's source numbers it (0 for specials)|
|`episode`?|int|≥ 0, as the setter's source numbers it|
|`durationMs`|int|1 ≤ durationMs ≤ 604800000 (7 days, §13.1): the reference file's duration, from its player or reliable metadata|
|`file`?|object|hints that pick the same release among candidates|
|`file.sizeBytes`?|int|≥ 0|
|`file.oshash`?|string|the OpenSubtitles hash: 16 lower-case hex digits|
|`file.name`?|string|1 or more characters: the file name without directories; see **File name** below|
|`ext`?|object|app-private hints, at most `maxExtBytes` (1024) bytes as received|

Each `ids` member maps a namespace to a non-empty string, even where the catalogue uses numbers (`"tmdb": "1396"`). The v1 namespaces are `imdb`, `tmdb`, `tvdb`, `plex` (a `plex://` GUID), `anilist`, `anidb`, `kitsu` and `mal`; a movie's `tmdb` is a movie ID and a series' `tmdb` a TV ID. Unknown namespaces are ignored; adding one is a documentation change.

Each `ext` member name is the owning app's reverse-DNS name: two or more dot-separated labels of `a`–`z`, `0`–`9`, `-` (`com.example`). Values are owner-defined JSON; apps ignore members they do not own.

**File name.** `file.name` MUST NOT contain `/`, `\` or a forbidden character, NUL being one, and MUST NOT be `.` or `..`. Other dot-leading names, such as `.Arrival.mkv` or `...`, are allowed. A name is a hint for matching, never a path.

The whole descriptor is at most `maxMediaBytes` (4096) bytes; descriptor and `ext` sizes are measured as the JSON text of the value as received.

**Nothing that fetches media.** A sender MUST NOT put URLs, magnet links, info hashes, stream links or anything else that locates or fetches media in any field, `ext` included. A receiver never treats descriptor content as a path, URL or host. `ids` values are identifiers, not locators.

The relay checks the schema, the table and the `ids`, `ext`-name, file-name and size rules, refusing violations with `invalid_message` (§6.2); it does not check the fetching rule. It reads only `durationMs`; the rest is opaque and forwarded unchanged.

## 11. Rooms and roles

### 11.1 Participants

A participant has a peer ID, a token, `name`, `client`, a place in the join order, and the status fields of §7.5. A peer ID is 1–16 characters of `A`–`Z`, `a`–`z`, `0`–`9`, `_` and `-`, unique in the room and never reused within one room's life. A token is 32 bytes from a CSPRNG, written in base64url without padding, 43 characters. A room holds at most `maxParticipants` (8) participants, connected or not. The join order is the order participants entered the room; `resume` keeps a participant's place.

### 11.2 Membership states

A participant is connected, disconnected and reserved for `resume`, or removed. Removed participants leave the snapshot, and their peer ID and token stop working.

### 11.3 Host and controllers

A room always has exactly one host, and the creator is the first. The host-only commands are `setMedia`, `clearMedia`, `setControl`, `transferHost`, `kick`, `lock` and `end`. With `control` `host`, the host is the only controller; with `control` `everyone`, every participant is a controller. `play`, `pause`, `seek` and `rate` from a non-controller, and host-only commands from anyone but the host, get `forbidden`.

### 11.4 Host absence and promotion

The host is *absent* while disconnected. Absence starts when the host disconnects or, for a disconnected participant that becomes host through `transferHost`, removal of the previous host, or restore after a restart (§14), at that moment; it ends when the host connects again. When the host has been absent for `hostPromotionMs` (30000) and at least one participant is connected, the connected participant earliest in join order becomes host, and the relay sets the action `host` by null. The relay checks when the absence reaches `hostPromotionMs` and again after every `join` and `resume` while the absence is that long or longer. With nobody connected, the host stays.

### 11.5 Disconnect

When a member's connection closes for any reason other than a `resume` takeover, in order: the participant stays, with `connected` false and `player`, `match` and `offsetMs` kept; its stall grace stops (§12.9) and its reservation timer starts, so after `resumeMs` (300000) disconnected it is removed (§11.6) unless it resumed; if it is the host, its absence starts (§11.4); and the peer-event rules run (§12.5). The relay sends a snapshot to the connected members.

### 11.6 Removal

`leave`, `kick` and an expired reservation remove a participant, in order:

1. It leaves `participants` and `waitingOn`; its peer ID and token stop working; its stall grace and reservation timer stop.
2. If no participant remains, the room is deleted (§11.8) and the later steps do not run.
3. If it was the host, the host passes at once to the connected participant earliest in join order or, with nobody connected, to the participant earliest in join order, and the relay sets the action `host` by null; a disconnected new host is absent from now (§11.4).
4. The peer-event rules run (§12.5).

The relay sends one snapshot to the remaining connected members.

### 11.7 Lock

A locked room refuses `join` with `room_locked`; `resume` still works. A kicked participant cannot come back into a locked room; into an unlocked room it can `join` again with the code.

### 11.8 End, expiry and deletion

A room is deleted when its last participant is removed (§11.6: `leave`, `kick` or an expired reservation), by `end` (§7.17), and at `maxRoomAgeMs` (86400000, 24 hours) after creation, when every connected member first gets `closed` with reason `expired`. Deletion stops every timer and forgets the media, participants and tokens; the room leaves the persisted state (§14); its code names no room afterwards, answering `room_not_found` and `resume_failed`, and may be drawn again; connections that were members stay open outside any room.

### 11.9 Refused clients (`client_blocked`)

A relay MAY keep a deny list of app names, the `<app>` of §6.3; the operator's terms of use decide its entries. The relay compares `<app>` with each entry case-insensitively for ASCII and exactly otherwise. A `create` or `join` whose `client` has a denied `<app>`, and a `resume` of a participant whose stored `client` has a denied `<app>`, get `client_blocked`. A relay MUST apply a changed deny list without a restart, and a change does not affect connections already in rooms.

### 11.10 Rooms per IP and room cap

The relay remembers, in memory only, the client IP that created each room. While a group of that IP (§3.1) is the creator of `maxRoomsPerIp` (3) × its group factor rooms that the relay holds, a `create` from it gets `rate_limited`. Restored rooms (§14) count for no IP. While the relay holds `maxRooms` (10000) rooms, restored ones included, `create` gets `relay_full` and the client creates on another relay (§5.4). Restoring rooms after a restart is never refused.

## 12. Playback state machine

### 12.1 Phases

In `idle` there is no media, and `play`, `pause`, `seek` and `rate` from a controller get `invalid_message`. In `paused` the position holds at `positionMs`. In `waiting` the position holds at `positionMs` while the barrier waits for `waitingOn`. In `playing` the position advances from `positionMs` at `atMs` with `rate`. *Pausing* at `p` stops every playback timer (§12.12) and sets `phase` to `paused`, `positionMs` to `p` and `waitingOn` to empty, leaving `atMs` and `rate` unchanged.

### 12.2 Position

`P(t)` is the room's position at relay time `t`. In `playing` it is `min(durationMs, floor(positionMs + max(0, t − atMs) × rate))`, computed in IEEE 754 double precision with the floor of the whole sum; in `paused`, `waiting` and `idle` it is `positionMs`. `durationMs` is the current media's. A `playing` room with `atMs` in the future is a scheduled group start: clients hold at `positionMs` and start at `atMs`. Every `positionMs` the relay stores is between 0 and `durationMs`.

### 12.3 Counted and ready

A participant's *effective player* is its reported `player` when its latest status has the room's `epoch`, and `loading` when its latest status is for another epoch or it has sent none. A participant is *counted* when all of these hold: it is connected; it is not excluded (§12.7); its effective player is not `idle`; and it has not reported unavailable for the current epoch (§7.5). A participant is *ready* when its status epoch is the room's `epoch` and its `player` is `ready`, so a counted participant whose latest status is for another epoch is not ready. After `setMedia`, everyone connected counts as `loading` until it reports on the new media, and a status in flight for the old media changes nothing.

### 12.4 Entering waiting

To enter waiting at `p` with timeout `T`, in order: every playback timer stops; `phase` becomes `waiting` and `positionMs` becomes `p`; the wait is a *stall wait* if a stall grace caused it (§12.9) and not otherwise; `waitingOn` becomes the counted participants that are not ready, in join order; the barrier timer starts with `T` (§12.7); and if `waitingOn` is empty, the room releases at once (§12.6).

### 12.5 Peer-event rules

After every `status`, disconnect (§11.5) and removal (§11.6), in order:

1. Nobody in the player: if `phase` is `playing` or `waiting` and no connected participant has an effective player other than `idle`, the room pauses at `P(now)`, setting no action, and the rules stop.
2. Barrier: if `phase` is `waiting`, `waitingOn` is recomputed as the counted participants that are not ready, in join order. Then, if `waitingOn` is non-empty, the stall release timer stops; if it is empty and the wait is not a stall wait, the room releases at once; if it is empty in a stall wait and the stall release timer is not running, that timer starts with `stallReleaseMs` (400), and when it fires the room releases if it is still waiting with `waitingOn` empty.

These rules run only after these events, not after commands, `join`, `resume` or an exclusion ending; their effects show at the next peer event.

### 12.6 Release

To release, in order: the barrier and stall release timers stop; `phase` becomes `playing` and `waitingOn` empty; `atMs` becomes `now + clamp(floor(startDelayRttFactor × maxRtt), startDelayMinMs, startDelayMaxMs)`, with the public values `now + clamp(floor(1.5 × maxRtt), 750, 2000)`; no action is set; and the end-of-media timer is scheduled (§12.11). `maxRtt` is the highest `rttMs` reported by a counted participant, counting `defaultRttMs` (500) for a counted participant that never reported one, and `defaultRttMs` when no participant is counted.

### 12.7 Barrier timeout and exclusion

The barrier timer starts each time the room enters waiting, with `waitMs` (15000), or `mediaWaitMs` (30000) after `setMedia`; leaving `waiting` stops it. When it fires, every participant then in `waitingOn` is *excluded* until `now + exclusionMs` (60000), and the room releases. Excluded participants catch up by themselves and do not hold the room until the exclusion ends. An exclusion survives disconnect and `resume`; `setMedia` and an effective `clearMedia` clear every exclusion. An exclusion ends silently when its time passes and takes effect at the next computation that looks at it.

### 12.8 Seek coalescing

The relay applies at most one seek per `seekCoalesceMs` (200) and records when it last applied a seek in the room. A `seek` arriving when no seek was applied in the last `seekCoalesceMs` is applied at once (§7.8); otherwise it becomes the room's *held seek*, replacing any held seek, and is applied at `last applied + seekCoalesceMs` with the target and sender it carries, under the rules of the phase at that moment. `setMedia`, an effective `clearMedia` and deletion drop the held seek. Permission is checked when the `seek` arrives, and a held seek is applied even if its sender lost control since.

### 12.9 Stall

A participant is *stalled* when it is counted and its latest status has the room's `epoch` and `player` `buffering`. In `playing`, a status that leaves its sender stalled starts its stall grace of `stallGraceMs` (500) unless one is running, and further `buffering` statuses do not restart it. A `ready` status from the participant, its disconnect or removal, and the room leaving `playing` stop the grace. When the grace runs out while the room is `playing` and the participant is still stalled, the relay sets the action `stall` by that participant and the room enters waiting at `P(now)` with timeout `waitMs` (15000) as a stall wait; otherwise nothing happens. A stall wait releases `stallReleaseMs` (400) after `waitingOn` empties (§12.5).

### 12.10 Nobody in the player

Section 12.5 step 1 looks at connected participants with an effective player other than `idle`, excluded ones and those that reported unavailable included, so an excluded participant that is still watching keeps the room going.

### 12.11 End of media

In `playing`, when `P(t)` reaches `durationMs`, the room pauses at `durationMs` and the relay sets the action `ended` by null, at that moment. The end-of-media timer fires at `atMs + ceil((durationMs − positionMs) / rate)`; it is scheduled on every release and every `rate` while `playing`, and stops whenever the room leaves `playing`. Scheduled while `P(now)` is already `durationMs`, the room ends at once in the same event, which publishes one snapshot, `paused` at `durationMs` with the action `ended`. The relay never advances to other media; the host's app decides what plays next and sets it with `setMedia`. A `play` at the end enters waiting, and the event that releases it ends the room again; the host seeks first.

### 12.12 Timers

|timer|per|starts|stops|on firing|
|---|---|---|---|---|
|barrier|room|entering waiting|leaving `waiting`|exclude `waitingOn`, release (§12.7)|
|stall release|room|`waitingOn` empties in a stall wait|`waitingOn` non-empty; leaving `waiting`|release if still waiting with `waitingOn` empty|
|end of media|room|release; `rate` in `playing`|leaving `playing`|pause at `durationMs`, `ended` (§12.11)|
|held seek|room|a coalesced `seek`|`setMedia`; `clearMedia`; deletion|apply the seek (§12.8)|
|stall grace|participant|§12.9|§12.9|enter a stall wait (§12.9)|
|host absence|room|§11.4|the host connects; another participant becomes host|promote (§11.4)|
|reservation|participant|disconnect|`resume`; removal|remove (§11.6)|
|maximum age|room|creation|deletion|`closed` `expired`, delete (§11.8)|

"Every playback timer stops" means the barrier, stall release and end-of-media timers and every stall grace; a held seek survives a phase change. A timer that fires runs as one event (§9.2); a timer stopped or restarted before its event runs does nothing.

## 13. Limits and policy

Protocol values are the same on every v1 relay, the schemas encode them, and changing one is a v2 change. Policy values are each relay's choice, and `/v1/info` reports them; only `resumeMs` is fixed, at 300000 on every v1 relay, scaled only in a relay's test mode (§13.3). Clients act on no policy value but `resumeMs` (§16.1): they assume 300000 and read nothing from `/v1/info`, so changing any other policy value needs no client release. The public values below are the public relays' values and the relay's built-in defaults.

### 13.1 Protocol limits (`limits`, fixed in v1)

|key|value|meaning|
|---|---|---|
|`maxMessageBytes`|8192|largest inbound message (§3.1)|
|`maxMediaBytes`|4096|largest media descriptor (§10)|
|`maxExtBytes`|1024|largest `ext` (§10)|
|`maxNameLength`|32|longest `name` (§6.3)|
|`maxClientLength`|64|longest `client` (§6.3)|
|`minRate`|0.5|lowest `rate`|
|`maxRate`|2.0|highest `rate`|

Two more bounds are fixed in v1 and encoded in the schemas, but not reported in `/v1/info`: `durationMs` is at most 604800000 (§10), and `offsetMs` is between −604800000 and 604800000 (§7.5).

### 13.2 Policy limits (`limits`)

|key|public value|meaning|
|---|---|---|
|`maxParticipants`|8|participants per room, disconnected ones included (§11.1); at most 20, so that every relay message stays within the 16384 bytes clients accept (§3.1)|
|`maxConnections`|10000|open WebSocket connections on the relay (§3.1)|
|`maxConnectionsPerIp`|10|open WebSocket connections per address group, times its factor (§3.1)|
|`maxRooms`|10000|rooms the relay holds; `create` beyond it gets `relay_full` (§11.10)|
|`maxRoomsPerIp`|3|rooms an address group created that the relay still holds, times its factor (§11.10)|
|`ipv6Factor56`|4|factor of an IPv6 /56 group (§3.1)|
|`ipv6Factor48`|16|factor of an IPv6 /48 group (§3.1)|
|`messageBurst`|30|per-connection message bucket size (§3.1)|
|`messagesPerSecond`|10|per-connection refill per second (§3.1)|
|`joinBurst`|20|per-group `join` bucket size, times its factor (§7.2)|
|`joinsPerSecond`|1|per-group `join` refill per second, times its factor (§7.2)|
|`joinBurstTotal`|2000|relay-wide `join` bucket size (§7.2)|
|`joinsPerSecondTotal`|200|relay-wide `join` refill per second (§7.2)|

### 13.3 Policy durations (`policy`)

|key|public value|scaled|meaning|
|---|---|---|---|
|`stallGraceMs`|500|yes|buffering this long while playing starts a stall wait (§12.9)|
|`stallReleaseMs`|400|yes|a stall wait releases this long after `waitingOn` empties (§12.5)|
|`waitMs`|15000|yes|barrier timeout after `play`, `seek` and a stall (§12.7)|
|`mediaWaitMs`|30000|yes|barrier timeout after `setMedia` (§12.7)|
|`exclusionMs`|60000|yes|how long a participant that timed out doesn't hold the room (§12.7)|
|`startDelayMinMs`|750|yes|shortest start delay (§12.6)|
|`startDelayMaxMs`|2000|yes|longest start delay (§12.6)|
|`startDelayRttFactor`|1.5|no|start delay per ms of the highest round trip (§12.6)|
|`defaultRttMs`|500|no|round trip assumed for a participant that reported none (§12.6)|
|`seekCoalesceMs`|200|yes|minimum time between applied seeks (§12.8)|
|`pauseToleranceMs`|2000|no|largest distance at which `pause` keeps the sender's position (§7.7)|
|`hostPromotionMs`|30000|yes|host absence before promotion (§11.4)|
|`resumeMs`|300000|yes|how long a disconnected participant stays reserved (§11.5); 300000 on every v1 relay (§13)|
|`maxRoomAgeMs`|86400000|yes|longest life of a room (§11.8)|

`startDelayRttFactor` is a number; every other policy value is an int. A relay MAY offer a test mode that multiplies every scaled duration by one factor, rounded down to whole milliseconds, so a conformance suite runs fast. Media positions, `durationMs`, reported `rttMs`, the keepalive (§3.1) and the rate-limit buckets are never scaled, and `/v1/info` reports the scaled values. The public relays run unscaled.

## 14. Persistence and restart

A relay MAY persist rooms across restarts. It then writes a changed room within 5 s, every `playing` room at least every 5 s, and all rooms at graceful shutdown, atomically (new file renamed over the old), in one file or one per room: per room the code, `rev`, `epoch`, media, position (`P` at writing), `rate`, `control`, `locked`, wall-clock creation time and peer-ID sequence; per participant the peer ID, SHA-256 hash of the token, `name`, `client`, join order, host flag, `match`, `offsetMs`. It MUST NOT persist tokens, client IPs, `rttMs`, actions, exclusions or timers. Deleted rooms leave the state within 5 s. The state is readable by the relay's user only.

On start it restores every room whose maximum age has not passed: `paused` at the persisted position (`idle` stays `idle`), `waitingOn` empty, `atMs` the restore time, `rev` continuing from the persisted value. After a crash the state can be up to 5 s old: `rev` can go back (§9.2), and a room that was playing restores a position at most 5 s old. Participants are disconnected, `player` `idle`, tokens working. Reservation timers and host absence start at restore, so a restored room lives until its participants' reservations expire, unless someone resumes; maximum age counts from the original creation. Held seeks, stall graces, exclusions and `rttMs` are gone, so `defaultRttMs` counts (§12.6) and the effective player is `loading` (§12.3) until reported again. Without persistence, rooms are lost and `resume` gets `resume_failed`.

## 15. Privacy

The relay reads titles and positions, as it must to run a room, but never media (§10). These rules bind the operator of a public relay; self-hosting operators SHOULD follow them.

The relay logs no media descriptors or any part of them, participant names, room codes, peer IDs, tokens, positions or message contents; `client` appears only in the counts below; logs may hold error codes and counts. IPs stay in memory only for §3.1, §7.2, §11.10 and the usage allowance, and MUST NOT reach logs or disk, relay or proxy. Room data lives in memory and §14 state, deleted with the room (§11.8).

Metrics are counts only, including rooms, participants, connections, rooms created, accepted joins and resumes, inbound messages, refused upgrades by reason, error replies by code, and participants per `<app>` name. Per-app series exist only for the 50 names with the highest counts in that UTC day's usage record, `(other)` excluded, with ties going to the name that sorts first; every other participant counts under `(other)`.

Public relays keep per-UTC-day counts per `<app>` of accepted `create`, `join` and `resume`, holding no IPs, participant names, titles, room codes or tokens; a day is deleted 400 days later. Each IPv4 address or IPv6 /64 adds at most 3 names per day: a new `<app>` from an address that added 3 counts under `(other)`; a recorded name counts under itself from any address. Which addresses added names is memory-only, never on disk, cleared at day end. Expired days and the per-address memory are dropped within a minute of their deadline, and from `usage.json` at the next save, so within two minutes, whatever the traffic. A relay MAY cap names per day; beyond it counts under `(other)`.

## 16. Client behaviour

This section states what a client MUST do on the wire. `SDK.md` defines the algorithms and values behind it: clock sync (§5), relay choice (§6) and following the room (§11).

### 16.1 Lifecycle

1. Connect: to create, to a relay chosen as §5.4 says; to join, to the relay owning the code's first symbol; to resume, back to the relay the party joined on (§5.4). Start clock sync at once (§16.2).
2. Send `create` or `join` and receive `joined`: the code, peer ID and token, the snapshot and the media.
3. While in the room, send `status` (§16.3), receive `room`, and follow the room (§16.4).
4. After a dropped connection, reconnect to the same relay and `resume` with the code, peer ID and token within `resumeMs`, 300000 (§13). A client MUST NOT resume after a close with status 4000 (§7.3), after `closed`, or after `leave`. After `resume_failed` or `client_blocked` the membership is over. Any other `error` answering `resume`, such as `rate_limited` or an unknown code, refuses only that attempt, and the client MAY retry within `resumeMs`.
5. Send `leave`, or receive `closed`.

### 16.2 Clock sync

A client estimates relay time from `ping` and `pong` on every connection, as `SDK.md` §5 defines. `status` `rttMs` is its best round trip: the lowest `pong` round trip, receipt time minus send time, among the samples its clock uses.

### 16.3 Status

A member MUST send `status` right after `joined`, after every change of `epoch`, `player`, `match` or `offsetMs`, and at least every 15 s. Its `epoch` is the room epoch of the last `joined` or accepted `room` (§9.2) the client processed, and `ready` means the player plays that epoch's media.

### 16.4 Following the room

The target at relay time `t` is `P(t) + offsetMs` (§12.2) in the client's own file: local position = room position + the user's offset for a differing file. A `matched` file plays at offset 0 unless the user sets one; the relay never applies offsets. A client reports `ready` only when its player can play at once and is close enough to the target that following it needs no seek, and `loading` while getting there (§7.5). `SDK.md` §11 defines how SDKs follow the target, with their thresholds.

### 16.5 Matching

Report `match` as §7.5 defines. `SDK.md` §9 defines how SDKs find, rank and choose files, and §9.3 there when two descriptors name the same title.

## Appendix A. Error codes

|code|answers|meaning|
|---|---|---|
|`invalid_message`|any message|malformed JSON or envelope (`re` null), a shape violation, a binary message (`re` null), `play`/`pause`/`seek`/`rate` without media, `transferHost`/`kick` naming no participant|
|`unsupported`|a message of unknown `type`|this relay doesn't implement the type (§4)|
|`rate_limited`|any message; `create`; `join`|per-connection bucket empty (§3.1); rooms per address group (§11.10); join allowance of an address group or of the relay (§7.2)|
|`relay_full`|`create`|the relay holds `maxRooms` rooms; create on another relay (§11.10, §5.4)|
|`room_not_found`|`join`|no room with this code (§7.2)|
|`room_full`|`join`|`maxParticipants` reached (§7.2)|
|`room_locked`|`join`|the room is locked (§7.2)|
|`forbidden`|`play`, `pause`, `seek`, `rate`, host-only commands|the sender lacks the role (§11.3)|
|`not_in_room`|room commands|the connection is outside a room (§6.2)|
|`already_in_room`|`create`, `join`, `resume`|the connection is a member (§6.2)|
|`resume_failed`|`resume`|unknown room or participant, or wrong token (§7.3)|
|`client_blocked`|`create`, `join`, `resume`|this relay refuses the app (§11.9)|

## Appendix B. WebSocket close codes and HTTP refusals

|code|sent by|meaning|client action|
|---|---|---|---|
|1001|relay|the relay is shutting down|reconnect and `resume`|
|1007|relay|an inbound text message wasn't valid UTF-8|fix the sender; reconnect and `resume`|
|1009|relay|an inbound message exceeded `maxMessageBytes`|fix the sender; reconnect and `resume`|
|4000|relay|replaced: a `resume` on another connection took over this participant|MUST NOT resume|
|HTTP 400|relay, at upgrade|behind a trusted proxy, the first `X-Forwarded-For` value isn't an IP address or carries a zone|fix the proxy|
|HTTP 429|relay, at upgrade|too many connections from this IP or its network (§3.1)|retry later|
|HTTP 503|relay, at upgrade|the relay is at `maxConnections`|retry later or use another relay to create|

A connection dropped by the keepalive (§3.1), by the network, or because the client does not read what the relay sends ends without a defined code; the client reconnects and resumes. The WebSocket layer may also close with 1002 on framing errors and answer HTTP 400 to a malformed handshake.

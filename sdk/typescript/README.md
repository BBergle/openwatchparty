# TypeScript SDK

`openwatchparty` keeps players in different apps in sync in one room, implementing the behaviour in [`spec/SDK.md`](../../spec/SDK.md); read that for the rules this page only names.

## Install

```sh
npm install openwatchparty
```

## Requirements

| Runtime | Minimum |
|---|---|
| Node | 22 |
| Chrome / Edge | 119 |
| Firefox | 121 |
| Safari | 17.4 |

The package is ESM only (no `require` condition) and has no runtime dependencies. TypeScript consumers need the `dom` lib, or `skipLibCheck`. A global `WebSocket` must exist even when you pass a custom `connect`.

## Quick start

```ts
import { Relays, WatchParty, attachVideo, formatCode, type Resolver } from "openwatchparty";

// One relay list for the whole app, passed to every party.
const relays = Relays.public();

// The app's search: the files the user could play for the room's title, best first.
const resolver: Resolver<string> = {
  async resolve(media, signal) {
    const res = await fetch(`/api/search?title=${encodeURIComponent(media.title)}`, { signal });
    const files: { id: string; url: string; durationMs: number }[] = await res.json();
    // ref: the absolute URL, as video.src reads it back
    return files.map((f) => ({ id: f.id, ref: new URL(f.url, document.baseURI).href, durationMs: f.durationMs }));
  },
};

const party = new WatchParty<string>({ relays, client: "Example/1.0", name: "Ann", resolver });
const video = document.querySelector("video")!;

// Open each choice, one at a time, and attach the <video> with its ref.
let attached: string | null = null; // the ref the <video> is attached with
let detach: (() => Promise<void>) | null = null;
let opening = false;
async function openChoice(): Promise<void> {
  if (opening) return; // the open under way goes on to the newest choice
  opening = true;
  try {
    while (true) {
      const ref = party.state.choice?.ref;
      if (ref === undefined || ref === attached) return; // no choice, or attached: it follows by itself
      if (video.src !== ref) {
        const { promise, resolve } = Promise.withResolvers<void>();
        const loading = new AbortController();
        for (const type of ["loadeddata", "error"]) video.addEventListener(type, () => resolve(), { signal: loading.signal });
        video.src = ref;
        await promise; // loaded, or failed to
        loading.abort(); // removes both listeners
      }
      if (party.state.choice?.ref !== ref) continue; // a newer choice came meanwhile: open that one
      const previous = detach;
      detach = attachVideo(party, video, ref);
      attached = ref;
      void previous?.(); // removes the earlier file's control listeners
    }
  } finally {
    opening = false;
  }
}
party.on("state", () => void openChoice());

party.on("event", (event) => {
  if (event.type !== "ended") return;
  console.log(`Out of the room: ${event.reason}`);
  void detach?.(); // the end released the <video>: its controls stop going to the party
  detach = attached = null;
});

const code = new URLSearchParams(location.search).get("room");
if (code !== null) {
  await party.join(code);
} else {
  console.log(`Room code: ${formatCode(await party.create())}`);
  // Host: share the file the <video> plays.
  party.setMedia(
    { kind: "movie", title: "Example Movie", year: 2024, ids: { tmdb: "12345" }, durationMs: Math.round(video.duration * 1000) },
    video.src,
    Math.floor(video.currentTime * 1000),
  );
}
```

[`examples/web`](../../examples/web/README.md) is a full browser page on `attachVideo`.

## Relays

`Relays.public()` starts from the copy of [`relays.json`](../../relays.json) compiled into the SDK, and each valid list a refresh reads from `https://openwatch.party/relays.json` replaces it in memory. `Relays.at(baseUrl)` is one [self-hosted relay](../../relay/README.md) for every room: its `list` is that relay alone, it fetches nothing, and a bad base (not a lower-case `https://` or `http://` URL with a host, such as `https://relay.example.com`) throws a `PartyError` with code `invalid_message`.

```ts
static public(options?: RelaysOptions): Relays;
static at(baseUrl: string): Relays;
get list(): readonly Relay[];
relayFor(code: string): Relay | null;
refresh(): Promise<boolean>;
```

`RelaysOptions` has one member, `httpGet: (url: string, signal: AbortSignal) => Promise<string>`, by default `fetch` with `redirect: "error"`. A custom `httpGet` must stop when `signal` aborts, follow no redirects, and fail on a non-200 status. `refresh()` resolves `false` when no valid list came back, and the list in use stays; on a `Relays.at` it does nothing and resolves `true`.

## WatchParty

| Option | Type | Default |
|---|---|---|
| `relays` | `Relays` | required |
| `client` | `string`, `<app>/<version>`, e.g. `Example/1.0` | required |
| `name` | `string`, 1–32 characters | required |
| `resolver` | `Resolver<T>` | none (others' media then has no candidates; `match` is `unavailable`) |
| `connect` | `(url: string) => WebSocket` | `new WebSocket(url)` |
| `nowMs` | `() => number` | `performance.now()` |
| `random` | `() => number` in [0, 1) | `Math.random` |

The constructor throws a `PartyError` with code `invalid_message` for a bad `client` or `name`. A custom `connect` must return a socket behaving like a browser `WebSocket` (`onopen`/`onmessage`/`onclose`/`onerror`, `readyState`, `send(string)`, `close(code)`).

```ts
constructor(options: WatchPartyOptions<T>);
get state(): PartyState<T>;
on(type: "state", listener: (state: PartyState<T>) => void): () => void;
on(type: "event", listener: (event: PartyEvent) => void): () => void;
create(): Promise<string>;
join(code: string): Promise<void>;
leave(): void;
close(): Promise<void>;
attachPlayer(player: PlayerAdapter, ref: T): void;
detachPlayer(player?: PlayerAdapter): Promise<void>;
playerChanged(): void;
acceptMismatch(): void;
declineMismatch(): void;
choose(candidate: Candidate<T>): Promise<boolean>;
resolveAgain(): void;
play(): void;
pause(): void;
seek(positionMs: number): void;
setRate(rate: number): void;
setOffset(offsetMs: number): void;
setMedia(media: Media, ref: T, positionMs?: number): void;
clearMedia(): void;
setControl(mode: Control): void;
transferHost(peerId: string): void;
kick(peerId: string): void;
lock(locked: boolean): void;
end(): void;
roomPositionMs(): number | null;
```

`join(code)` accepts a code as typed or pasted. `setMedia`, `clearMedia`, `setControl`, `transferHost`, `kick`, `lock` and `end` are host-only. `seek` takes a position in the app's own file. `setRate` takes 0.5 to 2.0. `setOffset` takes an integer within ±604800000 ms. `leave()` during `create`/`join` cancels the attempt. After `close()` the party can't be used again. An ended party may `create` or `join` again.

`party.state` is a new frozen `PartyState` object on every change, and `on("state", …)` / `on("event", …)` return an unsubscribe function and replay nothing.

| `PartyState<T>` field | Meaning |
|---|---|
| `connection`, `peerId`, `endReason` | session status, own ID, why it ended |
| `room`, `media` | last snapshot, current descriptor |
| `match`, `choice`, `mismatch`, `candidates` | match, file to open, file offered, ranked files |
| `offsetMs`, `player` | local offset, reported player state |
| `me`, `isHost`, `canControl` | own participant, host, may control playback |

Events by `type`: `action` (`kind`, `byPeerId`, `by`, `byMe`, `room`), `participantJoined` and `participantLeft` (`participant`), and `error` (`error: PartyError`). `resolveFailed` carries what the resolver threw, or a `DOMException` named `TimeoutError` after 30 s; `playBlocked` carries what the adapter's `play` threw when the player refused to start, such as a browser blocking autoplay, and the app then asks the user to press play on the player itself; `ended` carries `reason` and `error`. A later 1.x may add types, so ignore unknown ones. `Media` holds `kind`, `title`, `durationMs` and optional `year`, `ids`, `series`, `season`, `episode`, `file` and `ext` (keyed by a reverse-DNS name such as `com.example`).

## Player and resolver

For `<video>`/`<audio>`, `attachVideo(party, video, ref)` wraps the element in an adapter, calls `party.attachPlayer(adapter, ref)` with the `ref` of the file the element has open, and routes the element's own controls to the party, telling the user's presses from the SDK's commands. A rate the SDK set on the element is never sent back to the room. While the user drags the seek bar, it sends at most one `seek` per 250 ms with the latest position, so the position where the drag stops goes out within 250 ms. Call it once per file, once the element has the file open. It returns a cleanup `() => Promise<void>` that removes its listeners and calls `party.detachPlayer(adapter)`, so it never detaches a newer player, and returns that completion: await it before disposing the element (after `close()` it resolves at once). `videoAdapter(video)` is the adapter alone, with no control routing, for pages the app doesn't own, such as a browser extension's. Both throw from `play` when the element refuses to start.

Otherwise implement `PlayerAdapter`: cheap synchronous reads `positionMs`, `durationMs` (or `null`), `paused`, `buffering` and `firstFrameShown`. Add `play()`, `pause()` and `seek(positionMs)`, resolving when each lands; a command must finish or throw, and `play()` throws when the player refuses to start, so the party reports `loading`. Optional: `setRate(rate)` (without it the SDK only seeks) and `seekAccuracyMs`. Call `attachPlayer(player, ref)` once per opened file, and again after the party ends (`ended`); as host, `setMedia` and attaching may come in either order, and a `setMedia` lost on a dropped connection is sent again after the resume while the party is still host. Call `detachPlayer` when the user leaves the player or the app goes to the background, and await it or `close()` before disposing a player. While the user is away, the quick start's `openChoice()` must not load or attach, since attaching brings them back. Give it a flag it checks before it loads and before it attaches. When the user leaves, set the flag and await `detach?.()`; on return, clear it, set `detach = attached = null` and call `openChoice()`. Call `playerChanged()` between ticks.

`Resolver<T>` is `resolve(media, signal)` returning `Candidate<T>[]` best first; a candidate is `{ id, ref, durationMs?, sizeBytes?, oshash?, name?, probe? }`, and `probe(signal)` opens the file to learn its duration. Identity is the resolver's job for every kind: the SDK ranks candidates and drops none. Treat descriptor values as untrusted search terms, never a path, URL, host or query fragment. Helpers: `formatCode`, `sameTitle`, `checkMedia` (first broken send rule or `null`) and `parseMedia` (receive checks).

## Errors

`PartyError` carries `code` (relay codes pass through; the SDK adds `invalid_code`, `unreachable`, `cancelled`) and `re`, the refused message type; `message` is for logs, so show your own text per `code`. `create`/`join` reject, commands throw synchronously, refusals arrive as `error` events, and session loss as `ended`. No answer within 10 s is `unreachable`. `invalid_message` also covers a bad `Relays.at` base, a malformed `joined` and bad `parseMedia` input. `choose` resolves `false` when it can't take the file, including without media. Listeners run synchronously.

## Build and test

From `sdk/typescript` in a checkout:

```sh
npm ci
npm run typecheck
npm run test:unit
npm run test:integration
npm test
npm run build
```

`test:unit` (unit, [vector](../../conformance/vectors/README.md) and embedded-list tests) needs no Go; `test:integration` and `npm test` build `owp-relay` from `relay/` with Go 1.26. See also [`docs/integration.md`](../../docs/integration.md), [`spec/SPEC.md`](../../spec/SPEC.md), [`conformance/peer`](../../conformance/peer/README.md) and the [Dart](../dart/README.md), [Kotlin](../kotlin/README.md) and [Swift](../swift/README.md) SDKs.

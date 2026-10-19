# Swift SDK

`OpenWatchParty` is a Swift SDK that keeps players in different apps in sync in one room, implementing the behaviour in [`spec/SDK.md`](../../spec/SDK.md); read that for the rules this page only names.

## Install

SwiftPM, in `Package.swift`:

```swift
.package(url: "https://github.com/edde746/openwatchparty", from: "1.0.0"),
// target dependency:
.product(name: "OpenWatchParty", package: "openwatchparty"),
```

In Xcode: File → Add Package Dependencies → `https://github.com/edde746/openwatchparty`, product `OpenWatchParty`. SwiftPM takes the version from the git tag.

## Requirements

iOS 16, macOS 13, tvOS 16, watchOS 9, visionOS 1, Linux (Swift 6 toolchain). Swift tools 6.0, Swift 6 language mode, no dependencies beyond Foundation (`FoundationNetworking` on Linux).

## Quick start

```swift
import Foundation
import OpenWatchParty

/// The app's player as the party sees it: read on every tick, commanded by the party.
@MainActor
final class VideoPlayer: PlayerAdapter {
    var positionMs: Int64 = 0
    var durationMs: Int64? = 10_200_000
    var paused = true
    var buffering = false
    var firstFrameShown = true

    func play() async throws { paused = false } // throw when the player refuses to start
    func pause() async throws { paused = true }
    func seek(to positionMs: Int64) async throws { self.positionMs = positionMs }
}

@MainActor
func watchTogether(code: String?) async throws {
    let heat = URL(fileURLWithPath: "/Movies/Heat.mkv")
    let party = try WatchParty<URL>(
        relays: .public(),
        client: "Example/1.0",
        name: "Anna",
        resolver: Resolver { media in
            // The files in the app's library that are this title, best first.
            media.title == "Heat" ? [Candidate(id: "heat", ref: heat, durationMs: 10_200_000)] : []
        }
    )
    // Subscribe before create or join: a stream gets what is published from then on, nothing earlier.
    let states = party.states()
    let events = party.events()
    Task {
        for await event in events {
            if event is PlayBlockedEvent { print("Tap play to start the video") }
        }
    }
    if let code {
        try await party.join(code) // "k7m-q2x" works too
    } else {
        let code = try await party.create()
        print("Room code: \(try formatCode(code))")
        let media = Media(kind: .movie, title: "Heat", durationMs: 10_200_000, year: 1995, ids: ["imdb": "tt0113277"])
        try party.setMedia(media, ref: heat)
    }
    let player = VideoPlayer()
    var opened: URL?
    for await state in states {
        let file = state.choice?.ref
        if let file, file != opened {
            opened = file // open `file` in the player, then:
            party.attachPlayer(player, ref: file)
        }
        if state.connection == .ended { break }
    }
    await party.close().value
}
```

The party is `@MainActor`: it calls your adapter and resolver on the main actor. Call `states()`/`events()` before `create` or `join`: each call returns its own `AsyncStream` that gets every change or event in order, nothing is replayed, and both finish on `close()`.

## Relays

`Relays.public()` starts from the copy of [`relays.json`](../../relays.json) compiled into the SDK; a [self-hosted relay](../../relay/README.md) is `try Relays.at("https://relay.example.com")`.

| Member | Signature |
|---|---|
| public list | `Relays.public(httpGet:clock:)` |
| self-hosted | `Relays.at(_:) throws` |
| list | `list` |
| route | `relayFor(_:)` |
| refresh | `refresh() async -> Bool` |

The GET of the public list defaults to `URLSession` and may be replaced with `httpGet:`. `Relays.at` throws `invalid_message` on a bad base URL; it is the one relay for every room, and its `refresh()` does nothing and returns true. `refresh()` returns false when the source gave no valid list, leaving the list in use.

## WatchParty

`Ref` is the type of the app's `ref` in candidates; it must be `Equatable & Sendable`, and refs compare with `==`. The throwing initializer is `WatchParty<Ref>(relays:client:name:resolver:connect:clock:random:)`, with `resolver` defaulting to `nil`. Without a resolver the party never searches: for others' media `match` is `unavailable` at once.

| Group | Members |
|---|---|
| state | `private(set) var state: PartyState<Ref>`, `states() -> AsyncStream<PartyState<Ref>>`, `events() -> AsyncStream<any PartyEvent>` |
| session | `create() async throws -> String`, `join(_:) async throws`, `leave()`, `close() -> Task<Void, Never>` |
| player | `attachPlayer(_:ref:)`, `detachPlayer(_:) -> Task<Void, Never>`, `playerChanged()` |
| matching | `choose(_:) async -> Bool`, `acceptMismatch()`, `declineMismatch()`, `resolveAgain()` |
| playback | `play() throws`, `pause() throws`, `seek(to:) throws`, `setRate(_:) throws`, `setOffset(_:) throws` |
| host | `setMedia(_:ref:positionMs:) throws`, `clearMedia() throws`, `setControl(_:) throws`, `transferHost(_:) throws`, `kick(_:) throws`, `lock(_:) throws`, `end() throws` |
| position | `roomPositionMs() -> Int64?` |

Await the completions `close()` and `detachPlayer()` return with `.value` (`await party.close().value`) before disposing of the player. Cancelling the task that awaits `create`/`join` cancels the attempt and throws `CancellationError`.

| `PartyState` field | Meaning |
|---|---|
| `connection`, `peerId`, `endReason` | session status, own peer ID, why it ended |
| `room`, `media` | last snapshot, current descriptor |
| `match`, `choice`, `mismatch`, `candidates` | match, file to open, file offered, ranked files |
| `offsetMs`, `player` | local offset, reported player state |
| `me`, `isHost`, `canControl` | own participant, host, may control playback |

Events conform to the protocol `PartyEvent`: `ActionEvent`, `ParticipantJoinedEvent`, `ParticipantLeftEvent`, `ErrorEvent`, `ResolveFailedEvent`, `PlayBlockedEvent` and `EndedEvent`, so a `switch` over an event needs a `default` branch. `Media`, `Series` and `MediaFile` are value types, and `Media.ext` is `[String: JSONValue]?`.

## Player and resolver

Implement the `@MainActor` protocol `PlayerAdapter` in a class: reads `positionMs`, `durationMs` (nil when unknown), `paused`, `buffering` and `firstFrameShown`, and `play()`, `pause()` and `seek(to:)`, each `async throws`. Defaults in a protocol extension give `canSetRate` false, `seekAccuracyMs` 0 and a no-op `setRate(_:)`; without rate control the SDK follows by seeking only. `play` throws when the player refuses to start, and while it then stays paused the party emits `PlayBlockedEvent`: ask the user to press play on the player itself. Call `attachPlayer(_:ref:)` once per opened file.

`Resolver<Ref>` is a struct built from a closure, `@MainActor (Media) async throws -> [Candidate<Ref>]`, best first; a throw counts as no candidates. `Candidate<Ref>` holds `id`, `ref`, `durationMs`, `sizeBytes`, `oshash`, `name` and an optional `probe` that learns the duration; the SDK cancels the probe's task when it no longer needs the answer, and a failed probe counts as no duration. Helpers: `formatCode(_:) throws` gives `K7M-Q2X`, `sameTitle(_:_:)` compares descriptors, `checkMedia(_:)` returns the first broken send rule or nil, and `parseMedia(_:) throws` applies the receive checks.

## Errors

`PartyError` carries `code` (a `PartyError.Code` value such as `.roomNotFound`, raw value `"room_not_found"`), a log-only `message`, `re` and an optional `underlying` error; show your own text per `code` with a generic fallback. Named codes: `invalidCode`, `unreachable`, `cancelled`, `notInRoom`, `alreadyInRoom`, `forbidden`, `invalidMessage`, `roomNotFound`, `roomFull`, `roomLocked`, `rateLimited`, `relayFull`, `resumeFailed`, `clientBlocked`; other codes arrive as raw `Code` values. Calling `create` or `join` after `close()` throws `PartyClosedError`, a programming error, not a `PartyError`. `ResolveFailedEvent` carries `TimeoutError` when the resolver took longer than 30 s.

## Build and test

At the repository root:

```sh
swift build
swift test
swift test --filter VectorTests
```

`swift test` includes `IntegrationTests`, which runs whole parties against a relay: `OWP_RELAY_BIN` names the binary, otherwise the suite builds `relay/` with Go, so Go must be installed. On iOS and tvOS simulators, `xcodebuild test -scheme OpenWatchParty -destination 'platform=iOS Simulator,name=iPhone 17'` (and `platform=tvOS Simulator,name=Apple TV 4K (3rd generation)`) runs the suites that need no processes or local server; `IntegrationTests` and `DefaultHTTPGetTests` build on macOS and Linux only. watchOS and visionOS build with `xcodebuild build -scheme OpenWatchParty -destination 'generic/platform=watchOS Simulator'` (and `visionOS Simulator`). CI is `.github/workflows/sdk-swift.yml` (macOS and a Linux job in `swift:6.4`).

## Links

[`spec/SPEC.md`](../../spec/SPEC.md) is the wire protocol; [`docs/integration.md`](../../docs/integration.md) is the end-to-end guide; the [TypeScript](../typescript/README.md), [Dart](../dart/README.md) and [Kotlin](../kotlin/README.md) SDKs cover other platforms.

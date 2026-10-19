# Kotlin SDK

`party.openwatch:openwatchparty` is a Kotlin Multiplatform SDK that keeps players in different apps in sync in one room, implementing the behaviour in [`spec/SDK.md`](../../spec/SDK.md); read that for the rules this page only names.

## Install

From `mavenCentral()`, in a JVM or Android module:

```kotlin
dependencies {
    implementation("party.openwatch:openwatchparty:1.0.0")
    implementation("io.ktor:ktor-client-okhttp:3.6.0")
}
```

In a Kotlin Multiplatform module, depend on the SDK from `commonMain` and add a Ktor engine per target:

```kotlin
kotlin {
    sourceSets {
        commonMain.dependencies { implementation("party.openwatch:openwatchparty:1.0.0") }
        jvmMain.dependencies { implementation("io.ktor:ktor-client-okhttp:3.6.0") }
        appleMain.dependencies { implementation("io.ktor:ktor-client-darwin:3.6.0") }
    }
}
```

No Ktor engine is bundled, and the engine must support WebSockets (OkHttp on JVM and Android, Darwin on Apple; not Apache5, Jetty or the Android engine). With no engine argument in common code, Ktor picks the engine from each target's dependencies.

## Requirements

Targets are `jvm`, Android, `iosArm64`, `iosSimulatorArm64`, `macosArm64`, `tvosArm64` and `tvosSimulatorArm64`, all from `commonMain` code. Apple targets produce klibs, so a Swift-only app needs its own Kotlin Multiplatform module depending on the SDK. JVM and Android bytecode targets 17, and Android uses `compileSdk = 37` with `minSdk = 26`. The SDK is built with Kotlin 2.4.10, AGP 9.4.1, Ktor 3.6.0, kotlinx-coroutines 1.11.0 and kotlinx-serialization 1.11.0; an Android app on AGP 9 whose built-in Kotlin is older than 2.3 must pin the Kotlin Gradle plugin:

```kotlin
buildscript {
    repositories { mavenCentral() }
    dependencies { classpath("org.jetbrains.kotlin:kotlin-gradle-plugin:2.4.10") }
}
```

## Quick start

```kotlin
import io.ktor.client.HttpClient
import io.ktor.client.engine.okhttp.OkHttp
import io.ktor.client.plugins.websocket.WebSockets
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonObject
import party.openwatch.Candidate
import party.openwatch.Media
import party.openwatch.MediaKind
import party.openwatch.PartyEvent
import party.openwatch.PartyException
import party.openwatch.PlayerAdapter
import party.openwatch.Relays
import party.openwatch.Resolver
import party.openwatch.WatchParty
import party.openwatch.formatCode

// One Ktor client with WebSockets and one Relays for the whole app.
val http = HttpClient(OkHttp) { install(WebSockets) }
val relays = Relays.public(http)

// The app's player, read and commanded by the SDK.
class ExamplePlayer : PlayerAdapter {
    override var positionMs = 0L
        private set
    override val durationMs: Long? = null
    override var paused = true
        private set
    override val buffering = false
    override val firstFrameShown = true
    var openRef: String? = null
        private set

    suspend fun open(ref: String) { openRef = ref /* open the file in the app's player; return once it's open */ }

    override suspend fun play() { paused = false }
    override suspend fun pause() { paused = true }
    override suspend fun seek(positionMs: Long) { this.positionMs = positionMs }
}

// Stand-in for the app's own library.
class ExampleFile(val id: String, val path: String, val durationMs: Long)
fun searchLibrary(title: String): List<ExampleFile> = emptyList()

// The user's own files for the room's media, best first; ref is what the app opens (here a path).
val resolver = Resolver<String> { media ->
    searchLibrary(media.title).map { Candidate(id = it.id, ref = it.path, durationMs = it.durationMs) }
}

fun startParty(scope: CoroutineScope, player: ExamplePlayer, code: String?): WatchParty<String> {
    val party = WatchParty(relays, client = "Example/1.0", name = "Anna", resolver = resolver, scope = scope)
    // Open the file the SDK chose, one at a time, and attach the player with the ref it opened.
    var attached: String? = null // the ref the player is attached with
    val opening = Mutex()
    suspend fun openChoice() = opening.withLock {
        while (true) {
            val ref = party.state.value.choice?.ref ?: break
            if (ref == attached) break // attached: the player follows by itself
            if (player.openRef != ref) player.open(ref)
            if (party.state.value.choice?.ref != ref) continue // a newer choice came meanwhile: open that one
            party.attachPlayer(player, ref)
            attached = ref
        }
    }
    scope.launch { party.state.collect { openChoice() } }
    scope.launch {
        party.events.collect { event ->
            when (event) {
                is PartyEvent.Action -> if (!event.byMe) println("${event.by?.name}: ${event.kind}")
                is PartyEvent.Ended -> {
                    println("party ended: ${event.reason}")
                    attached = null // the end released the player
                }
                else -> {}
            }
        }
    }
    scope.launch {
        try {
            if (code == null) println("room code: ${formatCode(party.create())}") else party.join(code)
        } catch (e: PartyException) {
            println("could not start the party: ${e.code}")
        }
    }
    return party
}

// As host: share the title you play; ref reopens your own file.
fun shareMovie(party: WatchParty<String>) {
    party.setMedia(
        Media(
            kind = MediaKind.MOVIE,
            title = "Example Movie",
            durationMs = 5_400_000,
            ids = mapOf("imdb" to "tt0000001"),
            ext = buildJsonObject { putJsonObject("com.example") { put("itemId", "42") } },
        ),
        ref = "/movies/example-movie.mkv",
    )
}

// The user's controls go through the party, never straight to the player.
fun onPlayPause(party: WatchParty<String>, player: ExamplePlayer) {
    val state = party.state.value
    if (state.media == null || !state.canControl) return
    if (player.paused) party.play() else party.pause()
}

fun onSeek(party: WatchParty<String>, positionMs: Long) = party.seek(positionMs)

fun onLeave(party: WatchParty<String>) = party.close() // join the returned Job before disposing the player
```

Subscribe to `events` before `create`/`join`: it has no replay, though it drops nothing while subscribed.

## Relays

The app's `HttpClient` given here is the SDK's only transport, it must have the `WebSockets` plugin installed, and the SDK never closes it. `Relays.public(http)` starts from the copy of [`relays.json`](../../relays.json) compiled into the SDK, and a [self-hosted relay](../../relay/README.md) is `Relays.at(http, "https://relay.example.com")`, that one relay for every room, never fetched.

| Member | Signature |
|---|---|
| public list | `Relays.public(http: HttpClient): Relays` |
| self-hosted | `Relays.at(http: HttpClient, baseUrl: String): Relays` |
| list | `val list: List<Relay>` |
| route | `fun relayFor(code: String): Relay?` |
| refresh | `suspend fun refresh(): Boolean` |

`Relay` is `(id, name, url, prefixes)`, and `refresh()` returns false when the public source gave no valid list, leaving the list in use. A `Relays.at` list is one `custom` relay owning every code, whose `refresh()` does nothing and returns true. `Relays.at` throws `PartyException` with code `invalid_message` when `baseUrl` isn't `https://` or `http://` followed by a host, or gives a relay URL that [`spec/SDK.md`](../../spec/SDK.md) §3.2 refuses.

## WatchParty

`T` is the type of the app's `ref` in candidates. The constructor is `WatchParty(relays: Relays, client: String, name: String, resolver: Resolver<T>? = null, scope: CoroutineScope, clock: MonotonicClock = MonotonicClock.System, random: Random = Random.Default)`. It throws `PartyException` with code `invalid_message` for a bad `client` (`<name>/<version>`) or `name` (1–32 characters). Without a resolver the party never searches: for others' media `match` is `unavailable` at once.

| Group | Members |
|---|---|
| session | `suspend fun create(): String`, `suspend fun join(code: String)`, `fun leave()`, `fun close(): Job` |
| player | `fun attachPlayer(player: PlayerAdapter, ref: T)`, `fun detachPlayer(player: PlayerAdapter? = null): Job`, `fun playerChanged()` |
| matching | `fun acceptMismatch()`, `fun declineMismatch()`, `suspend fun choose(candidate: Candidate<T>): Boolean`, `fun resolveAgain()` |
| playback | `fun play()`, `fun pause()`, `fun seek(positionMs: Long)`, `fun setRate(rate: Double)`, `fun setOffset(offsetMs: Long)` |
| host | `fun setMedia(media: Media, ref: T, positionMs: Long = 0)`, `fun clearMedia()`, `fun setControl(mode: Control)`, `fun transferHost(peerId: String)`, `fun kick(peerId: String)`, `fun lock(locked: Boolean)`, `fun end()` |
| position | `fun roomPositionMs(): Long?` |

`create()` creates a room on the first relay to answer and returns its code. `join` accepts a typed or pasted code such as `k7m-q2x`. The host row is host-only. `seek` takes a position in the user's own file. `setRate` takes 0.5 to 2.0. `setOffset` takes at most 604800000 ms either way. The SDK copies what the app passes in and publishes read-only collections. `leave()` during `create`/`join` cancels them, and after `close()`, `create`/`join` throw `IllegalStateException`.

| `PartyState<T>` field | Meaning |
|---|---|
| `connection`, `peerId`, `endReason` | session status, own peer ID, why it ended |
| `room`, `media` | last snapshot, current descriptor |
| `match`, `choice`, `mismatch`, `candidates` | match, file to open, file offered, ranked files |
| `offsetMs`, `player` | local offset, reported player state |
| `me`, `isHost`, `canControl` | own participant, host, may control playback |

`state` is a `StateFlow<PartyState<T>>` and `events` a `SharedFlow<PartyEvent>` with `Action`, `ParticipantJoined`, `ParticipantLeft`, `Error`, `ResolveFailed`, `PlayBlocked` and `Ended`; `PartyEvent` is non-sealed and may grow in 1.x, so use an `else` branch. `Media` is `(kind, title, durationMs, year, ids, series, season, episode, file, ext)` with `MediaKind` `MOVIE`/`EPISODE`/`VIDEO`, an episode needing `series` and `ext` keyed by a reverse-DNS name such as `com.example`. Enum values are upper-case (`Phase.PLAYING`, `EndReason.LOST`).

## Player and resolver

Implement `PlayerAdapter`: reads `positionMs`, `durationMs` (null when unknown), `paused`, `buffering`, `firstFrameShown`, `canSetRate` (default false) and `seekAccuracyMs` (default 0: how far a seek may land). Add `suspend play()` and `pause()`, returning when done, `seek(positionMs)`, returning once the seek has landed, and `setRate(rate)` (default no-op). `play` throws when the player refuses to start; while it then stays paused, the party reports `loading` and emits `PlayBlocked`, carrying the thrown error, and the app asks the user to press play on the player itself. Without rate control the SDK follows by seeking only. A command must finish or throw. Call `attachPlayer(player, ref)` once per opened file, and again after the party ends (`Ended`); as host, with the `setMedia` ref, in either order. Call `detachPlayer` when the user leaves the player or the app goes to the background, and join it or `close()` before disposing the player. While the user is away, the quick start's `openChoice()` must not open or attach, since `attachPlayer` brings them back. Give it a flag it checks before it opens and before it attaches. When the user leaves, set the flag and join `party.detachPlayer()`; on return, clear it, set `attached = null` and call `openChoice()`. The `Mutex` keeps that call from running alongside the `state` collector's. Call `playerChanged()` between ticks.

`Resolver<T>` is a fun interface, `suspend fun resolve(media: Media): List<Candidate<T>>`, best first and cancelled when the room's media changes; a throw, its own cancellation such as its own timeout included, or over 30 s counts as none and emits `ResolveFailed`. `Candidate<T>` is `(id, ref, durationMs?, sizeBytes?, oshash?, name?, probe?)`, where `probe` opens the file to learn its duration; a probe that throws, its own cancellation included, counts as returning nothing. Identity is the resolver's job: the SDK ranks and drops none. Descriptor strings from others are untrusted input. Helpers: `formatCode(code)` gives `K7M-Q2X`, `sameTitle(a, b)` compares descriptors, `checkMedia(media)` returns the first broken send rule or null, and `parseMedia(json)` decodes with the receive checks.

## Threading

The party runs in a child `SupervisorJob` of the given `scope` on its dispatcher. It calls the adapter and resolver there, so use a single-threaded scope (on Android, a Main scope) and call the party from that thread. A scope with no dispatcher gets `Dispatchers.Default`. Non-suspend methods run on the caller's thread. Cancelling the caller of `create`/`join` cancels the attempt, and the caller gets its own `CancellationException`. `close()` cancels only the party's own child scope. Timers are coroutines on that scope, so a test dispatcher drives them.

## Errors

`PartyException` carries a `code` (with constants on its companion), a log-only `message` and `re`, the refused message type; show your own text per `code` with a generic fallback. Refusals of sent commands arrive as `PartyEvent.Error`, not exceptions.

| Error code | Raised by |
|---|---|
| `invalid_code` | `join`, `formatCode` with a non-code |
| `unreachable` | `create`/`join` when no relay answers within 10 s; `Ended.error` for `LOST` after the resume window |
| `cancelled` | `create`/`join` after `leave()`/`close()` |
| `already_in_room` | `create`/`join` while connecting or in a room |
| `not_in_room` | commands outside a room |
| `forbidden` | host-only commands as non-host; playback commands without control |
| `invalid_message` | invalid constructor names, a `Relays.at` base that isn't an http(s) URL with a host, arguments out of range, a malformed `joined`, a bad descriptor in `parseMedia`, no media, unknown peer |
| relay codes (`room_not_found`, `relay_full`, `resume_failed`, `client_blocked`, …) | passed through from the relay; `room_not_found` is also raised by `join` when no relay owns the code |

## Build and test

From `sdk/kotlin` in a checkout (Gradle 9.8.0 needs a JVM from 17 to 27; CI uses Temurin 21):

```
./gradlew --console=plain assemble check
./gradlew --console=plain -Popenwatchparty.android=false compileKotlinIosArm64 compileKotlinTvosArm64 macosArm64Test iosSimulatorArm64Test tvosSimulatorArm64Test
```

`assemble check` builds JVM and Android, plus the Apple targets and their tests on a Mac. The Android target is on when an Android SDK is known, and `-Popenwatchparty.android=false` skips it. The second line is the Apple CI job.

| Suite | Source set | Runs on | Needs |
|---|---|---|---|
| Unit (`PartyTest`, `RelayListTest`, `VectorTest`, `WireTest`, `TimersTest`) | `src/commonTest` | every test task: `jvmTest`, `testAndroidHostTest`, `macosArm64Test`, `iosSimulatorArm64Test`, `tvosSimulatorArm64Test` | `generateTestData` (automatic) |
| Integration (`IntegrationTest`) | `src/jvmTest` | `jvmTest` only | Go; relay on 127.0.0.1 ports 18300–18349 |

## Links

[`spec/SPEC.md`](../../spec/SPEC.md) is the wire protocol; [`docs/integration.md`](../../docs/integration.md) is the end-to-end guide; the [TypeScript](../typescript/README.md), [Dart](../dart/README.md) and [Swift](../swift/README.md) SDKs cover other platforms; Ktor engine support is at <https://ktor.io/docs/client-engines.html>.

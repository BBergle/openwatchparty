# Integration guide

This guide takes a player app to a working Open Watch Party (OWP) room with the TypeScript, Dart, Kotlin or Swift SDK. The app writes a player adapter, a resolver to match others' media, and the UI. [The SDK spec](../spec/SDK.md) holds the rules.

Install from the SDK README for your language: [TypeScript](../sdk/typescript/README.md), [Dart](../sdk/dart/README.md), [Kotlin](../sdk/kotlin/README.md) or [Swift](../sdk/swift/README.md). A complete small app is in [the web example](../examples/web/README.md).

## How a room works

The relay runs the room: its phase (`idle`, `paused`, `waiting`, `playing`), position, rate, host and settings. Each client plays its own copy, and room positions are in the timeline of the host's reference file, the one whose duration the shared descriptor carries.

## 1. Create the party

Construct the party with a `Relays` object, a true `client` name and the user's display name. Add your resolver (step 3) to match other participants' media. In Kotlin the party calls your adapter and resolver on the scope you pass. In Swift the party is `@MainActor`: it calls your adapter and resolver on the main actor, and `states()`/`events()` each return a new `AsyncStream`; the constructor and `formatCode` throw (`try`). See [relay list](../spec/SDK.md#3-relay-list) and [per-language idioms](../spec/SDK.md#168-per-language-idioms).

TypeScript:

```ts
import { Relays, WatchParty, formatCode } from "openwatchparty";

const relays = Relays.public(); // one per app, passed to every party

const party = new WatchParty<LibraryItem>({
  relays,
  client: "Example/1.0", // your app's published name and version
  name: "Anna", // the user's display name
  resolver: libraryResolver(library), // step 3; `library` is your app's library
});
party.on("state", render); // subscribe before create or join: nothing is replayed
party.on("event", onEvent);

const code = await party.create(); // or: await party.join(typedCode)
showCode(formatCode(code)); // "K7M-Q2X"
```

Dart:

```dart
final relays = Relays.public(); // one per app, passed to every party

final party = WatchParty<LibraryItem>(
  relays: relays,
  client: 'Example/1.0', // your app's published name and version
  name: 'Anna', // the user's display name
  resolver: LibraryResolver(library),
);
party.states.listen(render); // subscribe before create or join: nothing is replayed
party.events.listen(onEvent);

final code = await party.create(); // or: await party.join(typedCode)
showCode(formatCode(code)); // "K7M-Q2X"
```

Kotlin:

```kotlin
val http = HttpClient(OkHttp) { install(WebSockets) } // your Ktor client and engine
val relays = Relays.public(http) // one per app, passed to every party

val party = WatchParty(
    relays = relays,
    client = "Example/1.0", // your app's published name and version
    name = "Anna", // the user's display name
    resolver = LibraryResolver(library),
    scope = mainScope, // single-threaded; on Android a Main scope
)
mainScope.launch { party.state.collect { render(it) } }
mainScope.launch { party.events.collect { onEvent(it) } } // events are not replayed

val code = party.create() // or: party.join(typedCode)
showCode(formatCode(code)) // "K7M-Q2X"
```

Swift:

```swift
import OpenWatchParty

let relays = Relays.public() // one per app, passed to every party

let party = try WatchParty<LibraryItem>(
    relays: relays,
    client: "Example/1.0", // your app's published name and version
    name: "Anna", // the user's display name
    resolver: libraryResolver(library) // step 3; `library` is your app's library
)
// Subscribe before create or join: nothing is replayed. Each call is a stream of its own that gets every change.
let states = party.states()
let events = party.events()
Task { for await state in states { render(state) } }
Task { for await event in events { onEvent(event) } }

let code = try await party.create() // or: try await party.join(typedCode)
try showCode(formatCode(code)) // "K7M-Q2X"
```

`join(code)` takes the code as typed and normalizes it (separators removed, upper-cased) to route by its first symbol. Anything that isn't then 6 symbols from `23456789ABCDEFGHJKMNPQRSTUVWXYZ` is `invalid_code` before any network use. The relay applies the same normalization to `join` and `resume` codes it receives ([room codes](../spec/SPEC.md#51-room-codes)), which matters only to apps that speak the wire directly. A bad `client` or `name` throws `invalid_message` from the constructor.

## 2. Write the player adapter

Write an adapter over your player. Reads must be cheap and synchronous. Each command must finish or throw, and `play` throws when the player refuses to start. While it then stays paused, the SDK reports `loading` and emits `playBlocked` (TypeScript), `PlayBlockedEvent` (Dart and Swift) or `PartyEvent.PlayBlocked` (Kotlin); see step 9. A `play` that swallows the refusal is reported `ready` while paused. Optional `seekAccuracyMs` (default 0) says how far a seek may land: the keyframe distance for a keyframe-only player, a very large value for one that cannot seek. See [player state](../spec/SDK.md#102-player-state).

TypeScript:

```ts
import type { PlayerAdapter } from "openwatchparty";

const adapter: PlayerAdapter = {
  get positionMs() { return player.positionMs; }, // in your own file
  get durationMs() { return player.durationMs; }, // null when unknown
  get paused() { return player.paused; },
  get buffering() { return player.stalled; }, // stalled while trying to play
  get firstFrameShown() { return player.frameShown; },
  play: () => player.play(),
  pause: () => player.pause(),
  seek: (positionMs) => player.seek(positionMs), // resolves once the seek has landed
  setRate: (rate) => player.setSpeed(rate), // optional: without it the SDK only seeks
};
player.onChange(() => party.playerChanged()); // optional: changes reach the room sooner
```

Dart:

```dart
final class ExampleAdapter extends PlayerAdapter {
  ExampleAdapter(this.player);

  final ExamplePlayer player;

  @override
  int get positionMs => player.positionMs; // in your own file
  @override
  int? get durationMs => player.durationMs; // null when unknown
  @override
  bool get paused => player.paused;
  @override
  bool get buffering => player.stalled; // stalled while trying to play
  @override
  bool get firstFrameShown => player.frameShown;
  @override
  bool get canSetRate => true; // optional, with setRate: without it the SDK only seeks

  @override
  Future<void> play() => player.play();
  @override
  Future<void> pause() => player.pause();
  @override
  Future<void> seek(int positionMs) => player.seek(positionMs); // completes once the seek has landed
  @override
  Future<void> setRate(double rate) => player.setSpeed(rate);
}
```

Kotlin:

```kotlin
class ExampleAdapter(private val player: ExamplePlayer) : PlayerAdapter {
    override val positionMs: Long get() = player.positionMs // in your own file
    override val durationMs: Long? get() = player.durationMs // null when unknown
    override val paused: Boolean get() = player.paused
    override val buffering: Boolean get() = player.stalled // stalled while trying to play
    override val firstFrameShown: Boolean get() = player.frameShown
    override val canSetRate: Boolean get() = true // optional, with setRate: without it the SDK only seeks

    override suspend fun play() = player.play()
    override suspend fun pause() = player.pause()
    override suspend fun seek(positionMs: Long) = player.seek(positionMs) // returns once the seek has landed
    override suspend fun setRate(rate: Double) = player.setSpeed(rate)
}
```

Swift:

```swift
@MainActor
final class ExampleAdapter: PlayerAdapter {
    private let player: ExamplePlayer

    init(_ player: ExamplePlayer) { self.player = player }

    var positionMs: Int64 { player.positionMs } // in your own file
    var durationMs: Int64? { player.durationMs } // nil when unknown
    var paused: Bool { player.paused }
    var buffering: Bool { player.stalled } // stalled while trying to play
    var firstFrameShown: Bool { player.frameShown }
    var canSetRate: Bool { true } // optional, with setRate: without it the SDK only seeks

    func play() async throws { try await player.play() } // throws when the player refuses to start
    func pause() async throws { try await player.pause() }
    func seek(to positionMs: Int64) async throws { try await player.seek(positionMs) } // returns once the seek has landed
    func setRate(_ rate: Double) async throws { try await player.setSpeed(rate) }
}
```

On the web, `attachVideo(party, video, ref)` wraps an `HTMLMediaElement`. It routes the element's controls to the party. `videoAdapter(video)` makes the adapter alone, for pages the app does not own.

```ts
import { attachVideo } from "openwatchparty";

const video = document.querySelector("video")!;
const detach = attachVideo(party, video, party.state.choice!.ref); // once the element has the chosen file open (step 4)
// later: await detach(); // before disposing of the element
```

For a player controlled over IPC, reads return values cached from the player's property-change events, never waiting on the socket. Commands await the player's reply; a reply can precede its property event, so set the cached `paused` after it. A seek's reply also precedes the landing, so `seek` waits for the report that it landed, under a timeout. A refused command rejects, so `play` throws as above. Call `party.playerChanged()` on changes other than position.

In a browser extension on a page the app doesn't own, put `videoAdapter(video)` on the main `<video>` (the largest on screen is a workable rule). On navigation the page may replace the element: release the old adapter with `detachPlayer(adapter)` and attach a new one under the same ref — a string key for the page's title, so refs compare equal across reads. Release it while the same element plays an ad, by the site's markers, or the SDK plays whatever the element shows. With no resolver the match stays `unavailable` until the user opens the title and the app calls `choose` with the element's duration; attach only while the page's key is the chosen ref. Call `playerChanged()` from capturing media-event listeners on `document`, since `videoAdapter` doesn't.

## 3. Write the resolver

The resolver runs once per new media and returns the library files that are this title; it may also return near misses (no duration, no probe) for the choose list, which are never chosen automatically. The SDK cancels it when the media changes or the party ends. A throw, or a call longer than 30 seconds, counts as no candidates and emits `ResolveFailed`. When your app finds more files, call `resolveAgain()`: without a choice it searches again; with one it only replaces `state.candidates`.

Identity is the resolver's job; the SDK only ranks candidates and compares durations. `sameTitle(a, b)` needs a shared catalogue ID for a movie, and for an episode a shared own ID, or a shared series ID with equal season and episode — unless some namespace holds a different own ID on each side, which rules out the number match; an episode with no own ID can still match on numbers. A `video` needs the same file hash, or size and name. It never reads titles, so a movie or episode without IDs matches nothing through it alone. See [same title](../spec/SDK.md#93-same-title).

The samples resolve in two steps over stand-ins for your library (`LibraryItem`, `Show`, `Library`). Step 1 returns files sharing a catalogue ID with the media or its series, checked with `sameTitle`, whatever their titles. The title search always runs (it collects the near misses), but only without an ID match does step 2 use it — an episode searched by its show's title, never its own — taking movies with the same folded title and a year within one, and episodes with the same show, season and episode, minus any item with a conflicting ID. The filter runs only then because ranking prefers durations over resolver order: next to ID matches, a title-only copy could match first. A title filed under a different name, without a shared ID, is beyond both steps; the app's own search feeds `choose`. The samples also define `describe` (step 5's descriptor), `playable` (a matchable candidate) and `chooseList` (step 6).

TypeScript:

```ts
import { sameTitle, type Candidate, type Media, type Resolver, type WatchParty } from "openwatchparty";

// Stand-ins for the app's own library.
export interface Show {
  title: string;
  year?: number;
  ids: Record<string, string>; // the catalogue IDs the library knows; often none
}

export interface LibraryItem {
  id: string;
  kind: "movie" | "episode" | "video";
  title: string; // the movie's, episode's or video's title; an episode's may be empty
  year?: number;
  ids: Record<string, string>; // for an episode, its own IDs
  show?: Show; // episodes only
  season?: number;
  episode?: number;
  durationMs?: number; // when the library knows it
  sizeBytes?: number;
  fileName?: string;
}

export interface Library {
  /** Items holding any of these catalogue IDs, as their own or their show's, whatever their titles. */
  findByIds(ids: Readonly<Record<string, string>>, signal: AbortSignal): Promise<LibraryItem[]>;
  /** The library's own text search over movie, show and video titles. */
  search(text: string, signal: AbortSignal): Promise<LibraryItem[]>;
  /** Opens the file, reads its duration and closes it; null when it doesn't open. */
  probeDuration(item: LibraryItem, signal: AbortSignal): Promise<number | null>;
}

const NEAR_MISSES = 10;

/** The descriptor this app sends for `item` as host. `title` is required, so an untitled episode gets "Episode N". */
export function describe(item: LibraryItem, durationMs: number): Media {
  const title =
    item.title.trim() !== "" ? item.title : item.kind === "episode" && item.episode !== undefined ? `Episode ${item.episode}` : (item.fileName ?? "Untitled");
  return {
    kind: item.kind,
    title,
    year: item.year,
    ids: item.ids,
    series: item.show && { title: item.show.title, year: item.show.year, ids: item.show.ids },
    season: item.season,
    episode: item.episode,
    durationMs,
    file: { sizeBytes: item.sizeBytes, name: item.fileName },
  };
}

/** Lower case, with every run of characters other than letters and digits as one space. */
function fold(s: string): string {
  return s.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, " ").trim();
}

function closeYears(a: number | undefined, b: number | undefined): boolean {
  return a === undefined || b === undefined || Math.abs(a - b) <= 1;
}

/** Some namespace holds a different ID on each side: two different titles. */
function conflicts(a: Readonly<Record<string, string>> | undefined, b: Readonly<Record<string, string>>): boolean {
  return Object.entries(a ?? {}).some(([ns, id]) => b[ns] !== undefined && b[ns] !== id);
}

/** Step 2: the same title and year, or the same show, season and episode, with no ID saying otherwise. */
function sameName(media: Media, item: LibraryItem): boolean {
  if (item.kind !== media.kind) return false;
  switch (media.kind) {
    case "movie":
      return fold(item.title) === fold(media.title) && closeYears(media.year, item.year) && !conflicts(media.ids, item.ids);
    case "episode":
      return (
        item.show !== undefined &&
        media.series !== undefined &&
        fold(item.show.title) === fold(media.series.title) &&
        closeYears(media.series.year, item.show.year) &&
        media.season !== undefined &&
        media.season === item.season &&
        media.episode !== undefined &&
        media.episode === item.episode &&
        !conflicts(media.ids, item.ids)
      );
    case "video":
      return (
        fold(item.title) === fold(media.title) ||
        (media.file?.name !== undefined && item.fileName?.toLowerCase() === media.file.name.toLowerCase())
      );
  }
}

/** A candidate the walk may match: its duration, or a probe that learns it. */
export function playable(library: Library, item: LibraryItem): Candidate<LibraryItem> {
  return {
    id: item.id,
    ref: item,
    durationMs: item.durationMs,
    probe: (signal) => library.probeDuration(item, signal),
    sizeBytes: item.sizeBytes,
    name: item.fileName,
  };
}

/** The user's list of files to choose from: the resolver's candidates, each with its difference once known. */
export function chooseList(party: WatchParty<LibraryItem>, library: Library, show: (label: string, onPick: () => void) => void): void {
  for (const { candidate, deltaMs } of party.state.candidates) {
    const item = candidate.ref;
    const name =
      item.title.trim() !== ""
        ? item.season !== undefined && item.episode !== undefined
          ? `${item.title} S${item.season}E${item.episode}`
          : item.title
        : (item.fileName ?? "Untitled");
    const label = deltaMs === null ? name : `${name} (${Math.round(deltaMs / 1000)} s)`;
    // choose takes a playable candidate, rebuilt from the ref: near misses carry no duration and no probe.
    show(label, () => void party.choose(playable(library, item)));
  }
}

export function libraryResolver(library: Library): Resolver<LibraryItem> {
  return {
    async resolve(media, signal) {
      const out = new Map<string, Candidate<LibraryItem>>(); // by item ID, first find wins, in order
      // Step 1: files sharing a catalogue ID with the media, whatever their titles. sameTitle reads no durationMs.
      const byId = [...(await library.findByIds(media.ids ?? {}, signal)), ...(await library.findByIds(media.series?.ids ?? {}, signal))];
      for (const item of byId) {
        if (!out.has(item.id) && sameTitle(media, describe(item, media.durationMs))) out.set(item.id, playable(library, item));
      }
      // Step 2, only without an ID match: an episode is searched by its show's title, never its own. Durations decide.
      const text = media.kind === "episode" ? (media.series?.title ?? media.title) : media.title;
      const found = await library.search(text, signal); // a search term only, never a path or a query fragment
      const byName = out.size === 0 ? found.filter((item) => sameName(media, item)) : [];
      for (const item of byName) if (!out.has(item.id)) out.set(item.id, playable(library, item));
      // Near misses keep the choose list from being empty. Without a duration and a probe the walk never picks them.
      for (const item of found.filter((i) => i.kind === media.kind).slice(0, NEAR_MISSES)) {
        if (!out.has(item.id)) out.set(item.id, { id: item.id, ref: item, sizeBytes: item.sizeBytes, name: item.fileName });
      }
      return [...out.values()];
    },
  };
}
```

Dart:

```dart
import 'dart:async';

import 'package:openwatchparty/openwatchparty.dart';

// Stand-ins for the app's own library.
final class Show {
  const Show({required this.title, this.year, this.ids = const {}});

  final String title;
  final int? year;
  final Map<String, String> ids; // the catalogue IDs the library knows; often none
}

final class LibraryItem {
  const LibraryItem({
    required this.id,
    required this.kind,
    required this.title,
    this.year,
    this.ids = const {},
    this.show,
    this.season,
    this.episode,
    this.durationMs,
    this.sizeBytes,
    this.fileName,
  });

  final String id;
  final MediaKind kind;
  final String title; // the movie's, episode's or video's title; an episode's may be empty
  final int? year;
  final Map<String, String> ids; // for an episode, its own IDs
  final Show? show; // episodes only
  final int? season;
  final int? episode;
  final int? durationMs; // when the library knows it
  final int? sizeBytes;
  final String? fileName;
}

abstract interface class Library {
  /// Items holding any of these catalogue IDs, as their own or their show's, whatever their titles.
  Future<List<LibraryItem>> findByIds(Map<String, String> ids, Cancellation cancel);

  /// The library's own text search over movie, show and video titles.
  Future<List<LibraryItem>> search(String text, Cancellation cancel);

  /// Opens the file, reads its duration and closes it; null when it doesn't open.
  Future<int?> probeDuration(LibraryItem item, Cancellation cancel);
}

const nearMisses = 10;

/// The descriptor this app sends for [item] as host. `title` is required, so an untitled episode gets "Episode N".
Media describe(LibraryItem item, int durationMs) {
  final show = item.show;
  final title = item.title.trim().isNotEmpty
      ? item.title
      : item.kind == MediaKind.episode && item.episode != null
          ? 'Episode ${item.episode}'
          : item.fileName ?? 'Untitled';
  return Media(
    kind: item.kind,
    title: title,
    year: item.year,
    ids: item.ids,
    series: show == null ? null : Series(title: show.title, year: show.year, ids: show.ids),
    season: item.season,
    episode: item.episode,
    durationMs: durationMs,
    file: MediaFile(sizeBytes: item.sizeBytes, name: item.fileName),
  );
}

final _separators = RegExp(r'[^\p{L}\p{N}]+', unicode: true);

/// Lower case, with every run of characters other than letters and digits as one space.
String _fold(String s) => s.toLowerCase().replaceAll(_separators, ' ').trim();

bool _closeYears(int? a, int? b) => a == null || b == null || (a - b).abs() <= 1;

/// Some namespace holds a different ID on each side: two different titles.
bool _conflicts(Map<String, String> a, Map<String, String> b) =>
    a.entries.any((e) => b[e.key] != null && b[e.key] != e.value);

/// Step 2: the same title and year, or the same show, season and episode, with no ID saying otherwise.
bool _sameName(Media media, LibraryItem item) {
  if (item.kind != media.kind) return false;
  switch (media.kind) {
    case MediaKind.movie:
      return _fold(item.title) == _fold(media.title) &&
          _closeYears(media.year, item.year) &&
          !_conflicts(media.ids, item.ids);
    case MediaKind.episode:
      final show = item.show, series = media.series;
      return show != null &&
          series != null &&
          _fold(show.title) == _fold(series.title) &&
          _closeYears(series.year, show.year) &&
          media.season != null &&
          media.season == item.season &&
          media.episode != null &&
          media.episode == item.episode &&
          !_conflicts(media.ids, item.ids);
    case MediaKind.video:
      final name = media.file?.name;
      return _fold(item.title) == _fold(media.title) ||
          (name != null && item.fileName?.toLowerCase() == name.toLowerCase());
  }
}

/// A candidate the walk may match: its duration, or a probe that learns it.
Candidate<LibraryItem> playable(Library library, LibraryItem item) => Candidate(
      id: item.id,
      ref: item,
      durationMs: item.durationMs,
      probe: (cancel) => library.probeDuration(item, cancel),
      sizeBytes: item.sizeBytes,
      name: item.fileName,
    );

final class LibraryResolver implements Resolver<LibraryItem> {
  LibraryResolver(this.library);

  final Library library;

  @override
  Future<List<Candidate<LibraryItem>>> resolve(Media media, Cancellation cancel) async {
    final out = <String, Candidate<LibraryItem>>{}; // by item ID, first find wins, in order
    // Step 1: files sharing a catalogue ID with the media, whatever their titles. sameTitle reads no durationMs.
    final byId = [
      ...await library.findByIds(media.ids, cancel),
      ...await library.findByIds(media.series?.ids ?? const {}, cancel),
    ];
    for (final item in byId) {
      if (!out.containsKey(item.id) && sameTitle(media, describe(item, media.durationMs))) {
        out[item.id] = playable(library, item);
      }
    }
    // Step 2, only without an ID match: an episode is searched by its show's title, never its own. Durations decide.
    final text = media.kind == MediaKind.episode ? media.series?.title ?? media.title : media.title;
    final found = await library.search(text, cancel); // a search term only, never a path or a query fragment
    final byName = out.isEmpty ? found.where((item) => _sameName(media, item)).toList() : const <LibraryItem>[];
    for (final item in byName) {
      out.putIfAbsent(item.id, () => playable(library, item));
    }
    // Near misses keep the choose list from being empty. Without a duration and a probe the walk never picks them.
    for (final item in found.where((i) => i.kind == media.kind).take(nearMisses)) {
      out.putIfAbsent(item.id, () => Candidate(id: item.id, ref: item, sizeBytes: item.sizeBytes, name: item.fileName));
    }
    return out.values.toList();
  }
}

/// The user's list of files to choose from: the resolver's candidates, each with its difference once known.
void chooseList(
  WatchParty<LibraryItem> party,
  Library library,
  void Function(String label, void Function() onPick) show,
) {
  for (final ranked in party.state.candidates) {
    final item = ranked.candidate.ref;
    final delta = ranked.deltaMs;
    final name = item.title.trim().isNotEmpty
        ? (item.season != null && item.episode != null ? '${item.title} S${item.season}E${item.episode}' : item.title)
        : item.fileName ?? 'Untitled';
    final label = delta == null ? name : '$name (${(delta / 1000).round()} s)';
    // choose takes a playable candidate, rebuilt from the ref: near misses carry no duration and no probe.
    show(label, () => unawaited(party.choose(playable(library, item))));
  }
}
```

Kotlin:

```kotlin
package example

import kotlin.math.abs
import kotlin.math.roundToLong
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import party.openwatch.Candidate
import party.openwatch.Media
import party.openwatch.MediaFile
import party.openwatch.MediaKind
import party.openwatch.Resolver
import party.openwatch.Series
import party.openwatch.WatchParty
import party.openwatch.sameTitle

// Stand-ins for the app's own library.
data class Show(
    val title: String,
    val year: Long? = null,
    val ids: Map<String, String> = emptyMap(), // the catalogue IDs the library knows; often none
)

data class LibraryItem(
    val id: String,
    val kind: MediaKind,
    val title: String, // the movie's, episode's or video's title; an episode's may be empty
    val year: Long? = null,
    val ids: Map<String, String> = emptyMap(), // for an episode, its own IDs
    val show: Show? = null, // episodes only
    val season: Long? = null,
    val episode: Long? = null,
    val durationMs: Long? = null, // when the library knows it
    val sizeBytes: Long? = null,
    val fileName: String? = null,
)

interface Library {
    /** Items holding any of these catalogue IDs, as their own or their show's, whatever their titles. */
    suspend fun findByIds(ids: Map<String, String>): List<LibraryItem>

    /** The library's own text search over movie, show and video titles. */
    suspend fun search(text: String): List<LibraryItem>

    /** Opens the file, reads its duration and closes it; null when it doesn't open. */
    suspend fun probeDuration(item: LibraryItem): Long?
}

const val NEAR_MISSES = 10

/** The descriptor this app sends for [item] as host. `title` is required, so an untitled episode gets "Episode N". */
fun describe(item: LibraryItem, durationMs: Long): Media = Media(
    kind = item.kind,
    title = when {
        item.title.isNotBlank() -> item.title
        item.kind == MediaKind.EPISODE && item.episode != null -> "Episode ${item.episode}"
        else -> item.fileName ?: "Untitled"
    },
    durationMs = durationMs,
    year = item.year,
    ids = item.ids,
    series = item.show?.let { Series(it.title, it.year, it.ids) },
    season = item.season,
    episode = item.episode,
    file = MediaFile(sizeBytes = item.sizeBytes, name = item.fileName),
)

private val separators = Regex("[^\\p{L}\\p{N}]+")

/** Lower case, with every run of characters other than letters and digits as one space. */
private fun fold(s: String) = s.lowercase().replace(separators, " ").trim()

private fun closeYears(a: Long?, b: Long?) = a == null || b == null || abs(a - b) <= 1

/** Some namespace holds a different ID on each side: two different titles. */
private fun conflicts(a: Map<String, String>, b: Map<String, String>) = a.any { (ns, id) -> b[ns] != null && b[ns] != id }

/** Step 2: the same title and year, or the same show, season and episode, with no ID saying otherwise. */
private fun sameName(media: Media, item: LibraryItem): Boolean {
    if (item.kind != media.kind) return false
    return when (media.kind) {
        MediaKind.MOVIE ->
            fold(item.title) == fold(media.title) && closeYears(media.year, item.year) && !conflicts(media.ids, item.ids)
        MediaKind.EPISODE -> {
            val show = item.show
            val series = media.series
            show != null && series != null &&
                fold(show.title) == fold(series.title) && closeYears(series.year, show.year) &&
                media.season != null && media.season == item.season &&
                media.episode != null && media.episode == item.episode &&
                !conflicts(media.ids, item.ids)
        }
        MediaKind.VIDEO ->
            fold(item.title) == fold(media.title) || media.file?.name?.equals(item.fileName, ignoreCase = true) == true
    }
}

/** A candidate the walk may match: its duration, or a probe that learns it. */
fun playable(library: Library, item: LibraryItem): Candidate<LibraryItem> = Candidate(
    id = item.id,
    ref = item,
    durationMs = item.durationMs,
    sizeBytes = item.sizeBytes,
    name = item.fileName,
    probe = { library.probeDuration(item) },
)

class LibraryResolver(private val library: Library) : Resolver<LibraryItem> {
    override suspend fun resolve(media: Media): List<Candidate<LibraryItem>> {
        val out = linkedMapOf<String, Candidate<LibraryItem>>() // by item ID, first find wins, in order
        // Step 1: files sharing a catalogue ID with the media, whatever their titles. sameTitle reads no durationMs.
        val byId = library.findByIds(media.ids) + library.findByIds(media.series?.ids.orEmpty())
        for (item in byId) {
            if (item.id !in out && sameTitle(media, describe(item, media.durationMs))) out[item.id] = playable(library, item)
        }
        // Step 2, only without an ID match: an episode is searched by its show's title, never its own. Durations decide.
        val text = if (media.kind == MediaKind.EPISODE) media.series?.title ?: media.title else media.title
        val found = library.search(text) // a search term only, never a path or a query fragment
        val byName = if (out.isEmpty()) found.filter { sameName(media, it) } else emptyList()
        for (item in byName) out.getOrPut(item.id) { playable(library, item) }
        // Near misses keep the choose list from being empty. Without a duration and a probe the walk never picks them.
        for (item in found.filter { it.kind == media.kind }.take(NEAR_MISSES)) {
            out.getOrPut(item.id) { Candidate(item.id, item, sizeBytes = item.sizeBytes, name = item.fileName) }
        }
        return out.values.toList()
    }
}

/** The user's list of files to choose from: the resolver's candidates, each with its difference once known. */
fun chooseList(
    party: WatchParty<LibraryItem>,
    library: Library,
    scope: CoroutineScope,
    show: (label: String, onPick: () -> Unit) -> Unit,
) {
    for ((candidate, deltaMs) in party.state.value.candidates) {
        val item = candidate.ref
        val name =
            if (item.title.isNotBlank()) {
                if (item.season != null && item.episode != null) {
                    "${item.title} S${item.season}E${item.episode}"
                } else {
                    item.title
                }
            } else {
                item.fileName ?: "Untitled"
            }
        val label = if (deltaMs == null) name else "$name (${(deltaMs / 1000.0).roundToLong()} s)"
        // choose takes a playable candidate, rebuilt from the ref: near misses carry no duration and no probe.
        show(label) { scope.launch { party.choose(playable(library, item)) } }
    }
}
```

Swift:

```swift
import OpenWatchParty

// Stand-ins for the app's own library.
struct Show: Hashable, Sendable {
    var title: String
    var year: Int64? = nil
    var ids: [String: String] = [:] // the catalogue IDs the library knows; often none
}

struct LibraryItem: Hashable, Sendable {
    var id: String
    var kind: MediaKind
    var title: String // the movie's, episode's or video's title; an episode's may be empty
    var year: Int64? = nil
    var ids: [String: String] = [:] // for an episode, its own IDs
    var show: Show? = nil // episodes only
    var season: Int64? = nil
    var episode: Int64? = nil
    var durationMs: Int64? = nil // when the library knows it
    var sizeBytes: Int64? = nil
    var fileName: String? = nil
}

@MainActor
protocol Library: AnyObject, Sendable {
    /// Items holding any of these catalogue IDs, as their own or their show's, whatever their titles.
    func findByIds(_ ids: [String: String]) async throws -> [LibraryItem]

    /// The library's own text search over movie, show and video titles.
    func search(_ text: String) async throws -> [LibraryItem]

    /// Opens the file, reads its duration and closes it; nil when it doesn't open.
    func probeDuration(_ item: LibraryItem) async throws -> Int64?
}

let nearMisses = 10

/// The descriptor this app sends for `item` as host. `title` is required, so an untitled episode gets "Episode N".
func describe(_ item: LibraryItem, durationMs: Int64) -> Media {
    let title: String
    if !item.title.allSatisfy(\.isWhitespace) {
        title = item.title
    } else if item.kind == .episode, let episode = item.episode {
        title = "Episode \(episode)"
    } else {
        title = item.fileName ?? "Untitled"
    }
    return Media(
        kind: item.kind,
        title: title,
        durationMs: durationMs,
        year: item.year,
        ids: item.ids,
        series: item.show.map { Series(title: $0.title, year: $0.year, ids: $0.ids) },
        season: item.season,
        episode: item.episode,
        file: MediaFile(sizeBytes: item.sizeBytes, name: item.fileName)
    )
}

/// Lower case, with every run of characters other than letters and digits as one space.
private func fold(_ s: String) -> String {
    s.lowercased().split(whereSeparator: { !$0.isLetter && !$0.isNumber }).joined(separator: " ")
}

private func closeYears(_ a: Int64?, _ b: Int64?) -> Bool {
    guard let a, let b else { return true }
    return abs(a - b) <= 1
}

/// Some namespace holds a different ID on each side: two different titles.
private func conflicts(_ a: [String: String], _ b: [String: String]) -> Bool {
    a.contains { ns, id in b[ns].map { $0 != id } ?? false }
}

/// Step 2: the same title and year, or the same show, season and episode, with no ID saying otherwise.
private func sameName(_ media: Media, _ item: LibraryItem) -> Bool {
    guard item.kind == media.kind else { return false }
    switch media.kind {
    case .movie:
        return fold(item.title) == fold(media.title) && closeYears(media.year, item.year) && !conflicts(media.ids, item.ids)
    case .episode:
        guard let show = item.show, let series = media.series, let season = media.season, let episode = media.episode else {
            return false
        }
        return fold(show.title) == fold(series.title) && closeYears(series.year, show.year)
            && season == item.season && episode == item.episode && !conflicts(media.ids, item.ids)
    case .video:
        return fold(item.title) == fold(media.title)
            || (media.file?.name).map { $0.lowercased() == item.fileName?.lowercased() } ?? false
    }
}

/// A candidate the walk may match: its duration, or a probe that learns it.
func playable(_ library: any Library, _ item: LibraryItem) -> Candidate<LibraryItem> {
    Candidate(
        id: item.id,
        ref: item,
        durationMs: item.durationMs,
        sizeBytes: item.sizeBytes,
        name: item.fileName,
        probe: { try await library.probeDuration(item) } // the SDK cancels the task when it no longer needs the answer
    )
}

func libraryResolver(_ library: any Library) -> Resolver<LibraryItem> {
    Resolver { media in
        var out: [Candidate<LibraryItem>] = [] // first find wins, in order
        var seen: Set<String> = [] // item IDs in `out`
        // Step 1: files sharing a catalogue ID with the media, whatever their titles. sameTitle reads no durationMs.
        let byId = try await library.findByIds(media.ids) + library.findByIds(media.series?.ids ?? [:])
        for item in byId where !seen.contains(item.id) && sameTitle(media, describe(item, durationMs: media.durationMs)) {
            seen.insert(item.id)
            out.append(playable(library, item))
        }
        // Step 2, only without an ID match: an episode is searched by its show's title, never its own. Durations decide.
        let text = media.kind == .episode ? media.series?.title ?? media.title : media.title
        let found = try await library.search(text) // a search term only, never a path or a query fragment
        let byName = out.isEmpty ? found.filter { sameName(media, $0) } : []
        for item in byName where seen.insert(item.id).inserted {
            out.append(playable(library, item))
        }
        // Near misses keep the choose list from being empty. Without a duration and a probe the walk never picks them.
        for item in found.filter({ $0.kind == media.kind }).prefix(nearMisses) where seen.insert(item.id).inserted {
            out.append(Candidate(id: item.id, ref: item, sizeBytes: item.sizeBytes, name: item.fileName))
        }
        return out
    }
}

/// The user's list of files to choose from: the resolver's candidates, each with its difference once known.
@MainActor
func chooseList(
    _ party: WatchParty<LibraryItem>,
    _ library: any Library,
    show: (_ label: String, _ onPick: @escaping @MainActor () -> Void) -> Void
) {
    for ranked in party.state.candidates {
        let item = ranked.candidate.ref
        let name: String
        if !item.title.allSatisfy(\.isWhitespace) {
            if let season = item.season, let episode = item.episode {
                name = "\(item.title) S\(season)E\(episode)"
            } else {
                name = item.title
            }
        } else {
            name = item.fileName ?? "Untitled"
        }
        let label = ranked.deltaMs.map { "\(name) (\(Int64((Double($0) / 1000).rounded())) s)" } ?? name
        // choose takes a playable candidate, rebuilt from the ref: near misses carry no duration and no probe.
        show(label) { Task { await party.choose(playable(library, item)) } }
    }
}
```

Treat descriptors as untrusted input: search terms only, never a path, URL, host, shell or SQL fragment. Never open `file.name`. Anyone can forge `ext`, so use it only for lookups in your own library. Show titles and names as plain text. See [descriptors are untrusted](../spec/SDK.md#97-descriptors-are-untrusted).

## 4. Open `state.choice`

When `state.choice` changes, open `choice.ref` (unless open) and `attachPlayer(player, ref)`, skipping both if already attached with that ref. Open one file at a time; if the choice changed mid-open, open the newer one instead. The party's end releases the player, so clear the attached ref on `Ended` (in Kotlin, watch `events`: `state` can skip `ENDED`).

TypeScript:

```ts
let attached: LibraryItem | null = null;
let away = false; // step 8
let opening = false;

async function openChoice(): Promise<void> {
  if (opening) return;
  opening = true;
  try {
    while (true) {
      const ref = party.state.choice?.ref;
      if (away || ref === undefined || ref === attached) return;
      if (player.item !== ref) await player.open(ref);
      if (away || party.state.choice?.ref !== ref) continue;
      party.attachPlayer(adapter, ref);
      attached = ref;
    }
  } finally {
    opening = false;
  }
}

party.on("state", () => void openChoice());
party.on("event", (event) => {
  if (event.type === "ended") attached = null;
});
```

Dart:

```dart
LibraryItem? attached;
var away = false; // step 8
var opening = false;

Future<void> openChoice() async {
  if (opening) return;
  opening = true;
  try {
    while (true) {
      final ref = party.state.choice?.ref;
      if (away || ref == null || ref == attached) return;
      if (player.item != ref) await player.open(ref);
      if (away || party.state.choice?.ref != ref) continue;
      party.attachPlayer(adapter, ref);
      attached = ref;
    }
  } finally {
    opening = false;
  }
}

party.states.listen((_) => openChoice());
party.events.listen((event) {
  if (event is EndedEvent) attached = null;
});
```

Kotlin:

```kotlin
var attached: LibraryItem? = null
var away = false // step 8
val opening = Mutex()

suspend fun openChoice() = opening.withLock {
    while (true) {
        val ref = party.state.value.choice?.ref ?: break
        if (away || ref == attached) break
        if (player.item != ref) player.open(ref)
        if (away || party.state.value.choice?.ref != ref) continue
        party.attachPlayer(adapter, ref)
        attached = ref
    }
}

mainScope.launch { party.state.collect { openChoice() } }
mainScope.launch { party.events.collect { if (it is PartyEvent.Ended) attached = null } }
```

Swift:

```swift
var attached: LibraryItem?
var away = false // step 8
var opening = false

@MainActor
func openChoice() async throws {
    if opening { return }
    opening = true
    defer { opening = false }
    while true {
        guard let ref = party.state.choice?.ref, !away, ref != attached else { return }
        if player.item != ref { try await player.open(ref) }
        if away || party.state.choice?.ref != ref { continue }
        party.attachPlayer(adapter, ref: ref)
        attached = ref
    }
}

let choices = party.states()
let endings = party.events()
Task { for await _ in choices { try? await openChoice() } }
Task { for await event in endings where event is EndedEvent { attached = nil } }
```

## 5. Host flow: share media

The host calls `setMedia(media, ref)` for the file it will play. An optional start `positionMs` is in that file. `setMedia` and `attachPlayer(player, ref)` can come in either order once the file is open. Both take the same `ref` (in TypeScript, the same object; in Swift, an equal value, as refs compare with `==`). `clearMedia()` stops the media for everyone.

Build the descriptor from your library entry, as `describe` does in step 3. Required: `kind`, a non-empty `title`, `durationMs`, and for an episode `series.title`; all else is optional. Sparse metadata is fine: an untitled episode sends a stand-in such as `Episode 5`, and empty `ids` may be omitted. Keep the real kind — `video` is only for a file with no catalogue identity (a home video, a clip); a movie or episode without IDs stays `movie`/`episode`. ID values are non-empty strings, numbers included (`"tmdb": "1396"`; a series' is a TV ID, a movie's a movie ID), and `plex` is a `plex://` GUID. V1 has no field for absolute episode numbers or alternate titles, so a series' anime-namespace IDs matter more than numbers. File hints and `ext` under your reverse-DNS name are optional. Nothing may locate or fetch media. See [the media descriptor](../spec/SPEC.md#10-media-descriptor).

TypeScript:

```ts
import { checkMedia, type Media } from "openwatchparty";

const media: Media = {
  kind: "episode",
  title: "Pilot",
  durationMs: 3487000,
  ids: { imdb: "tt0959621", tvdb: "349232" },
  series: { title: "Breaking Bad", year: 2008, ids: { imdb: "tt0903747", tmdb: "1396", tvdb: "81189" } },
  season: 1,
  episode: 1,
  ext: { "com.example.player": { item: "4711" } },
};
const problem = checkMedia(media); // null when the relay accepts it, else the first broken rule
if (problem === null) party.setMedia(media, item); // or setMedia(media, item, positionMs)
```

Dart:

```dart
final media = Media(
  kind: MediaKind.episode,
  title: 'Pilot',
  durationMs: 3487000,
  ids: {'imdb': 'tt0959621', 'tvdb': '349232'},
  series: Series(title: 'Breaking Bad', year: 2008, ids: {'imdb': 'tt0903747', 'tmdb': '1396', 'tvdb': '81189'}),
  season: 1,
  episode: 1,
  ext: {'com.example.player': {'item': '4711'}},
);
final problem = checkMedia(media); // null when the relay accepts it, else the first broken rule
if (problem == null) party.setMedia(media, item); // or setMedia(media, item, positionMs: ms)
```

Kotlin:

```kotlin
val media = Media(
    kind = MediaKind.EPISODE,
    title = "Pilot",
    durationMs = 3_487_000,
    ids = mapOf("imdb" to "tt0959621", "tvdb" to "349232"),
    series = Series("Breaking Bad", year = 2008, ids = mapOf("imdb" to "tt0903747", "tmdb" to "1396", "tvdb" to "81189")),
    season = 1,
    episode = 1,
    ext = buildJsonObject { putJsonObject("com.example.player") { put("item", "4711") } },
)
val problem = checkMedia(media) // null when the relay accepts it, else the first broken rule
if (problem == null) party.setMedia(media, ref = item) // positionMs = 0 by default
```

Swift:

```swift
let media = Media(
    kind: .episode,
    title: "Pilot",
    durationMs: 3_487_000,
    ids: ["imdb": "tt0959621", "tvdb": "349232"],
    series: Series(title: "Breaking Bad", year: 2008, ids: ["imdb": "tt0903747", "tmdb": "1396", "tvdb": "81189"]),
    season: 1,
    episode: 1,
    ext: ["com.example.player": ["item": "4711"]]
)
let problem = checkMedia(media) // nil when the relay accepts it, else the first broken rule
if problem == nil { try party.setMedia(media, ref: item) } // positionMs: 0 by default
```

## 6. Guest flow, matching and the offset

A guest calls `join(code)`. The SDK routes the code to its relay. It searches with your resolver when the room has media. The outcomes are in [the spec](../spec/SDK.md#96-outcomes):

- **Matched**: a file within 1500 ms of the reference duration. `state.choice` is set; open it (step 4).
- **Mismatch**: no file within 1500 ms but some file opened. `state.mismatch` holds the closest file and its `deltaMs` (own duration minus reference). Ask the user, then answer once.
- **Unavailable**: no candidate opened, or the party has no resolver. `choose` still works, with a candidate from `state.candidates` or anywhere else.

A matched episode can still be the wrong one: the walk stops at the first candidate within 1500 ms, so under different numbering another episode of similar length matches silently. Always show the matched file — its own title, season, episode and file name — next to the room's `media`, and offer the choose list; choosing keeps the current offset.

TypeScript:

```ts
// state.mismatch is set: ask the user, then answer with one of these.
party.acceptMismatch(); // play the offered file
party.declineMismatch(); // sit out this title
chooseList(party, library, showChoice); // step 3: the user's pick calls choose(playable(library, item))
await party.choose(playable(library, found)); // or a file from the app's own search, from anywhere
// On a different cut: local position = room position + offset.
party.setOffset(party.state.offsetMs + 1000);
```

Dart uses the same calls, `chooseList(party, library, show)` included. Kotlin reads the state through `.value` and passes its scope to `chooseList(party, library, mainScope, show)`: `party.setOffset(party.state.value.offsetMs + 1000)`. Swift uses the same calls, with `try` on the throwing ones and `await` on `choose`: `_ = await party.choose(playable(library, found))` and `try party.setOffset(party.state.offsetMs + 1000)`.

A different cut plays in sync with an offset (positive for a longer intro), reset on new media. Offer 100 ms and 1 s steps, a reset, and the mismatch's `deltaMs` as a hint. See [offset](../spec/SDK.md#13-offset).

## 7. Controls and host tools

Play, pause, seek and rate need `canControl` and media. Without them they throw `forbidden` or `invalid_message`. The host-only calls need the host (else `forbidden`), not media. Outside a room, these calls throw `not_in_room`. Dart and Kotlin differ in the enum names: `Control.everyone` / `Control.host`, `Control.EVERYONE` / `Control.HOST`. In Swift they are synchronous and throw (`try party.play()`); `leave()` doesn't throw.

TypeScript:

```ts
// The user's own presses go to the party, never straight to the player.
if (party.state.canControl) {
  party.play();
  party.pause();
  party.seek(754000); // in the user's own file
  party.setRate(1.5); // 0.5 to 2.0
}

// Host only.
party.setControl("everyone"); // or "host"
party.transferHost(peerId);
party.kick(peerId);
party.lock(true);
party.clearMedia();
party.end();

party.leave(); // ends this session with "left"
await party.close(); // leaves and releases the party for good; dispose of the player after this
```

Host changes need no app code beyond the UI. `transferHost(peerId)` hands over at once. If the host stays disconnected for 30 s, the relay promotes the first connected participant.

## 8. Leaving the player, background and reconnects

Call `detachPlayer()` when the user leaves the player or the app backgrounds. Set `away` first, so step 4 doesn't re-attach. A non-playing app, such as a remote control, detaches right after joining. Await the completion `detachPlayer` and `close()` return before disposing the player, and cancel or await any open under way.

TypeScript:

```ts
// The room stops waiting for this user.
away = true;
await party.detachPlayer(); // settles once no SDK command runs on the player: dispose of it after this

// Back in the player:
away = false;
attached = null;
await openChoice();
```

Dart:

```dart
// The room stops waiting for this user.
away = true;
await party.detachPlayer(); // settles once no SDK command runs on the player: dispose of it after this

// Back in the player:
away = false;
attached = null;
await openChoice();
```

Kotlin:

```kotlin
// The room stops waiting for this user.
away = true
party.detachPlayer().join() // settles once no SDK command runs on the player: dispose of it after this

// Back in the player:
away = false
attached = null
openChoice()
```

Swift:

```swift
// The room stops waiting for this user.
away = true
await party.detachPlayer().value // settles once no SDK command runs on the player: dispose of it after this

// Back in the player:
away = false
attached = nil
try await openChoice()
```

On a dropped connection the SDK resumes on its own, with backoff, for up to 5 minutes; while reconnecting, commands throw `not_in_room`. The SDK keeps nothing across an app restart: join the room again with `join(code)`.

A party ends with a reason your UI handles: `left`, `ended` (the host ended the room), `kicked`, `expired` (the room reached its maximum age, 24 h by default), `replaced` or `lost`. An ended party may `create` or `join` again. After `close()` it cannot be used. See [ending](../spec/SDK.md#74-ending) and [reconnect and resume](../spec/SDK.md#14-reconnect-and-resume).

## 9. Events and errors

State drives rendering, events drive messages. An `action` event says who played, paused, seeked or changed things; its `ended` kind means the media ended. `playBlocked` (step 2) carries what `play` threw: prompt the user to press play on the player, and hide it once the player leaves `loading` or the room stops playing. Ignore unknown kinds. Errors (`PartyError`, or `PartyException` in Dart/Kotlin) carry `code`, `message` and `re`. Plan wording for `relay_full`/`unreachable` (`create`) and `room_not_found`, `room_full`, `room_locked`, `invalid_code` (`join`), with a generic text for the rest. Relay codes are in [the protocol's error codes](../spec/SPEC.md#appendix-a-error-codes); the SDK adds `invalid_code`, `unreachable` and `cancelled`.

TypeScript:

```ts
import { PartyError, type PartyEvent } from "openwatchparty";

try {
  await party.join(typedCode);
} catch (e) {
  if (!(e instanceof PartyError)) throw e;
  showError(e.code); // your own text per code; e.message is for logs
}

function onEvent(event: PartyEvent): void {
  switch (event.type) {
    case "action":
      if (!event.byMe) toast(`${event.by?.name ?? "The room"}: ${event.kind}`);
      break;
    case "playBlocked":
      promptPlay(); // the player refused to start: ask the user to press play on it
      break;
    case "ended":
      showEnded(event.reason);
      break;
    default:
      break; // other events, also types a later SDK 1.x adds
  }
}
```

In Dart and Kotlin the same shape catches `PartyException`. Events are `ActionEvent`/`PlayBlockedEvent`/`EndedEvent` in Dart and `PartyEvent.Action`/`PartyEvent.PlayBlocked`/`PartyEvent.Ended` in Kotlin. Neither type is exhaustive. A Kotlin `when` expression needs an `else` branch. A Dart `switch` expression needs a `_` case. In Swift the same shape catches `PartyError`, and `create` and `join` after `close()` throw `PartyClosedError`. Events are the structs `ActionEvent`/`PlayBlockedEvent`/`EndedEvent` conforming to the protocol `PartyEvent`. A Swift `switch` needs a `default` branch.

## 10. Relays

`Relays.public()` starts from the SDK's compiled-in list and refreshes it from `https://openwatch.party/relays.json` only; a code's first symbol names its relay, returned by `relays.relayFor(code)`. `Relays.at(baseUrl)` is one relay for everything: the party creates and joins rooms there and fetches nothing. Codes don't cross sources. See [relay list](../spec/SDK.md#3-relay-list) and [routing](../spec/SDK.md#42-routing).

TypeScript:

```ts
import { Relays } from "openwatchparty";

const publicRelays = Relays.public();
const ownRelay = Relays.at("https://relay.example.com");
```

In Dart these are `Relays.public()` and `Relays.at('https://relay.example.com')`; in Kotlin `Relays.public(http)` and `Relays.at(http, "https://relay.example.com")`. In Swift these are `Relays.public()` and `try Relays.at("https://relay.example.com")`.

For testing, point `Relays.at` at a local relay (ready when it prints `listening 127.0.0.1:8080`); raise the per-IP limits (`--max-connections-per-ip`, `--max-rooms-per-ip`, `--join-burst`) for many instances on one machine. See the [relay README](../relay/README.md) and [`owp-peer`](../conformance/peer/README.md).

```sh
cd relay && go run ./cmd/owp-relay --addr 127.0.0.1:8080 --admin-addr ""
```

## 11. Using the public relay well

Under [the terms](terms.md#who-may-use-the-public-relay) the public relay is free for open-source apps; others need written permission. `client` is `<app>/<version>` (1–64 characters, non-empty on both sides of the first `/`), naming the real app: deny lists and usage counts key on `<app>`, and a denied app gets `client_blocked`. `name` is 1–32 characters, not unique. Stay within [acceptable use](terms.md#acceptable-use). See [name and client](../spec/SPEC.md#63-name-and-client).

For your own privacy text, [privacy in the protocol](../spec/SPEC.md#15-privacy) and [the privacy policy](privacy.md) state what the relay and other participants see.

## Checklist

- The resolver returns catalogue-ID matches first, title matches only when no ID matched, episodes searched by the show's title, and near misses to fill the choose list.
- The app shows the matched file next to the room's media and offers the choose list, so a wrong episode matched on duration can be replaced.
- Over IPC, reads come from a property-event cache, commands await the player's reply, and a seek waits until it lands. In a browser extension, the adapter follows element replacement and is released during ads.

The SDK spec lists [what apps must do](../spec/SDK.md#167-what-apps-must-do); work through it before shipping.

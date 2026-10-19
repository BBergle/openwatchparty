# Dart SDK

`openwatchparty` joins a watch party room by its code and keeps your own player in sync with everyone else in it, implementing the behaviour in [`spec/SDK.md`](../../spec/SDK.md); read that for the rules this page only names.

## Install

```sh
dart pub add openwatchparty
flutter pub add openwatchparty
```

```dart
import 'package:openwatchparty/openwatchparty.dart';
```

## Requirements

The package needs Dart `^3.4.0` and is pure Dart with no Flutter dependency, so the same code runs in Dart and Flutter apps. On native platforms the default relay-list fetch uses `dart:io`, and on the web it uses `fetch`; where neither exists the default throws `UnsupportedError`, so pass `httpGet` to `Relays`. There is no Flutter-specific API: listen to `party.states`, rebuild from `party.state`, and call `party.close()` when the owning widget or model is disposed.

## Quick start

```dart
import 'package:openwatchparty/openwatchparty.dart';

// One relay list for the whole app, passed to every party.
final relays = Relays.public();

/// The app's own player and library, as far as this example needs them.
abstract interface class AppPlayer {
  String? get fileId;
  int get positionMs;
  int? get durationMs;
  bool get isPlaying;
  bool get isBuffering;
  bool get hasFrame;
  Future<void> open(String fileId);
  Future<void> play();
  Future<void> pause();
  Future<void> seekTo(int positionMs);
  Future<void> setSpeed(double rate);
}

abstract interface class AppLibrary {
  Future<List<({String id, int? durationMs})>> search(Media media);
}

/// The SDK reads the player on every tick and commands it.
final class ExamplePlayerAdapter extends PlayerAdapter {
  ExamplePlayerAdapter(this._player);

  final AppPlayer _player;

  @override
  int get positionMs => _player.positionMs;

  @override
  int? get durationMs => _player.durationMs;

  @override
  bool get paused => !_player.isPlaying;

  @override
  bool get buffering => _player.isBuffering;

  @override
  bool get firstFrameShown => _player.hasFrame;

  @override
  bool get canSetRate => true;

  @override
  Future<void> play() => _player.play();

  @override
  Future<void> pause() => _player.pause();

  @override
  Future<void> seek(int positionMs) => _player.seekTo(positionMs);

  @override
  Future<void> setRate(double rate) => _player.setSpeed(rate);
}

/// Finds the user's own files for the room's media; `ref` is what the app needs to open one.
final class ExampleResolver implements Resolver<String> {
  ExampleResolver(this._library);

  final AppLibrary _library;

  @override
  Future<List<Candidate<String>>> resolve(Media media, Cancellation cancel) async {
    final files = await _library.search(media);
    return [for (final file in files) Candidate(id: file.id, ref: file.id, durationMs: file.durationMs)];
  }
}

/// Creates a room when [code] is null, else joins it. Returns the party.
Future<WatchParty<String>> watchTogether(AppPlayer player, AppLibrary library, {String? code}) async {
  final party = WatchParty<String>(
    relays: relays,
    client: 'Example/1.0',
    name: 'Anna',
    resolver: ExampleResolver(library),
  );
  final adapter = ExamplePlayerAdapter(player);

  // Open each new choice, one at a time, and attach the player with the ref it opened.
  String? attached; // the file the adapter is attached with
  var opening = false;
  Future<void> openChoice() async {
    if (opening) return; // the open under way goes on to the newest choice
    opening = true;
    try {
      while (true) {
        final ref = party.state.choice?.ref;
        if (ref == null || ref == attached) return; // no choice, or attached: the player follows by itself
        if (player.fileId != ref) await player.open(ref);
        if (party.state.choice?.ref != ref) continue; // a newer choice came meanwhile: open that one
        party.attachPlayer(adapter, ref);
        attached = ref;
      }
    } finally {
      opening = false;
    }
  }

  party.states.listen((_) => openChoice());
  party.events.listen((event) {
    if (event case EndedEvent(:final reason)) {
      print('The party ended: ${reason.name}');
      attached = null; // the end released the adapter
    }
  });

  try {
    if (code == null) {
      print('Room code: ${formatCode(await party.create())}');
    } else {
      await party.join(code);
    }
  } on PartyException catch (error) {
    print('Could not join: ${error.code}');
  }
  return party;
}

/// As host: share the title playing in [fileId]. The states listener attaches the player once `choice` names it.
void shareMovie(WatchParty<String> party, String fileId) {
  const media = Media(
    kind: MediaKind.movie,
    title: 'Example Movie',
    year: 2024,
    ids: {'imdb': 'tt0000001'},
    durationMs: 5400000,
    ext: {
      'com.example': {'itemId': '42'},
    },
  );
  final problem = checkMedia(media);
  if (problem != null) throw ArgumentError(problem);
  party.setMedia(media, fileId);
}
```

## Relays

`Relays.public({httpGet})` starts from the copy of [`relays.json`](../../relays.json) compiled into the SDK, and each valid list a refresh reads from `https://openwatch.party/relays.json` replaces it in memory. `Relays.at(baseUrl)` is one [self-hosted](../../relay/README.md) relay for every room: its `list` is that relay alone, it fetches nothing, and a bad base (not a lower-case `https://` or `http://` URL with a host) throws `PartyException` `invalid_message`. Members: `List<Relay> get list`, `Relay? relayFor(String code)`, `Future<bool> refresh()` (false: no valid list, current list stays; always true for `Relays.at`). A custom `HttpGet` must return a 200 body, follow no redirects, fail a body over `maxHttpBodyBytes` and stop on cancel.

## WatchParty

`WatchParty<T>({required Relays relays, required String client, required String name, Resolver<T>? resolver, WebSocketConnector? connect, int Function()? nowMs, Random? random})`; `T` is the app's file `ref` type. Without a resolver, others' media has no candidates (`match` `unavailable`). A bad `client` (`<app>/<version>`) or `name` (1–32 characters) throws `PartyException` `invalid_message`.

| Group | Members |
|---|---|
| state | `PartyState<T> get state`, `Stream<PartyState<T>> get states`, `Stream<PartyEvent> get events` |
| session | `Future<String> create()`, `Future<void> join(String code)`, `leave()`, `Future<void> close()`, `int? roomPositionMs()` |
| matching/player | `acceptMismatch()`, `declineMismatch()`, `Future<bool> choose(Candidate<T> candidate)`, `resolveAgain()`, `attachPlayer(PlayerAdapter player, T ref)`, `Future<void> detachPlayer([PlayerAdapter? player])`, `playerChanged()` |
| playback | `play()`, `pause()`, `seek(int positionMs)`, `setRate(double rate)`, `setOffset(int offsetMs)` |
| host | `setMedia(Media media, T ref, {int positionMs = 0})`, `clearMedia()`, `setControl(Control mode)`, `transferHost(String peerId)`, `kick(String peerId)`, `lock(bool locked)`, `end()` |

Untyped members above return `void`. `create()` completes with the code. `join` takes a code as typed or pasted. `seek` is in the app's own file. `setRate` is 0.5–2.0. `setOffset` is ±604800000 ms. The SDK copies what the app passes in and publishes unmodifiable collections. `leave()`/`close()` cancel a pending `create`/`join`; an ended party may start again.

`PartyState` fields: `connection`, `peerId`, `room`, `media`, `match`, `choice` (file to open), `mismatch` (file offered), `candidates`, `offsetMs`, `player`, `endReason`, plus `me`, `isHost`, `canControl`. `Media` needs `kind`, `title`, `durationMs`, takes `year`, `ids`, `series` (required for episodes), `season`, `episode`, `file` and `ext` (reverse-DNS keys). Events: `ActionEvent`, `ParticipantJoinedEvent`, `ParticipantLeftEvent`, `ErrorEvent`, `ResolveFailedEvent`, `PlayBlockedEvent`, `EndedEvent`; use a default case: 1.x may add events.

`states`/`events` are broadcast with async delivery. Use a party on the one isolate holding the player (Flutter's UI isolate).

## Player and resolver

Extend `PlayerAdapter` as above with a `final` or `base` class: cheap synchronous reads, commands that must complete or throw, `canSetRate` false meaning seek-only following, and optional `seekAccuracyMs` (how far a seek may land). Its `play` throws when the player refuses to start, such as a browser blocking playback without a user gesture; while the player then stays paused the SDK reports `loading`, and `PlayBlockedEvent`, carrying the thrown error, tells the app to ask the user to press play on the player itself. Call `attachPlayer(player, ref)` once per opened file, and again after the party ends (`EndedEvent`); as host, `setMedia` and attaching may come in either order, and a `setMedia` lost on a dropped connection is sent again after the resume while the party is still host. Call `detachPlayer` when the user leaves the player or the app goes to the background, and await it or `close()` before disposing the player. While the user is away, the quick start's `openChoice()` must not open or attach, since `attachPlayer` brings them back. Give it a flag it checks before it opens and before it attaches. When the user leaves, set the flag and await `party.detachPlayer()`; on return, clear it, set `attached = null` and call `openChoice()`. Implement `Resolver<T>.resolve(media, cancel)` best-first and honour `Cancellation`. Add a candidate `Probe` to learn a duration by opening the file. A resolver still running after 30 s counts as no candidates, and identity is the resolver's job (the SDK ranks, drops none). Treat descriptor values as untrusted search terms only. Helpers: `formatCode`, `sameTitle`, `checkMedia`, and `parseMedia(json)` (receive checks).

## Errors

`PartyException` has `code` (relay codes pass through; SDK adds `invalid_code`, `unreachable`, `cancelled`; see `ErrorCode`), log-only `message` and `re` — show your own text per `code`. The session futures complete with it and commands throw it. The `close()` and `detachPlayer()` futures never fail. Refusals arrive as `ErrorEvent`, session loss as `EndedEvent`. No answer within 10 s is `unreachable`. `invalid_message` also covers a bad `Relays.at` base, a malformed `joined` and a bad descriptor in `parseMedia`. After `close()`: `StateError`.

## Build and test

From a full checkout:

```sh
cd relay && go build -o /tmp/owp-relay ./cmd/owp-relay && cd ..
cd sdk/dart
dart pub get
dart format --output=none --set-exit-if-changed .
dart analyze --fatal-infos
dart test -x integration                          # unit, vector and embedded-list tests
OWP_RELAY_BIN=/tmp/owp-relay dart test -t integration   # integration tests against a relay built from relay/
dart test                                         # everything; builds the relay with Go when OWP_RELAY_BIN is unset
```

The `-x integration` run needs no Go. See [`CHANGELOG.md`](CHANGELOG.md), [`docs/integration.md`](../../docs/integration.md) and the [TypeScript](../typescript/README.md), [Kotlin](../kotlin/README.md) and [Swift](../swift/README.md) SDKs.

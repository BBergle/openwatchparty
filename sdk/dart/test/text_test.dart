import 'package:openwatchparty/openwatchparty.dart';
import 'package:test/test.dart';

// Values an app builds in Dart that the JSON vectors can't express: unpaired surrogates in app strings (SDK.md §7.1),
// ints beyond the protocol range and values that aren't JSON.
void main() {
  WatchParty<String> party({String name = 'Ben', String client = 'Test/1.0'}) =>
      WatchParty(relays: Relays.public(), client: client, name: name);

  test('the constructor refuses an unpaired surrogate in name or client', () {
    for (final (name, client) in [('Ben\uD800', 'Test/1.0'), ('\uDC00', 'Test/1.0'), ('Ben', 'Test/1.0\uD83D')]) {
      expect(() => party(name: name, client: client),
          throwsA(isA<PartyException>().having((e) => e.code, 'code', ErrorCode.invalidMessage)));
    }
    expect(party(name: '\uD83D\uDE00').state.connection, Connection.idle, reason: 'a surrogate pair is one character');
  });

  test('checkMedia refuses an unpaired surrogate anywhere in the descriptor', () {
    const ok = Media(kind: MediaKind.movie, title: 'Heat', durationMs: 600000);
    expect(checkMedia(ok), isNull);
    expect(checkMedia(const Media(kind: MediaKind.movie, title: 'Heat\uD800', durationMs: 600000)), isNotNull);
    expect(checkMedia(const Media(kind: MediaKind.movie, title: 'Heat', ids: {'imdb': '\uDC00'}, durationMs: 1)),
        isNotNull);
    expect(
        checkMedia(const Media(kind: MediaKind.movie, title: 'Heat', durationMs: 1, ext: {
          'app.example': {'\uD800': 1},
        })),
        isNotNull);
  });

  test('checkMedia refuses ints beyond ±(2^53 − 1), and durations beyond 7 days', () {
    const max = 9007199254740991;
    expect(checkMedia(const Media(kind: MediaKind.movie, title: 'Heat', year: max, durationMs: 604800000)), isNull);
    expect(checkMedia(const Media(kind: MediaKind.movie, title: 'Heat', durationMs: 604800001)), isNotNull);
    expect(checkMedia(const Media(kind: MediaKind.movie, title: 'Heat', year: max + 1, durationMs: 1)), isNotNull);
    expect(checkMedia(const Media(kind: MediaKind.movie, title: 'Heat', year: -max - 1, durationMs: 1)), isNotNull);
    expect(
        checkMedia(
            const Media(kind: MediaKind.movie, title: 'Heat', durationMs: 1, file: MediaFile(sizeBytes: max + 1))),
        isNotNull);
  });

  test('parseMedia fails with invalid_message on a value that isn’t JSON', () {
    const heat = {'kind': 'movie', 'title': 'Heat', 'durationMs': 600000};
    expect(parseMedia(heat).toJson(), heat);
    for (final bad in <Object?>[
      {...heat, 1: 'one'},
      {...heat, 'durationMs': double.nan},
      {
        ...heat,
        'ext': {'com.example': DateTime(2000)},
      },
      {
        ...heat,
        'ext': {
          'com.example': {1: 'one'},
        },
      },
      {
        ...heat,
        'ext': {'com.example': double.infinity},
      },
      const MediaFile(name: 'heat.mkv'),
    ]) {
      expect(
          () => parseMedia(bad), throwsA(isA<PartyException>().having((e) => e.code, 'code', ErrorCode.invalidMessage)),
          reason: '$bad');
    }
  });
}

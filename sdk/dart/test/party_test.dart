import 'dart:async';
import 'dart:math';

import 'package:fake_async/fake_async.dart';
import 'package:openwatchparty/openwatchparty.dart';
import 'package:test/test.dart';

import 'support/fake_player.dart';
import 'support/fake_relay.dart';

// One party against scripted relays in virtual time (SDK.md §16.9).

const heat = Media(kind: MediaKind.movie, title: 'Heat', year: 1995, ids: {'imdb': 'tt0113277'}, durationMs: 600000);
const ronin = Media(kind: MediaKind.movie, title: 'Ronin', year: 1998, ids: {'imdb': 'tt0122690'}, durationMs: 7320000);

void main() {
  group('create and join', () {
    test('a joined with a lower-case code or without its media member fails join with invalid_message', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party();
        for (final bad in <Map<String, Object?> Function(Map<String, Object?>)>[
          (j) => {...j, 'code': 'k7mq2x'},
          (j) => {...j}..remove('media'),
        ]) {
          final join = Outcome(party.join('K7MQ2X'));
          w.run();
          w.relay.last.send(bad(joined()));
          w.run();
          expect(join.error, isCode(ErrorCode.invalidMessage));
        }
      });
    });

    test('after close() create and join fail with StateError, also for a code that isn’t one', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party();
        party.close();
        final created = Outcome(party.create());
        final joining = Outcome(party.join('K7MQ2X'));
        final notACode = Outcome(party.join('K7MQ2'));
        w.run();
        expect([created.error, joining.error, notACode.error], everyElement(isA<StateError>()));
        expect(w.relay.attempts, isEmpty);
        expect(w.gets, isEmpty);
      });
    });
  });

  group('room state and events', () {
    test('a relay refusal of a command is an Error event that carries the relay’s message', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, events) = w.party();
        final conn = joinBen(w, party,
            room: snapshot(control: 'everyone', participants: [
              participant('p1', 'Anna'),
              participant('p2', 'Ben', host: true),
            ]));
        party.lock(true);
        conn.send({'type': 'error', 'code': 'rate_limited', 're': 'lock', 'message': 'slow down'});
        w.run();
        expect(events.whereType<ErrorEvent>().single.error.message, 'slow down');
      });
    });

    test('kick by a participant who isn’t host fails with forbidden and sends nothing', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party();
        final conn = joinBen(w, party);
        expect(() => party.kick('p1'), throwsA(isCode(ErrorCode.forbidden)));
        w.run();
        expect(conn.received.map((m) => m['type']), ['join', 'status']);
      });
    });
  });

  group('matching', () {
    test('ResolveFailed carries the error the resolver threw', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, events) = w.party(resolve: (_) => throw StateError('server down'));
        joinBen(w, party, room: snapshot(epoch: 1, phase: 'paused'), media: heat);
        expect(events.whereType<ResolveFailedEvent>().single.error, isA<StateError>());
      });
    });

    test('a probe that throws a cancellation of its own, such as its own timeout, returned nothing; the walk goes on',
        () {
      fakeAsync((async) {
        final w = World(async);
        final probes = Probes();
        Cancellation? own;
        final (party, events) = w.party(
            resolve: (_) => [
                  Candidate(
                    id: 'a',
                    ref: 'a-file',
                    probe: (cancel) {
                      own = cancel;
                      return Completer<int?>().future.timeout(const Duration(seconds: 5));
                    },
                  ),
                  probes.candidate('b', 600000, takesMs: 1000),
                ]);
        joinBen(w, party, room: snapshot(epoch: 1, phase: 'paused'), media: heat);
        w.run(10000);
        expect(own!.isCancelled, isFalse);
        expect(probes.cancels.keys, ['b']);
        expect(party.state.match, const Match(MatchState.matched, 0));
        expect(party.state.choice!.id, 'b');
        expect(events, isEmpty);
      });
    });

    test('an earlier choose’s probe that answers after a later choose cancelled it changes nothing', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party();
        final choices = <String?>[];
        party.states.listen((s) => choices.add(s.choice?.id));
        joinBen(w, party, room: snapshot(epoch: 1, phase: 'paused'), media: heat);
        final probes = Probes();

        // The earlier probe ignores its cancellation and answers at 1000 ms, while the later one still runs.
        unawaited(party.choose(probes.candidate('a', 600000, takesMs: 1000)));
        w.run();
        unawaited(party.choose(probes.candidate('b', 600400, takesMs: 5000)));
        w.run(5000);
        expect(choices, isNot(contains('a')));
        expect((party.state.choice!.id, party.state.match), ('b', const Match(MatchState.matched, 400)));
      });
    });

    test('changing the maps passed to setMedia afterwards changes nothing in the party', () {
      fakeAsync((async) {
        final w = World(async);
        final calls = <Cancellation>[];
        final (party, _) = w.party(resolver: _HangingResolver(calls));
        final hosting = [participant('p1', 'Anna'), participant('p2', 'Ben', host: true)];
        final conn = joinBen(w, party, room: snapshot(participants: hosting));
        final ids = {'imdb': 'tt0113277'};
        final hints = <String, Object?>{'item': 1};
        final ext = <String, Object?>{'com.example': hints};
        party.setMedia(
            Media(kind: MediaKind.movie, title: 'Heat', ids: ids, durationMs: 600000, ext: ext), 'heat-file');
        w.run();
        final sent = conn.sent('setMedia').single['media'];

        ids['imdb'] = 'tt0000000';
        hints['item'] = 2;
        ext['com.other'] = true;
        conn.send({...snapshot(rev: 2, epoch: 1, phase: 'paused', participants: hosting), 'media': sent});
        w.run();
        expect(party.state.match, const Match(MatchState.matched, 0), reason: 'the relay echoed the media as sent');
        expect(party.state.choice!.ref, 'heat-file');
        expect(calls, isEmpty);
      });
    });

    test('the party’s own media choice carries the descriptor’s duration and no other file detail', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party();
        shareHeat(w, party);
        final choice = party.state.choice!;
        expect(choice.durationMs, 600000);
        expect((choice.sizeBytes, choice.oshash, choice.name, choice.probe), (null, null, null, null));
      });
    });

    test('resolveAgain with a choice keeps the walk’s candidate object as the choice', () {
      fakeAsync((async) {
        final w = World(async);
        var calls = 0;
        final (party, _) = w.party(
            resolve: (_) => ++calls == 1 ? [bFile] : [const Candidate(id: 'b', ref: 'b-file-2', durationMs: 600000)]);
        joinBen(w, party, room: snapshot(epoch: 1, phase: 'paused'), media: heat);
        expect(party.state.choice, same(bFile));
        party.resolveAgain();
        w.run();
        expect(calls, 2);
        expect(party.state.choice, same(bFile));
      });
    });
  });

  group('following', () {
    test('a play timer beyond 2147483647 ms is clamped, and the player holds for the start', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party(resolve: (_) => [bFile]);
        final player = FakePlayer(nowMs: w.now);
        joinBen(w, party, room: playingRoom(atMs: FakeRelay.clockOffsetMs + 10000000000000), media: heat);
        party.attachPlayer(player, 'b-file');
        w.run(1000);
        final delays = [for (final timer in async.pendingTimers) timer.duration.inMilliseconds];
        expect(delays, contains(2147483647), reason: 'the play timer');
        expect(delays, everyElement(lessThanOrEqualTo(2147483647)));
        expect((player.paused, player.positionMs), (true, 0));
        expect(party.state.player, PlayerState.ready);
      });
    });

    test('a seek before the offset sends position 0', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party(resolve: (_) => [bFile]);
        final conn = joinPlaying(w, party, FakePlayer(nowMs: w.now));
        party.setOffset(5000);
        party.seek(4000);
        w.run();
        expect(conn.sent('seek').single['positionMs'], 0);
      });
    });

    test('PlayBlocked carries the error the adapter’s play threw', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, events) = w.party(resolve: (_) => [bFile]);
        final refusal = StateError('NotAllowedError');
        final player = FakePlayer(nowMs: w.now)..refusePlay = refusal;
        joinBen(w, party, room: playingRoom(), media: heat);
        party.attachPlayer(player, 'b-file');
        w.run(1000);
        expect(events.whereType<PlayBlockedEvent>().map((e) => e.error), [same(refusal)]);
      });
    });
  });

  group('wire values', () {
    test('integer inputs and computed wire values out of bounds throw invalid_message before any local effect', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party(resolve: (_) => [bFile]);
        final player = FakePlayer(nowMs: w.now, durationMs: null);
        final conn = joinPlaying(w, party, player, host: true);
        final sent = conn.received.length;
        final issued = player.commands.length;
        final at = player.positionMs;

        expect(() => party.setOffset(604800001), throwsA(isCode(ErrorCode.invalidMessage)));
        expect(() => party.setOffset(-604800001), throwsA(isCode(ErrorCode.invalidMessage)));
        expect(() => party.seek(maxSafeInt + 1), throwsA(isCode(ErrorCode.invalidMessage)));
        expect(() => party.setMedia(ronin, 'ronin-file', positionMs: maxSafeInt + 1),
            throwsA(isCode(ErrorCode.invalidMessage)));
        const week = Media(kind: MediaKind.movie, title: 'Shoah', durationMs: 604800001);
        expect(() => party.setMedia(week, 'shoah-file'), throwsA(isCode(ErrorCode.invalidMessage)));
        // pause sends the adapter's position − offset.
        player.jump(maxSafeInt + 1 - at);
        expect(party.pause, throwsA(isCode(ErrorCode.invalidMessage)));
        expect(player.paused, isFalse);
        player.jump(at - maxSafeInt - 1);
        w.run();
        expect(party.state.offsetMs, 0);
        expect(party.state.media, heat);
        expect(player.commands.skip(issued), isEmpty);
        expect(conn.received.skip(sent), isEmpty);

        party.setOffset(-604800000);
        expect(party.state.offsetMs, -604800000);
        // seek sends ms − offset, which must stay an int.
        expect(() => party.seek(maxSafeInt - 604800000 + 1), throwsA(isCode(ErrorCode.invalidMessage)));
        party.seek(maxSafeInt - 604800000);
        w.run();
        expect(conn.sent('seek').single['positionMs'], maxSafeInt);
        party.setOffset(604800000);
        expect(party.state.offsetMs, 604800000);
      });
    });

    test('every outgoing int is written without a fraction or exponent, also when computed', () {
      fakeAsync((async) {
        final w = World(async);
        final (party, _) = w.party(resolve: (_) => [const Candidate(id: 'b', ref: 'b-file', durationMs: 600400)]);
        final player = FakePlayer(nowMs: w.now);
        final conn = joinPlaying(w, party, player, host: true);
        party.setOffset(300);
        w.run(300);
        party.pause();
        party.seek(5000);
        party.setRate(1.5);
        // Doubles with integral values, as a JSON decoder may hand them over.
        party.setMedia(
          parseMedia({
            'kind': 'movie',
            'title': 'Ronin',
            'year': 1998.0,
            'durationMs': 7320000.0,
            'file': {'sizeBytes': 4.7e9},
          }),
          'ronin-file',
          positionMs: 1000,
        );
        w.run(1000);
        for (final type in ['status', 'pause', 'seek', 'setMedia']) {
          expect(conn.sent(type), isNotEmpty, reason: type);
        }
        expect(conn.sent('status').map((s) => s['match']), contains(equals({'state': 'matched', 'deltaMs': 400})));
        final numbers = [
          for (final text in conn.texts)
            for (final m in RegExp(r'"(\w+)":(-?[0-9][0-9.eE+-]*)').allMatches(text))
              if (m[1] != 'rate') '${m[1]}:${m[2]}',
        ];
        expect(numbers, isNotEmpty);
        expect(numbers, everyElement(matches(RegExp(r':-?(0|[1-9][0-9]*)$'))));
      });
    });
  });
}

/// The test's world: a [FakeRelay] and the virtual clock. GETs fail.
final class World {
  World(this.async) : relay = FakeRelay(() => async.elapsed.inMilliseconds);

  final FakeAsync async;
  final FakeRelay relay;
  final List<Uri> gets = [];

  int now() => async.elapsed.inMilliseconds;

  void run([int ms = 0]) => async.elapse(Duration(milliseconds: ms));

  Future<String> httpGet(Uri url, Cancellation cancel) async {
    gets.add(url);
    throw StateError('HTTP 404 from $url');
  }

  /// Ben's party on the public relays, with u = 0 for the backoff.
  (WatchParty<String>, List<PartyEvent>) party({
    Resolver<String>? resolver,
    List<Candidate<String>> Function(Media media)? resolve,
  }) {
    final party = WatchParty<String>(
      relays: Relays.public(httpGet: httpGet),
      client: 'Test/1.0',
      name: 'Ben',
      resolver: resolver ?? _FunctionResolver(resolve ?? (_) => const []),
      connect: relay.connect,
      nowMs: now,
      random: _FixedRandom(),
    );
    final events = <PartyEvent>[];
    party.events.listen(events.add);
    return (party, events);
  }
}

/// Ben joins [code] (K7MQ2X on asia by default); returns once the burst pongs have arrived.
FakeConnection joinBen(World w, WatchParty<String> party,
    {Map<String, Object?>? room, Media? media, String code = 'K7MQ2X'}) {
  final join = Outcome(party.join(code));
  w.run();
  final conn = w.relay.last;
  conn.send(joined(room: room, media: media?.toJson()));
  w.run(1100);
  expect(join.done && join.error == null, isTrue, reason: '${join.error}');
  return conn;
}

/// A room playing [heat] from position 0 at local time 1100 ms, where everyone controls; Ben hosts when [host].
Map<String, Object?> playingRoom({int atMs = FakeRelay.clockOffsetMs + 1100, double rate = 1, bool host = false}) =>
    snapshot(
      epoch: 1,
      phase: 'playing',
      atMs: atMs,
      rate: rate,
      control: 'everyone',
      participants: [participant('p1', 'Anna', host: !host), participant('p2', 'Ben', host: host)],
    );

/// Ben joins [playingRoom], matches [bFile] and attaches [player] at 1100 ms; returns at 2100 ms, in sync.
FakeConnection joinPlaying(World w, WatchParty<String> party, FakePlayer player, {double rate = 1, bool host = false}) {
  final conn = joinBen(w, party, room: playingRoom(rate: rate, host: host), media: heat);
  party.attachPlayer(player, 'b-file');
  w.run(1000);
  expect((player.paused, (player.positionMs - party.roomPositionMs()!).abs() < 50), (false, true));
  return conn;
}

/// Ben joins room K7MQ2X as its host and shares [heat] as `heat-file`; returns once epoch 1 arrived, paused at 0.
FakeConnection shareHeat(World w, WatchParty<String> party) {
  final hosting = [participant('p1', 'Anna'), participant('p2', 'Ben', host: true)];
  final conn = joinBen(w, party, room: snapshot(participants: hosting));
  party.setMedia(heat, 'heat-file');
  w.run();
  expect(conn.sent('setMedia').single['media'], heat.toJson());
  conn.send({...snapshot(rev: 2, epoch: 1, phase: 'paused', participants: hosting), 'media': heat.toJson()});
  w.run();
  return conn;
}

const bFile = Candidate(id: 'b', ref: 'b-file', durationMs: 600000);

const maxSafeInt = 9007199254740991;

/// Candidates whose probe answers a duration after a while; keeps each probe's [Cancellation] by candidate ID.
final class Probes {
  final Map<String, Cancellation> cancels = {};

  Candidate<String> candidate(String id, int durationMs, {required int takesMs}) => Candidate(
        id: id,
        ref: '$id-file',
        probe: (cancel) {
          cancels[id] = cancel;
          return Future.delayed(Duration(milliseconds: takesMs), () => durationMs);
        },
      );
}

/// A future's result, read synchronously in `fakeAsync`.
final class Outcome<T> {
  Outcome(Future<T> future) {
    future.then((value) {
      this.value = value;
      done = true;
    }, onError: (Object error) {
      this.error = error;
      done = true;
    });
  }

  T? value;
  Object? error;
  var done = false;
}

final class _FunctionResolver implements Resolver<String> {
  const _FunctionResolver(this._resolve);

  final List<Candidate<String>> Function(Media media) _resolve;

  @override
  Future<List<Candidate<String>>> resolve(Media media, Cancellation cancel) async => _resolve(media);
}

/// Never answers; keeps each call's [Cancellation].
final class _HangingResolver implements Resolver<String> {
  _HangingResolver(this.cancels);

  final List<Cancellation> cancels;

  @override
  Future<List<Candidate<String>>> resolve(Media media, Cancellation cancel) {
    cancels.add(cancel);
    return Completer<List<Candidate<String>>>().future;
  }
}

final class _FixedRandom implements Random {
  @override
  double nextDouble() => 0;

  @override
  int nextInt(int max) => 0;

  @override
  bool nextBool() => false;
}

Matcher isCode(String code) => isA<PartyException>().having((e) => e.code, 'code', code);

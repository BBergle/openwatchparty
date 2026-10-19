@TestOn('vm')
library;

import 'dart:convert';
import 'dart:io';

import 'package:openwatchparty/src/clock.dart';
import 'package:openwatchparty/src/codes.dart';
import 'package:openwatchparty/src/errors.dart';
import 'package:openwatchparty/src/json.dart';
import 'package:openwatchparty/src/matching.dart';
import 'package:openwatchparty/src/media.dart';
import 'package:openwatchparty/src/reconcile.dart';
import 'package:openwatchparty/src/relay_list.dart';
import 'package:openwatchparty/src/room.dart';
import 'package:openwatchparty/src/text.dart';
import 'package:openwatchparty/src/timeline.dart';
import 'package:test/test.dart';

/// Every table in conformance/vectors/ runs here, one test per case (SDK.md §17), but party.json: its traces run in
/// party_traces_test.dart. A table without a runner fails `every table has a runner`.
void main() {
  table('timeline', (input) {
    final room = input.object('room');
    final t = input.integer('serverNowMs');
    final phase = room.string('phase');
    return {
      'positionMs': positionAt(
        playing: phase == 'playing',
        positionMs: room.integer('positionMs'),
        atMs: room.integer('atMs'),
        rate: room.number('rate'),
        durationMs: input.integer('durationMs'),
        t: t,
      ),
      'running': phase == 'playing' && t >= room.integer('atMs'),
    };
  });

  table('clock-sync', (input) {
    final clock = ClockSync();
    final pongs = <Object?>[];
    for (final event in input.list('events')) {
      final e = JsonObject.of(event, 'event');
      if (e.has('ping')) {
        clock.ping(e.integer('ping'), e.integer('atMs'));
        continue;
      }
      final result = clock.pong(e.integer('pong'), e.integer('serverTime'), e.integer('atMs'));
      final best = clock.best;
      pongs.add({'result': result.name, 'offsetMs': best?.offsetMs, 'rttMs': best?.rttMs});
    }
    return {'pongs': pongs};
  });

  table('code-routing', (input) {
    final code = normalizeCode(input.string('code'));
    if (code == null) return {'error': 'invalid_code'};
    final relays = [
      for (final r in input.list('relays'))
        Relay(
          id: JsonObject.of(r, 'relay').string('id'),
          name: '',
          url: '',
          prefixes: JsonObject.of(r, 'relay').string('prefixes'),
        ),
    ];
    return {'code': code, 'relay': routeCode(code, relays)?.id};
  });

  table('relay-list', (input) {
    try {
      return {
        'relays': [
          for (final r in parseRelayList(input.map['list']))
            {'id': r.id, 'name': r.name, 'url': r.url, 'prefixes': r.prefixes},
        ],
      };
    } on FormatException {
      return {'error': 'invalid_list'};
    }
  });

  table('room-check', (input) {
    try {
      parseRoomMessage(input.object('room').map);
      return {'valid': true};
    } on FormatException {
      return {'valid': false};
    }
  });

  table('media-check', (input) {
    final json = input.map['media'];
    switch (input.string('on')) {
      case 'receive':
        try {
          parseMedia(json);
          return {'valid': true};
        } on PartyException catch (error) {
          if (error.code != ErrorCode.invalidMessage) rethrow;
          return {'valid': false};
        }
      case 'send':
        try {
          // The decoder refuses an int over 2^53 − 1; the send cases hold one only where they expect false.
          return {'valid': checkMedia(decodeMedia(json)) == null};
        } on FormatException {
          return {'valid': false};
        }
      case final on:
        throw StateError('unknown on $on');
    }
  });

  table('names', (input) {
    final value = input.string('value');
    final problem = switch (input.string('field')) {
      'name' => checkName(value),
      'client' => checkClient(value),
      final field => throw FormatException('unknown field $field'),
    };
    return {'valid': problem == null};
  });

  // Every descriptor of these tables passes the receive checks (SDK.md §9.7), as one from a relay must.
  table('same-title', (input) {
    final a = parseReceivedMedia(input.map['a']);
    final b = parseReceivedMedia(input.map['b']);
    return {'same': sameTitle(a, b)};
  });

  tableAsync('matching', (input) async {
    final media = parseReceivedMedia(input.map['media']);
    final candidates = [
      for (final c in input.list('candidates')) _candidate(JsonObject.of(c, 'candidate')),
    ];
    var published = <Ranked<_SimulatedProbe?>>[];
    final ranked = rankCandidates(media, candidates);
    final outcome = (await walkCandidates(media, ranked, (candidate, deadlineMs) async {
      final probe = candidate.ref!;
      if (probe.takesMs > deadlineMs) return ProbeRun(null, deadlineMs, missedDeadline: true);
      return ProbeRun(probe.durationMs, probe.takesMs);
    }, progress: (candidates) => published = candidates))!;
    return {
      'order': [for (final c in outcome.order) c.id],
      'candidates': [
        for (final r in published) {'id': r.candidate.id, 'deltaMs': r.deltaMs},
      ],
      'probed': [for (final c in outcome.probed) c.id],
      'result': outcome.result.name,
      'choice': outcome.choice?.id,
      'deltaMs': outcome.deltaMs,
    };
  });

  table('reconcile', (input) {
    final room = input.object('room');
    final player = input.object('player');
    final result = reconcileStep(
      phase: Phase.values.byName(room.string('phase')),
      positionMs: room.integer('positionMs'),
      atMs: room.integer('atMs'),
      rate: room.number('rate'),
      durationMs: input.integer('durationMs'),
      relayNowMs: input.integer('serverNowMs'),
      offsetMs: input.integer('offsetMs'),
      player: PlayerReading(
        positionMs: player.integer('positionMs'),
        paused: player.boolean('paused'),
        buffering: player.boolean('buffering'),
        durationMs: player.nullableInteger('durationMs'),
        canSetRate: player.boolean('canSetRate'),
        appliedRate: player.number('rate'),
        seekAccuracyMs: player.optInteger('seekAccuracyMs') ?? 0,
      ),
      nudging: input.boolean('nudging'),
      sinceSeekMs: input.nullableInteger('sinceSeekMs'),
    );
    return {
      'actions': [
        for (final action in result.actions)
          switch (action) {
            PauseAction() => {'do': 'pause'},
            PlayAction() => {'do': 'play'},
            SetRateAction(:final rate) => {'do': 'setRate', 'rate': rate},
            SeekAction(:final positionMs) => {'do': 'seek', 'positionMs': positionMs},
          },
      ],
      'nudging': result.nudging,
      'playInMs': result.playInMs,
    };
  });

  test('every table has a runner', () {
    final tables = [
      for (final file in Directory('../../conformance/vectors').listSync())
        if (file is File && file.path.endsWith('.json')) file.uri.pathSegments.last.replaceFirst('.json', ''),
    ];
    expect(tables, unorderedEquals(_runners));
  });
}

/// The tables this file runs, and party, which party_traces_test.dart runs.
final Set<String> _runners = {'party'};

/// A simulated probe: its result and how long it takes.
typedef _SimulatedProbe = ({int? durationMs, int takesMs});

Candidate<_SimulatedProbe?> _candidate(JsonObject c) {
  final probe = c.optObject('probe');
  return Candidate(
    id: c.string('id'),
    ref: probe == null ? null : (durationMs: probe.nullableInteger('durationMs'), takesMs: probe.integer('takesMs')),
    durationMs: c.optInteger('durationMs'),
    sizeBytes: c.optInteger('sizeBytes'),
    oshash: c.optString('oshash'),
    name: c.optString('name'),
    probe: probe == null ? null : (_) async => null,
  );
}

List<JsonObject> _cases(String name) {
  final file = File('../../conformance/vectors/$name.json');
  final document = JsonObject.of(jsonDecode(file.readAsStringSync()), name);
  if (document.string('table') != name) throw StateError('$name.json holds table ${document.string('table')}');
  return [for (final c in document.list('cases')) JsonObject.of(c, 'case')];
}

void table(String name, Map<String, Object?> Function(JsonObject input) run) {
  tableAsync(name, (input) async => run(input));
}

void tableAsync(String name, Future<Map<String, Object?>> Function(JsonObject input) run) {
  _runners.add(name);
  group(name, () {
    for (final c in _cases(name)) {
      test(c.string('name'), () async {
        final actual = await run(c.object('input'));
        final expected = c.map['expect'];
        expect(jsonEquals(actual, expected), isTrue,
            reason: 'expected ${jsonEncode(expected)}\nactual   ${jsonEncode(actual)}');
      });
    }
  });
}

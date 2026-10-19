@TestOn('vm')
library;

import 'dart:convert';
import 'dart:io';

import 'package:openwatchparty/openwatchparty.dart';
import 'package:openwatchparty/src/public_relays.dart';
import 'package:test/test.dart';

void main() {
  test('the embedded relay list equals relays.json at the repository root', () {
    final canonical = jsonDecode(File('../../relays.json').readAsStringSync());
    expect(jsonDecode(publicRelaysJson), canonical);
  });

  test('the public list routes the symbols it assigns and leaves the others to no relay', () {
    final relays = Relays.public(httpGet: (url, cancel) async => throw StateError('no network in this test'));
    expect([for (final r in relays.list) r.id], ['na', 'eu', 'asia']);
    expect(relays.relayFor('2ABCDE')!.id, 'na');
    expect(relays.relayFor('9abcde')!.id, 'eu');
    expect(relays.relayFor('k7m-q2x')!.id, 'asia');
    expect(relays.relayFor('Q2X7MK'), isNull);
    expect(relays.relayFor('ZZZZZZ'), isNull);
    expect(relays.relayFor('K7MQ2'), isNull);
  });
}

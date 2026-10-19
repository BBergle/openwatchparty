@TestOn('vm')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:fake_async/fake_async.dart';
import 'package:openwatchparty/openwatchparty.dart';
import 'package:openwatchparty/src/cancellation.dart';
import 'package:openwatchparty/src/transport.dart' show defaultHttpGet;
import 'package:test/test.dart';

// The relay list's sources and HTTP rules (SDK.md §3.1, §3.4).
void main() {
  const embedded = ['na', 'eu', 'asia'];

  group('Relays', () {
    test('a fetcher that ignores cancel is cancelled and given up 5 s after the start', () {
      fakeAsync((async) {
        Cancellation? cancel;
        final relays = Relays.public(httpGet: (url, c) {
          cancel = c;
          return Completer<String>().future;
        });
        bool? refreshed;
        relays.refresh().then((ok) => refreshed = ok);
        async.elapse(const Duration(milliseconds: 4999));
        expect((refreshed, cancel!.isCancelled), (null, false));
        async.elapse(const Duration(milliseconds: 1));
        expect((refreshed, cancel!.isCancelled), (false, true));
        expect([for (final r in relays.list) r.id], embedded);
      });
    });

    test('an answer over 65536 bytes of UTF-8 from the fetcher fails the refresh', () async {
      final list = listOf(name: 'Café'); // one character, two bytes
      for (final (bytes, ok) in [(maxHttpBodyBytes, true), (maxHttpBodyBytes + 1, false)]) {
        final body = list + ' ' * (bytes - utf8.encode(list).length);
        final relays = Relays.public(httpGet: (url, cancel) async => body);
        expect(await relays.refresh(), ok, reason: '$bytes bytes');
      }
    });

    test('Relays.at is one relay at a lower-case https:// or http:// base with a host; else invalid_message', () async {
      for (final (base, name, url) in [
        ('https://relay.example.com/', 'https://relay.example.com', 'wss://relay.example.com/v1/ws'),
        ('http://192.168.1.5:8080/owp', 'http://192.168.1.5:8080/owp', 'ws://192.168.1.5:8080/owp/v1/ws'),
        ('https://relay.example.com//', 'https://relay.example.com', 'wss://relay.example.com/v1/ws'),
      ]) {
        final relays = Relays.at(base);
        final relay = Relay(id: 'custom', name: name, url: url, prefixes: '23456789ABCDEFGHJKMNPQRSTUVWXYZ');
        expect(relays.list, [relay], reason: base);
        expect(
            [relays.relayFor('2aa-aaa'), relays.relayFor('QRS-TUV'), relays.relayFor('K7M-Q2')], [relay, relay, null]);
        expect(await relays.refresh(), isTrue);
        expect(relays.list, [relay]);
      }
      for (final base in [
        'wss://relay.example.com/v1/ws',
        'HTTPS://relay.example.com',
        'Http://relay.example.com',
        'https://',
        'https:///relay',
        'http://?x',
        'http://#x',
        'http://relay.example.com?x',
        'https://relay.example.com/a b',
        'https://café.example',
        'relay.example.com',
        '',
      ]) {
        expect(
          () => Relays.at(base),
          throwsA(isA<PartyException>().having((e) => e.code, 'code', ErrorCode.invalidMessage)),
          reason: base,
        );
      }
    });
  });

  group('the dart:io HTTP GET', () {
    late HttpServer server;
    final paths = <String>[];

    setUpAll(() async {
      server = await HttpServer.bind(InternetAddress.loopbackIPv4, 18299);
      server.listen((request) {
        paths.add(request.uri.path);
        final response = request.response;
        switch (request.uri.path) {
          case '/ok':
            response.write('{"ok":true}');
          case '/missing':
            response
              ..statusCode = HttpStatus.notFound
              ..write('{}');
          case '/moved':
            response
              ..statusCode = HttpStatus.found
              ..headers.set(HttpHeaders.locationHeader, '/ok');
          case '/at-limit':
            response.write(' ' * maxHttpBodyBytes);
          case '/over-limit':
            response.write(' ' * (maxHttpBodyBytes + 1));
        }
        response.close();
      });
    });
    tearDownAll(() => server.close(force: true));

    Future<String> get(String path) => defaultHttpGet(Uri.parse('http://127.0.0.1:18299$path'), CancellationSource());

    test('returns the body of a 200 answer up to 65536 bytes', () async {
      expect(await get('/ok'), '{"ok":true}');
      expect(await get('/at-limit'), hasLength(maxHttpBodyBytes));
    });

    test('fails other statuses, redirects without following them, and longer bodies', () async {
      paths.clear();
      for (final path in ['/missing', '/moved', '/over-limit']) {
        await expectLater(get(path), throwsA(isA<HttpException>()), reason: path);
      }
      expect(paths, ['/missing', '/moved', '/over-limit']);
    });

    test('cancel closes a request whose body never ends', () async {
      final drip = await ServerSocket.bind(InternetAddress.loopbackIPv4, 18298);
      addTearDown(drip.close);
      final closed = Completer<void>();
      drip.listen((socket) {
        addTearDown(socket.destroy);
        void done() {
          if (!closed.isCompleted) closed.complete();
        }

        socket.listen((_) {}, onDone: done, onError: (Object _) => done());
        socket.write('HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n1\r\n[\r\n');
      });
      final cancel = CancellationSource();
      final answer = defaultHttpGet(Uri.parse('http://127.0.0.1:18298/'), cancel);
      await Future<void>.delayed(const Duration(milliseconds: 200));
      cancel.cancel();
      await expectLater(answer, throwsA(anything));
      await closed.future.timeout(const Duration(seconds: 2));
    });
  });
}

/// A one-relay list document.
String listOf({String url = 'wss://x.example/v1/ws', String name = 'X'}) => jsonEncode({
      'relays': [
        {'id': 'x', 'name': name, 'url': url, 'prefixes': '23456789ABCDEFGHJKMNPQRSTUVWXYZ'},
      ],
    });

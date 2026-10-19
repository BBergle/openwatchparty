import 'dart:async';
import 'dart:convert';

import 'package:web_socket/testing.dart';
import 'package:web_socket/web_socket.dart';

/// Relays scripted by the test: every `connect` opens a [FakeConnection]. Run it inside `fakeAsync` with the party's
/// clock; pongs carry relay time = local time + [clockOffsetMs].
final class FakeRelay {
  FakeRelay(this.nowMs);

  final int Function() nowMs;
  static const int clockOffsetMs = 1000000;

  /// Every connect, with the local time it was made.
  final List<({Uri url, int atMs})> attempts = [];
  final List<FakeConnection> connections = [];

  Future<WebSocket> connect(Uri url) async {
    attempts.add((url: url, atMs: nowMs()));
    final (client, server) = fakes();
    connections.add(FakeConnection._(this, url, server));
    return client;
  }

  FakeConnection get last => connections.last;
}

final class FakeConnection {
  FakeConnection._(this._relay, this.url, this._socket) {
    _socket.events.listen((event) {
      switch (event) {
        case TextDataReceived(:final text):
          texts.add(text);
          final message = (jsonDecode(text) as Map).cast<String, Object?>();
          if (message['type'] == 'ping') {
            _pong(message['id']);
          } else {
            received.add(message);
          }
        case CloseReceived(:final code):
          closeCode = code ?? 1005;
        case BinaryDataReceived():
          break;
      }
    });
  }

  final FakeRelay _relay;
  final Uri url;
  final WebSocket _socket;

  /// Everything but pings, in order.
  final List<Map<String, Object?>> received = [];

  /// Every text message as the client wrote it, pings included.
  final List<String> texts = [];

  /// The status the client closed with, once it did.
  int? closeCode;

  int get relayNow => _relay.nowMs() + FakeRelay.clockOffsetMs;

  List<Map<String, Object?>> sent(String type) => [
        for (final message in received)
          if (message['type'] == type) message,
      ];

  void _pong(Object? id) => Timer.run(() => send({'type': 'pong', 'id': id, 'serverTime': relayNow}));

  void send(Map<String, Object?> message) {
    if (closeCode == null) _socket.sendText(jsonEncode(message));
  }

  /// Closes from the relay's side; without [code] the client sees no status.
  void drop([int? code]) {
    closeCode = code ?? 1005;
    unawaited(_socket.close(code));
  }
}

/// A participant entry of a snapshot.
Map<String, Object?> participant(String peerId, String name,
        {bool host = false, String player = 'idle', Map<String, Object?>? match, int offsetMs = 0}) =>
    {
      'peerId': peerId,
      'name': name,
      'client': 'Test/1.0',
      'host': host,
      'connected': true,
      'player': player,
      'match': match,
      'offsetMs': offsetMs,
    };

/// A snapshot, by default of Anna (p1, host) and Ben (p2) in room K7MQ2X.
Map<String, Object?> snapshot({
  int rev = 1,
  int epoch = 0,
  String phase = 'idle',
  int positionMs = 0,
  int atMs = 0,
  double rate = 1,
  String control = 'host',
  List<String> waitingOn = const [],
  List<Map<String, Object?>>? participants,
}) =>
    {
      'type': 'room',
      'rev': rev,
      'code': 'K7MQ2X',
      'epoch': epoch,
      'phase': phase,
      'positionMs': positionMs,
      'atMs': atMs,
      'rate': rate,
      'control': control,
      'locked': false,
      'waitingOn': waitingOn,
      'participants': participants ?? [participant('p1', 'Anna', host: true), participant('p2', 'Ben')],
    };

/// `joined` into [room] as [peerId].
Map<String, Object?> joined({String peerId = 'p2', Map<String, Object?>? room, Map<String, Object?>? media}) => {
      'type': 'joined',
      'code': (room ?? snapshot())['code'],
      'you': {'peerId': peerId, 'token': tokenOf(peerId)},
      'room': {...room ?? snapshot()}..remove('type'),
      'media': media,
      'serverTime': 0,
    };

/// The resume token the fake relay gives [peerId]: 43 base64url characters.
String tokenOf(String peerId) => 'token-$peerId'.padRight(43, 'x');

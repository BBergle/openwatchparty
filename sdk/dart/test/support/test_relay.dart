import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// An `owp-relay` process built from `relay/` in this repository, on 127.0.0.1, in test mode.
///
/// Set `OWP_RELAY_BIN` to use a prebuilt binary; otherwise the first start runs `go build`.
final class TestRelay {
  TestRelay._(this.port, this._process, this._exit);

  /// Ports 18250–18299 belong to this SDK's tests.
  static var _nextPort = 18250;

  /// The next port of this SDK's range.
  static int nextPort() => _nextPort++;

  static Future<String>? _binary;

  final int port;
  final Process _process;
  final Future<int> _exit;

  String get baseUrl => 'http://127.0.0.1:$port';

  /// Starts a relay on [port] with relay timers at a tenth of their duration and per-IP limits raised for one-IP tests.
  static Future<TestRelay> start(int port) async {
    final binary = await (_binary ??= _build());
    final process = await Process.start(binary, [
      '--addr', '127.0.0.1:$port', //
      '--admin-addr', '',
      '--timer-scale', '0.1',
      '--max-connections-per-ip', '1000',
      '--max-rooms-per-ip', '1000',
      '--join-burst', '1000',
      '--joins-per-second', '1000',
      '--message-burst', '1000',
      '--messages-per-second', '1000',
    ]);
    final listening = Completer<void>();
    final output = StringBuffer();
    process.stdout.transform(utf8.decoder).transform(const LineSplitter()).listen((line) {
      output.writeln(line);
      if (line.startsWith('listening') && !listening.isCompleted) listening.complete();
    });
    process.stderr.transform(utf8.decoder).listen(output.write);
    final exit = process.exitCode;
    unawaited(exit.then((code) {
      if (!listening.isCompleted) listening.completeError(StateError('owp-relay exited with $code:\n$output'));
    }));
    try {
      await listening.future.timeout(const Duration(seconds: 10));
    } on Object {
      process.kill(ProcessSignal.sigkill);
      rethrow;
    }
    return TestRelay._(port, process, exit);
  }

  /// SIGTERM: the relay saves its rooms, closes every connection with 1001 and exits.
  Future<void> stop() async {
    _process.kill(ProcessSignal.sigterm);
    await _exit.timeout(const Duration(seconds: 10), onTimeout: () {
      _process.kill(ProcessSignal.sigkill);
      return _exit;
    });
  }

  static Future<String> _build() async {
    final prebuilt = Platform.environment['OWP_RELAY_BIN'];
    if (prebuilt != null && prebuilt.isNotEmpty) return prebuilt;
    final out = '${Directory.systemTemp.path}/owp-relay-sdkdart';
    final result = await Process.run('go', ['build', '-o', out, './cmd/owp-relay'], workingDirectory: '../../relay');
    if (result.exitCode != 0) throw StateError('go build failed:\n${result.stderr}');
    return out;
  }
}

/// A TCP proxy to a relay that can cut every connection at once, like a network drop: no close frame, no close code.
final class TcpProxy {
  TcpProxy._(this._server, this._targetPort) {
    _server.listen(_accept);
  }

  static Future<TcpProxy> start(int port, int targetPort) async =>
      TcpProxy._(await ServerSocket.bind(InternetAddress.loopbackIPv4, port), targetPort);

  final ServerSocket _server;
  final int _targetPort;
  final List<Socket> _sockets = [];

  Future<void> _accept(Socket client) async {
    final Socket upstream;
    try {
      upstream = await Socket.connect(InternetAddress.loopbackIPv4, _targetPort);
    } on SocketException {
      client.destroy();
      return;
    }
    _sockets.addAll([client, upstream]);
    void pipe(Socket from, Socket to) {
      from.listen(to.add, onError: (Object _) => to.destroy(), onDone: to.destroy, cancelOnError: true);
    }

    pipe(client, upstream);
    pipe(upstream, client);
  }

  /// Destroys every proxied connection; new ones are still accepted.
  void cut() {
    for (final socket in _sockets) {
      socket.destroy();
    }
    _sockets.clear();
  }

  Future<void> close() async {
    cut();
    await _server.close();
  }
}

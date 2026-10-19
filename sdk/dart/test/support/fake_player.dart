import 'dart:async';
import 'dart:math';

import 'package:openwatchparty/openwatchparty.dart';

/// A player that plays in real time, or on [nowMs] when given: its position moves while it isn't paused, at its rate.
/// Seeks take 30 ms. Records the SDK's commands in [commands].
final class FakePlayer extends PlayerAdapter {
  FakePlayer({this.durationMs = 600000, int Function()? nowMs}) : _nowMs = nowMs ?? _realTime();

  static int Function() _realTime() {
    final stopwatch = Stopwatch()..start();
    return () => stopwatch.elapsedMilliseconds;
  }

  @override
  final int? durationMs;

  @override
  bool get canSetRate => true;

  final int Function() _nowMs;

  /// While set, `play` is recorded, then throws it and the player stays paused, as a browser blocking autoplay.
  Object? refusePlay;

  /// `play`, `pause`, `seek <ms>` and `rate <rate>`, in the order the SDK issued them.
  final List<String> commands = [];
  var _base = 0;
  var _baseAt = 0;
  var _paused = true;

  /// The rate the SDK last set.
  var rate = 1.0;

  @override
  bool get paused => _paused;

  @override
  bool get buffering => false;

  @override
  bool get firstFrameShown => true;

  @override
  int get positionMs {
    if (_paused) return _base;
    final position = _base + ((_nowMs() - _baseAt) * rate).floor();
    final duration = durationMs;
    return duration == null ? position : min(position, duration);
  }

  /// Moves the position by [deltaMs] behind the SDK's back, as a stutter or a drifting clock would.
  void jump(int deltaMs) {
    _rebase();
    _base += deltaMs;
  }

  /// Starts playing behind the SDK's back, as the user pressing play on the player itself.
  void start() {
    _rebase();
    _paused = false;
  }

  void _rebase() {
    _base = positionMs;
    _baseAt = _nowMs();
  }

  @override
  Future<void> play() async {
    commands.add('play');
    if (refusePlay case final error?) throw error;
    _rebase();
    _paused = false;
  }

  @override
  Future<void> pause() async {
    commands.add('pause');
    _rebase();
    _paused = true;
  }

  @override
  Future<void> seek(int positionMs) async {
    commands.add('seek $positionMs');
    await Future<void>.delayed(const Duration(milliseconds: 30));
    _base = positionMs;
    _baseAt = _nowMs();
  }

  @override
  Future<void> setRate(double rate) async {
    commands.add('rate $rate');
    _rebase();
    this.rate = rate;
  }
}

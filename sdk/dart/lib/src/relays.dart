import 'dart:async';
import 'dart:convert';

import 'cancellation.dart';
import 'codes.dart';
import 'errors.dart';
import 'public_relays.dart';
import 'relay_list.dart';
import 'transport.dart';

/// Where parties find relays (SDK.md §3): the public relays, or one self-hosted relay. An app keeps one and passes it
/// to every party; the list and its refresh schedule belong to it.
final class Relays {
  Relays._(this._list, this._httpGet);

  /// The public relays, starting from the list compiled into the SDK. Each valid list a refresh reads from
  /// [publicSource] replaces it; nothing else does.
  factory Relays.public({HttpGet? httpGet}) =>
      Relays._(parseRelayList(jsonDecode(publicRelaysJson)), httpGet ?? defaultHttpGet);

  /// The self-hosted relay at [baseUrl], its `https://` or `http://` base (`https://relay.example.com`), for every
  /// room: its WebSocket URL is the base with `wss://` or `ws://` for its scheme, plus `/v1/ws`. [list] is that one
  /// relay, with id `custom`, the base as its name and every code symbol; nothing is ever fetched. Throws
  /// [PartyException] `invalid_message` when [baseUrl], without trailing `/`s, isn't lower-case `https://` or
  /// `http://` followed by a host, or its WebSocket URL isn't printable ASCII without `?` or `#`.
  factory Relays.at(String baseUrl) {
    var base = baseUrl;
    while (base.endsWith('/')) {
      base = base.substring(0, base.length - 1);
    }
    // `https://` becomes `wss://`, `http://` becomes `ws://`.
    final url = _base.hasMatch(base) ? '${base.replaceFirst('http', 'ws')}/v1/ws' : '';
    if (!isRelayUrl(url)) {
      throw PartyException(ErrorCode.invalidMessage, 'not an http:// or https:// base URL: $baseUrl');
    }
    return Relays._(List.unmodifiable([Relay(id: 'custom', name: base, url: url, prefixes: codeAlphabet)]), null);
  }

  /// A lower-case `https://` or `http://` and a host: something before the first `/`, `?` or `#`.
  static final _base = RegExp(r'^https?://[^/?#]');

  /// The only source of the public list.
  static const String publicSource = 'https://openwatch.party/relays.json';
  static const Duration httpTimeout = Duration(seconds: 5);
  static const Duration refreshAge = Duration(hours: 24);

  /// Null for a self-hosted relay, which never fetches.
  final HttpGet? _httpGet;
  List<Relay> _list;
  bool _background = false;
  Stopwatch? _sinceRefresh;

  /// The list in use.
  List<Relay> get list => _list;

  /// The relay that owns [code] (normalized as `join` does), or null for an invalid code or a symbol no relay owns.
  Relay? relayFor(String code) {
    final normalized = normalizeCode(code);
    return normalized == null ? null : routeCode(normalized, _list);
  }

  /// Fetches the public list now (SDK.md §3.3). False when the source gave no valid list; the list in use stays then.
  /// A self-hosted relay fetches nothing and returns true.
  Future<bool> refresh() async {
    final httpGet = _httpGet;
    return httpGet == null || await _fetch(httpGet);
  }

  /// One GET of [publicSource]. Each refresh makes its own, so a hanging one delays no other (SDK.md §3.3).
  Future<bool> _fetch(HttpGet httpGet) async {
    try {
      _list = parseRelayList(jsonDecode(await _get(httpGet, Uri.parse(publicSource))));
    } on Object {
      return false;
    }
    _sinceRefresh = Stopwatch()..start();
    return true;
  }

  /// [httpGet] within SDK.md §3.4: cancelled and given up 5 s after the start; a body over [maxHttpBodyBytes] fails.
  Future<String> _get(HttpGet httpGet, Uri url) async {
    final cancel = CancellationSource();
    final timer = Timer(httpTimeout, cancel.cancel);
    try {
      final body = await Future.any([
        Future.sync(() => httpGet(url, cancel)),
        cancel.whenCancelled.then<String>((_) => throw TimeoutException('no answer from $url', httpTimeout)),
      ]);
      if (body.length > maxHttpBodyBytes || utf8.encode(body).length > maxHttpBodyBytes) {
        throw FormatException('body over $maxHttpBodyBytes bytes from $url');
      }
      return body;
    } finally {
      timer.cancel();
    }
  }
}

/// What a party needs from [Relays] beyond the public API.
extension RelaysInternal on Relays {
  /// Called when a party joined: the public list refreshes in the background when this object never refreshed, or
  /// last did 24 hours ago, and no background refresh runs.
  void connected() {
    final httpGet = _httpGet;
    final since = _sinceRefresh;
    if (httpGet == null || _background || (since != null && since.elapsed < Relays.refreshAge)) return;
    _background = true;
    unawaited(_fetch(httpGet).whenComplete(() => _background = false));
  }
}

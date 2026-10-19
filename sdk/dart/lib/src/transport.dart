import 'package:web_socket/web_socket.dart';

import 'cancellation.dart';
import 'platform/http_get_stub.dart'
    if (dart.library.js_interop) 'platform/http_get_web.dart'
    if (dart.library.io) 'platform/http_get_io.dart' as platform;

/// Opens a WebSocket to a relay's `/v1/ws` URL. Pass your own to control DNS, TLS trust or proxies; wrap a `dart:io`
/// socket with `IOWebSocket.fromWebSocket` from `package:web_socket/io_web_socket.dart`.
typedef WebSocketConnector = Future<WebSocket> Function(Uri url);

/// Fetches [url] and returns the body of a 200 answer as text; throws otherwise (SDK.md §3.4).
///
/// It must not follow redirects (a 3xx answer fails), must fail a body over [maxHttpBodyBytes], and must stop and
/// close the request when [cancel] fires. The SDK fires it 5 s after the start and gives up on the answer then.
typedef HttpGet = Future<String> Function(Uri url, Cancellation cancel);

/// The most body bytes the SDK reads from an HTTP answer.
const int maxHttpBodyBytes = 65536;

/// `WebSocket.connect` of `package:web_socket`: `dart:io` on native platforms, the browser's WebSocket on the web.
Future<WebSocket> defaultConnect(Uri url) => WebSocket.connect(url);

/// `dart:io`'s `HttpClient` on native platforms, `fetch` on the web; both without redirects, closed when [cancel]
/// fires or the body passes [maxHttpBodyBytes].
Future<String> defaultHttpGet(Uri url, Cancellation cancel) => platform.defaultHttpGet(url, cancel);

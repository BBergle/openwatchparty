/// One relay of a relay list (SPEC.md §5.2).
///
/// `prefixes` holds the first symbols of the room codes the relay owns.
public struct Relay: Hashable, Sendable {
    public let id: String
    public let name: String
    public let url: String
    public let prefixes: String

    public init(id: String, name: String, url: String, prefixes: String) {
        self.id = id
        self.name = name
        self.url = url
        self.prefixes = prefixes
    }
}

/// The app's HTTP GET for relay list refreshes (SDK.md §3.4, §16.8).
///
/// The function returns the body of a status 200 answer as text. It throws for any other status or for a failed
/// connection.
///
/// It must follow no redirect, and stop when its task is cancelled. The SDK gives up after 5 seconds regardless, and
/// refuses a body over 65536 bytes of UTF-8.
public typealias HTTPGet = @MainActor (_ url: String) async throws -> String

/// The relays a party creates and joins rooms on (SDK.md §3).
///
/// A `Relays` holds either the public relays, refreshed only from `https://openwatch.party/relays.json`, or one
/// self-hosted relay.
///
/// An app keeps one `Relays` and passes it to every party. The list and its refresh schedule belong to the `Relays`.
@MainActor
public final class Relays {
    /// The GET of the public list's refreshes; nil for a self-hosted relay, which has no source.
    private let get: HTTPGet?
    /// Measures the age of the last successful refresh and times the GETs.
    private let clock: PartyClock
    private var current: [Relay]
    /// `clock` time of the last successful refresh of any kind; nil before the first.
    private var lastRefreshMs: Int64?
    private var backgroundRunning = false

    private init(list: [Relay], get: HTTPGet?, clock: PartyClock) {
        current = list
        self.get = get
        self.clock = clock
    }

    /// The public relays, starting from the list compiled into the SDK.
    ///
    /// - Parameters:
    ///   - httpGet: The GET for refreshes. The default uses `URLSession` without redirects.
    ///   - clock: Local time and timers. Tests pass a virtual clock.
    public static func `public`(httpGet: HTTPGet? = nil, clock: PartyClock = SystemClock()) -> Relays {
        Relays(
            list: parseRelayList(JSON.parse(publicRelaysJSON))!,
            get: httpGet ?? { try await urlSessionGet($0) },
            clock: clock
        )
    }

    /// One self-hosted relay at `baseUrl`, used for every room.
    ///
    /// `baseUrl` is the relay's base URL, such as `https://relay.example.com` or `http://192.168.1.5:8080/owp`. The
    /// WebSocket URL is the base with `https://` replaced by `wss://`, or `http://` by `ws://`, plus `/v1/ws`.
    ///
    /// Nothing is fetched, and `refresh()` does nothing.
    ///
    /// - Throws: `PartyError` with code `invalid_message` when `baseUrl`, without trailing slashes, isn't `https://` or
    ///   `http://` followed by a host. It also throws when the result is a WebSocket URL that SDK.md §3.2 refuses.
    public static func at(_ baseUrl: String) throws -> Relays {
        var base = Substring(baseUrl)
        while base.hasSuffix("/") { base = base.dropLast() }
        let url: String
        if base.hasPrefix("https://") {
            url = "wss://" + base.dropFirst(8) + "/v1/ws"
        } else if base.hasPrefix("http://") {
            url = "ws://" + base.dropFirst(7) + "/v1/ws"
        } else {
            throw PartyError(.invalidMessage, "not an http(s) base URL: \(baseUrl)")
        }
        guard isRelayUrl(url) else { throw PartyError(.invalidMessage, "not an http(s) base URL with a host: \(baseUrl)") }
        let relay = Relay(id: "custom", name: String(base), url: url, prefixes: Owp.alphabet)
        return Relays(list: [relay], get: nil, clock: SystemClock())
    }

    /// The relay list in use.
    public var list: [Relay] { current }

    /// The relay that owns `code`, or nil.
    ///
    /// The function normalizes `code` first. It returns nil for an invalid code, or for a code whose first symbol no
    /// relay owns. It never refreshes the list.
    public func relayFor(_ code: String) -> Relay? { normalizeCode(code).flatMap { route($0, current) } }

    /// Forces a refresh of the public list (SDK.md §3.3).
    ///
    /// A self-hosted relay has nothing to refresh, so the call returns true at once.
    ///
    /// - Returns: False when the public list's source gave no valid list.
    @discardableResult
    public func refresh() async -> Bool {
        guard get != nil else { return true }
        return await fetch()
    }

    /// The background refresh of SDK.md §3.3 when a party gets `joined`: due when no refresh of any kind has succeeded
    /// yet, or the last success is 24 h old, and none is running already. Never throws, and never delays `create`,
    /// `join` or `refresh`.
    func refreshInBackground() async {
        if get == nil || backgroundRunning { return }
        if let last = lastRefreshMs, clock.nowMs() - last < Owp.listRefreshMs { return }
        backgroundRunning = true
        defer { backgroundRunning = false }
        await fetch()
    }

    /// One GET of the public list; a valid list replaces the one in use.
    @discardableResult
    private func fetch() async -> Bool {
        guard let doc = await getJSON(Owp.publicListUrl), let relays = parseRelayList(doc) else { return false }
        current = relays
        lastRefreshMs = clock.nowMs()
        return true
    }

    /// A GET as SDK.md §3.4 allows it: given up after 5 s, at most 65536 bytes of UTF-8 JSON. Nil when any of that,
    /// or the GET itself, fails.
    private func getJSON(_ url: String) async -> JSON? {
        guard let get, case .value(let body) = await clock.race(timeoutMs: Owp.httpTimeoutMs, { try await get(url) }),
              utf8Length(body) <= Owp.httpMaxBodyBytes else { return nil }
        return JSON.parse(body)
    }
}

/// The `relays` of a public list document (fetched or embedded), validated as SDK.md §3.2 says; nil when invalid.
func parseRelayList(_ doc: JSON?) -> [Relay]? {
    guard let items = doc?["relays"]?.array, !items.isEmpty else { return nil }
    var ids = Set<String>()
    var owned = Set<Unicode.Scalar>()
    var relays: [Relay] = []
    for item in items {
        guard let r = item.object,
              let id = r["id"]?.string, isRelayId(id), ids.insert(id).inserted,
              let name = r["name"]?.text, textProblem(name.value, min: 1, max: 64, unpaired: name.unpaired) == nil,
              let url = r["url"]?.string, isRelayUrl(url), url.hasPrefix("wss://"),
              let prefixes = r["prefixes"]?.string, !prefixes.isEmpty else { return nil }
        for c in prefixes.unicodeScalars {
            guard Owp.alphabet.unicodeScalars.contains(c), owned.insert(c).inserted else { return nil }
        }
        relays.append(Relay(id: id, name: name.value, url: url, prefixes: prefixes))
    }
    return relays
}

private func isRelayId(_ id: String) -> Bool {
    let scalars = id.unicodeScalars
    guard (1...32).contains(scalars.count), scalars.first != "-" else { return false }
    return scalars.allSatisfy { (0x61...0x7A).contains($0.value) || (0x30...0x39).contains($0.value) || $0 == "-" }
}

/// A relay URL as SDK.md §3.2 shapes it: `wss://` or `ws://`, then printable ASCII other than `?` and `#`, with a host,
/// ending in `/v1/ws`.
func isRelayUrl(_ url: String) -> Bool {
    let rest: Substring
    if url.hasPrefix("wss://") {
        rest = url.dropFirst(6)
    } else if url.hasPrefix("ws://") {
        rest = url.dropFirst(5)
    } else {
        return false
    }
    return rest.unicodeScalars.allSatisfy({ (0x21...0x7E).contains($0.value) && $0 != "?" && $0 != "#" })
        && rest.hasSuffix("/v1/ws") && rest.first != "/"
}

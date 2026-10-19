import Foundation
import Testing
@testable import OpenWatchParty

/// The relay list's sources and HTTP rules (SDK.md §3), with the GET injected, in virtual time.
@MainActor
@Suite(.timeLimit(.minutes(1)))
struct RelayListTests {
    private func relay(_ id: String, _ url: String, _ prefixes: String) -> String {
        #"{"id":"\#(id)","name":"\#(id.uppercased())","url":"\#(url)","prefixes":"\#(prefixes)"}"#
    }

    private func list(_ relays: String...) -> String { #"{"relays":[\#(relays.joined(separator: ","))]}"# }

    /// A GET that records every URL it was asked for and answers `answer`.
    private func server(_ asked: Box<[String]>, _ answer: @escaping @MainActor (String) async throws -> String) -> HTTPGet {
        { url in
            asked.value.append(url)
            return try await answer(url)
        }
    }

    @Test func thePublicListComesOnlyFromOpenwatchParty() async throws {
        let asked = Box<[String]>([])
        let x = list(relay("x", "wss://x.example/v1/ws", Owp.alphabet))
        let relays = Relays.public(httpGet: server(asked) { _ in x }, clock: VirtualClock())
        #expect(await relays.refresh())
        #expect(asked.value == ["https://openwatch.party/relays.json"])
        #expect(relays.list.map(\.id) == ["x"])
    }

    @Test func aPublicListWithAWsUrlIsRefused() async {
        let x = list(relay("x", "ws://x.example/v1/ws", Owp.alphabet))
        let relays = Relays.public(httpGet: { _ in x }, clock: VirtualClock())
        #expect(await !relays.refresh())
        #expect(relays.list.map(\.id) == ["na", "eu", "asia"], "nothing changed")
    }

    @Test func theBackgroundRefreshIsDueUntilARefreshSucceedsAndThenEvery24Hours() async {
        let asked = Box<[String]>([])
        let answers = Box(false)
        let x = list(relay("x", "wss://x.example/v1/ws", Owp.alphabet))
        let clock = VirtualClock()
        let relays = Relays.public(httpGet: server(asked) { _ in
            guard answers.value else { throw TestFailure("404") }
            return x
        }, clock: clock)
        await relays.refreshInBackground()
        #expect(asked.value.count == 1, "the first party connecting refreshes")
        await clock.advance(by: 1)
        await relays.refreshInBackground()
        #expect(asked.value.count == 2, "a failed refresh doesn't count")
        answers.value = true
        await clock.advance(by: 1)
        #expect(await relays.refresh())
        await relays.refreshInBackground()
        #expect(asked.value.count == 3, "a forced refresh counts")
        await clock.advance(by: Owp.listRefreshMs - 1)
        await relays.refreshInBackground()
        #expect(asked.value.count == 3)
        await clock.advance(by: 1)
        await relays.refreshInBackground()
        #expect(asked.value.count == 4)
        await clock.advance(by: 1)
        await relays.refreshInBackground()
        #expect(asked.value.count == 4, "a background refresh counts")
        #expect(Set(asked.value) == [Owp.publicListUrl])
    }

    @Test func aSelfHostedRelayIsTheOneRelayForEveryCode() async throws {
        let cases = [
            ("http://127.0.0.1:1/owp/", "http://127.0.0.1:1/owp", "ws://127.0.0.1:1/owp/v1/ws"),
            ("https://relay.example.com//", "https://relay.example.com", "wss://relay.example.com/v1/ws"),
        ]
        for (base, name, url) in cases {
            let relays = try Relays.at(base)
            #expect(relays.list == [Relay(id: "custom", name: name, url: url, prefixes: Owp.alphabet)])
            #expect(relays.relayFor("k7m-q2x") == relays.list[0])
            #expect(relays.relayFor("2ABCDE") == relays.list[0])
            #expect(await relays.refresh())
            #expect(relays.list.count == 1)
        }
    }

    @Test func aBaseThatIsNotAnHttpUrlWithAHostThrowsInvalidMessage() throws {
        let bases = [
            "", "relay.example.com", "ftp://relay.example.com", "HTTPS://relay.example.com", "https://", "https:///owp",
            "http://?x", "https://#x", "https://relay.example.com/owp?x", "https://relay example.com",
        ]
        for base in bases {
            expectPartyError(.invalidMessage) { _ = try Relays.at(base) }
        }
    }

    @Test func aBodyOfAtMost65536BytesOfUtf8IsReadAlsoFromAnAppFetcher() async {
        let x = relay("x", "wss://x.example/v1/ws", Owp.alphabet)
        // "é" is 2 bytes of UTF-8: the limit counts bytes, not characters.
        func padded(_ size: Int) -> String {
            let empty = #"{"relays":[\#(x)],"pad":"é"}"#
            return #"{"relays":[\#(x)],"pad":"é\#(String(repeating: "p", count: size - utf8Length(empty)))"}"#
        }
        let exact = padded(Owp.httpMaxBodyBytes)
        #expect(await Relays.public(httpGet: { _ in exact }, clock: VirtualClock()).refresh())
        let tooLong = padded(Owp.httpMaxBodyBytes + 1)
        #expect(await !Relays.public(httpGet: { _ in tooLong }, clock: VirtualClock()).refresh())
    }

    @Test func aFailedGetOrABodyThatIsNotJsonFails() async {
        #expect(await !Relays.public(httpGet: { _ in throw TestFailure("500") }, clock: VirtualClock()).refresh())
        #expect(await !Relays.public(httpGet: { _ in "relays: x" }, clock: VirtualClock()).refresh())
    }

    @Test func aGetIsAbortedAfter5SecondsEvenWhenTheFetcherIgnoresCancellation() async throws {
        let clock = VirtualClock()
        let started = Box(false)
        let cancelled = Box(false)
        let never = Latch<Void>()
        let relays = Relays.public(httpGet: { _ in
            started.value = true
            await withTaskCancellationHandler {
                await never.wait() // ignores the cancellation
            } onCancel: {
                Task { @MainActor in cancelled.value = true }
            }
            return ""
        }, clock: clock)
        let refreshing = launch { await relays.refresh() }
        await clock.advance(by: Owp.httpTimeoutMs - 1)
        #expect(started.value)
        #expect(!refreshing.isOpen)
        await clock.advance(by: 1)
        #expect(try await refreshing.wait().get() == false)
        #expect(clock.now == Owp.httpTimeoutMs)
        #expect(cancelled.value, "the fetcher was told to stop")
        never.open()
    }
}

#if os(macOS) || os(Linux)
#if canImport(Glibc)
import Glibc
#else
import Darwin
#endif

/// The default `urlSessionGet` (SDK.md §3.4) against a local HTTP server on port 18390.
@Suite(.serialized, .timeLimit(.minutes(1)))
struct DefaultHTTPGetTests {
    private static let x = #"{"relays":[{"id":"x","name":"X","url":"wss://x.example/v1/ws","prefixes":"A"}],"pad":""#

    private static func body(_ size: Int) -> Data {
        Data((x + String(repeating: "p", count: size - x.utf8.count - 2) + #""}"#).utf8)
    }

    private static func answer(_ path: String, port: UInt16) -> (status: String, headers: String, body: Data) {
        switch path {
        case "/exact": ("200 OK", "", body(65536))
        case "/over": ("200 OK", "", body(65537))
        case "/redirect": ("302 Found", "Location: http://127.0.0.1:\(port)/exact\r\n", Data())
        case "/error": ("500 Internal Server Error", "", body(100))
        case "/created": ("201 Created", "", body(100))
        case "/not-utf8": ("200 OK", "", Data(x.utf8) + Data([0xFF]) + Data(#""}"#.utf8))
        default: ("404 Not Found", "", Data())
        }
    }

    private static let port: UInt16 = 18390

    private func start() throws -> HTTPServer { try HTTPServer(port: Self.port) { Self.answer($0, port: Self.port) } }

    private func url(_ path: String) -> String { "http://127.0.0.1:\(Self.port)\(path)" }

    @Test @MainActor func aBodyOfAtMost65536BytesIsRead() async throws {
        let server = try start()
        defer { server.stop() }
        let body = try await urlSessionGet(url("/exact"))
        #expect(body.utf8.count == 65536)
        await #expect(throws: PartyError.self) { try await urlSessionGet(url("/over")) }
    }

    @Test @MainActor func aRedirectIsNotFollowed() async throws {
        let server = try start()
        defer { server.stop() }
        await #expect(throws: PartyError.self) { try await urlSessionGet(url("/redirect")) }
        #expect(server.paths == ["/redirect"])
    }

    @Test @MainActor func onlyStatus200WithUtf8Succeeds() async throws {
        let server = try start()
        defer { server.stop() }
        for path in ["/error", "/created", "/not-utf8"] {
            await #expect(throws: PartyError.self, "\(path)") { try await urlSessionGet(url(path)) }
        }
    }
}

/// An HTTP/1.1 server on 127.0.0.1 that answers each request on a connection of its own, then closes it.
final class HTTPServer: @unchecked Sendable {
    private let fd: Int32
    private let port: UInt16
    private let lock = NSLock()
    private var stopping = false
    private var asked: [String] = []
    private let finished = DispatchSemaphore(value: 0)

    /// The request paths so far, in order.
    var paths: [String] { lock.withLock { asked } }

    init(port: UInt16, answer: @escaping @Sendable (String) -> (status: String, headers: String, body: Data)) throws {
        #if canImport(Glibc)
        fd = socket(AF_INET, Int32(SOCK_STREAM.rawValue), 0)
        #else
        fd = socket(AF_INET, SOCK_STREAM, 0)
        #endif
        self.port = port
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &on, socklen_t(MemoryLayout<Int32>.size))
        var addr = Self.loopback(port)
        let bound = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        guard bound == 0, listen(fd, 8) == 0 else {
            close(fd)
            throw TestFailure("port \(port) is taken")
        }
        let listener = fd
        Thread.detachNewThread { [self] in
            while true {
                let client = accept(listener, nil, nil)
                if lock.withLock({ stopping }) || client < 0 {
                    if client >= 0 { close(client) }
                    break
                }
                serve(client, answer)
            }
            finished.signal()
        }
    }

    private static func loopback(_ port: UInt16) -> sockaddr_in {
        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = port.bigEndian
        addr.sin_addr = in_addr(s_addr: inet_addr("127.0.0.1"))
        return addr
    }

    private func serve(_ client: Int32, _ answer: (String) -> (status: String, headers: String, body: Data)) {
        defer { close(client) }
        var request = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while request.range(of: Data("\r\n\r\n".utf8)) == nil {
            let n = read(client, &buffer, buffer.count)
            if n <= 0 { return }
            request.append(contentsOf: buffer[0..<n])
        }
        let line = String(decoding: request, as: UTF8.self).split(separator: "\r\n").first ?? ""
        let path = line.split(separator: " ").dropFirst().first.map(String.init) ?? "/"
        lock.withLock { asked.append(path) }
        let (status, headers, body) = answer(path)
        var response = Data("HTTP/1.1 \(status)\r\nContent-Length: \(body.count)\r\n\(headers)Connection: close\r\n\r\n".utf8)
        response.append(body)
        response.withUnsafeBytes { raw in
            var sent = 0
            while sent < raw.count {
                let n = write(client, raw.baseAddress! + sent, raw.count - sent)
                if n <= 0 { return }
                sent += n
            }
        }
    }

    /// Stops accepting and waits for the accept loop to end; wakes it with a connection of its own.
    func stop() {
        lock.withLock { stopping = true }
        #if canImport(Glibc)
        let wake = socket(AF_INET, Int32(SOCK_STREAM.rawValue), 0)
        #else
        let wake = socket(AF_INET, SOCK_STREAM, 0)
        #endif
        var addr = Self.loopback(port)
        _ = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(wake, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        close(wake)
        finished.wait()
        close(fd)
    }
}
#endif

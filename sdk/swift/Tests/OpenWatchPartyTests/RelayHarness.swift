// Runs a real relay, a TCP proxy and real-time players for IntegrationTests. Only macOS and Linux can start processes.
#if os(macOS) || os(Linux)
import Foundation
import Testing
@testable import OpenWatchParty
#if canImport(Glibc)
import Glibc
#else
import Darwin
#endif

// MARK: The relay binary

/// relay/ of this repository.
private let relaySource = URL(fileURLWithPath: #filePath)
    .deletingLastPathComponent() // OpenWatchPartyTests
    .deletingLastPathComponent() // Tests
    .deletingLastPathComponent() // swift
    .deletingLastPathComponent() // sdk
    .deletingLastPathComponent()
    .appendingPathComponent("relay")

/// Gives a suite the relay binary: `OWP_RELAY_BIN` when it is set, else relay/ built with `go build` into a temporary
/// directory that is removed after the suite.
struct RelayBinaryTrait: SuiteTrait, TestScoping {
    /// The binary while the suite runs.
    @TaskLocal static var path: String?

    func provideScope(
        for test: Test,
        testCase: Test.Case?,
        performing function: @Sendable () async throws -> Void
    ) async throws {
        if let prebuilt = ProcessInfo.processInfo.environment["OWP_RELAY_BIN"], !prebuilt.isEmpty {
            try await Self.$path.withValue(prebuilt) { try await function() }
            return
        }
        let dir = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let binary = dir.appendingPathComponent("owp-relay").path
        let log = dir.appendingPathComponent("go-build.log")
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = ["go", "build", "-o", binary, "./cmd/owp-relay"]
        process.currentDirectoryURL = relaySource
        let output = try writableFile(log)
        process.standardOutput = output
        process.standardError = output
        try await runToEnd(process)
        try? output.close()
        guard process.terminationStatus == 0 else {
            let text = (try? String(contentsOf: log, encoding: .utf8)) ?? ""
            throw TestFailure("go build in \(relaySource.path) failed with status \(process.terminationStatus):\n\(text)")
        }
        try await Self.$path.withValue(binary) { try await function() }
    }
}

extension Trait where Self == RelayBinaryTrait {
    /// The suite starts relays: it builds relay/ first, or takes `OWP_RELAY_BIN`.
    static var relayBinary: Self { Self() }
}

/// Runs `process` and returns once it has exited.
private func runToEnd(_ process: Process) async throws {
    try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, any Error>) in
        process.terminationHandler = { _ in continuation.resume() }
        do {
            try process.run()
        } catch {
            continuation.resume(throwing: error)
        }
    }
}

func makeTemporaryDirectory() throws -> URL {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("owp-swift-test-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    return dir
}

private func writableFile(_ url: URL) throws -> FileHandle {
    guard FileManager.default.createFile(atPath: url.path, contents: nil) else {
        throw TestFailure("could not create \(url.path)")
    }
    return try FileHandle(forWritingTo: url)
}

// MARK: Relays and proxies

/// Ports 18350–18389 belong to these tests. Each test stops what it started, so the ports are used again in turn.
@MainActor
enum RelayPorts {
    private static var next = 0

    static func take() -> Int {
        defer { next = (next + 1) % 40 }
        return 18350 + next
    }
}

/// A relay on 127.0.0.1:`port` in test mode (timers × 0.1) with the per-IP limits raised, so every client of the tests
/// can share one IP: the flags Kotlin's and Dart's integration tests start it with. Its output goes to files in `dir`.
@MainActor
final class RelayProcess {
    let port: Int
    private let process: Process

    private init(port: Int, process: Process) {
        self.port = port
        self.process = process
    }

    /// Starts the relay and returns once it printed its `listening` line, within 10 s.
    static func start(binary: String, port: Int, dir: URL) async throws -> RelayProcess {
        let out = dir.appendingPathComponent("relay-\(port).out")
        let err = dir.appendingPathComponent("relay-\(port).err")
        let process = Process()
        process.executableURL = URL(fileURLWithPath: binary)
        process.arguments = [
            "--addr", "127.0.0.1:\(port)",
            "--admin-addr", "",
            "--timer-scale", "0.1",
            "--max-connections-per-ip", "1000",
            "--max-rooms-per-ip", "1000",
            "--message-burst", "1000",
            "--messages-per-second", "1000",
            "--join-burst", "1000",
            "--joins-per-second", "1000",
        ]
        process.standardInput = FileHandle.nullDevice
        let stdout = try writableFile(out)
        let stderr = try writableFile(err)
        defer {
            try? stdout.close()
            try? stderr.close()
        }
        process.standardOutput = stdout
        process.standardError = stderr
        try process.run()
        let relay = RelayProcess(port: port, process: process)
        do {
            try await relay.listening(out, err)
        } catch {
            if process.isRunning { kill(process.processIdentifier, SIGKILL) }
            throw error
        }
        return relay
    }

    private func listening(_ out: URL, _ err: URL) async throws {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: .seconds(10))
        while true {
            let text = (try? String(contentsOf: out, encoding: .utf8)) ?? ""
            if let end = text.firstIndex(of: "\n") {
                if text[..<end].hasPrefix("listening") { return }
                throw TestFailure("the relay on port \(port) printed \(text[..<end]) instead of listening")
            }
            if !process.isRunning || clock.now >= deadline {
                let errors = (try? String(contentsOf: err, encoding: .utf8)) ?? ""
                throw TestFailure("the relay on port \(port) didn't start:\n\(errors)")
            }
            try await Task.sleep(for: .milliseconds(20))
        }
    }

    /// SIGTERM: the relay closes every connection with 1001 and exits; SIGKILL when it is still running after 10 s.
    /// Its waits sleep, so call it from a task that isn't cancelled.
    func stop() async {
        guard process.isRunning else { return }
        process.terminate()
        if await exited(within: .seconds(10)) { return }
        kill(process.processIdentifier, SIGKILL)
        _ = await exited(within: .seconds(10))
    }

    private func exited(within limit: Duration) async -> Bool {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: limit)
        while process.isRunning {
            if clock.now >= deadline { return false }
            try? await Task.sleep(for: .milliseconds(20))
        }
        return true
    }
}

#if canImport(Glibc)
private let streamSocket = Int32(SOCK_STREAM.rawValue)
private let sendFlags = Int32(MSG_NOSIGNAL)
#else
private let streamSocket = SOCK_STREAM
private let sendFlags: Int32 = 0
#endif

/// 127.0.0.1:`port`.
private func loopback(_ port: Int) -> sockaddr_in {
    var address = sockaddr_in()
    #if canImport(Darwin)
    address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
    #endif
    address.sin_family = sa_family_t(AF_INET)
    address.sin_port = in_port_t(port).bigEndian
    address.sin_addr = in_addr(s_addr: inet_addr("127.0.0.1"))
    return address
}

/// A socket that never raises SIGPIPE: on Darwin by its option, on Linux by `sendFlags`.
private func newSocket() -> Int32 {
    let fd = socket(AF_INET, streamSocket, 0)
    #if canImport(Darwin)
    if fd >= 0 {
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
    }
    #endif
    return fd
}

private func closeSocket(_ fd: Int32) {
    _ = close(fd)
}

private func errorText() -> String { String(cString: strerror(errno)) }

/// A TCP proxy on 127.0.0.1:`port` to a relay on 127.0.0.1:`targetPort`, whose connections the test can cut as a
/// network failure would: no WebSocket close, no close code. Blocking sockets with a thread for each direction, as
/// Kotlin's `TcpProxy`.
final class TcpProxy: @unchecked Sendable {
    let port: Int
    private let targetPort: Int
    private let server: Int32
    private let lock = NSLock()
    /// The proxied sockets still open. This and the flags below are guarded by `lock`.
    private var open: Set<Int32> = []
    private var stopping = false
    private var stopped = false

    /// Both directions of one proxied connection; the one that ends last closes its sockets.
    private final class Connection: @unchecked Sendable {
        var ended = 0
    }

    init(port: Int, targetPort: Int) throws {
        let fd = newSocket()
        guard fd >= 0 else { throw TestFailure("socket: \(errorText())") }
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &on, socklen_t(MemoryLayout<Int32>.size))
        let bound = withUnsafePointer(to: loopback(port)) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        guard bound == 0, listen(fd, 16) == 0 else {
            let text = errorText()
            closeSocket(fd)
            throw TestFailure("a proxy can't listen on port \(port): \(text)")
        }
        self.port = port
        self.targetPort = targetPort
        server = fd
        Thread.detachNewThread { [self] in accepting() }
    }

    private func locked<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }

    private func accepting() {
        while !locked({ stopping }) {
            var ready = pollfd(fd: server, events: Int16(POLLIN), revents: 0)
            guard poll(&ready, 1, 50) > 0 else { continue }
            let client = accept(server, nil, nil)
            guard client >= 0 else { continue }
            guard let upstream = connectToTarget() else {
                closeSocket(client)
                continue
            }
            let connection = Connection()
            locked { open.formUnion([client, upstream]) }
            Thread.detachNewThread { [self] in pipe(client, upstream, connection) }
            Thread.detachNewThread { [self] in pipe(upstream, client, connection) }
        }
        closeSocket(server)
        locked { stopped = true }
    }

    private func connectToTarget() -> Int32? {
        let fd = newSocket()
        guard fd >= 0 else { return nil }
        let connected = withUnsafePointer(to: loopback(targetPort)) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        if connected != 0 {
            closeSocket(fd)
            return nil
        }
        return fd
    }

    /// Copies `from` to `to` until either ends or the proxy cuts them.
    private func pipe(_ from: Int32, _ to: Int32, _ connection: Connection) {
        var buffer = [UInt8](repeating: 0, count: 65536)
        copying: while true {
            let n = buffer.withUnsafeMutableBytes { recv(from, $0.baseAddress, $0.count, 0) }
            if n < 0 && errno == EINTR { continue }
            if n <= 0 { break }
            var sent = 0
            while sent < n {
                let m = buffer.withUnsafeBytes { send(to, $0.baseAddress! + sent, n - sent, sendFlags) }
                if m < 0 && errno == EINTR { continue }
                if m <= 0 { break copying }
                sent += m
            }
        }
        locked {
            // The other direction ends too.
            _ = shutdown(from, Int32(SHUT_RDWR))
            _ = shutdown(to, Int32(SHUT_RDWR))
            connection.ended += 1
            if connection.ended == 2 {
                open.subtract([from, to])
                closeSocket(from)
                closeSocket(to)
            }
        }
    }

    /// Drops every proxied connection at once; new ones are still accepted.
    func cut() {
        locked {
            for fd in open { _ = shutdown(fd, Int32(SHUT_RDWR)) }
        }
    }

    /// Stops accepting and cuts every connection; the port is free once it returns.
    func stop() async {
        locked { stopping = true }
        while !locked({ stopped }) { try? await Task.sleep(for: .milliseconds(10)) }
        cut()
    }
}

// MARK: Players and parties

/// A player that plays in real time: commands take effect at once.
@MainActor
final class RealTimePlayer: PlayerAdapter {
    private let clock: any PartyClock
    let durationMs: Int64?
    let canSetRate = true
    let buffering = false
    let firstFrameShown = true
    private(set) var paused = true
    private(set) var rate = 1.0
    private var base: Int64 = 0
    private var baseAt: Int64

    init(clock: any PartyClock, durationMs: Int64?) {
        self.clock = clock
        self.durationMs = durationMs
        baseAt = clock.nowMs()
    }

    var positionMs: Int64 {
        if paused { return base }
        let position = base + Int64(Double(clock.nowMs() - baseAt) * rate)
        return durationMs.map { min(position, $0) } ?? position
    }

    func play() async throws {
        rebase()
        paused = false
    }

    func pause() async throws {
        rebase()
        paused = true
    }

    func seek(to positionMs: Int64) async throws {
        base = positionMs
        baseAt = clock.nowMs()
    }

    func setRate(_ rate: Double) async throws {
        rebase()
        self.rate = rate
    }

    private func rebase() {
        base = positionMs
        baseAt = clock.nowMs()
    }
}

/// Every event `party` emits from now on.
@MainActor
final class EventLog {
    private(set) var events: [any PartyEvent] = []

    init(_ party: WatchParty<String>) {
        let stream = party.events()
        Task { [weak self] in
            for await event in stream { self?.events.append(event) }
        }
    }

    func of<E: PartyEvent>(_ type: E.Type) -> [E] { events.compactMap { $0 as? E } }
}

/// One test's relays, proxies, parties and files, all stopped and removed after it (Kotlin's `RelayTest`). The parties
/// use the SDK's defaults: the `URLSession` transport and GET, and the system clock.
@MainActor
final class RelayHarness {
    let clock = SystemClock()
    private let dir: URL
    private var relays: [RelayProcess] = []
    private var proxies: [TcpProxy] = []
    private var parties: [WatchParty<String>] = []

    private init() throws {
        dir = try makeTemporaryDirectory()
    }

    /// Runs `body` with a new harness, then stops and removes everything it started, also after a failure or when the
    /// test's time limit cancelled it.
    static func run(_ body: (RelayHarness) async throws -> Void) async throws {
        let harness = try RelayHarness()
        do {
            try await body(harness)
        } catch {
            await harness.stopEverything()
            throw error
        }
        await harness.stopEverything()
    }

    private func stopEverything() async {
        // A task of its own, which a cancelled test doesn't cancel, so the waits for the processes work.
        await Task { [self] in
            for party in parties { party.close() }
            for proxy in proxies { await proxy.stop() }
            for relay in relays { await relay.stop() }
            try? FileManager.default.removeItem(at: dir)
        }.value
    }

    /// A fresh relay on the next port.
    func relay() async throws -> RelayProcess {
        guard let binary = RelayBinaryTrait.path else {
            throw TestFailure("no relay binary: the suite needs the .relayBinary trait")
        }
        let relay = try await RelayProcess.start(binary: binary, port: RelayPorts.take(), dir: dir)
        relays.append(relay)
        return relay
    }

    /// A proxy to the relay on `targetPort`, on the next port.
    func proxy(to targetPort: Int) throws -> TcpProxy {
        let proxy = try TcpProxy(port: RelayPorts.take(), targetPort: targetPort)
        proxies.append(proxy)
        return proxy
    }

    /// The relay, or proxy, on `port` as the parties' one relay.
    func relaysAt(_ port: Int) throws -> Relays {
        try Relays.at("http://127.0.0.1:\(port)")
    }

    func party(_ relays: Relays, _ name: String, _ resolver: Resolver<String> = Resolver { _ in [] }) throws -> WatchParty<String> {
        let party = try WatchParty<String>(relays: relays, client: "OwpSwiftTest/1.0", name: name, resolver: resolver)
        parties.append(party)
        return party
    }

    func record(_ party: WatchParty<String>) -> EventLog { EventLog(party) }

    /// Waits until `condition` holds, checking every 20 ms; fails after `timeoutMs` with `what`.
    func eventually(
        _ what: String,
        timeoutMs: Int64 = 10_000,
        sourceLocation: SourceLocation = #_sourceLocation,
        _ condition: () -> Bool
    ) async throws {
        let deadline = clock.nowMs() + timeoutMs
        while true {
            let met = condition()
            if met { return }
            if clock.nowMs() > deadline {
                try #require(met, "timed out waiting for: \(what)", sourceLocation: sourceLocation)
            }
            try await Task.sleep(for: .milliseconds(20))
        }
    }
}
#endif

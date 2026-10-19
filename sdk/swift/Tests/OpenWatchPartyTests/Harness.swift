import Foundation
import Testing
@testable import OpenWatchParty

// MARK: Virtual time

/// Lets the main actor run the work queued on it: the SDK's tasks all run there. Under a bound `Work` it returns
/// exactly once every task the SDK started is parked or done; otherwise after 100 turns.
@MainActor
func yieldToMainActor() async {
    guard let work = Work.current else {
        for _ in 0..<100 { await Task.yield() }
        return
    }
    var turns = 0
    while !work.isIdle {
        turns += 1
        if turns > 100_000 {
            Issue.record("the SDK's work never became idle: a task waits outside a Waiter, or one not started by spawn in one")
            return
        }
        await Task.yield()
    }
}

/// Virtual time for the SDK (SDK.md §16.9): timers run only when a test runs or advances the clock, in deadline order,
/// each followed by the main-actor work it starts.
@MainActor
final class VirtualClock: PartyClock {
    private(set) var now: Int64
    /// Every delay the SDK asked for, in order.
    private(set) var requested: [Int64] = []
    private var timers: [Timer] = []
    private var nextId = 0

    private struct Timer {
        let id: Int
        let at: Int64
        let action: @MainActor () -> Void
    }

    init(startMs: Int64 = 0) { now = startMs }

    func nowMs() -> Int64 { now }

    func schedule(afterMs delayMs: Int64, _ action: @escaping @MainActor () -> Void) -> @MainActor () -> Void {
        requested.append(delayMs)
        let id = nextId
        nextId += 1
        timers.append(Timer(id: id, at: now + max(0, delayMs), action: action))
        return { [weak self] in self?.timers.removeAll { $0.id == id } }
    }

    /// Runs the queued work and every timer due now until nothing is left, without moving time (Kotlin's
    /// `runCurrent`).
    func run() async {
        await yieldToMainActor()
        while let due = next(dueBy: now) {
            fire(due)
            await yieldToMainActor()
        }
    }

    /// Moves time forward by `ms`, running each timer due by then at its deadline.
    func advance(by ms: Int64) async {
        let target = now + ms
        await run()
        while let due = next(dueBy: target) {
            now = max(now, due.at)
            fire(due)
            await run()
        }
        now = target
        await run()
    }

    private func next(dueBy t: Int64) -> Timer? {
        timers.filter { $0.at <= t }.min { ($0.at, $0.id) < ($1.at, $1.id) }
    }

    /// Runs the queued work; while `done` is false, moves time to the next timer and runs it, at most `limitMs` ahead
    /// (Kotlin's `runTest` advances virtual time the same way while a test waits). Returns `done()`.
    @discardableResult
    func runUntil(limitMs: Int64 = 600_000, _ done: () -> Bool) async -> Bool {
        let limit = now + limitMs
        await run()
        while !done(), let due = next(dueBy: limit) {
            now = max(now, due.at)
            fire(due)
            await run()
        }
        return done()
    }

    private func fire(_ timer: Timer) {
        timers.removeAll { $0.id == timer.id }
        timer.action()
    }

    /// Sleeps `ms` of virtual time; throws `CancellationError` when the task is cancelled first.
    func sleep(_ ms: Int64) async throws {
        let latch = Latch<Bool>()
        let cancel = schedule(afterMs: ms) { latch.open(true) }
        let slept = await withTaskCancellationHandler {
            await latch.wait()
        } onCancel: {
            Task { @MainActor in latch.open(false) }
        }
        cancel()
        if !slept { throw CancellationError() }
    }

    /// Suspends until the task is cancelled, then throws `CancellationError`.
    func never<T>() async throws -> T {
        try await sleep(Int64.max / 4)
        throw CancellationError()
    }
}

/// A one-shot value on the main actor: the first `open` wins; `wait` returns it.
@MainActor
final class Latch<T> {
    private var value: T?
    private var waiting: [CheckedContinuation<Void, Never>] = []

    var isOpen: Bool { value != nil }

    func open(_ value: T) {
        guard self.value == nil else { return }
        self.value = value
        let waiting = waiting
        self.waiting = []
        for c in waiting { c.resume() }
    }

    func wait() async -> T {
        if value == nil { await withCheckedContinuation { waiting.append($0) } }
        return value!
    }
}

extension Latch where T == Void {
    func open() { open(()) }
}

/// Runs `op` in a task of its own; the latch opens with its result.
@MainActor
func launch<T>(_ op: @escaping @MainActor () async throws -> T) -> Latch<Result<T, any Error>> {
    let latch = Latch<Result<T, any Error>>()
    Task { @MainActor in
        do {
            latch.open(.success(try await op()))
        } catch {
            latch.open(.failure(error))
        }
    }
    return latch
}

/// A value the test's closures share.
@MainActor
final class Box<T> {
    var value: T

    init(_ value: T) { self.value = value }
}

/// A generator with a fixed seed (SplitMix64), so every run draws the same numbers.
struct SeededRandom: RandomNumberGenerator {
    private var state: UInt64

    init(seed: UInt64) { state = seed }

    mutating func next() -> UInt64 {
        state &+= 0x9E37_79B9_7F4A_7C15
        var z = state
        z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE5_E9B9
        z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
        return z ^ (z >> 31)
    }
}

// MARK: JSON

/// A token as the relay makes one: 43 base64url characters.
let token = String(repeating: "A", count: 43)

extension JSONValue {
    subscript(key: String) -> JSONValue? {
        if case .object(let o) = self { return o[key] }
        return nil
    }

    var string: String? {
        if case .string(let s) = self { return s }
        return nil
    }

    var int: Int64? {
        if case .number(let d) = self, d == d.rounded(.towardZero), d.magnitude <= 9_007_199_254_740_991 {
            return Int64(d)
        }
        return nil
    }

    var objectValue: [String: JSONValue]? {
        if case .object(let o) = self { return o }
        return nil
    }

    /// This object with `changes` merged in (nil removes a member).
    func with(_ changes: [String: JSONValue?]) -> JSONValue {
        var o = objectValue ?? [:]
        for (k, v) in changes { o[k] = v }
        return .object(o)
    }
}

func num(_ x: Int64) -> JSONValue { .number(Double(x)) }

/// Fails the test when `text` repeats a member name in any object (SDK.md §7.1).
func expectNoRepeatedNames(_ text: String, sourceLocation: SourceLocation = #_sourceLocation) {
    var stack: [(object: Bool, names: Set<String>, key: Bool)] = []
    let s = Array(text.unicodeScalars)
    var i = 0
    while i < s.count {
        switch s[i] {
        case "{":
            stack.append((true, [], true))
        case "[":
            stack.append((false, [], false))
        case "}", "]":
            stack.removeLast()
        case ",":
            if stack.last?.object == true { stack[stack.count - 1].key = true }
        case "\"":
            var string = String.UnicodeScalarView()
            i += 1
            while s[i] != "\"" {
                if s[i] == "\\" {
                    string.append(s[i])
                    i += 1
                }
                string.append(s[i])
                i += 1
            }
            if let top = stack.last, top.object, top.key {
                let name = String(string)
                #expect(!top.names.contains(name), "repeated member \(name) in \(text)", sourceLocation: sourceLocation)
                stack[stack.count - 1].names.insert(name)
                stack[stack.count - 1].key = false
            }
        default:
            break
        }
        i += 1
    }
}

struct TestFailure: Error, CustomStringConvertible {
    let description: String

    init(_ description: String) { self.description = description }
}

// MARK: A scripted relay

/// One WebSocket as the relay sees it. Pings get a pong at once with relay time = local time + 1 000 000. Every message
/// the client sends must be free of repeated member names.
@MainActor
final class FakeSocket: WebSocketConnection {
    let url: String
    private let clock: VirtualClock
    private var inbox: [String] = []
    private var waiting: CheckedContinuation<String?, Never>?
    private var ended = false
    private var code: Int?
    private var sent: [JSONValue] = []
    private(set) var closedByClient: Int?

    init(url: String, clock: VirtualClock) {
        self.url = url
        self.clock = clock
    }

    func receive() async -> String? {
        if !inbox.isEmpty { return inbox.removeFirst() }
        if ended { return nil }
        return await withCheckedContinuation { waiting = $0 }
    }

    func closeCode() async -> Int? { code }

    func send(_ text: String) {
        expectNoRepeatedNames(text)
        guard let message = try? JSONValue(parsing: text) else {
            Issue.record("the client sent no JSON: \(text)")
            return
        }
        guard message["type"]?.string == "ping" else {
            sent.append(message)
            return
        }
        guard let id = message["id"] else { return }
        reply(["type": "pong", "id": id, "serverTime": num(clock.now + 1_000_000)])
    }

    func close(code: Int) {
        closedByClient = code
        end()
    }

    func reply(_ message: JSONValue) { reply(text: message.description) }

    func reply(text: String) {
        guard !ended else { return }
        if let waiting {
            self.waiting = nil
            waiting.resume(returning: text)
        } else {
            inbox.append(text)
        }
    }

    /// The relay drops the connection, with `code` or without a close status.
    func drop(_ code: Int?) {
        self.code = code
        end()
    }

    private func end() {
        guard !ended else { return }
        ended = true
        waiting?.resume(returning: nil)
        waiting = nil
    }

    /// The next message of `type` the client sent, skipping the others (`status`, …); time moves on until one comes.
    func next(_ type: String) async throws -> JSONValue {
        var found: JSONValue?
        await clock.runUntil {
            while found == nil, !sent.isEmpty {
                let message = sent.removeFirst()
                if message["type"]?.string == type { found = message }
            }
            return found != nil
        }
        guard let found else { throw TestFailure("no \(type) was sent to \(url)") }
        return found
    }

    /// The messages the client sent since the last read, pings excluded.
    func drainMessages() -> [JSONValue] {
        defer { sent.removeAll() }
        return sent
    }

    /// The types of the messages the client sent since the last read, pings excluded.
    func drain() -> [String] { drainMessages().compactMap { $0["type"]?.string } }
}

/// Opens a `FakeSocket` per connection.
@MainActor
final class FakeRelay {
    let clock: VirtualClock
    private(set) var sockets: [FakeSocket] = []
    private var taken = 0
    private(set) var opened = 0

    init(clock: VirtualClock) {
        self.clock = clock
    }

    func connect(_ url: String) async throws -> any WebSocketConnection {
        opened += 1
        let socket = FakeSocket(url: url, clock: clock)
        sockets.append(socket)
        return socket
    }

    /// The next connection the party opened; time moves on until one opens.
    func nextSocket() async throws -> FakeSocket {
        guard await clock.runUntil({ taken < sockets.count }) else { throw TestFailure("no connection was opened") }
        taken += 1
        return sockets[taken - 1]
    }
}

// MARK: Relay messages

func room(
    rev: Int64,
    _ peers: [String],
    epoch: Int64 = 0,
    phase: String = "idle",
    positionMs: Int64 = 0,
    atMs: Int64 = 0,
    rate: Double = 1
) -> JSONValue {
    [
        "rev": num(rev), "code": "K7MQ2X", "epoch": num(epoch), "phase": .string(phase), "positionMs": num(positionMs),
        "atMs": num(atMs), "rate": .number(rate), "control": "host", "locked": false, "waitingOn": [],
        "participants": .array(peers.map { peer in
            [
                "peerId": .string(peer), "name": .string(peer), "client": "Test/1", "host": .bool(peer == peers[0]),
                "connected": true, "player": "idle", "match": nil, "offsetMs": 0,
            ]
        }),
    ]
}

func roomMessage(rev: Int64, _ peers: String...) -> JSONValue { room(rev: rev, peers).with(["type": "room"]) }

/// `joined` for p2; p1 is the host unless `host`.
func joined(rev: Int64, host: Bool = false) -> JSONValue {
    [
        "type": "joined", "code": "K7MQ2X", "you": ["peerId": "p2", "token": .string(token)],
        "room": room(rev: rev, host ? ["p2", "p1"] : ["p1", "p2"]), "media": nil, "serverTime": 0,
    ]
}

/// The `room` that starts epoch `epoch` with `media` (SPEC.md §8.3), paused at 0; p1 is the host unless `host`.
func mediaRoom(rev: Int64, epoch: Int64, _ media: JSONValue, host: Bool = false) -> JSONValue {
    room(rev: rev, host ? ["p2", "p1"] : ["p1", "p2"], epoch: epoch, phase: "paused")
        .with(["type": "room", "media": media])
}

/// A `room` of epoch 1 in `phase`, hosted by p2 (this party). `atMs` is relay time (`relayNow`).
func snapshot(rev: Int64, phase: String, positionMs: Int64 = 0, atMs: Int64 = 0, rate: Double = 1) -> JSONValue {
    room(rev: rev, ["p2", "p1"], epoch: 1, phase: phase, positionMs: positionMs, atMs: atMs, rate: rate)
        .with(["type": "room"])
}

let movie = Media(kind: .movie, title: "Heat", durationMs: 600_000)

// MARK: Parties

/// The public list, whose background refresh gets HTTP 404.
@MainActor
func notFoundRelays(_ clock: VirtualClock) -> Relays {
    Relays.public(httpGet: { _ in throw TestFailure("404") }, clock: clock)
}

/// A party on a `FakeRelay` with its events recorded.
@MainActor
final class Fixture {
    let clock: VirtualClock
    let relay: FakeRelay
    let party: WatchParty<String>
    private(set) var events: [any PartyEvent] = []

    init(clock: VirtualClock = VirtualClock(), resolver: Resolver<String> = Resolver { _ in [] }) throws {
        self.clock = clock
        let relay = FakeRelay(clock: clock)
        self.relay = relay
        party = try WatchParty(
            relays: notFoundRelays(clock),
            client: "Test/1.0",
            name: "Ben",
            resolver: resolver,
            connect: { try await relay.connect($0) },
            clock: clock,
            random: SeededRandom(seed: 1)
        )
        let stream = party.events()
        Task { @MainActor [weak self] in
            for await event in stream { self?.events.append(event) }
        }
    }

    var state: PartyState<String> { party.state }

    /// Relay time: the fake relay answers pings with local time + 1 000 000.
    var relayNow: Int64 { clock.now + 1_000_000 }
}

/// A party that joined K7MQ2X on `socket`.
@MainActor
struct Session {
    let f: Fixture
    let socket: FakeSocket

    var party: WatchParty<String> { f.party }
    var relay: FakeRelay { f.relay }
    var clock: VirtualClock { f.clock }
    var state: PartyState<String> { f.party.state }
    var events: [any PartyEvent] { f.events }
    var relayNow: Int64 { f.relayNow }
}

/// A party that joined K7MQ2X, as its host when `host`.
@MainActor
func joinedSession(
    clock: VirtualClock = VirtualClock(),
    host: Bool = false,
    resolver: Resolver<String> = Resolver { _ in [] }
) async throws -> Session {
    let f = try Fixture(clock: clock, resolver: resolver)
    let joining = Task { try await f.party.join("k7m-q2x") }
    let socket = try await f.relay.nextSocket()
    #expect(try await socket.next("join")["code"]?.string == "K7MQ2X")
    socket.reply(joined(rev: 1, host: host))
    try await joining.value
    #expect(f.state.connection == .connected)
    return Session(f: f, socket: socket)
}

/// A player that stands still and logs every command; while `refusal` is set, `play` throws it and the player stays
/// paused.
@MainActor
final class TestPlayer: PlayerAdapter {
    var positionMs: Int64 = 0
    var durationMs: Int64? = 600_000
    var paused = true
    var buffering = false
    var firstFrameShown = true
    var canSetRate = true
    var log: [String] = []
    var refusal: (any Error)?

    func play() async throws {
        log.append("play")
        if let refusal { throw refusal }
        paused = false
    }

    func pause() async throws {
        log.append("pause")
        paused = true
    }

    func seek(to positionMs: Int64) async throws {
        log.append("seek \(positionMs)")
        self.positionMs = positionMs
    }

    func setRate(_ rate: Double) async throws { log.append("setRate \(rate)") }
}

/// The host shared `movie` as heat.mkv (epoch 1, the room paused at 0) and `player` is bound and ready.
@MainActor
func boundSession(_ player: TestPlayer) async throws -> Session {
    let s = try await joinedSession(host: true)
    try s.party.setMedia(movie, ref: "heat.mkv")
    _ = try await s.socket.next("setMedia")
    s.socket.reply(snapshot(rev: 2, phase: "paused").with(["media": movie.toJSON()]))
    await s.clock.run()
    s.party.attachPlayer(player, ref: "heat.mkv")
    await s.clock.run()
    #expect(player.log == ["setRate 1.0"])
    #expect(s.state.player == .ready)
    return s
}

/// `state` holds the party's own `media`: its choice is the file passed to `setMedia`, matched (SDK.md §9.1).
func expectOwnMedia(_ state: PartyState<String>, _ media: Media = movie, sourceLocation: SourceLocation = #_sourceLocation) {
    guard let choice = state.choice else {
        Issue.record("no choice", sourceLocation: sourceLocation)
        return
    }
    #expect(choice.id == "", sourceLocation: sourceLocation)
    #expect(choice.ref == "heat.mkv", sourceLocation: sourceLocation)
    #expect(choice.durationMs == media.durationMs, sourceLocation: sourceLocation)
    #expect(choice.sizeBytes == nil && choice.oshash == nil && choice.name == nil && choice.probe == nil, sourceLocation: sourceLocation)
    #expect(state.match == Match(.matched, deltaMs: 0), sourceLocation: sourceLocation)
    #expect(state.mismatch == nil, sourceLocation: sourceLocation)
    #expect(state.candidates.isEmpty, sourceLocation: sourceLocation)
    #expect(state.offsetMs == 0, sourceLocation: sourceLocation)
}

/// Expects `body` to throw a `PartyError` with `code`.
func expectPartyError(
    _ code: PartyError.Code,
    sourceLocation: SourceLocation = #_sourceLocation,
    _ body: () throws -> Void
) {
    do {
        try body()
        Issue.record("no error, expected \(code)", sourceLocation: sourceLocation)
    } catch let error as PartyError {
        #expect(error.code == code, sourceLocation: sourceLocation)
    } catch {
        Issue.record("\(error), expected \(code)", sourceLocation: sourceLocation)
    }
}

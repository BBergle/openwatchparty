/// One WebSocket to a relay with its own clock (SDK.md §5): pings at open, +500 and +1000 ms, then every 5 s from
/// open; closed after 15 s with nothing received. `onMessage` gets every message but `pong`, `onClosed` the close
/// status when the connection ends by itself (never after `close`).
@MainActor
final class Link {
    let url: String
    private let socket: any WebSocketConnection
    private let clock: PartyClock
    private(set) var sync = ClockSync()
    let openedAtMs: Int64

    var onMessage: (In) -> Void = { _ in }
    var onPong: () -> Void = {}
    var onClosed: (_ code: Int?) -> Void = { _ in }

    private var nextPingId: Int64 = 1
    private var ended = false
    private var reader: Task<Void, Never>?
    private var pinger: Alarm?
    private var silence: Alarm?

    init(url: String, socket: any WebSocketConnection, clock: PartyClock) {
        self.url = url
        self.socket = socket
        self.clock = clock
        openedAtMs = clock.nowMs()
    }

    func start() {
        reader = spawn { [self] in
            while let text = await socket.receive() {
                if ended { return }
                armSilence()
                switch In.parse(text) {
                case nil: break
                case .pong(let id, let serverTime): pong(id: id, serverTime: serverTime)
                case let message?: onMessage(message)
                }
            }
            if ended { return }
            let code = await socket.closeCode()
            if !ended { end(code) }
        }
        ping()
        armSilence()
    }

    /// False once the connection ended or was closed.
    var isOpen: Bool { !ended }

    func send(_ text: String) {
        if !ended { socket.send(text) }
    }

    /// Relay time now; nil before the first clock sample.
    func relayNow() -> Int64? { sync.relayTime(localMs: clock.nowMs()) }

    /// Closes the connection; `onClosed` isn't called.
    func close(_ code: Int = 1000) {
        if ended { return }
        ended = true
        stopTimers()
        socket.close(code: code)
    }

    private func end(_ code: Int?) {
        ended = true
        stopTimers()
        socket.close(code: 1000)
        onClosed(code)
    }

    private func stopTimers() {
        silence?.cancel()
        silence = nil
        pinger?.cancel()
        pinger = nil
        reader?.cancel()
        reader = nil
    }

    private func armSilence() {
        silence?.cancel()
        silence = clock.after(Owp.silenceTimeoutMs) { [self] in
            if !ended { end(nil) }
        }
    }

    /// Sends ping `nextPingId` and arms the next: at +500 and +1000 ms from open, then every 5 s from open.
    private func ping() {
        let id = nextPingId
        nextPingId += 1
        sync.ping(id: id, atMs: clock.nowMs())
        socket.send(Out.ping(id: id))
        let sent = Int(id)
        let next = sent < Owp.burstSize
            ? Int64(sent) * Owp.burstSpacingMs
            : Int64(sent - Owp.burstSize + 1) * Owp.pingIntervalMs
        pinger = clock.after(openedAtMs + next - clock.nowMs()) { [self] in
            if !ended { ping() }
        }
    }

    private func pong(id: Int64, serverTime: Int64) {
        _ = sync.pong(id: id, serverTime: serverTime, atMs: clock.nowMs())
        onPong()
    }
}

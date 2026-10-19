/// One connection's estimate of the relay's clock (SDK.md §5): a window of the 8 newest samples, the best one being the
/// lowest round trip (the oldest on a tie). Relay time now ≈ local now + `best`'s offset.
struct ClockSync {
    struct Sample: Equatable {
        let offsetMs: Int64
        let rttMs: Int64
    }

    enum Result: String {
        case accepted, stepped, ignored
    }

    private var pending: [Int64: Int64] = [:]
    private var window: [Sample] = []

    /// The sample in use; nil before the first.
    private(set) var best: Sample?

    /// Ping `id` went out at local time `atMs`; pings unanswered for 10 s are forgotten.
    mutating func ping(id: Int64, atMs: Int64) {
        pending = pending.filter { atMs - $0.value < Owp.pingForgetMs }
        pending[id] = atMs
    }

    mutating func pong(id: Int64, serverTime: Int64, atMs: Int64) -> Result {
        guard let sentAt = pending.removeValue(forKey: id) else { return .ignored }
        let rtt = atMs - sentAt
        if rtt < 0 || rtt > Owp.maxRttMs { return .ignored }
        let offset = serverTime - (sentAt + rtt / 2)
        var result = Result.accepted
        if let b = best, abs(offset - b.offsetMs) > (rtt + b.rttMs) / 2 + Owp.clockStepSlackMs {
            window.removeAll()
            result = .stepped
        }
        window.append(Sample(offsetMs: offset, rttMs: rtt))
        if window.count > Owp.clockWindow { window.removeFirst() }
        best = window.dropFirst().reduce(window[0]) { a, b in b.rttMs < a.rttMs ? b : a }
        return result
    }

    /// Relay time at local time `localMs`; nil before the first sample.
    func relayTime(localMs: Int64) -> Int64? { best.map { localMs + $0.offsetMs } }
}

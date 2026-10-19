import Foundation

/// The room position at relay time `t` (SPEC.md §12.2): in `playing`, `min(durationMs, floor(positionMs + max(0, t −
/// atMs) × rate))` in doubles, one rounding per operation; else `positionMs`.
func roomPosition(phase: Phase, positionMs: Int64, atMs: Int64, rate: Double, durationMs: Int64, t: Int64) -> Int64 {
    guard phase == .playing else { return positionMs }
    let elapsed = Double(max(0, t - atMs))
    let moved = elapsed * rate
    let sum = Double(positionMs) + moved
    return min(Int64(sum.rounded(.down)), durationMs)
}

/// Whether the room's position moves at relay time `t`.
func roomRunning(phase: Phase, atMs: Int64, t: Int64) -> Bool { phase == .playing && t >= atMs }

extension Room {
    func position(durationMs: Int64, at t: Int64) -> Int64 {
        roomPosition(phase: phase, positionMs: positionMs, atMs: atMs, rate: rate, durationMs: durationMs, t: t)
    }
}

/// Relay time when local playback of a `playing` room starts (SDK.md §11.2): later than `atMs` when the local file
/// starts after the room's position, `anchor` < 0.
func localStartAt(atMs: Int64, anchor: Int64, rate: Double) -> Double {
    if anchor >= 0 { return Double(atMs) }
    let lead = Double(-anchor) / rate
    return Double(atMs) + lead
}

/// What one reconciliation step reads (SDK.md §11.1).
struct StepInput {
    var phase: Phase
    var positionMs: Int64
    var atMs: Int64
    var rate: Double
    var durationMs: Int64
    var nowMs: Int64
    var offsetMs: Int64
    var playerPositionMs: Int64
    var paused: Bool
    var buffering: Bool
    var playerDurationMs: Int64?
    var canSetRate: Bool
    /// The adapter's seek accuracy, already at least 0.
    var seekAccuracyMs: Int64
    /// The rate the SDK last set on the adapter.
    var appliedRate: Double
    var nudging: Bool
    /// Nil when the SDK never seeked on this adapter.
    var sinceSeekMs: Int64?
}

enum StepAction: Equatable {
    case pause
    case setRate(Double)
    case seek(Int64)
    case play
}

struct StepOutput {
    /// In the order pause, setRate, seek, play.
    var actions: [StepAction]
    var nudging: Bool
    /// The play timer: run a step again in this many ms.
    var playInMs: Int64?
}

/// One reconciliation step (SDK.md §11.2).
func reconcile(_ input: StepInput) -> StepOutput {
    var actions: [StepAction] = []
    if input.phase == .idle { return StepOutput(actions: actions, nudging: false, playInMs: nil) }
    let cooled = input.sinceSeekMs.map { $0 >= Owp.seekCooldownMs } ?? true
    let end = input.playerDurationMs.flatMap { $0 > 0 ? max(0, $0 - Owp.endMarginMs) : nil }
    func clamp(_ x: Int64) -> Int64 {
        let low = max(0, x)
        return end.map { min(low, $0) } ?? low
    }
    func setRate(_ rate: Double) {
        if input.canSetRate && abs(input.appliedRate - rate) > Owp.rateEpsilon { actions.append(.setRate(round6(rate))) }
    }

    let anchor = input.positionMs + input.offsetMs
    var held = input.phase != .playing
    var playIn: Int64?
    if input.phase == .playing {
        let startAt = localStartAt(atMs: input.atMs, anchor: anchor, rate: input.rate)
        if Double(input.nowMs) < startAt {
            held = true
            playIn = Int64((startAt - Double(input.nowMs)).rounded(.up))
        }
    }

    if held {
        let holdAt = clamp(anchor)
        if !input.paused { actions.append(.pause) }
        setRate(input.rate)
        if abs(input.playerPositionMs - holdAt) > max(Owp.heldSeekMs, input.seekAccuracyMs) && cooled {
            actions.append(.seek(holdAt))
        }
        return StepOutput(actions: actions, nudging: false, playInMs: playIn)
    }

    let room = roomPosition(
        phase: input.phase, positionMs: input.positionMs, atMs: input.atMs, rate: input.rate,
        durationMs: input.durationMs, t: input.nowMs
    )
    let target = max(0, room + input.offsetMs)
    var nudging = input.nudging && input.canSetRate
    if let end, target >= end && input.playerPositionMs >= end {
        setRate(input.rate)
        return StepOutput(actions: actions, nudging: false, playInMs: nil)
    }
    if input.buffering { return StepOutput(actions: actions, nudging: nudging, playInMs: nil) }

    let drift = input.playerPositionMs - target
    let size = abs(drift)
    var seek = false
    if input.canSetRate {
        if size > max(Owp.seekMs, input.seekAccuracyMs) {
            nudging = false
            seek = cooled
        } else if nudging {
            nudging = size >= Owp.nudgeExitMs
        } else if size > Owp.deadbandMs {
            nudging = true
        }
    } else if size > max(Owp.seekOnlyMs, input.seekAccuracyMs) {
        seek = cooled
    }
    let want: Double
    if nudging {
        let factor = drift > 0 ? 1 - Owp.nudgeFactor : 1 + Owp.nudgeFactor
        want = input.rate * factor
    } else {
        want = input.rate
    }
    setRate(want)
    if seek { actions.append(.seek(clamp(target + Owp.seekLeadMs))) }
    if input.paused { actions.append(.play) }
    return StepOutput(actions: actions, nudging: nudging, playInMs: nil)
}

/// Rates as sent to the adapter: rounded to 6 decimals, halves away from zero, so 1.25 × 0.96 is 1.2.
func round6(_ x: Double) -> Double {
    let y = x * 1e6
    let t = y.rounded(.towardZero)
    let r = abs(y - t) >= 0.5 ? t + (y < 0 ? -1 : 1) : t
    return r / 1e6
}

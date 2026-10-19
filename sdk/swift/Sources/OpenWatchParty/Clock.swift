import Foundation

/// Local time and timers (SDK.md §2, §16.8): milliseconds of a monotonic clock, never wall-clock time, and one-shot
/// timers on the main actor. The party and `Relays` measure and wait only through it, so a test passes a virtual clock
/// and drives every timer of the SDK.
@MainActor
public protocol PartyClock: AnyObject {
    /// Milliseconds on a monotonic clock that a wall-clock step never moves.
    func nowMs() -> Int64

    /// Runs `action` on the main actor once `delayMs` ms have passed, always later than the call, unless the returned
    /// function, which cancels the timer, runs first. The SDK never asks for more than 2147483647 ms (SDK.md §2).
    func schedule(afterMs delayMs: Int64, _ action: @escaping @MainActor () -> Void) -> @MainActor () -> Void
}

/// The default clock: `ContinuousClock` for time, `Task.sleep` for timers.
@MainActor
public final class SystemClock: PartyClock {
    private let clock = ContinuousClock()
    private let origin: ContinuousClock.Instant

    public init() { origin = clock.now }

    public func nowMs() -> Int64 {
        let elapsed = origin.duration(to: clock.now).components
        return elapsed.seconds * 1000 + elapsed.attoseconds / 1_000_000_000_000_000
    }

    public func schedule(afterMs delayMs: Int64, _ action: @escaping @MainActor () -> Void) -> @MainActor () -> Void {
        let clock = clock
        let deadline = clock.now.advanced(by: .milliseconds(max(0, delayMs)))
        let task = Task { @MainActor in
            do {
                try await Task.sleep(until: deadline, clock: clock)
            } catch {
                return
            }
            if !Task.isCancelled { action() }
        }
        return { task.cancel() }
    }
}

/// A timer of the SDK: no native timer is longer than 2147483647 ms (SDK.md §2), so a longer wait runs as several,
/// each recomputing what is left when it fires.
@MainActor
final class Alarm {
    private var cancelNative: (@MainActor () -> Void)?
    private var cancelled = false

    fileprivate init() {}

    func cancel() {
        cancelled = true
        cancelNative?()
        cancelNative = nil
    }

    fileprivate func arm(_ clock: PartyClock, deadline: Int64, _ action: @escaping @MainActor () -> Void) {
        guard !cancelled else { return }
        let left = max(0, deadline - clock.nowMs())
        cancelNative = clock.schedule(afterMs: min(left, Owp.maxTimerMs)) { [self] in
            guard !cancelled else { return }
            if clock.nowMs() < deadline {
                arm(clock, deadline: deadline, action)
            } else {
                cancelNative = nil
                action()
            }
        }
    }

    /// Arms `action` every `intervalMs` until cancelled, the first an interval from now.
    fileprivate func repeating(_ clock: PartyClock, intervalMs: Int64, _ action: @escaping @MainActor () -> Void) {
        arm(clock, deadline: clock.nowMs() + intervalMs) { [self] in
            action()
            repeating(clock, intervalMs: intervalMs, action)
        }
    }
}

extension PartyClock {
    /// `action` once `delayMs` passed (negative counts as 0), always later than the call.
    func after(_ delayMs: Int64, _ action: @escaping @MainActor () -> Void) -> Alarm {
        let timer = Alarm()
        let now = nowMs()
        let deadline = delayMs > Int64.max - now ? Int64.max : now + max(0, delayMs)
        timer.arm(self, deadline: deadline, action)
        return timer
    }

    /// `action` every `intervalMs`, the first an interval from now, until cancelled.
    func every(_ intervalMs: Int64, _ action: @escaping @MainActor () -> Void) -> Alarm {
        let timer = Alarm()
        timer.repeating(self, intervalMs: intervalMs, action)
        return timer
    }

    /// Waits for `waiter` to settle. After `timeoutMs` (nil: never) it settles with `timedOut`, and when the waiting
    /// task is cancelled with `cancelled`.
    func wait<T: Sendable>(_ waiter: Waiter<T>, timeoutMs: Int64?, timedOut: T, cancelled: T) async -> T {
        await withTaskCancellationHandler {
            await waiter.wait {
                guard let timeoutMs else { return {} }
                let timer = after(timeoutMs) { waiter.resume(timedOut) }
                return { timer.cancel() }
            }
        } onCancel: {
            spawn { waiter.resume(cancelled) }
        }
    }

    /// Suspends for `delayMs` on this clock; throws `CancellationError` when the task is cancelled.
    func sleep(_ delayMs: Int64) async throws {
        if !(await wait(Waiter<Bool>(), timeoutMs: delayMs, timedOut: true, cancelled: false)) {
            throw CancellationError()
        }
    }

    /// Runs `operation` in a task of its own and waits for it, at most `timeoutMs` (nil: no limit). On the timeout the
    /// task is cancelled and the wait gives up at once, whether or not the operation stops; so does a cancellation of
    /// the waiting task, which then gets `.failure(CancellationError())`. A result that comes after the wait gave up
    /// goes to `late`.
    func race<T>(
        timeoutMs: Int64?,
        late: (@MainActor (T) -> Void)? = nil,
        _ operation: @escaping @MainActor () async throws -> T
    ) async -> Outcome<T> {
        if Task.isCancelled { return .failure(CancellationError()) }
        let waiter = Waiter<Outcome<T>>()
        let box = TaskBox()
        return await withTaskCancellationHandler {
            await waiter.wait {
                let work = spawn {
                    let result: Outcome<T>
                    do {
                        result = .value(try await operation())
                    } catch {
                        result = .failure(error)
                    }
                    if !waiter.resume(result), case .value(let v) = result { late?(v) }
                }
                box.set(work)
                guard let timeoutMs else { return {} }
                let timer = after(timeoutMs) {
                    work.cancel()
                    waiter.resume(.timedOut)
                }
                return { timer.cancel() }
            }
        } onCancel: {
            box.cancel()
            spawn { waiter.resume(.failure(CancellationError())) }
        }
    }
}

/// How `race` ended.
enum Outcome<T> {
    case value(T)
    case failure(any Error)
    case timedOut
}

/// A one-shot wait on the main actor: the first `resume` settles it, later ones are ignored. Under a bound `Work` the
/// waiting task counts as parked until the `resume`.
@MainActor
final class Waiter<T> {
    private var continuation: CheckedContinuation<Void, Never>?
    private var value: T?
    private var settled = false
    private var cleanup: (@MainActor () -> Void)?
    private var parked: Work?

    /// Whether a `resume` came.
    var isSettled: Bool { settled }

    /// Starts `begin`, which returns its cleanup, unless the wait is settled already, and waits for the first
    /// `resume`. One task waits at a time.
    func wait(_ begin: () -> @MainActor () -> Void) async -> T {
        if !settled {
            let work = Work.current
            await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
                continuation = c
                parked = work
                work?.add(-1)
                let done = begin()
                if settled { done() } else { cleanup = done }
            }
        }
        return value!
    }

    /// Settles the wait; false when it was settled already.
    @discardableResult
    func resume(_ value: T) -> Bool {
        guard !settled else { return false }
        settled = true
        self.value = value
        cleanup?()
        cleanup = nil
        parked?.add(1)
        parked = nil
        continuation?.resume()
        continuation = nil
        return true
    }
}

/// A task that a cancellation handler, which may run on any thread, cancels.
final class TaskBox: @unchecked Sendable {
    private let lock = NSLock()
    private var task: Task<Void, Never>?
    private var cancelled = false

    func set(_ task: Task<Void, Never>) {
        lock.lock()
        self.task = task
        let cancelNow = cancelled
        lock.unlock()
        if cancelNow { task.cancel() }
    }

    func cancel() {
        lock.lock()
        cancelled = true
        let task = self.task
        lock.unlock()
        task?.cancel()
    }
}

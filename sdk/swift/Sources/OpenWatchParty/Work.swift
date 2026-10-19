import Foundation

/// A test seam: the SDK's work in flight, so a test can run a party until it is quiet (the party traces, SDK.md §17).
/// While a test binds `Work.current`, every task the SDK starts with `spawn` counts from its start to its return,
/// except while it waits in a `Waiter`, and so does each event handed to an `events()` subscriber until the test took
/// it (`add(-1)`). `isIdle` then means nothing the SDK started runs or is about to run without input from outside: a
/// timer, the transport, the adapter, the resolver. Apps never bind it, and then nothing is counted.
final class Work: @unchecked Sendable {
    @TaskLocal static var current: Work?

    private let lock = NSLock()
    private var busy = 0
    private var ends: [Task<Void, Never>: TaskEnd] = [:]

    init() {}

    var isIdle: Bool {
        lock.lock()
        defer { lock.unlock() }
        return busy == 0
    }

    func add(_ delta: Int) {
        lock.lock()
        busy += delta
        lock.unlock()
    }

    fileprivate func started(_ task: Task<Void, Never>, _ end: TaskEnd) {
        lock.lock()
        ends[task] = end
        lock.unlock()
    }

    private func end(of task: Task<Void, Never>) -> TaskEnd? {
        lock.lock()
        defer { lock.unlock() }
        return ends[task]
    }

    /// Returns once `task` returned. Waiting for a task `spawn` started under this tracker counts as parked.
    @MainActor
    func join(_ task: Task<Void, Never>) async {
        if let end = end(of: task) { await end.wait() } else { await task.value }
    }
}

/// Starts `operation` on the main actor; under a bound `Work` it counts until it returns.
@discardableResult
func spawn(_ operation: sending @escaping @MainActor () async -> Void) -> Task<Void, Never> {
    guard let work = Work.current else { return Task(operation: operation) }
    work.add(1)
    let end = TaskEnd()
    let task = Task { @MainActor in
        await operation()
        end.finish()
        work.add(-1)
    }
    work.started(task, end)
    return task
}

/// The return of a task `spawn` counts, for `Work.join`.
@MainActor
private final class TaskEnd {
    private var finished = false
    private var waiting: [Waiter<Void>] = []

    nonisolated init() {}

    func finish() {
        finished = true
        for waiter in waiting { waiter.resume(()) }
        waiting = []
    }

    func wait() async {
        if finished { return }
        let waiter = Waiter<Void>()
        waiting.append(waiter)
        await waiter.wait { {} }
    }
}

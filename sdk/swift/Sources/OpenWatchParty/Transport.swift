import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// One WebSocket of text messages (SDK.md §16.8).
///
/// The party calls it on the main actor only.
@MainActor
public protocol WebSocketConnection: AnyObject, Sendable {
    /// Receives the next text message.
    ///
    /// Binary messages are skipped.
    ///
    /// - Returns: The message, or nil once the connection has ended, by either side or by a failure.
    func receive() async -> String?
    /// The close status once `receive` has returned nil, or nil when the connection ended without one.
    func closeCode() async -> Int?
    func send(_ text: String)
    /// Closes the connection with `code`, after which nothing more arrives.
    func close(code: Int)
}

/// Opens a WebSocket to a relay's URL, such as `wss://…/v1/ws`, and returns once it is open.
///
/// The function throws when the connection fails, including on HTTP 429 and 503. It should stop when its task is
/// cancelled. The party gives up after 10 seconds regardless, and closes a connection that opens later.
public typealias WebSocketConnect = @MainActor (_ url: String) async throws -> any WebSocketConnection

/// The default `WebSocketConnect`, which uses a `URLSessionWebSocketTask`.
@MainActor
public func urlSessionConnect(_ url: String) async throws -> any WebSocketConnection {
    try await URLSessionConnection.open(url)
}

/// A WebSocket over `URLSessionWebSocketTask`, one ephemeral session per connection.
@MainActor
private final class URLSessionConnection: WebSocketConnection {
    private let session: URLSession
    private let task: URLSessionWebSocketTask
    private let events: SocketEvents
    private var closed = false
    /// Sends handed to the task whose completion hasn't come yet; a close waits for them, since FoundationNetworking
    /// drops a queued send when the task is cancelled (Darwin flushes it).
    private var sending = 0
    private var pendingCloseCode: Int?
    private var cancelled = false

    private init(session: URLSession, task: URLSessionWebSocketTask, events: SocketEvents) {
        self.session = session
        self.task = task
        self.events = events
    }

    static func open(_ url: String) async throws -> URLSessionConnection {
        guard let target = URL(string: url) else { throw PartyError(.unreachable, "not a URL: \(url)") }
        let events = SocketEvents()
        let session = URLSession(configuration: .ephemeral, delegate: events, delegateQueue: nil)
        let task = session.webSocketTask(with: target)
        do {
            try await withTaskCancellationHandler {
                try await events.opened { task.resume() }
            } onCancel: {
                task.cancel()
            }
        } catch {
            task.cancel()
            session.invalidateAndCancel()
            throw error
        }
        return URLSessionConnection(session: session, task: task, events: events)
    }

    func receive() async -> String? {
        while !closed {
            let task = task
            let message: URLSessionWebSocketTask.Message
            do {
                message = try await withCheckedThrowingContinuation { continuation in
                    task.receive { continuation.resume(with: $0) }
                }
            } catch {
                return nil
            }
            if case .string(let text) = message { return text }
        }
        return nil
    }

    func closeCode() async -> Int? {
        if let code = await events.closeCode(waitingMs: 1000) { return code }
        return task.closeCode == .invalid ? nil : task.closeCode.rawValue
    }

    func send(_ text: String) {
        guard !closed else { return }
        sending += 1
        // Strong captures: the party may let go of a closing connection before its sends complete.
        task.send(.string(text)) { _ in
            Task { @MainActor in self.sent() }
        }
    }

    private func sent() {
        sending -= 1
        if sending == 0, let pendingCloseCode { cancel(pendingCloseCode) }
    }

    func close(code: Int) {
        guard !closed else { return }
        closed = true
        guard sending > 0 else { return cancel(code) }
        pendingCloseCode = code
        // A send that never completes doesn't hold the close up for long.
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(1))
            self.cancel(code)
        }
    }

    private func cancel(_ code: Int) {
        guard !cancelled else { return }
        cancelled = true
        task.cancel(with: URLSessionWebSocketTask.CloseCode(rawValue: code) ?? .normalClosure, reason: nil)
        session.finishTasksAndInvalidate()
    }
}

/// The delegate of one WebSocket session: the open handshake and the close status.
private final class SocketEvents: NSObject, URLSessionWebSocketDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private var open: CheckedContinuation<Void, any Error>?
    private var openResult: Result<Void, any Error>?
    private var code: Int?
    private var ended = false
    private var codeWaiters: [CheckedContinuation<Int?, Never>] = []

    /// Starts the connection and waits for the handshake to end.
    func opened(_ start: @Sendable () -> Void) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, any Error>) in
            lock.lock()
            if let result = openResult {
                lock.unlock()
                continuation.resume(with: result)
                return
            }
            open = continuation
            lock.unlock()
            start()
        }
    }

    /// The close status the peer sent, waiting up to `waitingMs` for it after the connection failed.
    func closeCode(waitingMs: Int) async -> Int? {
        await withCheckedContinuation { (continuation: CheckedContinuation<Int?, Never>) in
            lock.lock()
            if code != nil || ended {
                let code = self.code
                lock.unlock()
                continuation.resume(returning: code)
                return
            }
            codeWaiters.append(continuation)
            lock.unlock()
            DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(waitingMs)) { [self] in
                settleCode(nil, ended: false)
            }
        }
    }

    private func settleOpen(_ result: Result<Void, any Error>) {
        lock.lock()
        let waiting = open
        open = nil
        if openResult == nil { openResult = result }
        lock.unlock()
        waiting?.resume(with: result)
    }

    private func settleCode(_ code: Int?, ended: Bool) {
        lock.lock()
        if let code, self.code == nil { self.code = code }
        if ended { self.ended = true }
        let waiters = codeWaiters
        codeWaiters = []
        let result = self.code
        lock.unlock()
        for waiter in waiters { waiter.resume(returning: result) }
    }

    func urlSession(_ session: URLSession, webSocketTask: URLSessionWebSocketTask, didOpenWithProtocol protocol: String?) {
        settleOpen(.success(()))
    }

    func urlSession(
        _ session: URLSession,
        webSocketTask: URLSessionWebSocketTask,
        didCloseWith closeCode: URLSessionWebSocketTask.CloseCode,
        reason: Data?
    ) {
        settleCode(closeCode == .invalid ? nil : closeCode.rawValue, ended: true)
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: (any Error)?) {
        settleOpen(.failure(error ?? PartyError(.unreachable, "the connection closed during the handshake")))
        settleCode(nil, ended: false)
    }
}

/// The default `HTTPGet`, which uses `URLSession` (SDK.md §3.4).
///
/// It follows no redirects and accepts status 200 only. It reads at most 65536 bytes of UTF-8 body, and stops when its
/// task is cancelled.
@MainActor
public func urlSessionGet(_ url: String) async throws -> String {
    guard let target = URL(string: url) else { throw PartyError(.unreachable, "not a URL: \(url)") }
    let reader = BodyReader(limit: Owp.httpMaxBodyBytes)
    let session = URLSession(configuration: .ephemeral, delegate: reader, delegateQueue: nil)
    defer { session.finishTasksAndInvalidate() }
    let task = session.dataTask(with: target)
    let body = try await withTaskCancellationHandler {
        try await reader.read { task.resume() }
    } onCancel: {
        task.cancel()
    }
    guard let text = String(data: body, encoding: .utf8) else {
        throw PartyError(.unreachable, "the body of \(url) is not UTF-8")
    }
    return text
}

/// The delegate of one GET: refuses redirects and statuses other than 200, and stops past `limit` bytes.
private final class BodyReader: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private let limit: Int
    private let lock = NSLock()
    private var body = Data()
    private var failure: (any Error)?
    private var waiting: CheckedContinuation<Data, any Error>?
    private var result: Result<Data, any Error>?

    init(limit: Int) { self.limit = limit }

    func read(_ start: @Sendable () -> Void) async throws -> Data {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Data, any Error>) in
            lock.lock()
            if let result {
                lock.unlock()
                continuation.resume(with: result)
                return
            }
            waiting = continuation
            lock.unlock()
            start()
        }
    }

    private func fail(_ error: any Error) {
        lock.lock()
        if failure == nil { failure = error }
        lock.unlock()
    }

    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping @Sendable (URLRequest?) -> Void
    ) {
        fail(PartyError(.unreachable, "redirected (status \(response.statusCode))"))
        completionHandler(nil)
        task.cancel()
    }

    func urlSession(
        _ session: URLSession,
        dataTask: URLSessionDataTask,
        didReceive response: URLResponse,
        completionHandler: @escaping @Sendable (URLSession.ResponseDisposition) -> Void
    ) {
        let status = (response as? HTTPURLResponse)?.statusCode
        if status == 200 {
            completionHandler(.allow)
        } else {
            fail(PartyError(.unreachable, "status \(status.map(String.init) ?? "none")"))
            completionHandler(.cancel)
        }
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.lock()
        body.append(data)
        let over = body.count > limit
        if over, failure == nil { failure = PartyError(.unreachable, "the body is over \(limit) bytes") }
        lock.unlock()
        if over { dataTask.cancel() }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: (any Error)?) {
        lock.lock()
        let outcome: Result<Data, any Error>
        if let failure {
            outcome = .failure(failure)
        } else if let error {
            outcome = .failure(error)
        } else if (task.response as? HTTPURLResponse)?.statusCode != 200 {
            outcome = .failure(PartyError(.unreachable, "no status 200"))
        } else {
            outcome = .success(body)
        }
        if result == nil { result = outcome }
        let continuation = waiting
        waiting = nil
        lock.unlock()
        continuation?.resume(with: outcome)
    }
}

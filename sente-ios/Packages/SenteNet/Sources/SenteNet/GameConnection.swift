import Foundation

public enum ConnectionStatus: Equatable, Sendable {
    case disconnected
    case connecting
    case connected
    case reconnecting(attempt: Int)
    case failed(String)
}

public enum ClientCommand: Sendable {
    case move(clientMoveId: String, expectedMoveNo: Int, kind: String, point: String?)
    case markDead(point: String)
    case scoringAccept(Bool)
    case scoringResume
    case undoRequest
    case undoResponse(accept: Bool)

    var type: String {
        switch self {
        case .move: "move"
        case .markDead: "mark_dead"
        case .scoringAccept: "scoring_accept"
        case .scoringResume: "scoring_resume"
        case .undoRequest: "undo_request"
        case .undoResponse: "undo_response"
        }
    }

    func payload() throws -> JSONValue {
        switch self {
        case .move(let id, let expected, let kind, let point):
            return try JSONValue(encoding: MovePayload(clientMoveId: id, expectedMoveNo: expected, kind: kind, point: point))
        case .markDead(let point): return try JSONValue(encoding: MarkDeadPayload(point: point))
        case .scoringAccept(let accepted): return try JSONValue(encoding: AcceptPayload(accepted: accepted))
        case .scoringResume, .undoRequest: return .object([:])
        case .undoResponse(let accept): return try JSONValue(encoding: UndoResponsePayload(accept: accept))
        }
    }
}

/// One WebSocket to one game. Owns reconnection, the heartbeat, and a queue of
/// commands sent while offline (docs/07 §5).
///
/// Queued moves carry their idempotency key, so a resend after a reconnect can
/// never create a second move: the server answers the duplicate with the original
/// result (docs/03 ADR-007).
public actor GameConnection {
    public nonisolated let events: AsyncStream<ServerEvent>
    public nonisolated let status: AsyncStream<ConnectionStatus>

    private let eventsContinuation: AsyncStream<ServerEvent>.Continuation
    private let statusContinuation: AsyncStream<ConnectionStatus>.Continuation
    private let url: URL
    private let session: URLSession
    private var task: URLSessionWebSocketTask?
    private var outbox: [ClientCommand] = []
    private var backoff = Backoff()
    private var sequence = 0
    private var closed = false
    private var readLoop: Task<Void, Never>?
    private var heartbeat: Task<Void, Never>?
    private(set) public var clockOffset: TimeInterval = 0
    private var offsetSamples: [TimeInterval] = []

    public init(baseURL: URL, gameID: String, token: String, session: URLSession = .shared) {
        var components = URLComponents(url: baseURL.appending(path: "/v1/ws"), resolvingAgainstBaseURL: false)!
        components.scheme = components.scheme == "https" ? "wss" : "ws"
        components.queryItems = [
            .init(name: "pv", value: String(Protocol.version)),
            .init(name: "game_id", value: gameID),
            .init(name: "token", value: token),
        ]
        self.url = components.url!
        self.session = session
        (events, eventsContinuation) = AsyncStream.makeStream(bufferingPolicy: .bufferingNewest(256))
        (status, statusContinuation) = AsyncStream.makeStream(bufferingPolicy: .bufferingNewest(1))
    }

    public func connect() {
        guard task == nil, !closed else { return }
        statusContinuation.yield(backoff.attempt == 0 ? .connecting : .reconnecting(attempt: backoff.attempt))
        let task = session.webSocketTask(with: url)
        task.maximumMessageSize = 1 << 20
        self.task = task
        task.resume()
        readLoop = Task { [weak self] in await self?.read() }
    }

    public func close() {
        closed = true
        heartbeat?.cancel()
        readLoop?.cancel()
        task?.cancel(with: .normalClosure, reason: nil)
        task = nil
        eventsContinuation.finish()
        statusContinuation.finish()
    }

    /// Queues when offline; the queue drains after the next successful resume.
    public func send(_ command: ClientCommand) async {
        guard let task, backoff.attempt == 0 || outbox.isEmpty else {
            outbox.append(command)
            return
        }
        do {
            try await write(command, over: task)
        } catch {
            outbox.append(command)
        }
    }

    private func write(_ command: ClientCommand, over task: URLSessionWebSocketTask) async throws {
        sequence += 1
        let message = Message(id: "c-\(sequence)", type: command.type, payload: try command.payload())
        let data = try ProtocolDecoder.encoder.encode(message)
        try await task.send(.string(String(decoding: data, as: UTF8.self)))
    }

    private func read() async {
        guard let task else { return }
        while !Task.isCancelled {
            do {
                let raw = try await task.receive()
                let data: Data
                switch raw {
                case .string(let text): data = Data(text.utf8)
                case .data(let bytes): data = bytes
                @unknown default: continue
                }
                let message = try ProtocolDecoder.json.decode(Message.self, from: data)
                await handle(message)
            } catch {
                if Task.isCancelled || closed { return }
                await lost(error)
                return
            }
        }
    }

    private func handle(_ message: Message) async {
        guard let event = try? ProtocolDecoder.event(from: message) else { return }
        switch event {
        case .hello(let hello):
            // A successful handshake resets the schedule and drains the queue.
            backoff.reset()
            statusContinuation.yield(.connected)
            startHeartbeat(every: TimeInterval(hello.heartbeatIntervalMs) / 1000)
            await drainOutbox()
        case .pong(let pong):
            recordOffset(clientTime: pong.clientTime, serverTime: pong.serverTime)
        default:
            break
        }
        eventsContinuation.yield(event)
    }

    private func drainOutbox() async {
        guard let task else { return }
        let pending = outbox
        outbox.removeAll()
        for command in pending {
            do { try await write(command, over: task) } catch { outbox.append(command) }
        }
    }

    private func lost(_ error: Error) async {
        heartbeat?.cancel()
        task?.cancel()
        task = nil
        guard !closed else { return }
        let delay = backoff.next()
        statusContinuation.yield(.reconnecting(attempt: backoff.attempt))
        try? await Task.sleep(for: .seconds(delay))
        guard !closed else { return }
        connect()
    }

    // MARK: - Heartbeat and clock skew (docs/06 §3.9)

    private func startHeartbeat(every interval: TimeInterval) {
        heartbeat?.cancel()
        heartbeat = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(interval))
                await self?.ping()
            }
        }
    }

    private func ping() async {
        guard let task else { return }
        sequence += 1
        let message = Message(id: "c-\(sequence)", type: "ping",
                              payload: try? JSONValue(encoding: PingPayload(clientTime: Date())))
        if let data = try? ProtocolDecoder.encoder.encode(message) {
            try? await task.send(.string(String(decoding: data, as: UTF8.self)))
        }
    }

    /// Median of the last five samples, so one slow round trip does not swing it.
    private func recordOffset(clientTime: Date, serverTime: Date) {
        let now = Date()
        let rtt = now.timeIntervalSince(clientTime)
        let sample = serverTime.timeIntervalSince(now) + rtt / 2
        offsetSamples.append(sample)
        if offsetSamples.count > 5 { offsetSamples.removeFirst() }
        clockOffset = offsetSamples.sorted()[offsetSamples.count / 2]
    }
}

import Foundation
import SenteNet

/// Stands in for the WebSocket: the test pushes server events in, and reads back
/// what the store tried to send.
final class FakeTransport: GameTransport, @unchecked Sendable {
    let events: AsyncStream<ServerEvent>
    let status: AsyncStream<ConnectionStatus>
    private let eventContinuation: AsyncStream<ServerEvent>.Continuation
    private let statusContinuation: AsyncStream<ConnectionStatus>.Continuation
    private let lock = NSLock()
    private var recorded: [ClientCommand] = []
    var offset: TimeInterval = 0
    private(set) var connectCalls = 0
    private(set) var closeCalls = 0

    init() {
        (events, eventContinuation) = AsyncStream.makeStream()
        (status, statusContinuation) = AsyncStream.makeStream()
    }

    var clockOffset: TimeInterval { get async { offset } }
    func connect() async { connectCalls += 1 }
    func close() async { closeCalls += 1; eventContinuation.finish(); statusContinuation.finish() }
    func send(_ command: ClientCommand) async {
        lock.withLock { recorded.append(command) }
    }

    var sent: [ClientCommand] { lock.withLock { recorded } }

    func push(_ event: ServerEvent) { eventContinuation.yield(event) }
    func push(status: ConnectionStatus) { statusContinuation.yield(status) }
}

// MARK: - Payload builders. Decoded from JSON, the same way the real socket does it,
// so the fixtures stay honest about the wire shape.

enum Fixture {
    static func decode<T: Decodable>(_ type: T.Type, _ json: String) -> T {
        try! ProtocolDecoder.json.decode(T.self, from: Data(json.utf8))
    }

    static func board(_ rows: [String]) -> String {
        precondition(rows.allSatisfy { $0.count == rows.count })
        return rows.joined()
    }

    static let emptyNine = String(repeating: ".", count: 81)

    static func gameState(board: String = emptyNine, toPlay: String = "black", moveNo: Int = 0,
                          phase: String = "playing", lastMove: String? = nil, hash: String,
                          capturesBlack: Int = 0, capturesWhite: Int = 0, komi: Double = 6.5) -> GameStatePayload {
        let last = lastMove.map { "\"\($0)\"" } ?? "null"
        return decode(GameStatePayload.self, """
        {"game_id":"g1","phase":"\(phase)","rules":"japanese","rules_version":"1.0.0",
         "board_size":9,"komi":\(komi),"handicap":0,"board":"\(board)","board_hash":"\(hash)",
         "to_play":"\(toPlay)","move_no":\(moveNo),"last_move":\(last),"ko_point":null,
         "captures":{"black":\(capturesBlack),"white":\(capturesWhite)},"consecutive_passes":0,
         "clock":{"black":{"main_ms":600000,"periods_left":3,"period_ms":30000},
                  "white":{"main_ms":540000,"periods_left":3,"period_ms":30000},
                  "move_deadline":"2026-08-28T09:24:03Z"},
         "server_time":"2026-08-28T09:14:03Z"}
        """)
    }

    static func moveMade(moveNo: Int, color: String, kind: String = "play", point: String? = nil,
                         captured: [String] = [], hash: String) -> MoveMadePayload {
        let pointJSON = point.map { "\"\($0)\"" } ?? "null"
        let capturedJSON = captured.map { "\"\($0)\"" }.joined(separator: ",")
        return decode(MoveMadePayload.self, """
        {"game_id":"g1","move_no":\(moveNo),"color":"\(color)","kind":"\(kind)","point":\(pointJSON),
         "captured":[\(capturedJSON)],"board_hash":"\(hash)",
         "clock":{"black":{"main_ms":590000,"periods_left":3,"period_ms":30000},
                  "white":{"main_ms":540000,"periods_left":3,"period_ms":30000},
                  "move_deadline":"2026-08-28T09:25:03Z"}}
        """)
    }

    static func scoringState(dead: [String], blackAccepted: Bool, whiteAccepted: Bool,
                             black: Double, white: Double) -> ScoringStatePayload {
        decode(ScoringStatePayload.self, """
        {"game_id":"g1","dead_points":[\(dead.map { "\"\($0)\"" }.joined(separator: ","))],
         "black_accepted":\(blackAccepted),"white_accepted":\(whiteAccepted),
         "score":{"black":\(black),"white":\(white),"margin":\(abs(black - white))}}
        """)
    }

    static func gameOver(winner: String?, reason: String) -> GameOverPayload {
        decode(GameOverPayload.self, """
        {"game_id":"g1","result":{"winner":\(winner.map { "\"\($0)\"" } ?? "null"),"reason":"\(reason)"}}
        """)
    }
}

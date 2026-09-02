import Foundation
import GoKit

// The client half of docs/06. Every shape here mirrors a struct in the server's
// internal/httpapi/protocol.go; the decode tests pin the JSON so the two cannot
// drift without a test going red.

public enum Protocol {
    public static let version = 1
}

// MARK: - REST

public struct GuestSignUp: Decodable, Sendable {
    public struct User: Decodable, Sendable {
        public let id: String
        public let displayName: String
        public let friendCode: String
        public let isGuest: Bool
    }
    public let user: User
    public let accessToken: String
    public let expiresIn: Int
    public let refreshToken: String?
}

/// What `POST /v1/auth/refresh` returns: a new pair, the old refresh token spent.
public struct Session: Decodable, Sendable {
    public let accessToken: String
    public let expiresIn: Int
    public let refreshToken: String
}

public struct ServerConfig: Decodable, Sendable {
    public let rulesVersion: String
    public let protocolVersions: [Int]
    public let serverTime: Date
    public let boardSizes: [Int]
    public let featureFlags: [String: Bool]
}

public struct TimeControl: Codable, Sendable, Equatable {
    public enum Kind: String, Codable, Sendable { case absolute, byoyomi, fischer, correspondence }
    public var kind: Kind
    public var mainTimeMs: Int?
    public var periods: Int?
    public var periodTimeMs: Int?
    public var incrementMs: Int?
    public var daysPerMove: Int?

    public init(kind: Kind, mainTimeMs: Int? = nil, periods: Int? = nil, periodTimeMs: Int? = nil,
                incrementMs: Int? = nil, daysPerMove: Int? = nil) {
        self.kind = kind; self.mainTimeMs = mainTimeMs; self.periods = periods
        self.periodTimeMs = periodTimeMs; self.incrementMs = incrementMs; self.daysPerMove = daysPerMove
    }

    public static let standard = TimeControl(kind: .byoyomi, mainTimeMs: 20 * 60_000, periods: 3, periodTimeMs: 30_000)
    public static let quick = TimeControl(kind: .absolute, mainTimeMs: 10 * 60_000)
    public static let correspondence = TimeControl(kind: .correspondence, daysPerMove: 2)

    /// Short label for lists: "20 phút + 3×30″", "3 giờ", "2 ngày/nước".
    public var summary: String {
        let main = TimeLimits.format(ms: mainTimeMs ?? 0)
        switch kind {
        case .absolute: return main
        case .fischer: return "\(main) + \((incrementMs ?? 0) / 1000)″"
        case .byoyomi: return "\(main) + \(periods ?? 0)×\((periodTimeMs ?? 0) / 1000)″"
        case .correspondence: return String(localized: "\(daysPerMove ?? 0) ngày/nước", bundle: SenteNetL10n.bundle())
        }
    }
}

public struct GameConfigRequest: Encodable, Sendable {
    public var boardSize: Int
    public var rules: String
    public var komi: Double?
    public var handicap: Int
    public var timeControl: TimeControl
    public var creatorColor: String

    public init(boardSize: Int = 9, rules: RuleSet = .japanese, komi: Double? = nil, handicap: Int = 0,
                timeControl: TimeControl = .standard, creatorColor: String = "random") {
        self.boardSize = boardSize; self.rules = rules.rawValue; self.komi = komi
        self.handicap = handicap; self.timeControl = timeControl; self.creatorColor = creatorColor
    }
}

public struct Challenge: Decodable, Sendable, Identifiable, Equatable {
    public struct Config: Decodable, Sendable, Equatable {
        public let boardSize: Int
        public let rules: String
        public let komi: Double
        public let handicap: Int
        public let timeControl: TimeControl
    }
    public let code: String
    public let shareUrl: String?
    public let creatorName: String?
    public let creatorColor: String
    public let status: String
    public let config: Config
    public let gameId: String?
    public let expiresAt: Date
    public let isMine: Bool

    public var id: String { code }
}

public struct AcceptedChallenge: Decodable, Sendable {
    public let gameId: String
    public let yourColor: String
}

public struct GameSummary: Decodable, Sendable, Identifiable, Equatable, Hashable {
    public let gameId: String
    public let boardSize: Int
    public let rules: String
    public let phase: String
    public let toPlay: String?
    public let myColor: String
    public let yourTurn: Bool
    public let opponentId: String?
    public let opponentName: String
    public let moveNo: Int
    public let moveDeadline: Date?
    public let lastActivityAt: Date
    public let result: GameResultPayload?

    public var id: String { gameId }
    public var isActive: Bool { phase == "playing" || phase == "scoring" }
}

public struct GameMoves: Decodable, Sendable {
    public struct Item: Decodable, Sendable, Equatable {
        public let moveNo: Int
        public let color: String
        public let kind: String
        public let point: String?
    }
    public let gameId: String
    public let boardSize: Int
    public let rules: String
    public let komi: Double
    public let handicap: Int
    public let items: [Item]
}

// MARK: - WebSocket envelope

public struct Message: Codable, Sendable {
    public var id: String?
    public var re: String?
    public var type: String
    public var ts: Date
    public var payload: JSONValue?

    public init(id: String? = nil, re: String? = nil, type: String, ts: Date = Date(), payload: JSONValue? = nil) {
        self.id = id; self.re = re; self.type = type; self.ts = ts; self.payload = payload
    }
}

// MARK: - Server → client payloads

public struct HelloPayload: Decodable, Sendable {
    public let serverTime: Date
    public let rulesVersion: String
    public let heartbeatIntervalMs: Int
    public let protocolVersion: Int
}

public struct ClockSidePayload: Decodable, Sendable, Equatable {
    public let mainMs: Int
    public let periodsLeft: Int?
    public let periodMs: Int?
}

public struct ClockPayload: Decodable, Sendable, Equatable {
    public let black: ClockSidePayload
    public let white: ClockSidePayload
    public let moveDeadline: Date?
}

public struct CapturesPayload: Decodable, Sendable, Equatable {
    public let black: Int
    public let white: Int
}

public struct ScorePayload: Decodable, Sendable, Equatable, Hashable {
    public let black: Double
    public let white: Double
    public let margin: Double
}

public struct GameResultPayload: Decodable, Sendable, Equatable, Hashable {
    public let winner: String?
    public let reason: String
    public let score: ScorePayload?
}

public struct GameStatePayload: Decodable, Sendable {
    public let gameId: String
    public let phase: String
    public let rules: String
    public let rulesVersion: String
    public let boardSize: Int
    public let komi: Double
    public let handicap: Int
    public let board: String
    public let boardHash: String
    public let toPlay: String
    public let moveNo: Int
    public let lastMove: String?
    public let koPoint: String?
    public let captures: CapturesPayload
    public let consecutivePasses: Int
    public let clock: ClockPayload
    public let result: GameResultPayload?
    public let serverTime: Date
}

public struct MoveMadePayload: Decodable, Sendable {
    public let gameId: String
    public let moveNo: Int
    public let color: String
    public let kind: String
    public let point: String?
    public let captured: [String]?
    public let boardHash: String
    public let clock: ClockPayload
}

public struct ScoringStatePayload: Decodable, Sendable {
    public let gameId: String
    public let deadPoints: [String]
    public let suggestedPoints: [String]?
    public let blackAccepted: Bool
    public let whiteAccepted: Bool
    public let score: ScorePayload?
}

public struct GameOverPayload: Decodable, Sendable {
    public let gameId: String
    public let result: GameResultPayload
}

public struct ClockAdjustedPayload: Decodable, Sendable {
    public let gameId: String
    public let color: String
    public let deltaMs: Int
    public let reason: String
}

public struct ErrorPayload: Decodable, Sendable, Equatable {
    public let code: String
    public let message: String
}

public struct PongPayload: Decodable, Sendable {
    public let clientTime: Date
    public let serverTime: Date
}

// MARK: - Client → server payloads

public struct MovePayload: Encodable, Sendable {
    public let clientMoveId: String
    public let expectedMoveNo: Int
    public let kind: String
    public let point: String?
}

public struct MarkDeadPayload: Encodable, Sendable { public let point: String }
public struct ChatPayload: Encodable, Sendable { public let code: String }
public struct AcceptPayload: Encodable, Sendable { public let accepted: Bool }
public struct UndoResponsePayload: Encodable, Sendable { public let accept: Bool }
public struct PingPayload: Encodable, Sendable { public let clientTime: Date }

// MARK: - Typed events

/// What the connection hands the store, already decoded. Unknown message types
/// are dropped on the floor: the protocol is forward compatible (docs/03 ADR-014).
public enum ServerEvent: Sendable {
    case hello(HelloPayload)
    case gameState(GameStatePayload)
    case moveMade(MoveMadePayload)
    case scoringState(ScoringStatePayload)
    case gameOver(GameOverPayload)
    case clockAdjusted(ClockAdjustedPayload)
    case undoRequested(by: String)
    case undoResult(accepted: Bool, moveNo: Int)
    /// A canned quick-chat message from either player (including our own echo).
    case chat(by: String, code: String)
    case resyncRequired(moveNo: Int)
    case error(ErrorPayload, replyTo: String?)
    case pong(PongPayload)
}

public enum ProtocolDecoder {
    public static let json: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .custom { decoder in
            let value = try decoder.singleValueContainer().decode(String.self)
            if let date = iso8601Fractional.date(from: value) ?? iso8601Plain.date(from: value) {
                return date
            }
            throw DecodingError.dataCorruptedError(in: try decoder.singleValueContainer(),
                                                   debugDescription: "bad date \(value)")
        }
        return decoder
    }()

    public static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        encoder.dateEncodingStrategy = .custom { date, encoder in
            var container = encoder.singleValueContainer()
            try container.encode(iso8601Fractional.string(from: date))
        }
        return encoder
    }()

    // Go writes RFC 3339 with nanoseconds; iOS's ISO8601DateFormatter reads at most
    // milliseconds, so anything longer is trimmed before parsing.
    nonisolated(unsafe) static let iso8601Fractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()
    nonisolated(unsafe) static let iso8601Plain: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()

    public static func event(from message: Message) throws -> ServerEvent? {
        func decode<T: Decodable>(_: T.Type) throws -> T {
            guard let payload = message.payload else {
                throw DecodingError.valueNotFound(T.self, .init(codingPath: [], debugDescription: "no payload"))
            }
            return try json.decode(T.self, from: try JSONEncoder().encode(payload))
        }
        switch message.type {
        case "hello": return .hello(try decode(HelloPayload.self))
        case "game_state": return .gameState(try decode(GameStatePayload.self))
        case "move_made": return .moveMade(try decode(MoveMadePayload.self))
        case "scoring_state": return .scoringState(try decode(ScoringStatePayload.self))
        case "game_over": return .gameOver(try decode(GameOverPayload.self))
        case "clock_adjusted": return .clockAdjusted(try decode(ClockAdjustedPayload.self))
        case "undo_requested":
            struct P: Decodable { let by: String }
            return .undoRequested(by: try decode(P.self).by)
        case "undo_result":
            struct P: Decodable { let accepted: Bool; let moveNo: Int }
            let p = try decode(P.self)
            return .undoResult(accepted: p.accepted, moveNo: p.moveNo)
        case "resync_required":
            struct P: Decodable { let moveNo: Int }
            return .resyncRequired(moveNo: try decode(P.self).moveNo)
        case "chat":
            struct P: Decodable { let by: String; let code: String }
            let p = try decode(P.self)
            return .chat(by: p.by, code: p.code)
        case "error": return .error(try decode(ErrorPayload.self), replyTo: message.re)
        case "pong": return .pong(try decode(PongPayload.self))
        default: return nil
        }
    }
}

/// Untyped JSON, so an envelope can be decoded before its payload type is known.
public enum JSONValue: Codable, Sendable, Equatable {
    case string(String), number(Double), bool(Bool), null
    case array([JSONValue]), object([String: JSONValue])

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() { self = .null }
        else if let b = try? container.decode(Bool.self) { self = .bool(b) }
        else if let n = try? container.decode(Double.self) { self = .number(n) }
        else if let s = try? container.decode(String.self) { self = .string(s) }
        else if let a = try? container.decode([JSONValue].self) { self = .array(a) }
        else if let o = try? container.decode([String: JSONValue].self) { self = .object(o) }
        else { throw DecodingError.dataCorruptedError(in: container, debugDescription: "unsupported JSON") }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .null: try container.encodeNil()
        case .bool(let b): try container.encode(b)
        case .number(let n):
            if n == n.rounded(), abs(n) < 1e15 { try container.encode(Int64(n)) } else { try container.encode(n) }
        case .string(let s): try container.encode(s)
        case .array(let a): try container.encode(a)
        case .object(let o): try container.encode(o)
        }
    }

    /// Wraps any Encodable so it can ride inside a Message.
    public init<T: Encodable>(encoding value: T) throws {
        let data = try ProtocolDecoder.encoder.encode(value)
        self = try JSONDecoder().decode(JSONValue.self, from: data)
    }
}

/// GET /v1/me/stats — the personal record.
public struct PlayerStats: Decodable, Sendable {
    public struct Line: Decodable, Sendable {
        public let games: Int
        public let wins: Int
        public let losses: Int
    }
    public let games: Int
    public let wins: Int
    public let losses: Int
    public let draws: Int
    public let bySize: [String: Line]
}

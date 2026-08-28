import Foundation

/// A player, and by extension the colour of their stones.
public enum Player: String, Sendable, Codable, CaseIterable {
    case black
    case white

    public var opponent: Player { self == .black ? .white : .black }
}

/// The two scoring systems GoKit supports. See docs/02-go-rules-spec.md.
///
/// The choice affects more than counting: Japanese uses basic ko and voids a game
/// on repetition, Chinese uses positional superko.
public enum RuleSet: String, Sendable, Codable, CaseIterable {
    case japanese
    case chinese
}

public enum Phase: String, Sendable, Codable {
    case playing
    case scoring
    case finished
}

public enum Move: Equatable, Sendable {
    case play(Point)
    case pass
    case resign
}

public enum MoveError: Error, Equatable, Sendable {
    case outOfBounds
    case occupied
    case notYourTurn
    case suicide
    case ko
    case superko
    case gameNotPlaying

    /// Wire code shared with the server; see docs/06 §3.6.
    public var code: String {
        switch self {
        case .outOfBounds: "out_of_bounds"
        case .occupied: "occupied"
        case .notYourTurn: "not_your_turn"
        case .suicide: "suicide"
        case .ko: "ko"
        case .superko: "superko"
        case .gameNotPlaying: "game_not_playing"
        }
    }
}

/// Why a finished game ended.
public enum EndReason: String, Sendable, Codable {
    case counting
    case resignation
    case timeout
    case repetition
    case abandonment
    case mutualDraw = "mutual_draw"
}

public struct GameResult: Equatable, Sendable, Codable {
    /// `nil` for a void game -- Japanese repetition has no winner.
    public let winner: Player?
    public let reason: EndReason
    public let score: Score?

    public init(winner: Player?, reason: EndReason, score: Score? = nil) {
        self.winner = winner
        self.reason = reason
        self.score = score
    }
}

/// Prisoner counts, keyed by the player who took them.
public struct Captures: Equatable, Sendable, Codable {
    public var black: Int
    public var white: Int

    public init(black: Int = 0, white: Int = 0) {
        self.black = black
        self.white = white
    }

    public subscript(_ player: Player) -> Int {
        get { player == .black ? black : white }
        set { if player == .black { black = newValue } else { white = newValue } }
    }

    func adding(_ count: Int, to player: Player) -> Captures {
        var copy = self
        copy[player] += count
        return copy
    }
}

/// A board intersection. Row 0 is the top row, matching the internal layout and
/// the `board` string on the wire; display coordinates are converted separately.
public struct Point: Hashable, Sendable, Codable, CustomStringConvertible {
    public let col: Int
    public let row: Int

    public init(col: Int, row: Int) {
        self.col = col
        self.row = row
    }

    public var description: String { "(\(col),\(row))" }
}

/// Board sizes the rules are specified for.
public enum BoardSize {
    public static let supported = [9, 13, 19]

    public static func isSupported(_ size: Int) -> Bool { supported.contains(size) }
}

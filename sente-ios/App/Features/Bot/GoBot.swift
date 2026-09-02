import Foundation
import GoKit

/// Bot strength, worded by what the bot actually does. The lower two are
/// heuristics; the upper two are Monte-Carlo tree search (MCTS.swift).
enum BotLevel: Int, Codable, CaseIterable, Identifiable, Hashable {
    case novice = 1, greedy = 2, thoughtful = 3, deep = 4
    var id: Int { rawValue }

    var title: String {
        switch self {
        case .novice: LS(localized: "Mới tập")
        case .greedy: LS(localized: "Biết bắt quân")
        case .thoughtful: LS(localized: "Suy tính")
        case .deep: LS(localized: "Cao thủ")
        }
    }
}

/// Deterministic RNG so bot games replay exactly under test.
struct SplitMix64: RandomNumberGenerator, Sendable {
    private var state: UInt64
    init(seed: UInt64) { state = seed }
    mutating func next() -> UInt64 {
        state &+= 0x9E37_79B9_7F4A_7C15
        var z = state
        z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
        z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
        return z ^ (z >> 31)
    }
}

/// An offline Go player built on GoKit. Every move it returns is legal by
/// construction: candidates come from the engine itself.
struct GoBot: Sendable {
    let level: BotLevel
    private var rng: SplitMix64

    // MCTS budgets for the two search levels; tests shrink them. The top level
    // is bounded by time, so it plays as strong as the device allows.
    var searchIterations = 700
    var searchDeadline: TimeInterval? = 1.2
    var deepIterations = 20000
    var deepDeadline: TimeInterval? = 4.0

    init(level: BotLevel, seed: UInt64 = .random(in: 0 ... .max)) {
        self.level = level
        self.rng = SplitMix64(seed: seed)
    }

    mutating func chooseMove(_ engine: GameEngine) -> Move {
        let side = engine.toPlay
        // A runaway game is ended rather than played to the heat death of the sun.
        guard engine.state.moveNumber < engine.state.size * engine.state.size * 2 else { return .pass }

        if level == .thoughtful || level == .deep {
            var search = MCTSBot(iterations: level == .deep ? deepIterations : searchIterations,
                                 deadline: level == .deep ? deepDeadline : searchDeadline,
                                 canResign: level == .deep,
                                 seed: rng.next())
            return search.chooseMove(engine)
        }

        let candidates = Self.orderedLegalMoves(engine).filter { !Self.isOwnEye($0, board: engine.board, side: side) }
        guard !candidates.isEmpty else { return .pass }

        let scored: [(point: Point, score: Double)] = candidates.map { point in
            (point, score(of: point, in: engine, for: side))
        }
        guard let best = scored.max(by: { $0.score < $1.score }) else { return .pass }

        // The opponent passed and the count already favours us: take the win
        // unless something on the board is genuinely worth more than a capture.
        if engine.state.consecutivePasses == 1, BotHeuristics.areaMargin(engine, for: side) > 0, best.score < 8 {
            return .pass
        }
        // Nothing useful left (dame and self-harm only): stop playing.
        if best.score < 0.6 { return .pass }
        return .play(best.point)
    }

    /// The two beginner levels: shape sense, plus tactics for the greedy one.
    private mutating func score(of point: Point, in engine: GameEngine, for side: Player) -> Double {
        var value = BotHeuristics.positional(point, engine: engine)
        switch level {
        case .novice:
            // Mostly shape-blind, but even beginners grab a free capture.
            if let after = try? engine.apply(.play(point), by: side) {
                value += 6 * Double(BotHeuristics.captured(by: side, from: engine, to: after))
            }
            value += Double(rng.next() % 1000) / 1000 * 4
        case .greedy, .thoughtful, .deep:
            guard let after = try? engine.apply(.play(point), by: side) else { return -100 }
            value += 10 * Double(BotHeuristics.captured(by: side, from: engine, to: after))
            value += BotHeuristics.rescueValue(from: engine, to: after, side: side)
            value += BotHeuristics.threatValue(after: after, side: side)
            value += 0.35 * Double(after.board.liberties(at: point))
            if after.board.liberties(at: point) == 1 {
                value -= 6 // even the greedy bot avoids obvious self-atari
            }
            value += Double(rng.next() % 1000) / 1000 * 1.6
        }
        return value
    }

    /// legalMoves() is a Set, and Set order changes per process launch; iterating
    /// it directly broke the seeded-replay promise above. Board order instead.
    static func orderedLegalMoves(_ engine: GameEngine) -> [Point] {
        engine.legalMoves().sorted { ($0.row, $0.col) < ($1.row, $1.col) }
    }

    // MARK: - Shape

    /// A point every bot must refuse to fill: doing so kills its own group.
    /// All four sides ours, and not too many enemy diagonals (the false-eye rule).
    static func isOwnEye(_ point: Point, board: Board, side: Player) -> Bool {
        let size = board.size
        for neighbor in neighbors(of: point, size: size) where board[neighbor] != side { return false }
        var enemyDiagonals = 0, offBoard = 0
        for (dx, dy) in [(-1, -1), (-1, 1), (1, -1), (1, 1)] {
            let diagonal = Point(col: point.col + dx, row: point.row + dy)
            if !board.contains(diagonal) { offBoard += 1 } else if board[diagonal] == side.opponent { enemyDiagonals += 1 }
        }
        return offBoard > 0 ? enemyDiagonals == 0 : enemyDiagonals <= 1
    }

    static func neighbors(of point: Point, size: Int) -> [Point] {
        [Point(col: point.col - 1, row: point.row), Point(col: point.col + 1, row: point.row),
         Point(col: point.col, row: point.row - 1), Point(col: point.col, row: point.row + 1)]
            .filter { $0.col >= 0 && $0.col < size && $0.row >= 0 && $0.row < size }
    }
}

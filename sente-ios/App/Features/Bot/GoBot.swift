import Foundation
import GoKit

/// Bot strength, worded by what the bot actually does.
enum BotLevel: Int, Codable, CaseIterable, Identifiable, Hashable {
    case novice = 1, greedy = 2, thoughtful = 3, deep = 4
    var id: Int { rawValue }

    var title: String {
        switch self {
        case .novice: String(localized: "Mới tập")
        case .greedy: String(localized: "Biết bắt quân")
        case .thoughtful: String(localized: "Tính một nước")
        case .deep: String(localized: "Suy nghĩ sâu")
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
/// construction: candidates come from the engine itself, so the bot cannot
/// break a rule it does not know about.
struct GoBot: Sendable {
    let level: BotLevel
    private var rng: SplitMix64

    // Search budgets for .deep, small enough for a phone and for tests.
    var playoutCandidates = 6
    var playoutsPerCandidate = 16
    var playoutDepth = 50

    init(level: BotLevel, seed: UInt64 = .random(in: 0 ... .max)) {
        self.level = level
        self.rng = SplitMix64(seed: seed)
    }

    mutating func chooseMove(_ engine: GameEngine) -> Move {
        let side = engine.toPlay
        // A runaway game is ended rather than played to the heat death of the sun.
        guard engine.state.moveNumber < engine.state.size * engine.state.size * 2 else { return .pass }

        let candidates = engine.legalMoves().filter { !Self.isOwnEye($0, board: engine.board, side: side) }
        guard !candidates.isEmpty else { return .pass }

        let scored: [(point: Point, score: Double)] = candidates.map { point in
            (point, score(of: point, in: engine, for: side))
        }
        guard let best = scored.max(by: { $0.score < $1.score }) else { return .pass }

        // The opponent passed and the count already favours us: take the win
        // unless something on the board is genuinely worth more than a capture.
        if engine.state.consecutivePasses == 1, areaMargin(engine, for: side) > 0, best.score < 8 {
            return .pass
        }
        // Nothing useful left (dame and self-harm only): stop playing.
        if best.score < 0.6 { return .pass }

        if level == .deep {
            let top = scored.sorted { $0.score > $1.score }.prefix(playoutCandidates)
            if let chosen = deepChoice(among: Array(top), in: engine, for: side) { return .play(chosen) }
        }
        return .play(best.point)
    }

    // MARK: - Evaluation

    private mutating func score(of point: Point, in engine: GameEngine, for side: Player) -> Double {
        var value = positional(point, engine: engine, side: side)
        switch level {
        case .novice:
            // Mostly shape-blind, but even beginners grab a free capture.
            if let after = try? engine.apply(.play(point), by: side) {
                value += 6 * Double(captured(by: side, from: engine, to: after))
            }
            value += Double(rng.next() % 1000) / 1000 * 4
        case .greedy, .thoughtful, .deep:
            guard let after = try? engine.apply(.play(point), by: side) else { return -100 }
            value += 10 * Double(captured(by: side, from: engine, to: after))
            value += rescueValue(from: engine, to: after, side: side)
            value += threatValue(after: after, side: side)
            value += 0.35 * Double(after.board.liberties(at: point))
            if level != .greedy {
                value -= selfHarm(after: after, side: side)
            } else if after.board.liberties(at: point) == 1 {
                value -= 6 // even the greedy bot avoids obvious self-atari
            }
            let noise = level == .greedy ? 1.6 : 0.5
            value += Double(rng.next() % 1000) / 1000 * noise
        }
        return value
    }

    /// Opening sense: the third and fourth lines are worth more than the first,
    /// and playing near existing stones beats drifting into nowhere.
    private func positional(_ point: Point, engine: GameEngine, side: Player) -> Double {
        let size = engine.state.size
        let line = min(point.col, point.row, size - 1 - point.col, size - 1 - point.row)
        var value: Double = [(-1.6), 0.4, 1.6, 1.3][min(line, 3)]
        if line >= 4 { value = 0.9 }
        let contact = Self.neighbors(of: point, size: size).contains { engine.board[$0] != nil }
        if contact { value += 0.5 }
        if engine.state.moveNumber < 6 && line == 2 { value += 0.6 }
        return value
    }

    private func captured(by side: Player, from before: GameEngine, to after: GameEngine) -> Int {
        let count = { (engine: GameEngine) in side == .black ? engine.state.captures.black : engine.state.captures.white }
        return count(after) - count(before)
    }

    /// Reward for a chain of ours that was in atari and now breathes.
    private func rescueValue(from before: GameEngine, to after: GameEngine, side: Player) -> Double {
        var value = 0.0
        for stone in before.board.stones(of: side) where before.board.liberties(at: stone) == 1 {
            if after.board[stone] == side, after.board.liberties(at: stone) > 1 {
                value += 7 + Double(before.board.chain(at: stone).count)
                break
            }
        }
        return value
    }

    /// Enemy chains left in atari are stones half-way into the basket.
    private func threatValue(after: GameEngine, side: Player) -> Double {
        var counted = Set<Point>()
        var value = 0.0
        for stone in after.board.stones(of: side.opponent) where !counted.contains(stone) {
            let chain = after.board.chain(at: stone)
            counted.formUnion(chain)
            if after.board.liberties(at: stone) == 1 { value += 2.0 + Double(chain.count) }
        }
        return value
    }

    /// Own chains we would leave in atari — the opponent's next capture.
    private func selfHarm(after: GameEngine, side: Player) -> Double {
        var counted = Set<Point>()
        var value = 0.0
        for stone in after.board.stones(of: side) where !counted.contains(stone) {
            let chain = after.board.chain(at: stone)
            counted.formUnion(chain)
            if after.board.liberties(at: stone) == 1 { value += 6 + Double(chain.count) }
        }
        return value
    }

    // MARK: - Monte Carlo for .deep

    private mutating func deepChoice(among top: [(point: Point, score: Double)],
                                     in engine: GameEngine, for side: Player) -> Point? {
        var bestPoint: Point?
        var bestRate = -Double.infinity
        for candidate in top {
            guard let after = try? engine.apply(.play(candidate.point), by: side) else { continue }
            var wins = 0.0
            for _ in 0..<playoutsPerCandidate {
                let blackAhead = playout(from: after) > 0
                if blackAhead == (side == .black) { wins += 1 }
            }
            // Blend in the immediate evaluation: with a phone-sized number of
            // playouts, the win rate alone is too grainy to spot a plain capture.
            let rate = wins / Double(playoutsPerCandidate) + candidate.score / 40
            if rate > bestRate { bestRate = rate; bestPoint = candidate.point }
        }
        return bestPoint
    }

    /// Fast random continuation; returns the black-positive area margin.
    private mutating func playout(from start: GameEngine) -> Double {
        var engine = start
        var passes = 0
        for _ in 0..<playoutDepth {
            guard engine.state.phase == .playing else { break }
            let side = engine.toPlay
            var played = false
            for _ in 0..<10 {
                let point = Point(col: Int(rng.next() % UInt64(engine.state.size)),
                                  row: Int(rng.next() % UInt64(engine.state.size)))
                guard engine.board.isEmpty(point), !Self.isOwnEye(point, board: engine.board, side: side),
                      let next = try? engine.apply(.play(point), by: side) else { continue }
                engine = next
                played = true
                passes = 0
                break
            }
            if !played {
                guard let next = try? engine.apply(.pass, by: side) else { break }
                engine = next
                passes += 1
                if passes >= 2 { break }
            }
        }
        return areaMargin(engine, for: .black)
    }

    /// Stones plus surrounded territory, komi included; positive favours `side`.
    private func areaMargin(_ engine: GameEngine, for side: Player) -> Double {
        let map = engine.territory(deadStones: [])
        let black = Double(engine.board.stones(of: .black).count + map.black.count)
        let white = Double(engine.board.stones(of: .white).count + map.white.count) + engine.komi
        let margin = black - white
        return side == .black ? margin : -margin
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

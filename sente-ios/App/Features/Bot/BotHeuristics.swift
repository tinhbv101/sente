import Foundation
import GoKit

/// Position knowledge shared by the heuristic levels (as their whole brain) and
/// by MCTS (as move priors and playout reflexes). Free of randomness, so the
/// callers own their own noise.
enum BotHeuristics {
    /// Cheap prior: full tactical evaluation only where tactics can happen —
    /// next to stones. Anything floating in空 space is judged by shape alone,
    /// which cuts the cost of expanding a search node several-fold.
    static func prior(point: Point, in engine: GameEngine, for side: Player) -> Double {
        let contact = GoBot.neighbors(of: point, size: engine.state.size)
            .contains { engine.board[$0] != nil }
        return contact ? eval(point: point, in: engine, for: side)
                       : positional(point, engine: engine)
    }

    /// One-move evaluation: captures, rescues, threats, self-harm, shape.
    static func eval(point: Point, in engine: GameEngine, for side: Player) -> Double {
        guard let after = try? engine.apply(.play(point), by: side) else { return -100 }
        var value = positional(point, engine: engine)
        value += 10 * Double(captured(by: side, from: engine, to: after))
        value += rescueValue(from: engine, to: after, side: side)
        value += threatValue(after: after, side: side)
        value += 0.35 * Double(after.board.liberties(at: point))
        value -= selfHarm(after: after, side: side)
        return value
    }

    static func positional(_ point: Point, engine: GameEngine) -> Double {
        let size = engine.state.size
        let line = min(point.col, point.row, size - 1 - point.col, size - 1 - point.row)
        var value: Double = line >= 4 ? 0.9 : [(-1.6), 0.4, 1.6, 1.3][line]
        if GoBot.neighbors(of: point, size: size).contains(where: { engine.board[$0] != nil }) { value += 0.5 }
        if engine.state.moveNumber < 6 && line == 2 { value += 0.6 }
        return value
    }

    static func captured(by side: Player, from before: GameEngine, to after: GameEngine) -> Int {
        let count = { (engine: GameEngine) in side == .black ? engine.state.captures.black : engine.state.captures.white }
        return count(after) - count(before)
    }

    static func rescueValue(from before: GameEngine, to after: GameEngine, side: Player) -> Double {
        for stone in before.board.stones(of: side) where before.board.liberties(at: stone) == 1 {
            if after.board[stone] == side, after.board.liberties(at: stone) > 1 {
                return 7 + Double(before.board.chain(at: stone).count)
            }
        }
        return 0
    }

    static func threatValue(after: GameEngine, side: Player) -> Double {
        var counted = Set<Point>()
        var value = 0.0
        for stone in after.board.stones(of: side.opponent) where !counted.contains(stone) {
            let chain = after.board.chain(at: stone)
            counted.formUnion(chain)
            if after.board.liberties(at: stone) == 1 { value += 2.0 + Double(chain.count) }
        }
        return value
    }

    static func selfHarm(after: GameEngine, side: Player) -> Double {
        var counted = Set<Point>()
        var value = 0.0
        for stone in after.board.stones(of: side) where !counted.contains(stone) {
            let chain = after.board.chain(at: stone)
            counted.formUnion(chain)
            if after.board.liberties(at: stone) == 1 { value += 6 + Double(chain.count) }
        }
        return value
    }

    /// Stones plus surrounded territory, komi included; positive favours `side`.
    static func areaMargin(_ engine: GameEngine, for side: Player) -> Double {
        let map = engine.territory(deadStones: [])
        let black = Double(engine.board.stones(of: .black).count + map.black.count)
        let white = Double(engine.board.stones(of: .white).count + map.white.count) + engine.komi
        let margin = black - white
        return side == .black ? margin : -margin
    }

    /// The playout reflex: capture an enemy chain in atari, or pull a chain of
    /// ours out of one. Nil when nothing urgent is on the board.
    static func urgentMove(in engine: GameEngine, for side: Player) -> Move? {
        var counted = Set<Point>()
        // Enemy chains in atari: take the biggest.
        var bestCapture: (point: Point, size: Int)?
        for stone in engine.board.stones(of: side.opponent) where !counted.contains(stone) {
            let chain = engine.board.chain(at: stone)
            counted.formUnion(chain)
            guard engine.board.liberties(at: stone) == 1,
                  let liberty = lastLiberty(of: chain, in: engine) else { continue }
            if bestCapture == nil || chain.count > bestCapture!.size {
                if (try? engine.apply(.play(liberty), by: side)) != nil { bestCapture = (liberty, chain.count) }
            }
        }
        if let capture = bestCapture { return .play(capture.point) }

        counted.removeAll()
        for stone in engine.board.stones(of: side) where !counted.contains(stone) {
            let chain = engine.board.chain(at: stone)
            counted.formUnion(chain)
            guard engine.board.liberties(at: stone) == 1,
                  let liberty = lastLiberty(of: chain, in: engine),
                  let after = try? engine.apply(.play(liberty), by: side),
                  after.board.liberties(at: stone) > 1 else { continue }
            return .play(liberty)
        }
        return nil
    }

    private static func lastLiberty(of chain: Set<Point>, in engine: GameEngine) -> Point? {
        for stone in chain {
            for neighbor in GoBot.neighbors(of: stone, size: engine.state.size)
            where engine.board.isEmpty(neighbor) { return neighbor }
        }
        return nil
    }
}

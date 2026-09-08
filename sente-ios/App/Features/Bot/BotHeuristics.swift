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

    /// One chain that cannot make two eyes and has almost no liberties left.
    struct DeadShapedChain {
        let owner: Player
        let stones: Set<Point>
        let enclosedArea: Int
    }

    /// Area margin that counts dead-shaped groups as captured: their stones and
    /// space belong to the opponent. This is what lets a truncated playout see
    /// the point of a kill it has no time to finish.
    static func deadAwareMargin(_ engine: GameEngine, for side: Player) -> Double {
        var margin = areaMargin(engine, for: .black)
        for dead in deadShapedChains(engine) {
            let swing = Double(2 * dead.stones.count + 2 * dead.enclosedArea)
            margin += dead.owner == .black ? -swing : swing
        }
        return side == .black ? margin : -margin
    }

    /// The scoring suggestion for on-device games, in two tiers:
    /// 1. dead-shaped chains — no two eyes and out of liberties; and
    /// 2. stragglers — a chain whose every liberty lies in a region the enemy
    ///    border dominates three to one: the invasion stone left in territory.
    /// Benson-proved chains are never suggested. A seki can be over-marked,
    /// which is why the marks stay tappable.
    static func suggestDead(_ engine: GameEngine) -> Set<Point> {
        let size = engine.state.size
        var passAlive = Set<Point>()
        for player in [Player.black, .white] {
            for chain in engine.board.passAliveChains(for: player) { passAlive.formUnion(chain) }
        }
        var dead = Set<Point>()
        for candidate in deadShapedChains(engine) where candidate.stones.isDisjoint(with: passAlive) {
            dead.formUnion(candidate.stones)
        }

        // Tier 2 runs to a fixpoint: clearing one straggler can expose the next.
        while true {
            let board = engine.board.clearing(Array(dead))
            // Empty regions and the stones of each colour on their borders.
            var regionOf = [Int](repeating: -1, count: size * size)
            var borders: [[Player: Set<Point>]] = []
            var seen = [Bool](repeating: false, count: size * size)
            for row in 0..<size {
                for col in 0..<size {
                    let start = Point(col: col, row: row)
                    guard board.isEmpty(start), !seen[row * size + col] else { continue }
                    var stack = [start], points = [start]
                    var border: [Player: Set<Point>] = [:]
                    seen[row * size + col] = true
                    while let current = stack.popLast() {
                        for neighbor in GoBot.neighbors(of: current, size: size) {
                            if let color = board[neighbor] {
                                border[color, default: []].insert(neighbor)
                            } else if !seen[neighbor.row * size + neighbor.col] {
                                seen[neighbor.row * size + neighbor.col] = true
                                points.append(neighbor)
                                stack.append(neighbor)
                            }
                        }
                    }
                    for point in points { regionOf[point.row * size + point.col] = borders.count }
                    borders.append(border)
                }
            }

            var added = false
            var counted = Set<Point>()
            for color in [Player.black, .white] {
                for stone in board.stones(of: color) where !counted.contains(stone) {
                    let chain = board.chain(at: stone)
                    counted.formUnion(chain)
                    guard chain.isDisjoint(with: passAlive) else { continue }
                    var liberties = Set<Point>()
                    for member in chain {
                        for neighbor in GoBot.neighbors(of: member, size: size)
                        where board.isEmpty(neighbor) { liberties.insert(neighbor) }
                    }
                    guard !liberties.isEmpty else { continue }
                    let doomed = liberties.allSatisfy { liberty in
                        let region = regionOf[liberty.row * size + liberty.col]
                        guard region >= 0 else { return false }
                        let mine = borders[region][color]?.count ?? 0
                        let theirs = borders[region][color.opponent]?.count ?? 0
                        return theirs >= 3 * mine && theirs >= 3
                    }
                    if doomed {
                        dead.formUnion(chain)
                        added = true
                    }
                }
            }
            if !added { return dead }
        }
    }

    /// Chains with fewer than two eye-worthy regions and at most two liberties
    /// elsewhere — dead as they stand. Shared by the playout evaluation and the
    /// scoring suggestion.
    static func deadShapedChains(_ engine: GameEngine) -> [DeadShapedChain] {
        var out: [DeadShapedChain] = []
        let board = engine.board
        let size = engine.state.size

        // Empty regions enclosed by one colour: eye-worth 1 when too small to
        // split, 2 once big enough to make two eyes if the owner gets the move.
        struct EyeRegion { let owner: Player; let points: Set<Point>; let eyes: Int; let boundary: [Point] }
        var regions: [EyeRegion] = []
        var seen = [Bool](repeating: false, count: size * size)
        for row in 0..<size {
            for col in 0..<size {
                let start = Point(col: col, row: row)
                guard board.isEmpty(start), !seen[row * size + col] else { continue }
                var points: Set<Point> = [start], stack = [start], boundary: [Point] = []
                var touchesBlack = false, touchesWhite = false
                seen[row * size + col] = true
                while let current = stack.popLast() {
                    for neighbor in GoBot.neighbors(of: current, size: size) {
                        switch board[neighbor] {
                        case .black: touchesBlack = true; boundary.append(neighbor)
                        case .white: touchesWhite = true; boundary.append(neighbor)
                        case nil:
                            if !seen[neighbor.row * size + neighbor.col] {
                                seen[neighbor.row * size + neighbor.col] = true
                                points.insert(neighbor)
                                stack.append(neighbor)
                            }
                        }
                    }
                }
                guard touchesBlack != touchesWhite else { continue }
                regions.append(EyeRegion(owner: touchesBlack ? .black : .white, points: points,
                                         eyes: points.count >= 3 ? 2 : 1, boundary: boundary))
            }
        }

        var counted = Set<Point>()
        for color in [Player.black, .white] {
            for stone in board.stones(of: color) where !counted.contains(stone) {
                let chain = board.chain(at: stone)
                counted.formUnion(chain)
                var eyes = 0, enclosed = 0
                var libertiesElsewhere = Set<Point>()
                for member in chain {
                    for neighbor in GoBot.neighbors(of: member, size: size)
                    where board.isEmpty(neighbor) { libertiesElsewhere.insert(neighbor) }
                }
                for region in regions where region.owner == color
                    && region.boundary.contains(where: { chain.contains($0) }) {
                    eyes += region.eyes
                    enclosed += region.points.count
                    libertiesElsewhere.subtract(region.points)
                }
                guard eyes < 2, libertiesElsewhere.count <= 2 else { continue }
                out.append(DeadShapedChain(owner: color, stones: chain, enclosedArea: enclosed))
            }
        }
        return out
    }

    /// Life-and-death sight, the lessons' rule generalised: an empty region
    /// enclosed by a single colour is that group's eye space, and the point
    /// touching the most region points is where its life is decided — the centre
    /// of a straight or bent three, a pyramid four, a bulky five, a cross five.
    /// The value is the same for both sides: kill and live meet on one point.
    static func lifeDeathVitals(_ engine: GameEngine) -> [(point: Point, value: Double)] {
        let board = engine.board
        let size = engine.state.size
        var seen = [Bool](repeating: false, count: size * size)
        var vitals: [(point: Point, value: Double)] = []

        for row in 0..<size {
            for col in 0..<size {
                let start = Point(col: col, row: row)
                guard board.isEmpty(start), !seen[row * size + col] else { continue }
                // One empty region, with the stones around it.
                var region = [start], stack = [start], boundary: [Point] = []
                var touchesBlack = false, touchesWhite = false
                seen[row * size + col] = true
                while let current = stack.popLast() {
                    for neighbor in GoBot.neighbors(of: current, size: size) {
                        switch board[neighbor] {
                        case .black: touchesBlack = true; boundary.append(neighbor)
                        case .white: touchesWhite = true; boundary.append(neighbor)
                        case nil:
                            if !seen[neighbor.row * size + neighbor.col] {
                                seen[neighbor.row * size + neighbor.col] = true
                                region.append(neighbor)
                                stack.append(neighbor)
                            }
                        }
                    }
                }
                // Contested or open space has no single vital point.
                guard region.count >= 3, region.count <= 7, touchesBlack != touchesWhite else { continue }
                let inRegion = Set(region)
                var vital: (point: Point, touches: Int)?
                for point in region {
                    let touches = GoBot.neighbors(of: point, size: size).count { inRegion.contains($0) }
                    if vital == nil || touches > vital!.touches { vital = (point, touches) }
                }
                guard let vital, vital.touches >= 2 else { continue }
                // The stake: the surrounding group, decisive when this space is
                // nearly all the liberties it has left.
                var counted = Set<Point>(), outside = Set<Point>()
                var groupStones = 0
                for stone in boundary where !counted.contains(stone) {
                    let chain = board.chain(at: stone)
                    counted.formUnion(chain)
                    groupStones += chain.count
                    for member in chain {
                        for neighbor in GoBot.neighbors(of: member, size: size)
                        where board.isEmpty(neighbor) && !inRegion.contains(neighbor) {
                            outside.insert(neighbor)
                        }
                    }
                }
                let decisive = outside.count <= 2
                let stake = Double(groupStones + region.count)
                vitals.append((vital.point, (decisive ? 1.0 : 0.3) * min(3 + 0.8 * stake, 14)))
            }
        }
        return vitals
    }

    private static func lastLiberty(of chain: Set<Point>, in engine: GameEngine) -> Point? {
        for stone in chain {
            for neighbor in GoBot.neighbors(of: stone, size: engine.state.size)
            where engine.board.isEmpty(neighbor) { return neighbor }
        }
        return nil
    }
}

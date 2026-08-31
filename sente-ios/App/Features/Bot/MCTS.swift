import Foundation
import GoKit

/// Monte-Carlo tree search with UCT — the algorithm behind pre-neural Go
/// programs. Builds a tree of moves, walks it by upper confidence bounds,
/// finishes each walk with a heuristic-guided random playout, and propagates
/// the result back up. Strength scales with the budget, so one implementation
/// serves every level above the beginners.
struct MCTSBot {
    var iterations: Int
    /// Wall-clock cap; whichever of the two budgets ends first wins. This is
    /// what makes the top level "as strong as the phone allows".
    var deadline: TimeInterval?
    var explorationC = 0.7
    var priorWeight = 2.4
    /// Only the most promising moves get tree nodes: with phone-sized budgets,
    /// spreading visits over every legal move drowns the signal in noise.
    var maxBranch = 16
    /// Playouts are truncated and the position evaluated, rather than played to
    /// the end — far cheaper and far less noisy per sample.
    var playoutDepth = 28
    var canResign = false
    private var rng: SplitMix64

    init(iterations: Int, deadline: TimeInterval? = nil, canResign: Bool = false, seed: UInt64) {
        self.iterations = iterations
        self.deadline = deadline
        self.canResign = canResign
        self.rng = SplitMix64(seed: seed)
    }

    private final class Node {
        let move: Move?
        let mover: Player?          // who played `move`
        var wins = 0.0              // from the mover's perspective
        var visits = 0.0
        let prior: Double
        var untried: [(move: Move, prior: Double)]?
        var children: [Node] = []

        init(move: Move?, mover: Player?, prior: Double) {
            self.move = move
            self.mover = mover
            self.prior = prior
        }

        func uct(parentVisits: Double, c: Double, priorWeight: Double) -> Double {
            // First-play urgency: an unvisited branch is promising in proportion
            // to its prior, not infinitely — the budget is too small for courtesy visits.
            if visits == 0 { return 0.6 + priorWeight * prior + c * log(parentVisits).squareRoot() }
            return wins / visits
                + priorWeight * prior / (1 + visits).squareRoot()
                + c * (Foundation.log(parentVisits) / visits).squareRoot()
        }
    }

    mutating func chooseMove(_ engine: GameEngine) -> Move {
        guard engine.state.phase == .playing else { return .pass }
        let side = engine.toPlay
        let rootMoves = candidateMoves(engine)
        guard !rootMoves.isEmpty else { return .pass }
        if rootMoves.count == 1 { return rootMoves[0].move }

        let root = Node(move: nil, mover: nil, prior: 0)
        root.untried = rootMoves
        let start = Date()
        var iteration = 0
        while iteration < iterations {
            iteration += 1
            if let deadline, iteration % 16 == 0, Date().timeIntervalSince(start) > deadline { break }
            runOnce(root: root, engine: engine)
        }

        guard let best = root.children.max(by: { $0.visits < $1.visits }),
              let move = best.move else { return .pass }

        if canResign, engine.state.moveNumber > 20, best.visits > 40,
           best.wins / best.visits < 0.06,
           BotHeuristics.areaMargin(engine, for: side) < -12 {
            return .resign
        }
        return move
    }

    // MARK: - One tree walk

    private mutating func runOnce(root: Node, engine: GameEngine) {
        var node = root
        var sim = engine
        var path = [root]

        // Selection: follow UCT while the node is fully expanded.
        while node.untried?.isEmpty == true, !node.children.isEmpty, sim.state.phase == .playing {
            let parentVisits = max(node.visits, 1)
            node = node.children.max {
                $0.uct(parentVisits: parentVisits, c: explorationC, priorWeight: priorWeight)
                    < $1.uct(parentVisits: parentVisits, c: explorationC, priorWeight: priorWeight)
            }!
            if let move = node.move, let mover = node.mover, let next = try? sim.apply(move, by: mover) {
                sim = next
            }
            path.append(node)
        }

        // Expansion: try the most promising unexplored move first.
        if sim.state.phase == .playing {
            if node.untried == nil { node.untried = candidateMoves(sim) }
            if var untried = node.untried, !untried.isEmpty {
                let index = untried.indices.max { untried[$0].prior < untried[$1].prior }!
                let picked = untried.remove(at: index)
                node.untried = untried
                let mover = sim.toPlay
                if let next = try? sim.apply(picked.move, by: mover) {
                    sim = next
                    let child = Node(move: picked.move, mover: mover, prior: picked.prior / 8)
                    node.children.append(child)
                    path.append(child)
                }
            }
        }

        // Simulation and backpropagation. The margin becomes a smooth win
        // probability: a two-point lead and a twenty-point lead should not look
        // identical to the tree.
        let margin = playout(from: sim)
        let blackWinChance = 1 / (1 + exp(-margin / 5))
        for visited in path {
            visited.visits += 1
            guard let mover = visited.mover else { continue }
            visited.wins += mover == .black ? blackWinChance : 1 - blackWinChance
        }
    }

    /// Legal moves minus own eyes, each with a heuristic prior; pass only when
    /// it could sensibly end or concede nothing (late game, or answer a pass).
    private func candidateMoves(_ engine: GameEngine) -> [(move: Move, prior: Double)] {
        let side = engine.toPlay
        var moves: [(Move, Double)] = engine.legalMoves()
            .filter { !GoBot.isOwnEye($0, board: engine.board, side: side) }
            .map { (.play($0), BotHeuristics.prior(point: $0, in: engine, for: side)) }
        moves.sort { $0.1 > $1.1 }
        moves = Array(moves.prefix(maxBranch))
        let late = engine.state.moveNumber > engine.state.size * engine.state.size
        if moves.isEmpty || engine.state.consecutivePasses == 1 || late {
            moves.append((.pass, 0))
        }
        return moves
    }

    /// Guided playout: grab visible captures and escapes, otherwise random.
    private mutating func playout(from start: GameEngine) -> Double {
        var engine = start
        var passes = engine.state.consecutivePasses
        for _ in 0..<playoutDepth {
            guard engine.state.phase == .playing else { break }
            let side = engine.toPlay
            var move: Move?

            // Tactical reflexes first: a capture or an escape, if one is visible.
            if rng.next() % 8 != 0 {
                move = BotHeuristics.urgentMove(in: engine, for: side)
            }
            if move == nil {
                for _ in 0..<10 {
                    let point = Point(col: Int(rng.next() % UInt64(engine.state.size)),
                                      row: Int(rng.next() % UInt64(engine.state.size)))
                    guard engine.board.isEmpty(point),
                          !GoBot.isOwnEye(point, board: engine.board, side: side) else { continue }
                    move = .play(point)
                    break
                }
            }
            if let move, let next = try? engine.apply(move, by: side) {
                engine = next
                passes = 0
            } else if let next = try? engine.apply(.pass, by: side) {
                engine = next
                passes += 1
                if passes >= 2 { break }
            } else {
                break
            }
        }
        return BotHeuristics.areaMargin(engine, for: .black)
    }
}

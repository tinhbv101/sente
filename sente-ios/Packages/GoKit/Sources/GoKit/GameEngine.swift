import Foundation

/// The rules engine. Every operation is pure: `apply` returns a new engine and
/// leaves the receiver untouched, which is what makes optimistic move rollback on
/// the client a single assignment (docs/03 ADR-007).
public struct GameEngine: Equatable, Sendable {
    public let state: GameState
    public let rules: RuleSet
    public let komi: Double
    public let handicap: Int
    /// Board hashes of every position that has occurred, including the starting one.
    public let history: Set<UInt64>

    public init(state: GameState, rules: RuleSet, komi: Double, handicap: Int, history: Set<UInt64>) {
        self.state = state
        self.rules = rules
        self.komi = komi
        self.handicap = handicap
        self.history = history
    }

    // MARK: - Starting positions

    /// Komi conventions: 6.5 Japanese, 7.5 Chinese, and 0.5 whenever stones are
    /// given, so a handicap game cannot end in a draw (docs/02 §8).
    public static func defaultKomi(rules: RuleSet, handicap: Int) -> Double {
        if handicap > 0 { return 0.5 }
        return rules == .japanese ? 6.5 : 7.5
    }

    public static func newGame(
        size: Int,
        rules: RuleSet = .japanese,
        komi: Double? = nil,
        handicap: Int = 0
    ) -> GameEngine {
        precondition(handicap == 0 || (2...9).contains(handicap), "handicap must be 0 or 2...9")
        var board = Board(size: size)
        for point in Handicap.stones(count: handicap, size: size) {
            board = board.placing(.black, at: point)
        }
        // White moves first in a handicap game -- the placed stones are not moves.
        let hash = Zobrist.hash(of: board)
        let state = GameState(
            board: board,
            toPlay: handicap > 0 ? .white : .black,
            moveNumber: 0,
            koPoint: nil,
            captures: Captures(),
            consecutivePasses: 0,
            phase: .playing,
            result: nil,
            boardHash: hash
        )
        return GameEngine(
            state: state,
            rules: rules,
            komi: komi ?? defaultKomi(rules: rules, handicap: handicap),
            handicap: handicap,
            history: [hash]
        )
    }

    /// Builds an engine from an arbitrary position. Used by tests and by the
    /// server when rebuilding a game without replaying every move.
    public static func position(
        size: Int,
        black: [Point] = [],
        white: [Point] = [],
        toPlay: Player = .black,
        rules: RuleSet = .japanese,
        komi: Double? = nil,
        handicap: Int = 0,
        captures: Captures = Captures(),
        moveNumber: Int = 0
    ) -> GameEngine {
        var board = Board(size: size)
        for point in black { board = board.placing(.black, at: point) }
        for point in white { board = board.placing(.white, at: point) }
        let hash = Zobrist.hash(of: board)
        let state = GameState(
            board: board,
            toPlay: toPlay,
            moveNumber: moveNumber,
            koPoint: nil,
            captures: captures,
            consecutivePasses: 0,
            phase: .playing,
            result: nil,
            boardHash: hash
        )
        return GameEngine(
            state: state,
            rules: rules,
            komi: komi ?? defaultKomi(rules: rules, handicap: handicap),
            handicap: handicap,
            history: [hash]
        )
    }

    // MARK: - Queries

    public var board: Board { state.board }
    public var toPlay: Player { state.toPlay }

    public func chain(at point: Point) -> Set<Point> { state.board.chain(at: point) }
    public func liberties(at point: Point) -> Int { state.board.liberties(at: point) }

    /// Every point the player to move may legally play. Used for VoiceOver hints
    /// and for the illegal-move explanation in the UI.
    public func legalMoves() -> Set<Point> {
        guard state.phase == .playing else { return [] }
        return Set(state.board.allPoints.filter { validate(.play($0)).isSuccess })
    }

    // MARK: - Legality

    public func validate(_ move: Move) -> Result<Void, MoveError> {
        validate(move, by: state.toPlay)
    }

    /// Explicit-player form. The server needs it because it maps a connection to a
    /// colour before it knows whether that colour is to move.
    public func validate(_ move: Move, by player: Player) -> Result<Void, MoveError> {
        guard state.phase == .playing else { return .failure(.gameNotPlaying) }
        guard player == state.toPlay else { return .failure(.notYourTurn) }
        switch move {
        case .pass, .resign:
            return .success(())
        case .play(let point):
            return validatePlay(point, by: player).map { _ in () }
        }
    }

    public func apply(_ move: Move, by player: Player) throws -> GameEngine {
        if case .failure(let error) = validate(move, by: player) { throw error }
        return try apply(move)
    }

    /// Shared by `validate` and `apply` so legality is decided exactly once.
    private func validatePlay(_ point: Point, by player: Player) -> Result<Simulation, MoveError> {
        guard state.board.contains(point) else { return .failure(.outOfBounds) }
        guard state.board.isEmpty(point) else { return .failure(.occupied) }
        guard player == state.toPlay else { return .failure(.notYourTurn) }

        // Basic ko is a cheap pre-check; Japanese has no superko at all.
        if rules == .japanese, let ko = state.koPoint, ko == point {
            return .failure(.ko)
        }

        let simulation = simulate(placing: player, at: point)

        // Capturing first can create liberties, so suicide is judged last (docs/02 §3.2).
        if simulation.ownLiberties == 0 { return .failure(.suicide) }

        if rules == .chinese, history.contains(simulation.hash) {
            return .failure(.superko)
        }
        return .success(simulation)
    }

    // MARK: - Applying

    public func apply(_ move: Move) throws -> GameEngine {
        switch move {
        case .resign:
            return try applyResign()
        case .pass:
            return try applyPass()
        case .play(let point):
            return try applyPlay(point)
        }
    }

    private func applyResign() throws -> GameEngine {
        guard state.phase == .playing else { throw MoveError.gameNotPlaying }
        let result = GameResult(winner: state.toPlay.opponent, reason: .resignation)
        return replacing(state: GameState(
            board: state.board,
            toPlay: state.toPlay,
            moveNumber: state.moveNumber + 1,
            koPoint: nil,
            captures: state.captures,
            consecutivePasses: state.consecutivePasses,
            phase: .finished,
            result: result,
            boardHash: state.boardHash
        ))
    }

    private func applyPass() throws -> GameEngine {
        guard state.phase == .playing else { throw MoveError.gameNotPlaying }
        let passes = state.consecutivePasses + 1
        // A pass does not change the position, so it never enters the history.
        return replacing(state: GameState(
            board: state.board,
            toPlay: state.toPlay.opponent,
            moveNumber: state.moveNumber + 1,
            koPoint: nil,
            captures: state.captures,
            consecutivePasses: passes,
            phase: passes >= 2 ? .scoring : .playing,
            result: nil,
            boardHash: state.boardHash
        ))
    }

    private func applyPlay(_ point: Point) throws -> GameEngine {
        guard state.phase == .playing else { throw MoveError.gameNotPlaying }
        let player = state.toPlay
        let simulation: Simulation
        switch validatePlay(point, by: player) {
        case .success(let value): simulation = value
        case .failure(let error): throw error
        }

        // Japanese has no superko: a repeated position is a legal move that voids
        // the game (docs/02 §4.3).
        let voidsGame = rules == .japanese && history.contains(simulation.hash)

        let newState = GameState(
            board: simulation.board,
            toPlay: player.opponent,
            moveNumber: state.moveNumber + 1,
            koPoint: koPoint(after: simulation, played: point),
            captures: state.captures.adding(simulation.captured.count, to: player),
            consecutivePasses: 0,
            phase: voidsGame ? .finished : .playing,
            result: voidsGame ? GameResult(winner: nil, reason: .repetition) : nil,
            boardHash: simulation.hash
        )
        return GameEngine(
            state: newState,
            rules: rules,
            komi: komi,
            handicap: handicap,
            history: history.union([simulation.hash])
        )
    }

    private func replacing(state newState: GameState) -> GameEngine {
        GameEngine(state: newState, rules: rules, komi: komi, handicap: handicap, history: history)
    }

    // MARK: - Simulation

    struct Simulation {
        let board: Board
        let captured: [Point]
        let ownLiberties: Int
        let hash: UInt64
        let ownChainSize: Int
    }

    /// Places a stone, removes any opponent chain left without liberties, then
    /// measures the played chain. Order matters -- see docs/02 §3.2.
    func simulate(placing player: Player, at point: Point) -> Simulation {
        let placed = state.board.placing(player, at: point)
        let origin = placed.index(point)
        let opponentCell = Cell(player.opponent)

        var captured: [Point] = []
        var inspected = Set<Int>()
        placed.forEachNeighbour(origin) { neighbour in
            guard placed.cells[neighbour] == opponentCell, !inspected.contains(neighbour) else { return }
            let (stones, liberties) = placed.chainAndLiberties(at: neighbour)
            inspected.formUnion(stones)
            // Two distinct chains of one colour are never adjacent, so removing one
            // cannot revive another: the captures can be collected before clearing.
            if liberties == 0 {
                captured.append(contentsOf: stones.map { placed.point(at: $0) })
            }
        }

        let board = captured.isEmpty ? placed : placed.clearing(captured)
        let (ownChain, ownLiberties) = board.chainAndLiberties(at: board.index(point))
        return Simulation(
            board: board,
            captured: captured,
            ownLiberties: ownLiberties,
            hash: Zobrist.hash(of: board),
            ownChainSize: ownChain.count
        )
    }

    /// A ko point exists only in the classic single-stone recapture shape (docs/02 §4.1).
    private func koPoint(after simulation: Simulation, played: Point) -> Point? {
        guard simulation.captured.count == 1,
              simulation.ownChainSize == 1,
              simulation.ownLiberties == 1
        else { return nil }
        return simulation.captured[0]
    }
}

extension Result {
    var isSuccess: Bool { if case .success = self { true } else { false } }
}

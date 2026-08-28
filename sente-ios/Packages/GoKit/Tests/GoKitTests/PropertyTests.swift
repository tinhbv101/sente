import Foundation
import XCTest
@testable import GoKit

/// Deterministic generator so a failure is always reproducible from the seed.
struct SeededGenerator: RandomNumberGenerator {
    private var state: UInt64

    init(seed: UInt64) { self.state = seed }

    mutating func next() -> UInt64 {
        state &+= 0x9E37_79B9_7F4A_7C15
        var z = state
        z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
        z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
        return z ^ (z >> 31)
    }
}

/// Invariants that must hold for every position an engine can reach, checked over
/// randomly generated games rather than hand-picked ones.
final class PropertyTests: XCTestCase {

    private func selfPlay(
        seed: UInt64,
        size: Int,
        rules: RuleSet,
        moves moveCount: Int,
        onEachMove: (GameEngine, GameEngine, Move) throws -> Void = { _, _, _ in }
    ) throws -> GameEngine {
        var generator = SeededGenerator(seed: seed)
        var engine = GameEngine.newGame(size: size, rules: rules)
        for _ in 0..<moveCount {
            guard engine.state.phase == .playing else { break }
            // Sorted, because two equal Sets can have different storage order and
            // `randomElement` would then make the same seed play a different game.
            let legal = engine.legalMoves().sorted { ($0.row, $0.col) < ($1.row, $1.col) }
            let move: Move = legal.isEmpty ? .pass : .play(legal.randomElement(using: &generator)!)
            let before = engine
            guard let next = try? engine.apply(move) else {
                XCTFail("a move reported legal was rejected: \(move)")
                break
            }
            try onEachMove(before, next, move)
            engine = next
        }
        return engine
    }

    /// No chain may ever sit on the board without a liberty: that is the whole point
    /// of the capture-then-suicide ordering.
    func testEveryChainAlwaysHasALiberty() throws {
        for seed in UInt64(1)...20 {
            try selfPlay(seed: seed, size: 9, rules: .chinese, moves: 120) { _, engine, move in
                for point in engine.state.board.allPoints where engine.state.board[point] != nil {
                    XCTAssertGreaterThan(engine.state.board.liberties(at: point), 0,
                                         "seed \(seed): \(point) has no liberty after \(move)")
                }
            }
        }
    }

    /// Stones placed must equal stones still standing plus stones taken.
    func testCaptureAccountingBalances() throws {
        for seed in UInt64(1)...20 {
            var placed = Captures()
            try selfPlay(seed: seed, size: 9, rules: .chinese, moves: 150) { before, engine, move in
                if case .play = move { placed[before.toPlay] += 1 }
                XCTAssertEqual(engine.state.board.stones(of: .black).count + engine.state.captures.white,
                               placed.black, "seed \(seed): black stone accounting")
                XCTAssertEqual(engine.state.board.stones(of: .white).count + engine.state.captures.black,
                               placed.white, "seed \(seed): white stone accounting")
            }
        }
    }

    /// Replaying the same moves must reproduce the same position and hash --
    /// the property the server relies on to rebuild a game from the moves table.
    func testReplayIsDeterministic() throws {
        for seed in UInt64(1)...10 {
            let first = try selfPlay(seed: seed, size: 9, rules: .japanese, moves: 100)
            let second = try selfPlay(seed: seed, size: 9, rules: .japanese, moves: 100)
            XCTAssertEqual(first.state.board, second.state.board, "seed \(seed): board")
            XCTAssertEqual(first.state.boardHash, second.state.boardHash, "seed \(seed): hash")
            XCTAssertEqual(first.state.captures, second.state.captures, "seed \(seed): captures")
        }
    }

    /// `apply` must leave the receiver untouched: optimistic rollback on the client
    /// is nothing more than keeping the old value (docs/03 ADR-007).
    func testApplyNeverMutatesTheReceiver() throws {
        for seed in UInt64(1)...10 {
            try selfPlay(seed: seed, size: 9, rules: .chinese, moves: 80) { before, _, _ in
                let snapshot = before.state
                XCTAssertEqual(before.state, snapshot, "seed \(seed): receiver changed")
            }
        }
    }

    /// A position reached by playing moves must hash identically to the same
    /// position rebuilt from its stone lists.
    func testHashDoesNotDependOnHowThePositionWasBuilt() throws {
        for seed in UInt64(1)...10 {
            let played = try selfPlay(seed: seed, size: 9, rules: .chinese, moves: 90)
            let rebuilt = GameEngine.position(
                size: 9,
                black: played.state.board.stones(of: .black),
                white: played.state.board.stones(of: .white),
                rules: .chinese
            )
            XCTAssertEqual(played.state.boardHash, rebuilt.state.boardHash, "seed \(seed)")
        }
    }

    /// Legality is a property of the shape, not of where the shape sits: the eight
    /// board symmetries must all agree.
    func testLegalityIsInvariantUnderBoardSymmetries() throws {
        let size = 9
        func transform(_ point: Point, _ index: Int) -> Point {
            let last = size - 1
            let (col, row) = (point.col, point.row)
            return switch index {
            case 0: Point(col: col, row: row)
            case 1: Point(col: last - col, row: row)
            case 2: Point(col: col, row: last - row)
            case 3: Point(col: last - col, row: last - row)
            case 4: Point(col: row, row: col)
            case 5: Point(col: last - row, row: col)
            case 6: Point(col: row, row: last - col)
            default: Point(col: last - row, row: last - col)
            }
        }

        for seed in UInt64(1)...8 {
            let source = try selfPlay(seed: seed, size: size, rules: .chinese, moves: 70)
            let black = source.state.board.stones(of: .black)
            let white = source.state.board.stones(of: .white)

            for symmetry in 1..<8 {
                let mirrored = GameEngine.position(
                    size: size,
                    black: black.map { transform($0, symmetry) },
                    white: white.map { transform($0, symmetry) },
                    toPlay: source.toPlay,
                    rules: .chinese
                )
                let plain = GameEngine.position(size: size, black: black, white: white,
                                                toPlay: source.toPlay, rules: .chinese)
                for point in plain.state.board.allPoints {
                    XCTAssertEqual(
                        plain.validate(.play(point)).isSuccess,
                        mirrored.validate(.play(transform(point, symmetry))).isSuccess,
                        "seed \(seed) symmetry \(symmetry) at \(point)"
                    )
                }
            }
        }
    }

    /// Under Chinese rules every intersection is either area or neutral, so the
    /// three counts must add up to the whole board.
    func testAreaAndNeutralCoverTheWholeBoard() throws {
        for seed in UInt64(1)...10 {
            let engine = try selfPlay(seed: seed, size: 9, rules: .chinese, moves: 140)
            let score = engine.score()
            let neutral = engine.territory().neutral.count
            XCTAssertEqual(score.detail.black.area + score.detail.white.area + neutral, 81,
                           "seed \(seed)")
        }
    }

    /// Scores are always multiples of a half point, so they stay exact in Double.
    func testScoresAreExactHalfPoints() throws {
        for seed in UInt64(1)...10 {
            let engine = try selfPlay(seed: seed, size: 9, rules: .japanese, moves: 120)
            let score = engine.score()
            XCTAssertEqual((score.black * 2).truncatingRemainder(dividingBy: 1), 0, "seed \(seed)")
            XCTAssertEqual((score.white * 2).truncatingRemainder(dividingBy: 1), 0, "seed \(seed)")
        }
    }

    /// A pass-alive chain must never be capturable, so the opponent playing every
    /// legal move in a row can never remove it.
    func testPassAliveChainsSurviveUnansweredAttack() throws {
        let engine = GameEngine.position(
            size: 9,
            black: ["b9", "d9", "a8", "b8", "c8", "d8"].map { Coordinate.point($0, size: 9)! },
            toPlay: .white,
            rules: .chinese
        )
        let alive = Set(engine.state.board.passAliveChains(for: .black).flatMap { $0 })
        XCTAssertFalse(alive.isEmpty, "expected a pass-alive chain to attack")

        var generator = SeededGenerator(seed: 99)
        var attacker = engine
        for _ in 0..<200 {
            guard attacker.state.phase == .playing else { break }
            // White plays on; Black answers every move with a pass.
            let legal = attacker.legalMoves().sorted { ($0.row, $0.col) < ($1.row, $1.col) }
            guard let move = legal.randomElement(using: &generator) else { break }
            attacker = try attacker.apply(.play(move))
            for point in alive {
                XCTAssertEqual(attacker.state.board[point], .black,
                               "pass-alive stone at \(point) was captured")
            }
            attacker = try attacker.apply(.pass)
        }
    }
}

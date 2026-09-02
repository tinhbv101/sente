import XCTest
import GoKit
@testable import Sente

/// The whole-game review: ranking is pure arithmetic (tested exactly), and the
/// end-to-end pass must blame the player who threw the game away.
@MainActor
final class GameReviewTests: XCTestCase {
    private func point(_ text: String) -> Point { Coordinate.point(text, size: 9)! }

    func testWorstMovesRanksDropsFromTheMoversPerspective() {
        let suggestion = Move.play(point("C3"))
        let moves = [
            RecordedMove(player: .black, move: .play(point("E5"))),
            RecordedMove(player: .white, move: .play(point("D4"))),
            RecordedMove(player: .black, move: .play(point("A1"))),
        ]
        // Black-win chances around each position: black's A1 (move index 2)
        // burns 30 points; everything else is noise below the cutoff.
        let rates: [Int: (move: Move, blackWin: Double)] = [
            0: (suggestion, 0.50), 1: (suggestion, 0.52),
            2: (suggestion, 0.55), 3: (suggestion, 0.25),
        ]
        let findings = GameReviewStore.worstMoves(moves: moves, rates: rates)
        XCTAssertEqual(findings.count, 1)
        XCTAssertEqual(findings.first?.id, 2)
        XCTAssertEqual(findings.first?.mover, .black)
        XCTAssertEqual(findings.first!.drop, 0.30, accuracy: 0.0001)
        XCTAssertEqual(findings.first?.suggested, suggestion)
    }

    func testAMoveMatchingTheSuggestionIsNeverAMistake() {
        let played = Move.play(point("E5"))
        let moves = [RecordedMove(player: .black, move: played)]
        let rates: [Int: (move: Move, blackWin: Double)] = [
            0: (played, 0.9), 1: (played, 0.2),
        ]
        XCTAssertTrue(GameReviewStore.worstMoves(moves: moves, rates: rates).isEmpty,
                      "the bot cannot call its own preferred move a blunder")
    }

    func testTheReviewBlamesARealBlunder() async throws {
        // Black builds a three-stone column, lets white surround it, and keeps
        // answering in the far corner. The review must blame black.
        let script: [(Player, String)] = [
            (.black, "E5"), (.white, "D5"), (.black, "E6"), (.white, "D6"),
            (.black, "E7"), (.white, "D7"), (.black, "A1"), (.white, "F6"),
            (.black, "A2"), (.white, "F5"), (.black, "A3"), (.white, "F7"),
            (.black, "B1"), (.white, "E8"), (.black, "B2"), (.white, "E4"),
        ]
        var engine = GameEngine.newGame(size: 9)
        var positions = [engine]
        var moves: [RecordedMove] = []
        for (player, text) in script {
            engine = try engine.apply(.play(point(text)), by: player)
            positions.append(engine)
            moves.append(RecordedMove(player: player, move: .play(point(text))))
        }

        let review = GameReviewStore()
        review.iterations = 160
        review.seed = 3
        review.run(positions: positions, moves: moves)
        for _ in 0..<600 where review.state != .done {
            try await Task.sleep(for: .milliseconds(50))
        }
        XCTAssertEqual(review.state, .done, "the review must finish")
        XCTAssertFalse(review.findings.isEmpty, "losing three stones for nothing is a mistake")
        XCTAssertEqual(review.findings.first?.mover, .black)
    }
}

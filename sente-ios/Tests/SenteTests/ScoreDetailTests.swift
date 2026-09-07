import XCTest
import GoKit
import SenteNet
@testable import Sente

/// Marked dead stones must show up in the score as prisoners — the arithmetic
/// the breakdown view displays, computed client-side from the same engine.
@MainActor
final class ScoreDetailTests: XCTestCase {
    private func point(_ text: String) -> Point { Coordinate.point(text, size: 9)! }

    func testDeadStonesCountAsPrisonersInTheDetail() async {
        // One white stone surrounded by four black ones, marked dead.
        let engine = GameEngine.position(size: 9,
                                         black: [point("D5"), point("F5"), point("E4"), point("E6")],
                                         white: [point("E5")], toPlay: .black, komi: 6.5)
        let hash = String(format: "0x%016llx", engine.state.boardHash)

        let store = GameStore(gameID: "g1", myColor: .black)
        await store.handle(.gameState(Fixture.gameState(
            board: engine.board.wireString, toPlay: "black", moveNo: 8,
            phase: "scoring", hash: hash)))
        await store.handle(.scoringState(Fixture.scoringState(
            dead: ["E5"], blackAccepted: false, whiteAccepted: false, black: 78, white: 6.5)))

        let detail = store.scoreDetail
        XCTAssertNotNil(detail)
        XCTAssertEqual(detail?.detail.black.deadStones, 1, "the marked stone is black's prisoner")
        XCTAssertEqual(detail?.detail.black.captures, 0)
        // 77 territory + 0 in-game captures + 1 dead stone = 78.
        XCTAssertEqual(detail?.detail.black.territory, 77)
        XCTAssertEqual(detail?.black, 78)
        XCTAssertEqual(detail?.white, 6.5)
    }

    func testTheDetailSurvivesIntoTheResult() async {
        let engine = GameEngine.position(size: 9,
                                         black: [point("D5"), point("F5"), point("E4"), point("E6")],
                                         white: [point("E5")], toPlay: .black, komi: 6.5)
        let hash = String(format: "0x%016llx", engine.state.boardHash)
        let store = GameStore(gameID: "g1", myColor: .black)
        await store.handle(.gameState(Fixture.gameState(
            board: engine.board.wireString, toPlay: "black", moveNo: 8,
            phase: "scoring", hash: hash)))
        await store.handle(.scoringState(Fixture.scoringState(
            dead: ["E5"], blackAccepted: true, whiteAccepted: false, black: 78, white: 6.5)))
        await store.handle(.gameOver(Fixture.gameOver(winner: "black", reason: "counting")))

        XCTAssertEqual(store.scoreDetail?.detail.black.deadStones, 1,
                       "the result sheet still shows where the points came from")
    }
}

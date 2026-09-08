import XCTest
import GoKit
@testable import Sente

/// Entering the count in an on-device game must pre-mark the dead stones —
/// otherwise an unmarked dead group silently scores as alive, which is exactly
/// the bug report this guards against.
@MainActor
final class DeadSuggestionTests: XCTestCase {
    private func point(_ text: String) -> Point { Coordinate.point(text, size: 9)! }

    func testAOneLibertyEyelessPairIsSuggestedDead() {
        // White A1-A2 with a single liberty at B1, no eye space of its own.
        let engine = GameEngine.position(size: 9,
                                         black: [point("A3"), point("B2"), point("C1")],
                                         white: [point("A1"), point("A2")], toPlay: .black)
        let suggested = BotHeuristics.suggestDead(engine)
        XCTAssertTrue(suggested.isSuperset(of: [point("A1"), point("A2")]))
        XCTAssertFalse(suggested.contains(point("B2")), "open black stones are not dead")
    }

    func testAStragglerInsideEnemyTerritoryIsSuggestedDead() {
        // The reported board: a white wall on F, a black wall on G plus H5, and
        // one white stone at H3 abandoned inside black's side — three liberties,
        // clearly dead, and exactly what the count must pre-mark.
        let black = (1...9).map { point("G\($0)") } + [point("H5")]
        let white = (1...9).map { point("F\($0)") } + [point("H3")]
        let engine = GameEngine.position(size: 9, black: black, white: white, toPlay: .black)
        XCTAssertEqual(BotHeuristics.suggestDead(engine), [point("H3")])

        // And with it marked, black's side counts: 17 territory + 1 prisoner.
        let score = engine.score(deadStones: [point("H3")])
        XCTAssertEqual(score.detail.black.territory, 17)
        XCTAssertEqual(score.detail.black.deadStones, 1)
        XCTAssertEqual(score.detail.black.total, 18)
    }

    func testAPassAliveGroupIsNeverSuggested() {
        // White with two one-point eyes at A1 and C1: Benson-proved alive.
        let white = ["A2", "B2", "C2", "D2", "B1", "D1"].map(point)
        let engine = GameEngine.position(size: 9,
                                         black: [point("E5")], white: white, toPlay: .black)
        let suggested = BotHeuristics.suggestDead(engine)
        for stone in white {
            XCTAssertFalse(suggested.contains(stone), Coordinate.text(stone, size: 9))
        }
    }

    func testTheLocalCountPreMarksAndScoresTheDeadStone() throws {
        // Black surrounds white E5 on three sides, both players pass: the count
        // opens with E5 already marked, and it scores as territory + prisoner.
        let store = LocalGameStore(config: LocalGameConfig(), storage: NullGameStorage(),
                                   library: KifuTests.MemoryLibrary())
        for (player, move) in [(Player.black, Move.play(point("D5"))),
                               (.white, .play(point("E5"))),
                               (.black, .play(point("E4"))),
                               (.white, .pass),
                               (.black, .play(point("E6"))),
                               (.white, .pass),
                               (.black, .pass)] {
            XCTAssertEqual(store.toPlay, player)
            if case .play(let target) = move { store.place(target) } else { store.pass() }
        }
        XCTAssertEqual(store.phase, .scoring)
        XCTAssertEqual(store.deadStones, [point("E5")], "the dead stone is pre-marked")

        store.finishCounting()
        let score = try XCTUnwrap(store.finalScore)
        XCTAssertEqual(score.detail.black.deadStones, 1)
        // 78 empty points (E5 cleared) + 1 prisoner = 79.
        XCTAssertEqual(score.black, 79)
        XCTAssertEqual(score.detail.white.total, 6.5)
        XCTAssertEqual(score.winner, .black)
    }
}

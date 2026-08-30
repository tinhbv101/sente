import XCTest
import GoKit
@testable import Sente

/// Pass-and-play has no server to lean on: the store must be the whole arbiter,
/// and what it saves must bring the game back exactly.
@MainActor
final class LocalGameStoreTests: XCTestCase {
    final class MemoryStorage: LocalGameStorage, @unchecked Sendable {
        var record: LocalGameRecord?
        func load() -> LocalGameRecord? { record }
        func save(_ record: LocalGameRecord) { self.record = record }
        func clear() { record = nil }
    }

    private var storage: MemoryStorage!
    private var store: LocalGameStore!

    override func setUp() {
        storage = MemoryStorage()
        store = LocalGameStore(config: LocalGameConfig(size: 9), storage: storage)
    }

    private func point(_ text: String) -> Point { Coordinate.point(text, size: 9)! }

    func testTurnsAlternateAndCapturesCount() {
        XCTAssertEqual(store.toPlay, .black)
        store.place(point("E5")); XCTAssertEqual(store.toPlay, .white)
        store.place(point("E4")); XCTAssertEqual(store.toPlay, .black)
        // Black surrounds E4: D4, F4, E3 -> capture.
        store.place(point("D4")); store.place(point("A1"))
        store.place(point("F4")); store.place(point("A2"))
        store.place(point("E3"))
        XCTAssertEqual(store.captures.black, 1)
        XCTAssertNil(store.snapshot.board[point("E4")])
        XCTAssertEqual(store.moveNumber, 7)
        XCTAssertEqual(store.lastMove, point("E3"))
    }

    func testIllegalMovesAreRefusedWithAReason() {
        store.place(point("E5"))
        store.place(point("E5"))
        XCTAssertEqual(store.toPlay, .white, "an occupied point is not a move")
        XCTAssertEqual(store.toast, String(localized: "Đã có quân ở đó."))
        XCTAssertEqual(store.legality(point("E5")), String(localized: "Đã có quân"))
        XCTAssertNil(store.legality(point("D4")))
    }

    func testUndoTakesBackTheLastMove() {
        store.place(point("E5")); store.place(point("D4"))
        store.undo()
        XCTAssertEqual(store.moveNumber, 1)
        XCTAssertEqual(store.toPlay, .white)
        XCTAssertNil(store.snapshot.board[point("D4")])
        XCTAssertEqual(store.lastMove, point("E5"))
        store.undo(); store.undo()
        XCTAssertEqual(store.moveNumber, 0, "undoing past the start is a no-op")
    }

    func testTwoPassesOpenScoringAndCountingEndsTheGame() {
        store.place(point("E5")); store.place(point("D5"))
        store.pass(); store.pass()
        XCTAssertEqual(store.phase, .scoring)
        XCTAssertNotNil(store.score)

        // Marking a chain dead flips the whole chain, and back.
        store.toggleDead(point("D5"))
        XCTAssertEqual(store.deadStones, [point("D5")])
        XCTAssertTrue(store.snapshot.deadStones.contains(point("D5")))
        store.toggleDead(point("D5"))
        XCTAssertTrue(store.deadStones.isEmpty)

        store.toggleDead(point("D5"))
        store.finishCounting()
        XCTAssertEqual(store.phase, .finished)
        XCTAssertEqual(store.result?.reason, .counting)
        XCTAssertEqual(store.result?.winner, .black, "white's only stone is dead; black owns the board")
        XCTAssertNotNil(store.finalScore)
        XCTAssertNil(storage.record, "a finished game is not resumed on the next launch")
    }

    func testPlayingOnFromScoringDropsThePasses() {
        store.place(point("E5")); store.place(point("D5"))
        store.pass(); store.pass()
        store.toggleDead(point("D5"))
        store.resumePlay()
        XCTAssertEqual(store.phase, .playing)
        XCTAssertEqual(store.moveNumber, 2)
        XCTAssertEqual(store.toPlay, .black)
        XCTAssertTrue(store.deadStones.isEmpty)
    }

    func testResignationEndsTheGameForThePlayerToMove() {
        store.place(point("E5"))
        store.resign()
        XCTAssertEqual(store.phase, .finished)
        XCTAssertEqual(store.result?.reason, .resignation)
        XCTAssertEqual(store.result?.winner, .black, "white was to move and gave up")
    }

    func testHandicapGamesStartWithWhite() {
        let handicap = LocalGameStore(config: LocalGameConfig(size: 9, handicap: 3), storage: MemoryStorage())
        XCTAssertEqual(handicap.toPlay, .white)
        XCTAssertEqual(handicap.snapshot.board.stones(of: .black).count, 3)
        XCTAssertEqual(handicap.config.komi, 0.5)
    }

    func testASavedGameComesBackExactly() {
        store.place(point("E5")); store.place(point("D4")); store.pass()
        let saved = storage.record!
        XCTAssertEqual(saved.moves.count, 3)

        let restored = LocalGameStore(record: saved, storage: MemoryStorage())
        XCTAssertEqual(restored.engine.state.boardHash, store.engine.state.boardHash)
        XCTAssertEqual(restored.moveNumber, 3)
        XCTAssertEqual(restored.toPlay, .white, "black played, white played, black passed")
        XCTAssertEqual(restored.config.blackName, String(localized: "Đen"))
    }

    func testSGFCarriesTheGame() {
        store.place(point("E5")); store.place(point("D4")); store.resign()
        let sgf = store.sgf
        XCTAssertTrue(sgf.contains("SZ[9]"))
        XCTAssertTrue(sgf.contains("PB[\(String(localized: "Đen"))]"))
        XCTAssertTrue(sgf.contains(";B[") && sgf.contains(";W["))
        XCTAssertTrue(sgf.contains("RE[W+R"), "black was to move and resigned: \(sgf)")
        let decoded = try? SGF.decode(sgf)
        XCTAssertEqual(decoded?.moves.count, 3)
    }
}

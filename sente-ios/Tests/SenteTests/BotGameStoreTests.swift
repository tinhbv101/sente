import XCTest
import GoKit
@testable import Sente

/// The bot seats inside the local game: it answers, it stays legal, undo lands
/// back on the person's turn, and a bot-vs-bot game counts itself at the end.
@MainActor
final class BotGameStoreTests: XCTestCase {
    final class MemoryStorage: LocalGameStorage, @unchecked Sendable {
        var record: LocalGameRecord?
        func load() -> LocalGameRecord? { record }
        func save(_ record: LocalGameRecord) { self.record = record }
        func clear() { record = nil }
    }

    private func point(_ text: String) -> Point { Coordinate.point(text, size: 9)! }

    func testTheBotAnswersAndTheBoardStaysConsistent() {
        var config = LocalGameConfig(size: 9)
        config.whiteBot = BotLevel.greedy.rawValue
        let store = LocalGameStore(config: config, storage: MemoryStorage())
        XCTAssertTrue(store.isHumanTurn)

        store.place(point("C3"))
        // The async bot is beaten to it by the synchronous test hook; the stale
        // scheduled move is dropped by the board-hash guard.
        store.stepBotNow()
        XCTAssertEqual(store.moveNumber, 2)
        XCTAssertEqual(store.toPlay, .black)
        XCTAssertTrue(store.isHumanTurn)
        XCTAssertEqual(store.moves.count, 2)
        XCTAssertEqual(store.moves[1].player, .white)
    }

    func testUndoReturnsToTheHumansTurn() {
        var config = LocalGameConfig(size: 9)
        config.whiteBot = BotLevel.novice.rawValue
        let store = LocalGameStore(config: config, storage: MemoryStorage())
        store.place(point("C3"))
        store.stepBotNow()
        XCTAssertEqual(store.moveNumber, 2)

        store.undo()
        XCTAssertEqual(store.moveNumber, 0, "the bot's answer goes with the person's move")
        XCTAssertTrue(store.isHumanTurn)
    }

    func testHumansCannotMoveOnTheBotsTurn() {
        var config = LocalGameConfig(size: 9)
        config.blackBot = BotLevel.novice.rawValue // the bot has the first move
        let store = LocalGameStore(config: config, storage: MemoryStorage())
        XCTAssertFalse(store.isHumanTurn)
        store.place(point("C3"))
        XCTAssertEqual(store.moveNumber, 0, "a tap on the bot's turn does nothing")
        store.stepBotNow()
        XCTAssertEqual(store.moveNumber, 1)
        XCTAssertTrue(store.isHumanTurn)
    }

    func testAWatchedMatchPlaysItselfOutAndCounts() {
        var config = LocalGameConfig(size: 9)
        config.blackBot = BotLevel.novice.rawValue
        config.whiteBot = BotLevel.greedy.rawValue
        let store = LocalGameStore(config: config, storage: NullGameStorage())
        store.paused = true // keep the async loop out of the synchronous test

        for _ in 0..<240 where store.phase == .playing { store.stepBotNow() }
        XCTAssertEqual(store.phase, .finished, "bots must finish their game")
        XCTAssertNotNil(store.finalScore, "a bot match counts itself")
        XCTAssertNotNil(store.result)
        XCTAssertGreaterThan(store.moveNumber, 10)
    }

    func testABotGameSurvivesARestart() {
        let storage = MemoryStorage()
        var config = LocalGameConfig(size: 9)
        config.whiteBot = BotLevel.greedy.rawValue
        let store = LocalGameStore(config: config, storage: storage)
        store.place(point("C3"))
        store.stepBotNow()

        let restored = LocalGameStore(record: storage.record!, storage: storage)
        restored.paused = true
        XCTAssertEqual(restored.moveNumber, 2)
        XCTAssertEqual(restored.config.botLevel(for: .white), .greedy)
        XCTAssertEqual(restored.engine.state.boardHash, store.engine.state.boardHash)
        XCTAssertTrue(restored.isHumanTurn)
    }
}

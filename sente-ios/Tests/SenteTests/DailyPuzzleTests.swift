import XCTest
@testable import Sente

/// The daily puzzle must rotate deterministically and count streaks honestly.
final class DailyPuzzleTests: XCTestCase {
    private func defaults(_ name: String) -> UserDefaults {
        let d = UserDefaults(suiteName: name)!
        d.removePersistentDomain(forName: name)
        return d
    }

    func testThePoolIsRealAndRotationIsStable() {
        XCTAssertGreaterThanOrEqual(DailyPuzzles.all.count, 10)
        let count = DailyPuzzles.all.count
        XCTAssertEqual(DailyPuzzles.puzzle(forDay: 3)?.id, DailyPuzzles.puzzle(forDay: 3 + count)?.id)
        XCTAssertNotEqual(DailyPuzzles.puzzle(forDay: 3)?.id, DailyPuzzles.puzzle(forDay: 4)?.id)
    }

    func testStreaksGrowOnConsecutiveDaysAndResetOnGaps() {
        let d = defaults("puzzles-1")
        DailyPuzzles.recordSolved(day: 100, defaults: d)
        XCTAssertEqual(DailyPuzzles.status(day: 100, defaults: d).streak, 1)
        DailyPuzzles.recordSolved(day: 101, defaults: d)
        DailyPuzzles.recordSolved(day: 101, defaults: d) // same day twice: no double count
        XCTAssertEqual(DailyPuzzles.status(day: 101, defaults: d).streak, 2)
        XCTAssertTrue(DailyPuzzles.status(day: 101, defaults: d).doneToday)
        XCTAssertFalse(DailyPuzzles.status(day: 102, defaults: d).doneToday)
        XCTAssertEqual(DailyPuzzles.status(day: 102, defaults: d).streak, 2, "yesterday's streak still stands today")
        DailyPuzzles.recordSolved(day: 104, defaults: d) // skipped a day
        XCTAssertEqual(DailyPuzzles.status(day: 104, defaults: d).streak, 1)
    }
}

final class BotLadderTests: XCTestCase {
    func testBeatingALevelUnlocksTheNext() {
        let d = UserDefaults(suiteName: "ladder-1")!
        d.removePersistentDomain(forName: "ladder-1")
        XCTAssertTrue(BotLadder.isUnlocked(.novice, defaults: d))
        XCTAssertTrue(BotLadder.isUnlocked(.greedy, defaults: d), "the first two levels start open")
        XCTAssertFalse(BotLadder.isUnlocked(.thoughtful, defaults: d))

        BotLadder.recordWin(over: .greedy, defaults: d)
        XCTAssertTrue(BotLadder.isUnlocked(.thoughtful, defaults: d))
        XCTAssertFalse(BotLadder.isUnlocked(.deep, defaults: d))
        BotLadder.recordWin(over: .novice, defaults: d) // a lower win never locks things back
        XCTAssertTrue(BotLadder.isUnlocked(.thoughtful, defaults: d))
        BotLadder.recordWin(over: .thoughtful, defaults: d)
        XCTAssertTrue(BotLadder.isUnlocked(.deep, defaults: d))
    }
}

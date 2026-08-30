import XCTest
import SenteNet

/// The picker must never offer a clock the server refuses (game.MaxMainTime).
final class TimeLimitsTests: XCTestCase {
    func testCapsFollowTheBoard() {
        XCTAssertEqual(TimeLimits.maxMainTimeMs(boardSize: 9), 3 * 3_600_000)
        XCTAssertEqual(TimeLimits.maxMainTimeMs(boardSize: 13), 9 * 3_600_000)
        XCTAssertEqual(TimeLimits.maxMainTimeMs(boardSize: 19), 24 * 3_600_000)
    }

    func testChoicesStopAtTheCapAndIncludeIt() {
        for size in [9, 13, 19] {
            let cap = TimeLimits.maxMainTimeMs(boardSize: size)
            let choices = TimeLimits.mainTimeChoicesMs(boardSize: size)
            XCTAssertTrue(choices.allSatisfy { $0 <= cap }, "\(size)×\(size) offers more than the cap")
            XCTAssertEqual(choices.last, cap, "the cap itself should be offered")
            XCTAssertEqual(choices, choices.sorted())
            XCTAssertTrue(choices.contains(10 * 60_000), "a quick game is always possible")
        }
        XCTAssertLessThan(TimeLimits.mainTimeChoicesMs(boardSize: 9).count,
                          TimeLimits.mainTimeChoicesMs(boardSize: 19).count)
    }

    func testDurationsReadNaturally() {
        // Resolved through the same catalog the app uses, whatever the simulator language.
        XCTAssertEqual(TimeLimits.format(ms: 10 * 60_000), String(localized: "\(10) phút"))
        XCTAssertEqual(TimeLimits.format(ms: 90 * 60_000), String(localized: "\(1) giờ \(30) phút"))
        XCTAssertEqual(TimeLimits.format(ms: 3 * 3_600_000), String(localized: "\(3) giờ"))
        XCTAssertEqual(TimeControl(kind: .absolute, mainTimeMs: 24 * 3_600_000).summary, String(localized: "\(24) giờ"))
        XCTAssertEqual(TimeControl.standard.summary, String(localized: "\(20) phút") + " + 3×30″")
    }
}

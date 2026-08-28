import Foundation
import XCTest
@testable import GoKit

final class CoordinateTests: XCTestCase {
    /// The letter I is skipped so it cannot be confused with the digit 1.
    func testColumnLettersSkipI() {
        XCTAssertFalse(Coordinate.columnLetters.contains("I"))
        XCTAssertEqual(Coordinate.columnLetters.count, 19)
        XCTAssertEqual(Coordinate.columnLetters[8], "J")
    }

    func testKnownPointsOn19x19() {
        XCTAssertEqual(Coordinate.point("d4", size: 19), Point(col: 3, row: 15))
        XCTAssertEqual(Coordinate.point("Q16", size: 19), Point(col: 15, row: 3))
        XCTAssertEqual(Coordinate.text(Point(col: 3, row: 15), size: 19), "D4")
        XCTAssertEqual(Coordinate.text(Point(col: 15, row: 3), size: 19), "Q16")
    }

    func testRoundTripsForEveryPointOnEverySize() {
        for size in BoardSize.supported {
            for point in Board(size: size).allPoints {
                let text = Coordinate.text(point, size: size)
                XCTAssertEqual(Coordinate.point(text, size: size), point, "\(text) on \(size)x\(size)")
            }
        }
    }

    func testRejectsOffBoardCoordinates() {
        XCTAssertNil(Coordinate.point("e10", size: 9))
        XCTAssertNil(Coordinate.point("k5", size: 9))
        XCTAssertNil(Coordinate.point("i5", size: 19))
        XCTAssertNil(Coordinate.point("", size: 19))
        XCTAssertNil(Coordinate.point("d0", size: 19))
    }

    /// SGF counts rows from the top, display coordinates from the bottom.
    func testSGFOriginDiffersFromDisplayOrigin() {
        XCTAssertEqual(Coordinate.sgfText(Point(col: 15, row: 3)), "pd")
        XCTAssertEqual(Coordinate.sgfText(Point(col: 3, row: 15)), "dp")
        XCTAssertEqual(Coordinate.sgfPoint("pd", size: 19), Point(col: 15, row: 3))
    }
}

final class BoardTests: XCTestCase {
    func testWireStringRoundTrips() {
        let engine = GameEngine.position(
            size: 9,
            black: [Point(col: 0, row: 0), Point(col: 4, row: 4)],
            white: [Point(col: 8, row: 8)]
        )
        let wire = engine.state.board.wireString
        XCTAssertEqual(wire.count, 81)
        XCTAssertEqual(wire.first, "b")
        XCTAssertEqual(wire.last, "w")
        XCTAssertEqual(Board(size: 9, wireString: wire), engine.state.board)
    }

    func testChainAndLiberties() {
        let points = ["d5", "e5", "f5"].map { Coordinate.point($0, size: 9)! }
        let board = GameEngine.position(size: 9, black: points).state.board
        XCTAssertEqual(board.chain(at: points[0]).count, 3)
        // Three stones in a row: two liberties at each end plus three above and below.
        XCTAssertEqual(board.liberties(at: points[1]), 8)
    }

    func testEmptyBoardHashesToZero() {
        for size in BoardSize.supported {
            XCTAssertEqual(Zobrist.hash(of: Board(size: size)), 0)
        }
    }

    func testHashChangesWithColour() {
        let point = Point(col: 2, row: 3)
        let black = GameEngine.position(size: 9, black: [point]).state.boardHash
        let white = GameEngine.position(size: 9, white: [point]).state.boardHash
        XCTAssertNotEqual(black, white)
        XCTAssertNotEqual(black, 0)
    }
}

final class GameFlowTests: XCTestCase {
    func testTwoPassesOpenScoringAndAThirdIsRejected() throws {
        var engine = GameEngine.newGame(size: 9)
        engine = try engine.apply(.pass)
        XCTAssertEqual(engine.state.phase, .playing)
        engine = try engine.apply(.pass)
        XCTAssertEqual(engine.state.phase, .scoring)
        XCTAssertThrowsError(try engine.apply(.pass)) { error in
            XCTAssertEqual(error as? MoveError, .gameNotPlaying)
        }
    }

    func testPassResetsAfterAPlay() throws {
        var engine = GameEngine.newGame(size: 9)
        engine = try engine.apply(.pass)
        engine = try engine.apply(.play(Coordinate.point("e5", size: 9)!))
        XCTAssertEqual(engine.state.consecutivePasses, 0)
        engine = try engine.apply(.pass)
        XCTAssertEqual(engine.state.phase, .playing, "one pass on each side is not two in a row")
    }

    func testDefaultKomi() {
        XCTAssertEqual(GameEngine.defaultKomi(rules: .japanese, handicap: 0), 6.5)
        XCTAssertEqual(GameEngine.defaultKomi(rules: .chinese, handicap: 0), 7.5)
        XCTAssertEqual(GameEngine.defaultKomi(rules: .japanese, handicap: 4), 0.5)
    }

    func testHandicapStonesAreNotCountedAsMoves() {
        let engine = GameEngine.newGame(size: 19, handicap: 9)
        XCTAssertEqual(engine.state.moveNumber, 0)
        XCTAssertEqual(engine.toPlay, .white)
        XCTAssertEqual(engine.state.board.stones(of: .black).count, 9)
        XCTAssertEqual(engine.history.count, 1, "the handicap position is the starting position")
    }

    /// NFR-P5 allows 50us per legality check plus apply on 19x19. The bound here is
    /// deliberately loose: it exists to catch an order-of-magnitude regression.
    func testMoveCostStaysWithinBudget() throws {
        var engine = GameEngine.newGame(size: 19, rules: .chinese)
        var generator = SeededGenerator(seed: 7)
        var points = engine.state.board.allPoints.shuffled(using: &generator)

        let start = Date()
        var applied = 0
        while applied < 200, let point = points.popLast() {
            guard engine.validate(.play(point)).isSuccess else { continue }
            engine = try engine.apply(.play(point))
            applied += 1
        }
        let microsecondsPerMove = Date().timeIntervalSince(start) / Double(applied) * 1_000_000
        print("validate+apply on 19x19: \(String(format: "%.1f", microsecondsPerMove))us per move")
        XCTAssertLessThan(microsecondsPerMove, 500, "move cost regressed by an order of magnitude")
    }
}

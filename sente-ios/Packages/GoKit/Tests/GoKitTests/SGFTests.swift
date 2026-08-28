import Foundation
import XCTest
@testable import GoKit

final class SGFTests: XCTestCase {
    private func record(result: GameResult?) -> GameRecord {
        GameRecord(size: 19, komi: 6.5, blackPlayer: "an", whitePlayer: "binh", result: result)
    }

    func testResultEncodingForEveryEnding() {
        let counting = GameEngine.newGame(size: 9).score()
        XCTAssertTrue(SGF.encode(record(result: GameResult(winner: .black, reason: .resignation)))
            .contains("RE[B+R]"))
        XCTAssertTrue(SGF.encode(record(result: GameResult(winner: .white, reason: .timeout)))
            .contains("RE[W+T]"))
        XCTAssertTrue(SGF.encode(record(result: GameResult(winner: .black, reason: .abandonment)))
            .contains("RE[B+F]"))
        XCTAssertTrue(SGF.encode(record(result: GameResult(winner: nil, reason: .mutualDraw)))
            .contains("RE[0]"))
        XCTAssertTrue(SGF.encode(record(result: GameResult(winner: nil, reason: .repetition)))
            .contains("RE[Void]"))
        XCTAssertTrue(SGF.encode(record(result: GameResult(winner: .white, reason: .counting,
                                                          score: counting)))
            .contains("RE[W+6.5]"))
        // Counting without a score is not something the engine produces, but the
        // encoder must not invent a winner if it ever sees one.
        XCTAssertTrue(SGF.encode(record(result: GameResult(winner: .white, reason: .counting)))
            .contains("RE[0]"))
        XCTAssertFalse(SGF.encode(record(result: nil)).contains("RE["))
    }

    func testWholeNumbersLoseTheirDecimal() {
        XCTAssertEqual(SGF.number(0), "0")
        XCTAssertEqual(SGF.number(7), "7")
        XCTAssertEqual(SGF.number(6.5), "6.5")
        XCTAssertEqual(SGF.number(0.5), "0.5")
    }

    func testPlayerNamesAreEscaped() throws {
        var source = record(result: nil)
        source.blackPlayer = "an]nguy[en"
        let text = SGF.encode(source)
        XCTAssertTrue(text.contains("PB[an\\]nguy[en]"), text)
        XCTAssertEqual(try SGF.decode(text).blackPlayer, "an]nguy[en")
    }

    func testOptionalRootPropertiesRoundTrip() throws {
        var source = record(result: nil)
        source.date = "2026-08-28"
        source.timeControl = "1200"
        let decoded = try SGF.decode(SGF.encode(source))
        XCTAssertEqual(decoded.date, "2026-08-28")
        XCTAssertEqual(decoded.timeControl, "1200")
    }

    func testLegacyTTMeansPass() throws {
        let decoded = try SGF.decode("(;GM[1]FF[4]SZ[19];B[tt];W[pd])")
        XCTAssertEqual(decoded.moves.first?.move, .pass)
        XCTAssertEqual(decoded.moves.last?.move, .play(Point(col: 15, row: 3)))
    }

    func testMissingPropertiesFallBackToDefaults() throws {
        let decoded = try SGF.decode("(;GM[1]FF[4];B[pd])")
        XCTAssertEqual(decoded.size, 19)
        XCTAssertEqual(decoded.komi, 6.5)
        XCTAssertEqual(decoded.rules, .japanese)
        XCTAssertEqual(decoded.handicap, 0)
    }

    func testChineseRulesAreRecognised() throws {
        XCTAssertEqual(try SGF.decode("(;SZ[9]RU[Chinese])").rules, .chinese)
        XCTAssertEqual(try SGF.decode("(;SZ[9]RU[Japanese])").rules, .japanese)
    }

    func testVariationsAreIgnored() throws {
        // Sente records one line of play; a branch ends the main line.
        let decoded = try SGF.decode("(;SZ[9];B[cc];W[dd](;B[ee])(;B[ff]))")
        XCTAssertEqual(decoded.moves.count, 2)
    }

    func testMalformedInputThrows() {
        XCTAssertThrowsError(try SGF.decode("no parenthesis here")) { error in
            guard case SGF.DecodeError.malformed = error as! SGF.DecodeError else {
                return XCTFail("wrong error: \(error)")
            }
        }
        XCTAssertThrowsError(try SGF.decode("(;SZ[9];B[cc")) { error in
            guard case SGF.DecodeError.malformed = error as! SGF.DecodeError else {
                return XCTFail("wrong error: \(error)")
            }
        }
        XCTAssertThrowsError(try SGF.decode("(;SZ[11])")) { error in
            XCTAssertEqual(error as? SGF.DecodeError, .unsupportedSize(11))
        }
    }

    func testUnparseableMoveIsDropped() throws {
        // "zz" is off every supported board, so the node contributes no move.
        XCTAssertEqual(try SGF.decode("(;SZ[9];B[zz])").moves.count, 0)
    }

    func testResignEncodesAsAnEmptyValue() throws {
        var source = record(result: nil)
        source.moves = [RecordedMove(player: .black, move: .resign)]
        XCTAssertTrue(SGF.encode(source).contains(";B[]"))
    }
}

final class HandicapTests: XCTestCase {
    private func names(_ points: [Point], size: Int) -> [String] {
        points.map { Coordinate.text($0, size: size) }
    }

    func testEveryCountFrom2To9On19x19() {
        for count in 2...9 {
            let stones = Handicap.stones(count: count, size: 19)
            XCTAssertEqual(stones.count, count, "handicap \(count)")
            XCTAssertEqual(Set(stones).count, count, "handicap \(count) placed a duplicate")
            XCTAssertTrue(Set(stones).isSubset(of: Set(Handicap.starPoints(size: 19))),
                          "handicap \(count) landed off a star point")
        }
    }

    func testThreeAndSevenStonePlacements() {
        XCTAssertEqual(names(Handicap.stones(count: 3, size: 19), size: 19),
                       ["Q16", "D4", "Q4"])
        XCTAssertEqual(Set(names(Handicap.stones(count: 7, size: 19), size: 19)),
                       ["Q16", "D4", "Q4", "D16", "D10", "Q10", "K10"])
    }

    func testCentreIsTakenLastOnOddCounts() {
        for count in [5, 7, 9] {
            XCTAssertEqual(names(Handicap.stones(count: count, size: 19), size: 19).last, "K10",
                           "handicap \(count)")
        }
        for count in [4, 6, 8] {
            XCTAssertFalse(names(Handicap.stones(count: count, size: 19), size: 19).contains("K10"),
                           "handicap \(count) should not use the centre")
        }
    }

    func testCountsBelowTwoPlaceNothing() {
        XCTAssertTrue(Handicap.stones(count: 0, size: 19).isEmpty)
        XCTAssertTrue(Handicap.stones(count: 1, size: 19).isEmpty)
        XCTAssertTrue(Handicap.stones(count: 10, size: 19).isEmpty)
    }

    func testUnsupportedSizeHasNoStarPoints() {
        XCTAssertTrue(Handicap.starPoints(size: 11).isEmpty)
        XCTAssertTrue(Handicap.stones(count: 4, size: 11).isEmpty)
    }

    func testStarPointCountPerSize() {
        for size in BoardSize.supported {
            XCTAssertEqual(Handicap.starPoints(size: size).count, 9, "\(size)x\(size)")
        }
    }
}

final class StateTests: XCTestCase {
    func testStateExposesBoardSize() {
        for size in BoardSize.supported {
            XCTAssertEqual(GameEngine.newGame(size: size).state.size, size)
        }
    }

    func testMoveErrorCodesMatchTheWireProtocol() {
        let expected: [MoveError: String] = [
            .outOfBounds: "out_of_bounds", .occupied: "occupied", .notYourTurn: "not_your_turn",
            .suicide: "suicide", .ko: "ko", .superko: "superko", .gameNotPlaying: "game_not_playing"
        ]
        for (error, code) in expected { XCTAssertEqual(error.code, code) }
    }

    func testWireStringRejectsBadInput() {
        XCTAssertNil(Board(size: 9, wireString: "too short"))
        XCTAssertNil(Board(size: 9, wireString: String(repeating: "x", count: 81)))
        XCTAssertNil(Board(size: 11, wireString: String(repeating: ".", count: 121)))
    }
}

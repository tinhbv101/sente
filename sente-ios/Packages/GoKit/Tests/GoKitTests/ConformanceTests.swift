import Foundation
import XCTest
@testable import GoKit

/// Runs the shared conformance vectors. The Go engine runs the exact same files;
/// if these two suites ever disagree, the engines have drifted (docs/03 ADR-002).
final class ConformanceTests: XCTestCase {

    func testLegality() throws { try runGroup("legality") }
    func testCapture() throws { try runGroup("capture") }
    func testSuicide() throws { try runGroup("suicide") }
    func testKo() throws { try runGroup("ko") }
    func testSuperko() throws { try runGroup("superko") }
    func testLifeDeath() throws { try runGroup("life_death") }
    func testScoring() throws { try runGroup("scoring") }
    func testHandicap() throws { try runGroup("handicap") }
    func testSGF() throws { try runGroup("sgf") }

    func testEveryGroupIsCovered() {
        let covered = Set(["legality", "capture", "suicide", "ko", "superko",
                           "life_death", "scoring", "handicap", "sgf"])
        let onDisk = Set(VectorLoader.allGroups)
        XCTAssertEqual(onDisk.subtracting(covered), [], "vector group with no test method")
        XCTAssertFalse(onDisk.isEmpty, "no vectors found")
    }

    // MARK: - Runner

    private func runGroup(_ group: String, file: StaticString = #filePath, line: UInt = #line) throws {
        let vectors = try VectorLoader.load(group: group)
        XCTAssertFalse(vectors.isEmpty, "group \(group) has no vectors", file: file, line: line)
        for vector in vectors {
            run(vector, file: file, line: line)
        }
    }

    private func run(_ vector: Vector, file: StaticString, line: UInt) {
        let size = vector.boardSize ?? 19
        let rules: RuleSet = vector.rules == "chinese" ? .chinese : .japanese
        let handicap = vector.handicap ?? 0

        func fail(_ message: String) {
            XCTFail("[\(vector.group ?? "?")/\(vector.id)] \(message)", file: file, line: line)
        }
        func point(_ text: String) -> Point {
            // An unparseable coordinate is deliberately mapped off the board so the
            // engine -- not the parser -- decides that it is out of bounds.
            Coordinate.point(text, size: size) ?? Point(col: size, row: size)
        }
        func points(_ list: [String]?) -> [Point] { (list ?? []).map(point) }
        func names(_ list: [Point]) -> Set<String> { Set(list.map { Coordinate.text($0, size: size) }) }

        var engine: GameEngine
        if handicap > 0 {
            engine = GameEngine.newGame(size: size, rules: rules, komi: vector.komi, handicap: handicap)
        } else {
            engine = GameEngine.position(
                size: size,
                black: points(vector.setup?.black),
                white: points(vector.setup?.white),
                toPlay: vector.toPlay == "white" ? .white : .black,
                rules: rules,
                komi: vector.komi,
                captures: Captures(black: vector.captures?["black"] ?? 0,
                                   white: vector.captures?["white"] ?? 0)
            )
        }
        let startHash = engine.state.boardHash

        // Pre-moves run before the assertion; any failure here is a broken vector.
        for encoded in vector.moves ?? [] {
            guard let (player, move) = parse(encoded, size: size) else {
                return fail("cannot parse pre-move '\(encoded)'")
            }
            do {
                engine = try engine.apply(move, by: player)
            } catch {
                return fail("pre-move '\(encoded)' rejected: \(error)")
            }
        }

        let expect = vector.expect

        // MARK: move under test
        if let encodedMove = vector.move {
            let mover: Player = vector.moveBy.map { $0 == "white" ? .white : .black } ?? engine.toPlay
            let move: Move
            switch encodedMove {
            case "pass": move = .pass
            case "resign": move = .resign
            default: move = .play(point(encodedMove))
            }

            let verdict = engine.validate(move, by: mover)
            if let expectedLegal = expect.legal {
                XCTAssertEqual(verdict.isSuccess, expectedLegal,
                               "[\(vector.id)] legality; got \(verdict)", file: file, line: line)
            }
            if let expectedReason = expect.reason {
                if case .failure(let error) = verdict {
                    XCTAssertEqual(error.code, expectedReason, "[\(vector.id)] reason", file: file, line: line)
                } else {
                    fail("expected rejection '\(expectedReason)' but the move was accepted")
                }
            }

            guard expect.legal != false else { return }

            let before = engine
            let after: GameEngine
            do {
                after = try engine.apply(move, by: mover)
            } catch {
                return fail("apply threw \(error)")
            }
            XCTAssertEqual(before.state, engine.state,
                           "[\(vector.id)] apply mutated the receiver", file: file, line: line)

            if let expectedCaptured = expect.captured {
                let removed = before.state.board.allPoints.filter {
                    before.state.board[$0] != nil && after.state.board[$0] == nil
                }
                XCTAssertEqual(names(removed), Set(expectedCaptured.map { $0.uppercased() }),
                               "[\(vector.id)] captured stones", file: file, line: line)
            }
            if let expectedCaptures = expect.capturesAfter {
                XCTAssertEqual(after.state.captures.black, expectedCaptures["black"] ?? 0,
                               "[\(vector.id)] black prisoners", file: file, line: line)
                XCTAssertEqual(after.state.captures.white, expectedCaptures["white"] ?? 0,
                               "[\(vector.id)] white prisoners", file: file, line: line)
            }
            if let expectedKo = expect.koPoint {
                XCTAssertEqual(after.state.koPoint.map { Coordinate.text($0, size: size) },
                               expectedKo.uppercased(), "[\(vector.id)] ko point", file: file, line: line)
            }
            if expect.koPointAbsent == true {
                XCTAssertNil(after.state.koPoint, "[\(vector.id)] expected no ko point", file: file, line: line)
            }
            if let expectedPhase = expect.phase {
                XCTAssertEqual(after.state.phase.rawValue, expectedPhase,
                               "[\(vector.id)] phase", file: file, line: line)
            }
            if let expectedResult = expect.result {
                XCTAssertEqual(after.state.result?.winner?.rawValue, expectedResult.winner,
                               "[\(vector.id)] winner", file: file, line: line)
                XCTAssertEqual(after.state.result?.reason.rawValue, expectedResult.reason,
                               "[\(vector.id)] end reason", file: file, line: line)
            }
            engine = after
        }

        // MARK: position-level expectations
        let dead = Set(points(vector.deadStones))

        if let expectedTerritory = expect.territory {
            let map = engine.territory(deadStones: dead)
            XCTAssertEqual(map.black.count, expectedTerritory["black"] ?? 0,
                           "[\(vector.id)] black territory", file: file, line: line)
            XCTAssertEqual(map.white.count, expectedTerritory["white"] ?? 0,
                           "[\(vector.id)] white territory", file: file, line: line)
        }
        if let expectedNeutral = expect.neutral {
            XCTAssertEqual(engine.territory(deadStones: dead).neutral.count, expectedNeutral,
                           "[\(vector.id)] neutral points", file: file, line: line)
        }
        if let expectedArea = expect.area {
            let score = engine.score(deadStones: dead)
            XCTAssertEqual(score.detail.black.area, expectedArea["black"] ?? 0,
                           "[\(vector.id)] black area", file: file, line: line)
            XCTAssertEqual(score.detail.white.area, expectedArea["white"] ?? 0,
                           "[\(vector.id)] white area", file: file, line: line)
        }
        if let expectedScore = expect.score {
            let score = engine.score(deadStones: dead)
            XCTAssertEqual(score.black, expectedScore["black"] ?? 0,
                           "[\(vector.id)] black score", file: file, line: line)
            XCTAssertEqual(score.white, expectedScore["white"] ?? 0,
                           "[\(vector.id)] white score", file: file, line: line)
        }
        if let expectedWinner = expect.winner {
            XCTAssertEqual(engine.score(deadStones: dead).winner?.rawValue, expectedWinner,
                           "[\(vector.id)] score winner", file: file, line: line)
        }
        if let expectedMargin = expect.margin {
            XCTAssertEqual(engine.score(deadStones: dead).margin, expectedMargin,
                           "[\(vector.id)] margin", file: file, line: line)
        }
        if let expectedPassAlive = expect.passAlive {
            for player in Player.allCases {
                let chains = engine.state.board.passAliveChains(for: player)
                let actual = names(Array(chains.flatMap { $0 }))
                let wanted = Set((expectedPassAlive[player.rawValue] ?? []).map { $0.uppercased() })
                XCTAssertEqual(actual, wanted, "[\(vector.id)] \(player.rawValue) pass-alive",
                               file: file, line: line)
            }
        }
        if let expectedStones = expect.handicapStones {
            let placed = engine.state.board.stones(of: .black)
            XCTAssertEqual(names(placed), Set(expectedStones.map { $0.uppercased() }),
                           "[\(vector.id)] handicap placement", file: file, line: line)
        }
        if let expectedToPlay = expect.toPlay {
            XCTAssertEqual(engine.toPlay.rawValue, expectedToPlay,
                           "[\(vector.id)] to play", file: file, line: line)
        }
        if let expectedKomi = expect.komi {
            XCTAssertEqual(engine.komi, expectedKomi, "[\(vector.id)] komi", file: file, line: line)
        }
        if let expectedMoveNumber = expect.moveNumber {
            XCTAssertEqual(engine.state.moveNumber, expectedMoveNumber,
                           "[\(vector.id)] move number", file: file, line: line)
        }
        if expect.historyContainsStart == true {
            XCTAssertTrue(engine.history.contains(startHash),
                          "[\(vector.id)] starting position missing from history", file: file, line: line)
        }
        if let expectedHistorySize = expect.historySize {
            XCTAssertEqual(engine.history.count, expectedHistorySize,
                           "[\(vector.id)] history size", file: file, line: line)
        }

        // MARK: SGF
        if expect.sgfContains != nil || expect.roundTrip == true {
            runSGF(vector, size: size, rules: rules, handicap: handicap, file: file, line: line)
        }
    }

    private func runSGF(_ vector: Vector, size: Int, rules: RuleSet, handicap: Int,
                        file: StaticString, line: UInt) {
        var record = GameRecord(
            size: size,
            rules: rules,
            komi: vector.komi ?? GameEngine.defaultKomi(rules: rules, handicap: handicap),
            handicap: handicap,
            handicapStones: Handicap.stones(count: handicap, size: size),
            blackPlayer: "an",
            whitePlayer: "binh"
        )
        record.moves = (vector.moves ?? []).compactMap { encoded in
            guard let (player, move) = parse(encoded, size: size) else { return nil }
            return RecordedMove(player: player, move: move)
        }

        let text = SGF.encode(record)
        for fragment in vector.expect.sgfContains ?? [] {
            XCTAssertTrue(text.contains(fragment),
                          "[\(vector.id)] SGF missing '\(fragment)' in \(text)", file: file, line: line)
        }
        guard vector.expect.roundTrip == true else { return }
        do {
            let decoded = try SGF.decode(text)
            XCTAssertEqual(decoded.size, record.size, "[\(vector.id)] size", file: file, line: line)
            XCTAssertEqual(decoded.komi, record.komi, "[\(vector.id)] komi", file: file, line: line)
            XCTAssertEqual(decoded.rules, record.rules, "[\(vector.id)] rules", file: file, line: line)
            XCTAssertEqual(decoded.handicap, record.handicap, "[\(vector.id)] handicap", file: file, line: line)
            XCTAssertEqual(decoded.handicapStones, record.handicapStones,
                           "[\(vector.id)] handicap stones", file: file, line: line)
            XCTAssertEqual(decoded.moves, record.moves, "[\(vector.id)] moves", file: file, line: line)
        } catch {
            XCTFail("[\(vector.id)] SGF decode failed: \(error)", file: file, line: line)
        }
    }

    /// Parses the `"B:e5"` / `"W:pass"` shorthand used for pre-moves.
    private func parse(_ encoded: String, size: Int) -> (Player, Move)? {
        let parts = encoded.split(separator: ":", maxSplits: 1).map(String.init)
        guard parts.count == 2 else { return nil }
        let player: Player = parts[0].uppercased() == "B" ? .black : .white
        switch parts[1].lowercased() {
        case "pass": return (player, .pass)
        case "resign": return (player, .resign)
        default:
            guard let point = Coordinate.point(parts[1], size: size) else { return nil }
            return (player, .play(point))
        }
    }
}

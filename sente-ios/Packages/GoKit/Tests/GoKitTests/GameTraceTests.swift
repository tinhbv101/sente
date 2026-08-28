import Foundation
import XCTest
@testable import GoKit

/// Cross-engine differential trace.
///
/// The parity lock compares final positions; this compares *every move* of full
/// games. One engine records a game as a move list plus the hash after each move,
/// the other replays it and must agree at every step. That is what turns ADR-002
/// from an intention into a check: a divergence anywhere in a 200-move game is
/// caught at the exact move it happens.
final class GameTraceTests: XCTestCase {
    struct Trace: Codable {
        struct Game: Codable {
            let id: String
            let size: Int
            let rules: String
            let moves: [String]
            let hashes: [String]
            let captures: [String: Int]
            let finalPhase: String
        }
        let rulesVersion: String
        let note: String
        let games: [Game]
    }

    static let traceURL = VectorLoader.vectorsURL
        .deletingLastPathComponent()
        .appendingPathComponent("parity/games.json")

    private func buildGames() -> [Trace.Game] {
        var games: [Trace.Game] = []
        for size in BoardSize.supported {
            for rules in [RuleSet.japanese, .chinese] {
                for seed in UInt64(1)...4 {
                    var generator = SeededGenerator(seed: seed &* 7919 &+ UInt64(size))
                    var engine = GameEngine.newGame(size: size, rules: rules)
                    var moves: [String] = []
                    var hashes: [String] = []

                    for _ in 0..<(size * 12) {
                        guard engine.state.phase == .playing else { break }
                        let legal = engine.legalMoves().sorted { ($0.row, $0.col) < ($1.row, $1.col) }
                        let move: Move = legal.isEmpty ? .pass
                            : .play(legal.randomElement(using: &generator)!)
                        guard let next = try? engine.apply(move) else { break }
                        engine = next
                        switch move {
                        case .play(let point): moves.append(Coordinate.text(point, size: size))
                        case .pass: moves.append("pass")
                        case .resign: moves.append("resign")
                        }
                        hashes.append(String(format: "0x%016llX", engine.state.boardHash))
                    }

                    games.append(Trace.Game(
                        id: "\(size)-\(rules.rawValue)-\(seed)",
                        size: size,
                        rules: rules.rawValue,
                        moves: moves,
                        hashes: hashes,
                        captures: ["black": engine.state.captures.black,
                                   "white": engine.state.captures.white],
                        finalPhase: engine.state.phase.rawValue
                    ))
                }
            }
        }
        return games
    }

    /// Set `WRITE_PARITY_LOCK=1` to regenerate.
    func testGameTraceMatches() throws {
        let games = buildGames()

        if ProcessInfo.processInfo.environment["WRITE_PARITY_LOCK"] == "1" {
            let trace = Trace(
                rulesVersion: "1.0.0",
                note: "Recorded by GoKit. Every engine must reproduce each hash, move by move.",
                games: games
            )
            let encoder = JSONEncoder()
            encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            try encoder.encode(trace).write(to: Self.traceURL)
            let total = games.reduce(0) { $0 + $1.moves.count }
            print("wrote \(games.count) games, \(total) moves to \(Self.traceURL.path)")
            return
        }

        let trace = try JSONDecoder().decode(Trace.self, from: Data(contentsOf: Self.traceURL))
        XCTAssertEqual(trace.games.count, games.count)
        for (expected, actual) in zip(trace.games, games) {
            XCTAssertEqual(actual.moves, expected.moves, "\(expected.id): move list")
            XCTAssertEqual(actual.hashes, expected.hashes, "\(expected.id): hashes")
            XCTAssertEqual(actual.captures, expected.captures, "\(expected.id): captures")
        }
    }

    /// Replaying the recorded move list must reproduce every recorded hash. This is
    /// the same assertion the Go suite makes, run against the same file.
    func testReplayingTheTraceReproducesEveryHash() throws {
        let trace = try JSONDecoder().decode(Trace.self, from: Data(contentsOf: Self.traceURL))
        var checkedMoves = 0

        for game in trace.games {
            var engine = GameEngine.newGame(
                size: game.size,
                rules: game.rules == "chinese" ? .chinese : .japanese
            )
            for (index, encoded) in game.moves.enumerated() {
                let move: Move
                switch encoded {
                case "pass": move = .pass
                case "resign": move = .resign
                default:
                    guard let point = Coordinate.point(encoded, size: game.size) else {
                        return XCTFail("\(game.id): bad coordinate \(encoded)")
                    }
                    move = .play(point)
                }
                engine = try engine.apply(move)
                XCTAssertEqual(String(format: "0x%016llX", engine.state.boardHash),
                               game.hashes[index],
                               "\(game.id): hash diverged at move \(index + 1) (\(encoded))")
                checkedMoves += 1
            }
            XCTAssertEqual(engine.state.captures.black, game.captures["black"], "\(game.id): black prisoners")
            XCTAssertEqual(engine.state.captures.white, game.captures["white"], "\(game.id): white prisoners")
            XCTAssertEqual(engine.state.phase.rawValue, game.finalPhase, "\(game.id): final phase")
        }
        print("replayed \(trace.games.count) games, \(checkedMoves) move-by-move hash checks")
    }
}

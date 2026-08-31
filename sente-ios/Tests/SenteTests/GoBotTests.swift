import XCTest
import GoKit
@testable import Sente

/// The bot's contract: only legal moves, games that end, obvious tactics taken,
/// own eyes never filled, and a stronger level that actually plays stronger.
final class GoBotTests: XCTestCase {
    private func point(_ text: String) -> Point { Coordinate.point(text, size: 9)! }

    private func play(black: GoBot, white: GoBot, maxPlies: Int = 240) throws -> GameEngine {
        var engine = GameEngine.newGame(size: 9)
        var bots = [Player.black: black, .white: white]
        for _ in 0..<maxPlies {
            guard engine.state.phase == .playing else { break }
            let side = engine.toPlay
            let move = bots[side]!.chooseMove(engine)
            engine = try engine.apply(move, by: side)
        }
        return engine
    }

    func testBotsFinishLegalGames() throws {
        for seed: UInt64 in [1, 2, 3] {
            let end = try play(black: GoBot(level: .novice, seed: seed),
                               white: GoBot(level: .greedy, seed: seed &+ 99))
            XCTAssertNotEqual(end.state.phase, .playing, "seed \(seed): the game must end")
            XCTAssertGreaterThan(end.state.moveNumber, 10, "seed \(seed): they should actually play")
        }
    }

    func testEveryLevelTakesTheFreeCapture() {
        // The white stone at E5 has one liberty (E4) — from the first lesson.
        let engine = GameEngine.position(size: 9,
                                         black: [point("D5"), point("E6"), point("F5")],
                                         white: [point("E5")], toPlay: .black)
        for level in BotLevel.allCases {
            var bot = GoBot(level: level, seed: 7)
            bot.searchIterations = 250; bot.deepIterations = 250; bot.deepDeadline = nil
            guard level != .novice else { continue } // the novice is allowed to miss it
            XCTAssertEqual(bot.chooseMove(engine), .play(point("E4")), "\(level)")
        }
    }

    func testSavesItsOwnStoneFromAtari() throws {
        // Black E5 in atari, escape at E4 (the escape lesson's shape).
        let engine = GameEngine.position(size: 9,
                                         black: [point("E5")],
                                         white: [point("D5"), point("E6"), point("F5")], toPlay: .black)
        var bot = GoBot(level: .thoughtful, seed: 5)
        bot.searchIterations = 250
        let move = bot.chooseMove(engine)
        guard case .play(let chosen) = move else { return XCTFail("must not pass") }
        let after = try engine.apply(.play(chosen), by: .black)
        XCTAssertGreaterThan(after.board.liberties(at: point("E5")), 1, "chose \(chosen)")
    }

    func testNeverFillsItsOwnEyes() {
        // Real single-point eyes at A1 and C1.
        let engine = GameEngine.position(size: 9,
                                         black: ["B1", "D1", "E1", "A2", "B2", "C2", "D2", "E2"].map(point),
                                         white: [point("G7")], toPlay: .black)
        for seed: UInt64 in 1...20 {
            for level in BotLevel.allCases {
                var bot = GoBot(level: level, seed: seed)
                bot.searchIterations = 40; bot.deepIterations = 40; bot.deepDeadline = nil
                let move = bot.chooseMove(engine)
                XCTAssertNotEqual(move, .play(point("A1")), "\(level) seed \(seed)")
                XCTAssertNotEqual(move, .play(point("C1")), "\(level) seed \(seed)")
            }
        }
    }

    /// Levels must mean something: the search level beats the tactical one.
    func testSearchBeatsGreedy() throws {
        var wins = 0
        for (index, seed) in ([21, 22, 23, 24] as [UInt64]).enumerated() {
            let strongIsBlack = index % 2 == 0
            var strong = GoBot(level: .thoughtful, seed: seed)
            strong.searchIterations = 320
            let weak = GoBot(level: .greedy, seed: seed &+ 500)
            let end = try play(black: strongIsBlack ? strong : weak,
                               white: strongIsBlack ? weak : strong)
            let map = end.territory(deadStones: [])
            let margin = Double(end.board.stones(of: .black).count + map.black.count)
                - Double(end.board.stones(of: .white).count + map.white.count) - end.komi
            if (margin > 0) == strongIsBlack { wins += 1 }
        }
        XCTAssertGreaterThanOrEqual(wins, 3, "search should win at least 3 of 4 fixed-seed games")
    }
}

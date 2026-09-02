import XCTest
import GoKit
@testable import Sente

/// The strong bot must solve the life-and-death lessons the app teaches: the
/// same boards, the same single correct answer. Budgets are iteration-bounded
/// and seeded, so the outcome is identical on every machine.
@MainActor
final class BotLifeDeathTests: XCTestCase {
    private static let lessons = LessonLibrary.load().chapters
        .first { $0.id == "life-death" }!.lessons

    private func firstTask(_ id: String) -> (engine: GameEngine, accepted: [Point]) {
        let lesson = Self.lessons.first { $0.id == id }!
        let step = lesson.steps.first { $0.kind == .task && $0.board != nil }!
        let size = step.size ?? 9
        let engine = LessonBoard.engine(size: size, rows: step.board, toPlay: .black)
        return (engine, step.correct!.map { Coordinate.point($0, size: size)! })
    }

    func testTheDeepBotSolvesTheLessonTsumego() {
        // Kills of every dead shape the lessons name, plus the two-eye defence.
        for id in ["vital-point", "bent-three", "pyramid-four", "bulky-five", "cross-five", "two-eyes"] {
            let (engine, accepted) = firstTask(id)
            var bot = GoBot(level: .deep, seed: 42)
            bot.deepIterations = 900
            bot.deepDeadline = nil
            guard case .play(let chosen) = bot.chooseMove(engine) else {
                XCTFail("\(id): the bot did not play"); continue
            }
            XCTAssertTrue(accepted.contains(chosen),
                          "\(id): played \(Coordinate.text(chosen, size: engine.state.size))")
        }
    }

    func testTheVitalPointScanNamesTheLessonAnswers() {
        // The static scan alone — no search — must already point at each answer.
        for id in ["vital-point", "bent-three", "pyramid-four", "bulky-five", "cross-five", "two-eyes"] {
            let (engine, accepted) = firstTask(id)
            let vitals = BotHeuristics.lifeDeathVitals(engine)
            XCTAssertTrue(vitals.contains { accepted.contains($0.point) },
                          "\(id): scan found \(vitals.map { Coordinate.text($0.point, size: engine.state.size) })")
        }
    }

    func testAnOpenOrContestedSpaceHasNoVitalPoint() {
        // An empty board is one huge region; a mixed border is a normal fight.
        XCTAssertTrue(BotHeuristics.lifeDeathVitals(GameEngine.newGame(size: 9)).isEmpty)
        let point = { Coordinate.point($0, size: 9)! }
        let contested = GameEngine.position(size: 9,
                                            black: [point("A2"), point("B2")],
                                            white: [point("C2"), point("D1")], toPlay: .black)
        XCTAssertTrue(BotHeuristics.lifeDeathVitals(contested).isEmpty)
    }
}

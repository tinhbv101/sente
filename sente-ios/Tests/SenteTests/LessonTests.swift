import XCTest
import GoKit
@testable import Sente

/// Every lesson is replayed through the real engine: a diagram that does not
/// parse, an illegal "correct" move, or a wrong capture count fails here, so the
/// app cannot ship content that teaches wrong Go.
@MainActor
final class LessonContentTests: XCTestCase {
    private let library = LessonLibrary.load()

    func testTheLibraryIsSubstantialAndUnique() {
        XCTAssertGreaterThanOrEqual(library.chapters.count, 4)
        XCTAssertGreaterThanOrEqual(library.lessonCount, 20)
        let ids = library.chapters.flatMap { $0.lessons.map(\.id) }
        XCTAssertEqual(ids.count, Set(ids).count, "lesson ids must be unique")
        for chapter in library.chapters {
            XCTAssertFalse(chapter.title.vi.isEmpty); XCTAssertFalse(chapter.title.en.isEmpty)
        }
    }

    func testEveryStepReplaysCleanlyThroughTheEngine() throws {
        for chapter in library.chapters + DailyPuzzles.library.chapters {
            for lesson in chapter.lessons {
                var engine = GameEngine.position(size: 9)
                XCTAssertFalse(lesson.steps.isEmpty, lesson.id)
                for (index, step) in lesson.steps.enumerated() {
                    let label = "\(lesson.id) step \(index)"
                    let size = step.size ?? 9
                    if let rows = step.board {
                        XCTAssertEqual(rows.count, size, label)
                        for row in rows {
                            XCTAssertEqual(row.count, size, label)
                            XCTAssertTrue(row.allSatisfy { "bw.".contains($0) }, label)
                        }
                    }
                    let side = Player(rawValue: step.toPlay ?? "black") ?? .black
                    if step.board != nil {
                        engine = LessonBoard.engine(size: size, rows: step.board, toPlay: side)
                    }
                    for mark in step.highlight ?? [] {
                        XCTAssertNotNil(Coordinate.point(mark, size: size), "\(label) highlight \(mark)")
                    }
                    guard step.kind == .task else { continue }
                    let correct = try XCTUnwrap(step.correct, label)
                    XCTAssertFalse(correct.isEmpty, label)

                    // Every accepted answer must be legal from this position.
                    for answer in correct {
                        let point = try XCTUnwrap(Coordinate.point(answer, size: size), "\(label) \(answer)")
                        if case .failure(let error) = engine.validate(.play(point), by: side) {
                            XCTFail("\(label): correct move \(answer) is illegal: \(error)")
                        }
                    }
                    // The lesson continues along the first answer.
                    let first = try XCTUnwrap(Coordinate.point(correct[0], size: size))
                    func taken(_ engine: GameEngine) -> Int {
                        side == .black ? engine.state.captures.black : engine.state.captures.white
                    }
                    let before = taken(engine)
                    engine = try engine.apply(.play(first), by: side)
                    if let expected = step.captures {
                        XCTAssertEqual(taken(engine) - before, expected,
                                       "\(label): capture count")
                    }
                    if let atari = step.atariAt {
                        let point = try XCTUnwrap(Coordinate.point(atari, size: size), "\(label) atariAt")
                        XCTAssertNotNil(engine.board[point], "\(label): atariAt point is empty")
                        XCTAssertEqual(engine.liberties(at: point), 1,
                                       "\(label): \(atari) should be in atari after the move")
                    }
                    if let reply = step.reply {
                        let replyPoint = try XCTUnwrap(Coordinate.point(reply, size: size), "\(label) reply")
                        engine = try engine.apply(.play(replyPoint), by: side.opponent)
                    } else if engine.state.phase == .playing, engine.toPlay != side {
                        // Keep the side to move consistent for a continuation step.
                        engine = GameEngine.position(size: size,
                                                     black: engine.board.stones(of: .black),
                                                     white: engine.board.stones(of: .white),
                                                     toPlay: side, captures: engine.state.captures)
                    }
                }
            }
        }
    }
}

@MainActor
final class LessonPlayerTests: XCTestCase {
    private var lesson: Lesson {
        LessonLibrary.load().chapters
            .flatMap(\.lessons).first { $0.id == "snapback" }!
    }

    private func store(_ suite: String) -> LessonPlayerStore {
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        return LessonPlayerStore(lesson: lesson, progress: LessonProgress(defaults: defaults))
    }

    private func point(_ text: String) -> Point { Coordinate.point(text, size: 9)! }

    func testWrongMovesDoNotAdvanceAndCorrectOnesDo() {
        let player = store("lesson-test-1")
        player.advance() // past the info step
        XCTAssertTrue(player.awaitingMove)

        player.tap(point("A9"))
        XCTAssertFalse(player.solved)
        XCTAssertNotNil(player.feedback)
        XCTAssertFalse(player.feedbackIsPraise)
        XCTAssertNil(player.engine.board[point("A9")], "a wrong answer must not be placed")

        player.tap(point("D1")) // the throw-in; white recaptures at C1 by script
        XCTAssertTrue(player.solved)
        XCTAssertTrue(player.feedbackIsPraise)
        XCTAssertEqual(player.engine.board[point("C1")], .white)
        XCTAssertNil(player.engine.board[point("D1")], "white captured the sacrifice")
    }

    func testTheSnapbackPlaysOutAndCompletionIsRecorded() {
        let suite = "lesson-test-2"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        let progress = LessonProgress(defaults: defaults)
        let player = LessonPlayerStore(lesson: lesson, progress: progress)

        player.advance()
        player.tap(point("D1"))
        player.advance()
        XCTAssertTrue(player.awaitingMove)
        player.tap(point("D1")) // recapture the lot
        XCTAssertTrue(player.solved)
        XCTAssertNil(player.engine.board[point("C1")])
        XCTAssertNil(player.engine.board[point("E2")])
        XCTAssertEqual(player.engine.state.captures.black, 5)

        XCTAssertFalse(progress.isDone(lesson.id))
        player.advance()
        XCTAssertTrue(player.completed)
        XCTAssertTrue(progress.isDone(lesson.id), "finishing the last step records the lesson")
    }

    func testResetRestoresTheStepPosition() {
        let player = store("lesson-test-3")
        player.advance()
        player.tap(point("D1"))
        XCTAssertTrue(player.solved)
        player.retry()
        XCTAssertFalse(player.solved)
        XCTAssertNil(player.engine.board[point("D1")])
        XCTAssertNil(player.engine.board[point("C1")])
    }
}

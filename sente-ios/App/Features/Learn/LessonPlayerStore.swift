import Foundation
import Observation
import GoKit
import SenteUI

/// Walks one lesson: shows a step, referees the task through GoKit, and moves on.
@MainActor @Observable
final class LessonPlayerStore {
    let lesson: Lesson
    private(set) var stepIndex = 0
    private(set) var engine: GameEngine
    private(set) var solved = false
    private(set) var completed = false
    private(set) var feedback: String?
    private(set) var feedbackIsPraise = false
    private(set) var lastMove: Point?
    private let progress: LessonProgress

    init(lesson: Lesson, progress: LessonProgress = LessonProgress()) {
        self.lesson = lesson
        self.progress = progress
        self.engine = GameEngine.position(size: 9)
        loadStep()
    }

    var step: LessonStep { lesson.steps[stepIndex] }
    var boardSize: Int { engine.state.size }
    var awaitingMove: Bool { step.kind == .task && !solved && !completed }
    var mySide: Player { Player(rawValue: step.toPlay ?? "black") ?? .black }

    var snapshot: BoardSnapshot {
        var snapshot = BoardSnapshot(board: engine.board, lastMove: lastMove)
        if let marks = step.highlight, !solved {
            snapshot.territoryBlack = Set(marks.compactMap { Coordinate.point($0, size: boardSize) })
        }
        return snapshot
    }

    /// The step's own board, or the position the previous step ended in.
    private func loadStep() {
        solved = false
        feedback = nil
        if step.board != nil || stepIndex == 0 {
            engine = LessonBoard.engine(size: step.size ?? 9, rows: step.board,
                                        toPlay: mySide)
            lastMove = nil
        } else if engine.toPlay != mySide {
            // A continuation step keeps the stones but the lesson says who moves.
            engine = GameEngine.position(size: boardSize,
                                         black: engine.board.stones(of: .black),
                                         white: engine.board.stones(of: .white),
                                         toPlay: mySide,
                                         captures: engine.state.captures)
        }
    }

    func legality(_ point: Point) -> String? {
        guard awaitingMove else { return String(localized: "Chưa tới lượt") }
        if case .failure = engine.validate(.play(point), by: mySide) { return String(localized: "Không hợp lệ") }
        return nil
    }

    func tap(_ point: Point) {
        guard awaitingMove else { return }
        let text = Coordinate.text(point, size: boardSize)
        guard step.correct?.contains(text) == true,
              let next = try? engine.apply(.play(point), by: mySide) else {
            feedback = step.wrong?.text ?? String(localized: "Chưa đúng — thử lại.")
            feedbackIsPraise = false
            return
        }
        engine = next
        lastMove = point
        if let reply = step.reply, let replyPoint = Coordinate.point(reply, size: boardSize),
           let answered = try? engine.apply(.play(replyPoint), by: mySide.opponent) {
            engine = answered
            lastMove = replyPoint
        }
        solved = true
        feedback = step.success?.text ?? String(localized: "Đúng rồi!")
        feedbackIsPraise = true
    }

    func advance() {
        guard stepIndex + 1 < lesson.steps.count else {
            completed = true
            progress.markDone(lesson.id)
            return
        }
        stepIndex += 1
        loadStep()
    }

    func retry() { loadStep() }
}

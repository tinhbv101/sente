import Foundation
import Observation
import GoKit
import SwiftUI
import SenteUI

/// One place the game swung: the bot's before/after verdict on a played move.
/// `id` is the move index — position `id` is where the mistake was played.
struct ReviewFinding: Identifiable, Equatable {
    let id: Int
    let mover: Player
    let played: Move
    let suggested: Move
    let before: Double
    let after: Double
    var drop: Double { before - after }
}

/// Replays the whole game through MCTS and surfaces the biggest win-rate drops.
@MainActor @Observable
final class GameReviewStore {
    enum State: Equatable { case idle, running(done: Int, total: Int), done }
    private(set) var state: State = .idle
    private(set) var findings: [ReviewFinding] = []
    /// Budget per position; tests shrink it. Seeded, so a review is repeatable.
    var iterations = 240
    var seed: UInt64 = 9

    private var task: Task<Void, Never>?

    func run(positions: [GameEngine], moves: [RecordedMove]) {
        guard positions.count == moves.count + 1, moves.count >= 2 else { return }
        if case .running = state { return }
        findings = []
        state = .running(done: 0, total: positions.count)
        let budget = iterations
        let baseSeed = seed
        task = Task {
            var rates: [Int: (move: Move, blackWin: Double)] = [:]
            for (index, position) in positions.enumerated() {
                if Task.isCancelled { return }
                let verdict = await Task.detached(priority: .userInitiated) { () -> (Move, Double)? in
                    var bot = MCTSBot(iterations: budget, seed: baseSeed &+ UInt64(index))
                    guard let result = bot.analyze(position) else { return nil }
                    let blackWin = position.toPlay == .black ? result.winRate : 1 - result.winRate
                    return (result.move, blackWin)
                }.value
                if let verdict { rates[index] = (verdict.0, verdict.1) }
                state = .running(done: index + 1, total: positions.count)
            }
            findings = Self.worstMoves(moves: moves, rates: rates)
            state = .done
        }
    }

    func cancel() {
        task?.cancel()
        if state != .done { state = .idle }
    }

    /// Biggest drops from the mover's own perspective, worst first. Small swings
    /// are playout noise, not mistakes, so they are cut off.
    static func worstMoves(moves: [RecordedMove],
                           rates: [Int: (move: Move, blackWin: Double)]) -> [ReviewFinding] {
        var all: [ReviewFinding] = []
        for (index, recorded) in moves.enumerated() {
            guard let before = rates[index], let after = rates[index + 1] else { continue }
            let mine = { (blackWin: Double) in recorded.player == .black ? blackWin : 1 - blackWin }
            let finding = ReviewFinding(id: index, mover: recorded.player, played: recorded.move,
                                        suggested: before.move,
                                        before: mine(before.blackWin), after: mine(after.blackWin))
            if finding.drop >= 0.06, finding.played != finding.suggested { all.append(finding) }
        }
        return Array(all.sorted { $0.drop > $1.drop }.prefix(3))
    }
}

/// "Review the whole game": a button, a progress bar, then the three worst
/// moves — tapping one jumps the replay to just before it was played.
struct GameReviewSection: View {
    let positions: [GameEngine]
    let moves: [RecordedMove]
    @Binding var index: Int
    @State private var review = GameReviewStore()

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            switch review.state {
            case .idle:
                Button {
                    review.run(positions: positions, moves: moves)
                } label: {
                    Label("Máy soát cả ván — tìm nước sai", systemImage: "stethoscope")
                        .font(.caption.weight(.semibold))
                }
                .disabled(moves.count < 2)
            case .running(let done, let total):
                HStack(spacing: 8) {
                    ProgressView(value: Double(done), total: Double(max(total, 1)))
                    Text("\(done)/\(total)")
                        .font(.caption2.monospacedDigit()).foregroundStyle(Tokens.inkSecondary)
                }
            case .done:
                if review.findings.isEmpty {
                    Text("Không thấy sai lầm lớn nào — ván đấu chắc tay!")
                        .font(.caption).foregroundStyle(Tokens.inkSecondary)
                } else {
                    ForEach(review.findings) { finding in row(finding) }
                }
            }
        }
        .padding(.horizontal, 6)
        .foregroundStyle(Tokens.ink)
        .onDisappear { review.cancel() }
    }

    private func row(_ finding: ReviewFinding) -> some View {
        Button {
            index = finding.id
        } label: {
            HStack(spacing: 6) {
                Text("Nước \(finding.id + 1)").font(.caption.weight(.bold))
                Text("\(finding.mover == .black ? LS(localized: "Đen") : LS(localized: "Trắng")) \(text(finding.played))")
                    .font(.caption)
                Spacer()
                Text("−\(Int((finding.drop * 100).rounded()))%")
                    .font(.caption.weight(.bold)).foregroundStyle(.red)
                Text("nên \(text(finding.suggested))")
                    .font(.caption).foregroundStyle(Tokens.inkSecondary)
            }
        }
        .buttonStyle(.plain)
    }

    private func text(_ move: Move) -> String {
        switch move {
        case .play(let point): Coordinate.text(point, size: positions[0].state.size)
        case .pass: LS(localized: "nhường lượt")
        case .resign: LS(localized: "xin thua")
        }
    }
}

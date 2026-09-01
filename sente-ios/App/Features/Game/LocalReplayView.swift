import SwiftUI
import GoKit
import SenteUI

/// Replay for a game that lives only on this device: an imported SGF, or a
/// finished local game. Positions are rebuilt by GoKit, like the online replay.
struct LocalReplayView: View {
    let record: GameRecord
    @Environment(AppSession.self) private var session
    @State private var positions: [GameEngine] = []
    @State private var index = 0
    @State private var analysis: ReplayAnalysis?

    var body: some View {
        VStack(spacing: 12) {
            header
            BoardView(snapshot: snapshot, interactive: false,
                      showsCoordinates: session.settings.showCoordinates,
                      colourBlindSymbols: session.settings.colourBlindSymbols)
            ReplayAnalysisBar(positions: positions, index: index, analysis: $analysis)
            controls
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .background(Tokens.paper.ignoresSafeArea())
        .foregroundStyle(Tokens.ink)
        .navigationTitle("Xem lại")
        .navigationBarTitleDisplayMode(.inline)
        .onAppear { build() }
        .onChange(of: index) { _, _ in analysis = nil }
    }

    private var snapshot: BoardSnapshot {
        guard !positions.isEmpty else { return BoardSnapshot(board: Board(size: record.size)) }
        let engine = positions[index]
        var snapshot = BoardSnapshot(board: engine.board)
        if index > 0, case .play(let point) = record.moves[index - 1].move { snapshot.lastMove = point }
        if let analysis, analysis.forIndex == index, case .play(let point) = analysis.move {
            snapshot.pending = point
        }
        return snapshot
    }

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 1) {
                Text(record.blackPlayer.isEmpty || record.whitePlayer.isEmpty
                     ? LS(localized: "Ván nhập từ SGF")
                     : "\(record.blackPlayer) vs \(record.whitePlayer)")
                    .font(.callout.weight(.semibold))
                Text("\(record.size)×\(record.size) · komi \(record.komi.formatted())")
                    .font(.caption).foregroundStyle(Tokens.inkSecondary)
            }
            Spacer()
        }
        .padding(.horizontal, 6)
    }

    private var controls: some View {
        VStack(spacing: 10) {
            Text("NƯỚC \(index) / \(record.moves.count)")
                .font(.system(.caption, design: .monospaced)).foregroundStyle(Tokens.inkSecondary)
                .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 6)
            Slider(value: Binding(get: { Double(index) }, set: { index = Int($0.rounded()) }),
                   in: 0...Double(max(1, positions.count - 1)), step: 1)
                .disabled(positions.count < 2)
                .tint(Tokens.indigo)
            HStack(spacing: 9) {
                replayStep("backward.end", index > 0) { index = 0 }
                replayStep("chevron.left", index > 0) { index -= 1 }
                replayStep("chevron.right", index < positions.count - 1) { index += 1 }
                replayStep("forward.end", index < positions.count - 1) { index = positions.count - 1 }
            }
        }
    }

    private func replayStep(_ symbol: String, _ enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) { Image(systemName: symbol).frame(maxWidth: .infinity).frame(height: 44) }
            .buttonStyle(SecondaryButton()).disabled(!enabled)
    }

    private func build() {
        var engine = GameEngine.newGame(size: record.size, rules: record.rules,
                                        komi: record.komi, handicap: record.handicap)
        var built = [engine]
        for recorded in record.moves {
            guard let next = try? engine.apply(recorded.move, by: recorded.player) else { break }
            engine = next
            built.append(next)
        }
        positions = built
        index = built.count - 1
    }
}

/// What the bot thinks of one replay position.
struct ReplayAnalysis: Equatable {
    let forIndex: Int
    let move: Move
    let winRate: Double
    let side: Player
}

/// One row: "ask the bot" — MCTS on the shown position, suggestion + win rate.
struct ReplayAnalysisBar: View {
    let positions: [GameEngine]
    let index: Int
    @Binding var analysis: ReplayAnalysis?
    @State private var busy = false

    var body: some View {
        HStack(spacing: 10) {
            if let analysis, analysis.forIndex == index {
                let sideName = analysis.side == .black ? LS(localized: "Đen") : LS(localized: "Trắng")
                let winText = "\(Int((analysis.winRate * 100).rounded()))%"
                if case .play(let point) = analysis.move {
                    Text("Máy đề xuất \(Coordinate.text(point, size: positions[index].state.size)) · \(sideName) thắng khoảng \(winText)")
                        .font(.caption.weight(.semibold))
                } else {
                    Text("Máy sẽ nhường lượt · \(sideName) thắng khoảng \(winText)")
                        .font(.caption.weight(.semibold))
                }
            } else if busy {
                ProgressView().controlSize(.small)
                Text("Máy đang phân tích…").font(.caption).foregroundStyle(Tokens.inkSecondary)
            } else {
                Button {
                    analyze()
                } label: {
                    Label("Máy phân tích thế cờ", systemImage: "brain")
                        .font(.caption.weight(.semibold))
                }
                .disabled(positions.isEmpty || positions[index].state.phase != .playing)
            }
            Spacer()
        }
        .padding(.horizontal, 6)
        .foregroundStyle(Tokens.ink)
    }

    private func analyze() {
        guard !positions.isEmpty else { return }
        let engine = positions[index]
        let at = index
        busy = true
        Task {
            let result = await Task.detached(priority: .userInitiated) { () -> ReplayAnalysis? in
                var bot = MCTSBot(iterations: 1200, deadline: 2.0, seed: .random(in: 0 ... .max))
                guard let verdict = bot.analyze(engine) else { return nil }
                return ReplayAnalysis(forIndex: at, move: verdict.move,
                                      winRate: verdict.winRate, side: engine.toPlay)
            }.value
            busy = false
            if let result, result.forIndex == index { analysis = result }
        }
    }
}

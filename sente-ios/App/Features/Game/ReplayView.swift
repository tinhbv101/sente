import SwiftUI
import GoKit
import SenteNet
import SenteUI

/// Step through a finished game (docs/01 FR-R3). The position at every move is
/// recomputed by GoKit from the move list, so replay costs nothing to store and
/// can never disagree with the engine.
struct ReplayView: View {
    let summary: GameSummary
    @Environment(AppSession.self) private var session
    @State private var moves: GameMoves?
    @State private var positions: [GameEngine] = []
    @State private var index = 0
    @State private var error: String?

    var body: some View {
        VStack(spacing: 12) {
            if let moves, !positions.isEmpty {
                header(moves)
                BoardView(snapshot: snapshot, interactive: false,
                          showsCoordinates: session.settings.showCoordinates,
                          colourBlindSymbols: session.settings.colourBlindSymbols)
                controls(moves)
            } else if let error {
                ContentUnavailableView("Không tải được ván", systemImage: "exclamationmark.triangle", description: Text(error))
            } else {
                ProgressView().frame(maxHeight: .infinity)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .background(Tokens.paper.ignoresSafeArea())
        .foregroundStyle(Tokens.ink)
        .navigationTitle("Xem lại")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                ShareLink(item: session.settings.serverURL.appending(path: "/v1/games/\(summary.gameId)/sgf")) {
                    Image(systemName: "square.and.arrow.up")
                }
            }
        }
        .task { await load() }
    }

    private var snapshot: BoardSnapshot {
        let engine = positions[index]
        var last: Point?
        if index > 0, let move = moves?.items[index - 1], move.kind == "play",
           let text = move.point, let point = Coordinate.point(text, size: engine.board.size) {
            last = point
        }
        return BoardSnapshot(board: engine.board, lastMove: last)
    }

    private func header(_ moves: GameMoves) -> some View {
        HStack {
            VStack(alignment: .leading, spacing: 1) {
                Text(summary.opponentName.isEmpty ? "Người chơi đã xóa" : summary.opponentName)
                    .font(.callout.weight(.semibold))
                Text("\(moves.boardSize)×\(moves.boardSize) · \(moves.rules == "japanese" ? "Nhật" : "Trung") · komi \(moves.komi.formatted())")
                    .font(.caption).foregroundStyle(Tokens.inkSecondary)
            }
            Spacer()
            if let result = summary.result {
                Text(verdict(result)).font(.subheadline.weight(.semibold)).foregroundStyle(Tokens.inkSecondary)
            }
        }
        .padding(.horizontal, 6)
    }

    private func controls(_ moves: GameMoves) -> some View {
        VStack(spacing: 10) {
            HStack {
                Text("NƯỚC \(index) / \(moves.items.count)").font(.system(.caption, design: .monospaced))
                Spacer()
                if index > 0 {
                    let move = moves.items[index - 1]
                    Text("\(move.color == "black" ? "Đen" : "Trắng") \(move.kind == "play" ? "đi \(move.point ?? "")" : move.kind == "pass" ? "nhường lượt" : "xin thua")")
                        .font(.caption.weight(.semibold))
                }
            }
            .foregroundStyle(Tokens.inkSecondary).padding(.horizontal, 6)
            Slider(value: Binding(get: { Double(index) }, set: { index = Int($0.rounded()) }),
                   in: 0...Double(max(1, moves.items.count)), step: 1)
                .disabled(moves.items.isEmpty)
                .tint(Tokens.indigo)
            HStack(spacing: 9) {
                stepButton("backward.end", enabled: index > 0) { index = 0 }
                stepButton("chevron.left", enabled: index > 0) { index -= 1 }
                stepButton("chevron.right", enabled: index < moves.items.count) { index += 1 }
                stepButton("forward.end", enabled: index < moves.items.count) { index = moves.items.count }
            }
        }
    }

    private func stepButton(_ symbol: String, enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) { Image(systemName: symbol).frame(maxWidth: .infinity).frame(height: 44) }
            .buttonStyle(SecondaryButton()).disabled(!enabled)
    }

    private func verdict(_ result: GameResultPayload) -> String {
        guard let winner = result.winner else { return "Vô hiệu" }
        let mine = winner == summary.myColor
        if let score = result.score { return "\(mine ? "Thắng" : "Thua") \(score.margin.formatted())" }
        switch result.reason {
        case "resignation": return mine ? "Thắng (đối thủ xin thua)" : "Thua (xin thua)"
        case "timeout": return mine ? "Thắng (hết giờ)" : "Thua (hết giờ)"
        default: return mine ? "Thắng" : "Thua"
        }
    }

    private func load() async {
        do {
            let fetched = try await session.api.moves(gameID: summary.gameId)
            var engine = GameEngine.newGame(size: fetched.boardSize,
                                            rules: fetched.rules == "chinese" ? .chinese : .japanese,
                                            komi: fetched.komi, handicap: fetched.handicap)
            var built = [engine]
            for item in fetched.items {
                let move: Move
                switch item.kind {
                case "pass": move = .pass
                case "resign": move = .resign
                default:
                    guard let text = item.point, let point = Coordinate.point(text, size: fetched.boardSize) else { continue }
                    move = .play(point)
                }
                guard let next = try? engine.apply(move, by: item.color == "white" ? .white : .black) else { break }
                engine = next
                built.append(next)
            }
            positions = built
            moves = fetched
            index = built.count - 1
        } catch let apiError as APIError { error = apiError.userMessage } catch { self.error = error.localizedDescription }
    }
}

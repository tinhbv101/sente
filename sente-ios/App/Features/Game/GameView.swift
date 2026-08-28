import SwiftUI
import GoKit
import SenteNet
import SenteUI

struct GameView: View {
    let summary: GameSummary
    @Environment(AppSession.self) private var session
    @Environment(\.dismiss) private var dismiss
    @State private var store: GameStore
    @State private var confirmResign = false

    init(summary: GameSummary) {
        self.summary = summary
        _store = State(initialValue: GameStore(gameID: summary.gameId,
                                               myColor: summary.myColor == "white" ? .white : .black))
    }

    var body: some View {
        VStack(spacing: 10) {
            playerRow(for: store.myColor.opponent, name: summary.opponentName)
            board
            moveBar
            playerRow(for: store.myColor, name: session.user?.displayName ?? "bạn")
            controls
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .background(Tokens.paper.ignoresSafeArea())
        .navigationBarTitleDisplayMode(.inline)
        .overlay(alignment: .top) { banner }
        .overlay(alignment: .bottom) { toastView }
        .sheet(isPresented: Binding(get: { store.phase == .finished && store.result != nil }, set: { _ in })) {
            ResultView(store: store, opponentName: summary.opponentName) { dismiss() }
                .presentationDetents([.medium, .large])
                .interactiveDismissDisabled()
        }
        .confirmationDialog("Xin thua ván này?", isPresented: $confirmResign, titleVisibility: .visible) {
            Button("Xin thua", role: .destructive) { store.resign() }
        }
        .alert("Đối thủ xin hoãn một nước", isPresented: Binding(
            get: { store.undoRequestedByOpponent }, set: { _ in })) {
            Button("Đồng ý") { store.answerUndo(true) }
            Button("Từ chối", role: .cancel) { store.answerUndo(false) }
        }
        .task {
            let token = await session.api.token ?? ""
            await store.connect(api: session.api, token: token)
        }
        .onDisappear { Task { await store.disconnect(); await session.refreshQuietly() } }
    }

    private var board: some View {
        BoardView(
            snapshot: store.snapshot,
            ghostPlayer: store.ghostPlayer,
            interactive: store.isMyTurn,
            showsCoordinates: session.settings.showCoordinates,
            colourBlindSymbols: session.settings.colourBlindSymbols,
            legality: { store.legality($0) },
            onPlace: { store.place($0) },
            onTapChain: store.phase == .scoring ? { store.toggleDead($0) } : nil)
        .overlay {
            if store.phase == .loading {
                ProgressView().controlSize(.large).tint(.white)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(.black.opacity(0.25))
            }
        }
        .accessibilityLabel("Bàn cờ")
    }

    private func playerRow(for player: Player, name: String) -> some View {
        let active = store.phase == .playing && store.toPlay == player
        let clock = store.remaining(for: player)
        let prisoners = player == .black ? store.captures.black : store.captures.white
        return HStack(spacing: 11) {
            ZStack {
                Circle().fill(player == .black ? Color(red: 0.11, green: 0.11, blue: 0.13) : Color(red: 0.93, green: 0.91, blue: 0.86))
                    .frame(width: 34, height: 34)
                    .overlay(Circle().stroke(.black.opacity(0.12)))
                Text(String(name.prefix(1)).lowercased())
                    .font(.subheadline.weight(.bold))
                    .foregroundStyle(player == .black ? .white : Tokens.ink)
            }
            VStack(alignment: .leading, spacing: 1) {
                Text(name).font(.callout.weight(.semibold))
                Text(active ? "đang tới lượt" : (player == .black ? "Đen" : "Trắng"))
                    .font(.caption).foregroundStyle(active ? Tokens.seal : Tokens.inkSecondary)
            }
            Spacer()
            HStack(spacing: 5) {
                Circle().fill(player == .black ? .white : .black).frame(width: 12, height: 12)
                    .overlay(Circle().stroke(.black.opacity(0.2)))
                Text("\(prisoners)").font(.subheadline.weight(.semibold)).foregroundStyle(Tokens.inkSecondary)
            }
            .accessibilityLabel("\(prisoners) tù binh")
            ClockLabel(deadline: clock.deadline, frozenMs: clock.frozenMs, periodsLeft: clock.periods,
                       active: active, offset: store.clockOffset)
        }
        .padding(.horizontal, 14).padding(.vertical, 10)
        .background(active ? Tokens.seal.opacity(0.10) : .clear, in: RoundedRectangle(cornerRadius: 14))
        .overlay(alignment: .leading) {
            if active { RoundedRectangle(cornerRadius: 2).fill(Tokens.seal).frame(width: 3).padding(.vertical, 12) }
        }
    }

    private var moveBar: some View {
        HStack {
            Text("NƯỚC \(store.moveNumber)").font(.system(.caption, design: .monospaced))
            Spacer()
            if let last = store.lastMove {
                Text("\(store.toPlay == .black ? "Trắng" : "Đen") đi \(Coordinate.text(last, size: store.boardSize))")
                    .font(.caption.weight(.semibold))
            }
            Spacer()
            Text(store.rules == .japanese ? "Nhật · komi \(store.komi.formatted())" : "Trung · komi \(store.komi.formatted())")
                .font(.caption).foregroundStyle(Tokens.inkSecondary)
        }
        .foregroundStyle(Tokens.inkSecondary)
        .padding(.horizontal, 8)
    }

    @ViewBuilder
    private var controls: some View {
        switch store.phase {
        case .scoring:
            ScoringControls(store: store)
        case .finished, .loading:
            EmptyView()
        case .playing:
            HStack(spacing: 9) {
                Button("Nhường lượt") { store.pass() }
                    .buttonStyle(SecondaryButton()).disabled(!store.isMyTurn)
                Menu {
                    Button("Xin hoãn một nước") { store.requestUndo() }
                    Button("Xin thua", role: .destructive) { confirmResign = true }
                } label: {
                    Image(systemName: "ellipsis").frame(width: 56, height: 50)
                }
                .buttonStyle(SecondaryButton())
            }
        }
    }

    @ViewBuilder
    private var banner: some View {
        switch store.connection {
        case .reconnecting where (store.reconnectingSince.map { Date().timeIntervalSince($0) } ?? 0) > 30:
            ConnectionBanner(kind: .offline).padding(.top, 6)
        case .reconnecting where (store.reconnectingSince.map { Date().timeIntervalSince($0) } ?? 0) > 3:
            ConnectionBanner(kind: .reconnecting).padding(.top, 6)
        case .connecting:
            ConnectionBanner(kind: .syncing).padding(.top, 6)
        default:
            EmptyView()
        }
    }

    @ViewBuilder
    private var toastView: some View {
        if let toast = store.toast {
            Text(toast)
                .font(.footnote.weight(.medium))
                .padding(.horizontal, 14).padding(.vertical, 10)
                .background(Tokens.ink.opacity(0.9), in: Capsule())
                .foregroundStyle(.white)
                .padding(.bottom, 8)
                .onTapGesture { store.dismissToast() }
                .task { try? await Task.sleep(for: .seconds(3)); store.dismissToast() }
        }
    }
}

struct ScoringControls: View {
    @Bindable var store: GameStore

    var body: some View {
        VStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Đánh dấu quân chết").font(.subheadline.weight(.semibold))
                Text("Chạm vào một đám quân để bật hoặc tắt. Cả hai cùng đồng ý thì ván kết thúc.")
                    .font(.caption).foregroundStyle(Tokens.inkSecondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12).background(Tokens.indigoSoft, in: RoundedRectangle(cornerRadius: 12))

            if let score = store.score {
                HStack {
                    Text("Đen ").foregroundStyle(Tokens.inkSecondary) + Text(score.black.formatted()).font(.system(.body, design: .monospaced).weight(.medium))
                    Spacer()
                    Text("Trắng ").foregroundStyle(Tokens.inkSecondary) + Text(score.white.formatted()).font(.system(.body, design: .monospaced).weight(.medium))
                }.font(.footnote)
            }
            HStack(spacing: 8) {
                Label(store.opponentAccepted ? "Đối thủ đã đồng ý" : "Đối thủ đang xem",
                      systemImage: store.opponentAccepted ? "checkmark" : "clock")
                    .font(.caption).foregroundStyle(store.opponentAccepted ? .green : Tokens.inkTertiary)
                Spacer()
            }
            HStack(spacing: 9) {
                Button("Chơi tiếp") { store.resumePlay() }.buttonStyle(SecondaryButton())
                Button(store.iAccepted ? "Rút lại" : "Đồng ý kết quả") { store.accept(!store.iAccepted) }
                    .buttonStyle(PrimaryButton())
            }
        }
    }
}

struct ResultView: View {
    let store: GameStore
    let opponentName: String
    let onClose: () -> Void

    var body: some View {
        VStack(spacing: 14) {
            Text(reasonLabel).font(.caption.weight(.bold)).textCase(.uppercase)
                .padding(.horizontal, 11).padding(.vertical, 5)
                .background(Tokens.paper, in: Capsule()).foregroundStyle(Tokens.inkSecondary)
            Text(title).font(.system(size: 36, weight: .regular, design: .serif))
            if let score = store.result?.score {
                Text("\(score.margin.formatted()) điểm").font(.system(.body, design: .monospaced)).foregroundStyle(Tokens.inkSecondary)
                Grid(alignment: .trailing, horizontalSpacing: 24, verticalSpacing: 8) {
                    GridRow { Text(""); Text("Đen").font(.caption.weight(.bold)); Text("Trắng").font(.caption.weight(.bold)) }
                    GridRow { Text("Tổng").gridColumnAlignment(.leading).foregroundStyle(Tokens.inkSecondary)
                        Text(score.black.formatted()).monospacedDigit(); Text(score.white.formatted()).monospacedDigit() }
                }.font(.subheadline).padding(.top, 6)
            }
            Spacer(minLength: 8)
            Button("Đóng") { onClose() }.buttonStyle(PrimaryButton())
        }
        .padding(24)
    }

    private var title: String {
        guard let result = store.result else { return "" }
        guard let winner = result.winner else { return "Ván vô hiệu" }
        return winner == "black" ? "Đen thắng" : "Trắng thắng"
    }

    private var reasonLabel: String {
        switch store.result?.reason {
        case "counting": "Đếm điểm · nước \(store.moveNumber)"
        case "resignation": "Xin thua"
        case "timeout": "Hết giờ"
        case "repetition": "Lặp thế cờ"
        default: store.result?.reason ?? ""
        }
    }
}

struct PrimaryButton: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.semibold))
            .frame(maxWidth: .infinity).frame(height: 50)
            .background(Tokens.indigo.opacity(configuration.isPressed ? 0.8 : 1), in: RoundedRectangle(cornerRadius: 15))
            .foregroundStyle(.white)
    }
}

struct SecondaryButton: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.semibold))
            .frame(maxWidth: .infinity).frame(height: 50)
            .background(RoundedRectangle(cornerRadius: 15).strokeBorder(Tokens.inkTertiary.opacity(0.5), lineWidth: 1.5))
            .foregroundStyle(Tokens.indigo)
            .opacity(configuration.isPressed ? 0.7 : 1)
    }
}

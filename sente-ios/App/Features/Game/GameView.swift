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
    @State private var showReport = false
    @State private var moderationNote: String?

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
            playerRow(for: store.myColor, name: session.user?.displayName ?? LS(localized: "bạn"))
            controls
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .background(Tokens.paper.ignoresSafeArea())
        .navigationBarTitleDisplayMode(.inline)
        // Pushes about the game on screen are held back (PushRegistrar).
        .onAppear { PushRegistrar.visibleGameID = summary.gameId }
        .overlay(alignment: .top) { banner }
        .overlay(alignment: .top) { chatBubble }
        .overlay(alignment: .bottom) { toastView }
        .onChange(of: store.moveNumber) { old, new in if new > old { Feedback.stone() } }
        .onChange(of: store.captures.black + store.captures.white) { old, new in if new > old { Feedback.capture() } }
        .onChange(of: store.incomingChat?.at) { _, new in if new != nil { Feedback.chat() } }
        .onChange(of: store.phase) { _, new in if new == .finished { Feedback.gameEnd() } }
        .sheet(isPresented: Binding(get: { store.phase == .finished && store.result != nil }, set: { _ in })) {
            ResultView(store: store, opponentName: summary.opponentName,
                       rematch: summary.opponentId == nil ? nil : { try await session.api.rematch(gameID: summary.gameId) }) { dismiss() }
                .presentationDetents([.medium, .large])
                .interactiveDismissDisabled()
        }
        .confirmationDialog("Xin thua ván này?", isPresented: $confirmResign, titleVisibility: .visible) {
            Button("Xin thua", role: .destructive) { store.resign() }
        }
        .confirmationDialog("Báo cáo \(summary.opponentName)", isPresented: $showReport, titleVisibility: .visible) {
            Button("Quấy rối / lời lẽ xúc phạm") { Task { await report("abuse") } }
            Button("Nghi dùng AI") { Task { await report("cheating") } }
            Button("Bỏ ván giữa chừng") { Task { await report("escaping") } }
            Button("Tên hiển thị không phù hợp") { Task { await report("name") } }
        } message: {
            Text("Báo cáo được người thật xem xét trong 24 giờ. Ván đang chơi không bị ảnh hưởng.")
        }
        .alert("Đã ghi nhận", isPresented: Binding(get: { moderationNote != nil }, set: { _ in moderationNote = nil })) {
            Button("Đóng") {}
        } message: { Text(moderationNote ?? "") }
        .alert("Đối thủ xin hoãn một nước", isPresented: Binding(
            get: { store.undoRequestedByOpponent }, set: { _ in })) {
            Button("Đồng ý") { store.answerUndo(true) }
            Button("Từ chối", role: .cancel) { store.answerUndo(false) }
        }
        .task {
            let token = await session.api.token ?? ""
            await store.connect(api: session.api, token: token)
        }
        .onDisappear {
            if PushRegistrar.visibleGameID == summary.gameId { PushRegistrar.visibleGameID = nil }
            Task { await store.disconnect(); await session.refreshQuietly() }
        }
    }

    private func report(_ category: String) async {
        guard let opponent = summary.opponentId else { return }
        do {
            try await session.api.report(userID: opponent, gameID: summary.gameId, category: category, note: "")
            moderationNote = LS(localized: "Cảm ơn bạn. Báo cáo đã được gửi.")
        } catch let error as APIError { moderationNote = error.userMessage } catch { moderationNote = error.localizedDescription }
    }

    private func block() async {
        guard let opponent = summary.opponentId else { return }
        do {
            try await session.api.block(userID: opponent)
            moderationNote = LS(localized: "Đã chặn \(summary.opponentName). Người này không thể mời bạn nữa; ván này vẫn tiếp tục.")
        } catch let error as APIError { moderationNote = error.userMessage } catch { moderationNote = error.localizedDescription }
    }

    private var board: some View {
        BoardView(
            snapshot: store.snapshot,
            ghostPlayer: store.ghostPlayer,
            interactive: store.isMyTurn,
            showsCoordinates: session.settings.showCoordinates,
            colourBlindSymbols: session.settings.colourBlindSymbols,
            fingerOffset: session.settings.offsetPlacement ? BoardView.defaultFingerOffset : 0,
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
                    .foregroundStyle(player == .black ? .white : Color(red: 0.11, green: 0.11, blue: 0.13))
            }
            VStack(alignment: .leading, spacing: 1) {
                Text(name).font(.callout.weight(.semibold))
                Text(active ? LS(localized: "đang tới lượt") : (player == .black ? LS(localized: "Đen") : LS(localized: "Trắng")))
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
        .background(active ? Tokens.sealSoft : .clear, in: RoundedRectangle(cornerRadius: 14))
        .overlay(alignment: .leading) {
            if active { RoundedRectangle(cornerRadius: 2).fill(Tokens.seal).frame(width: 3).padding(.vertical, 12) }
        }
    }

    private var moveBar: some View {
        HStack {
            Text("NƯỚC \(store.moveNumber)").font(.system(.caption, design: .monospaced))
            Spacer()
            if let last = store.lastMove {
                Text("\(store.toPlay == .black ? LS(localized: "Trắng") : LS(localized: "Đen")) đi \(Coordinate.text(last, size: store.boardSize))")
                    .font(.caption.weight(.semibold))
            }
            Spacer()
            Text(store.rules == .japanese ? LS(localized: "Nhật · komi \(store.komi.formatted())") : LS(localized: "Trung · komi \(store.komi.formatted())"))
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
                    ForEach(QuickChat.codes, id: \.self) { code in
                        Button(QuickChat.text(code)) { store.sendChat(code) }
                    }
                } label: {
                    Image(systemName: "bubble.left").frame(width: 56, height: 50)
                }
                .buttonStyle(SecondaryButton())
                .accessibilityLabel("Tin nhắn nhanh")
                Menu {
                    Button("Xin hoãn một nước") { store.requestUndo() }
                    if summary.opponentId != nil {
                        Divider()
                        Button("Báo cáo đối thủ") { showReport = true }
                        Button("Chặn đối thủ") { Task { await block() } }
                    }
                    Divider()
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

    /// A quick-chat line floats in briefly and removes itself.
    @ViewBuilder
    private var chatBubble: some View {
        if let chat = store.incomingChat {
            let name = chat.by == store.myColor ? LS(localized: "Bạn") : summary.opponentName
            Text("\(name): \(QuickChat.text(chat.code))")
                .font(.footnote.weight(.semibold))
                .padding(.horizontal, 13).padding(.vertical, 8)
                .background(Tokens.indigoSoft, in: Capsule())
                .foregroundStyle(Tokens.ink)
                .padding(.top, 44)
                .id(chat.at)
                .task { try? await Task.sleep(for: .seconds(3)); store.dismissChat() }
        }
    }

    @ViewBuilder
    private var toastView: some View {
        if let toast = store.toast {
            Text(toast)
                .font(.footnote.weight(.medium))
                .padding(.horizontal, 14).padding(.vertical, 10)
                .background(Tokens.ink.opacity(0.9), in: Capsule())
                .foregroundStyle(Tokens.paper)
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
            .foregroundStyle(Tokens.ink)

            if let detail = store.scoreDetail {
                ScoreBreakdownView(score: detail, blackName: LS(localized: "Đen"), whiteName: LS(localized: "Trắng"))
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else if let score = store.score {
                HStack {
                    Text("Đen ").foregroundStyle(Tokens.inkSecondary) + Text(score.black.formatted()).font(.system(.body, design: .monospaced).weight(.medium))
                    Spacer()
                    Text("Trắng ").foregroundStyle(Tokens.inkSecondary) + Text(score.white.formatted()).font(.system(.body, design: .monospaced).weight(.medium))
                }.font(.footnote)
            }
            HStack(spacing: 8) {
                Label(store.opponentAccepted ? LS(localized: "Đối thủ đã đồng ý") : LS(localized: "Đối thủ đang xem"),
                      systemImage: store.opponentAccepted ? "checkmark" : "clock")
                    .font(.caption).foregroundStyle(store.opponentAccepted ? .green : Tokens.inkTertiary)
                Spacer()
            }
            HStack(spacing: 9) {
                Button("Chơi tiếp") { store.resumePlay() }.buttonStyle(SecondaryButton())
                Button(store.iAccepted ? LS(localized: "Rút lại") : LS(localized: "Đồng ý kết quả")) { store.accept(!store.iAccepted) }
                    .buttonStyle(PrimaryButton())
            }
        }
    }
}

struct ResultView: View {
    let store: GameStore
    let opponentName: String
    var rematch: (() async throws -> Challenge)? = nil
    let onClose: () -> Void
    @State private var rematchSent = false
    @State private var rematchError: String?

    var body: some View {
        VStack(spacing: 14) {
            Text(reasonLabel).font(.caption.weight(.bold)).textCase(.uppercase)
                .padding(.horizontal, 11).padding(.vertical, 5)
                .background(Tokens.sheetSecondary, in: Capsule()).foregroundStyle(Tokens.inkSecondary)
            Text(title).font(.system(size: 36, weight: .regular, design: .serif))
            if let score = store.result?.score {
                Text("\(score.margin.formatted()) điểm").font(.system(.body, design: .monospaced)).foregroundStyle(Tokens.inkSecondary)
                if let detail = store.scoreDetail {
                    ScoreBreakdownView(score: detail, blackName: LS(localized: "Đen"), whiteName: LS(localized: "Trắng"))
                        .padding(.top, 6)
                } else {
                    Grid(alignment: .trailing, horizontalSpacing: 24, verticalSpacing: 8) {
                        GridRow { Text(""); Text("Đen").font(.caption.weight(.bold)); Text("Trắng").font(.caption.weight(.bold)) }
                        GridRow { Text("Tổng").gridColumnAlignment(.leading).foregroundStyle(Tokens.inkSecondary)
                            Text(score.black.formatted()).monospacedDigit(); Text(score.white.formatted()).monospacedDigit() }
                    }.font(.subheadline).padding(.top, 6)
                }
            }
            Spacer(minLength: 8)
            if let rematch {
                if rematchSent {
                    Label("Đã gửi lời mời đấu lại", systemImage: "checkmark")
                        .font(.callout.weight(.semibold)).foregroundStyle(.green)
                } else {
                    Button("Mời đấu lại") {
                        Task {
                            do { _ = try await rematch(); rematchSent = true }
                            catch let error as APIError { rematchError = error.userMessage }
                            catch { rematchError = error.localizedDescription }
                        }
                    }
                    .buttonStyle(SecondaryButton())
                }
                if let rematchError { Text(rematchError).font(.footnote).foregroundStyle(.red) }
            }
            Button("Đóng") { onClose() }.buttonStyle(PrimaryButton())
        }
        .padding(24)
        .foregroundStyle(Tokens.ink)
        .presentationBackground(Tokens.sheet)
    }

    private var title: String {
        guard let result = store.result else { return "" }
        guard let winner = result.winner else { return LS(localized: "Ván vô hiệu") }
        return winner == "black" ? LS(localized: "Đen thắng") : LS(localized: "Trắng thắng")
    }

    private var reasonLabel: String {
        switch store.result?.reason {
        case "counting": LS(localized: "Đếm điểm · nước \(store.moveNumber)")
        case "resignation": LS(localized: "Xin thua")
        case "timeout": LS(localized: "Hết giờ")
        case "repetition": LS(localized: "Lặp thế cờ")
        default: store.result?.reason ?? ""
        }
    }
}

/// The canned quick-chat vocabulary; codes match the server whitelist.
enum QuickChat {
    static let codes = ["hi", "gl", "good_move", "oops", "thanks", "gg"]
    static func text(_ code: String) -> String {
        switch code {
        case "hi": LS(localized: "👋 Chào bạn")
        case "gl": LS(localized: "🍀 Chúc may mắn")
        case "good_move": LS(localized: "👍 Nước hay!")
        case "oops": LS(localized: "😅 Nhầm tay rồi")
        case "thanks": LS(localized: "🙏 Cảm ơn ván đấu")
        case "gg": LS(localized: "🤝 Ván hay quá")
        default: code
        }
    }
}

struct PrimaryButton: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.semibold))
            .frame(maxWidth: .infinity).frame(height: 50)
            .background(Tokens.indigo.opacity(configuration.isPressed ? 0.8 : 1), in: RoundedRectangle(cornerRadius: 15))
            .foregroundStyle(Tokens.onIndigo)
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

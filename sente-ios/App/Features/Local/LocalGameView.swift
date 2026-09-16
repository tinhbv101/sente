import SwiftUI
import GoKit
import SenteUI

/// Pass-and-play: the phone is handed back and forth. No account, no network.
/// Opens on the saved game if there is one, otherwise on the setup form.
struct LocalGameView: View {
    @Environment(AppSession.self) private var session
    @State private var store: LocalGameStore?
    @State private var confirmResign = false
    @State private var confirmNew = false

    var body: some View {
        Group {
            if let store {
                LocalBoardView(store: store, confirmResign: $confirmResign, confirmNew: $confirmNew)
            } else {
                LocalSetupView { config in store = LocalGameStore(config: config) }
            }
        }
        .navigationTitle(store == nil ? LS(localized: "Chơi trên máy này") : "")
        .hidesSenteTabBar()
        .navigationBarTitleDisplayMode(.inline)
        .onAppear {
            if store == nil, let record = FileLocalGameStorage().load(), !record.moves.isEmpty {
                store = LocalGameStore(record: record)
            }
        }
        .confirmationDialog("\(store.map { $0.name(of: $0.toPlay) } ?? "") xin thua?", isPresented: $confirmResign, titleVisibility: .visible) {
            Button("Xin thua", role: .destructive) { store?.resign() }
        }
        .confirmationDialog("Bỏ ván này và bắt đầu ván mới?", isPresented: $confirmNew, titleVisibility: .visible) {
            Button("Ván mới", role: .destructive) { FileLocalGameStorage().clear(); store = nil }
        }
    }
}

struct LocalSetupView: View {
    let onStart: (LocalGameConfig) -> Void
    @State private var config = LocalGameConfig()

    var body: some View {
        Form {
            Section("Bàn cờ") {
                Picker("Cỡ bàn", selection: $config.size) {
                    ForEach([9, 13, 19], id: \.self) { Text("\($0)×\($0)").tag($0) }
                }
                .pickerStyle(.segmented)
                Picker("Hệ luật", selection: $config.rules) {
                    Text("Nhật Bản").tag(RuleSet.japanese); Text("Trung Quốc").tag(RuleSet.chinese)
                }
                Picker("Chấp quân", selection: $config.handicap) {
                    Text("Không").tag(0)
                    ForEach(2...9, id: \.self) { Text("\($0) quân").tag($0) }
                }
                LabeledContent("Komi", value: config.komi.formatted())
            }
            Section {
                TextField("Đen", text: $config.blackName)
                TextField("Trắng", text: $config.whiteName)
            } header: { Text("Người chơi") } footer: {
                Text("Không cần tài khoản hay mạng. Ván đang chơi được giữ lại khi đóng app.")
            }
        }
        .scrollContentBackground(.hidden)
        .background(Tokens.paper.ignoresSafeArea())
        .safeAreaInset(edge: .bottom) {
            Button("Bắt đầu") {
                var final = config
                if final.blackName.trimmingCharacters(in: .whitespaces).isEmpty { final.blackName = LS(localized: "Đen") }
                if final.whiteName.trimmingCharacters(in: .whitespaces).isEmpty { final.whiteName = LS(localized: "Trắng") }
                onStart(final)
            }
            .buttonStyle(PrimaryButton()).padding(16)
        }
    }
}

struct LocalBoardView: View {
    @Environment(AppSession.self) private var session
    let store: LocalGameStore
    @Binding var confirmResign: Bool
    @Binding var confirmNew: Bool

    var body: some View {
        VStack(spacing: 10) {
            playerRow(.white)
            BoardView(
                snapshot: store.snapshot,
                ghostPlayer: store.toPlay == .black ? .black : .white,
                interactive: store.isHumanTurn,
                showsCoordinates: session.settings.showCoordinates,
                colourBlindSymbols: session.settings.colourBlindSymbols,
                fingerOffset: session.settings.offsetPlacement ? BoardView.defaultFingerOffset : 0,
                legality: { store.legality($0) },
                onPlace: { store.place($0) },
                onTapChain: store.phase == .scoring ? { store.toggleDead($0) } : nil)
            .accessibilityLabel("Bàn cờ")
            moveBar
            if store.thinking {
                Label("Máy đang nghĩ…", systemImage: "hourglass")
                    .font(.caption.weight(.medium)).foregroundStyle(Tokens.inkSecondary)
            }
            playerRow(.black)
            controls
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .background(Tokens.paper.ignoresSafeArea())
        .overlay(alignment: .bottom) { toastView }
        .onChange(of: store.moveNumber) { old, new in if new > old { Feedback.stone() } }
        .onChange(of: store.captures.black + store.captures.white) { old, new in if new > old { Feedback.capture() } }
        .onChange(of: store.phase) { _, new in if new == .finished { Feedback.gameEnd() } }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                ShareLink(item: sgfFile, preview: SharePreview("Ván cờ \(store.config.size)×\(store.config.size)")) {
                    Image(systemName: "square.and.arrow.up")
                }
                .accessibilityLabel("Chia sẻ SGF")
            }
        }
    }

    /// SGF goes out as a file so other Go apps can open it directly.
    private var sgfFile: URL {
        let url = FileManager.default.temporaryDirectory.appending(path: "sente-local.sgf")
        try? store.sgf.write(to: url, atomically: true, encoding: .utf8)
        return url
    }

    private func playerRow(_ player: Player) -> some View {
        let active = store.phase == .playing && store.toPlay == player
        let prisoners = player == .black ? store.captures.black : store.captures.white
        let name = store.name(of: player)
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
                Text("\(store.toPlay == .black ? LS(localized: "Trắng") : LS(localized: "Đen")) đi \(Coordinate.text(last, size: store.config.size))")
                    .font(.caption.weight(.semibold))
            }
            Spacer()
            Text("\(store.config.rules == .japanese ? LS(localized: "Nhật") : LS(localized: "Trung")) · komi \(store.config.komi.formatted())")
                .font(.caption)
        }
        .foregroundStyle(Tokens.inkSecondary)
        .padding(.horizontal, 8)
    }

    @ViewBuilder private var controls: some View {
        switch store.phase {
        case .playing:
            HStack(spacing: 9) {
                Button("Nhường lượt") { store.pass() }.buttonStyle(SecondaryButton())
                    .disabled(!store.isHumanTurn)
                Menu {
                    Button("Đi lại một nước") { store.undo() }.disabled(store.moves.isEmpty)
                    Button("\(store.name(of: store.toPlay)) xin thua", role: .destructive) { confirmResign = true }
                    Divider()
                    Button("Ván mới", role: .destructive) { confirmNew = true }
                } label: {
                    Image(systemName: "ellipsis").frame(width: 56, height: 50)
                }
                .buttonStyle(SecondaryButton())
            }
        case .scoring:
            scoringControls
        case .finished:
            resultCard
        }
    }

    private var scoringControls: some View {
        VStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Đánh dấu quân chết").font(.subheadline.weight(.semibold))
                Text("Máy đã đánh dấu sẵn những đám chắc chắn chết — chạm vào một đám quân để sửa, rồi bấm Đếm điểm.")
                    .font(.caption).foregroundStyle(Tokens.inkSecondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12).background(Tokens.indigoSoft, in: RoundedRectangle(cornerRadius: 12))
            .foregroundStyle(Tokens.ink)
            if let score = store.score {
                ScoreBreakdownView(score: score, blackName: store.config.blackName,
                                   whiteName: store.config.whiteName)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            HStack(spacing: 9) {
                Button("Chơi tiếp") { store.resumePlay() }.buttonStyle(SecondaryButton())
                Button("Đếm điểm") { store.finishCounting() }.buttonStyle(PrimaryButton())
            }
        }
    }

    private var resultCard: some View {
        VStack(spacing: 10) {
            Text(reasonLabel).font(.caption.weight(.bold)).textCase(.uppercase)
                .padding(.horizontal, 11).padding(.vertical, 5)
                .background(Tokens.sheetSecondary, in: Capsule()).foregroundStyle(Tokens.inkSecondary)
            Text(title).font(.system(size: 30, weight: .regular, design: .serif))
            if let score = store.finalScore {
                ScoreBreakdownView(score: score, blackName: store.config.blackName,
                                   whiteName: store.config.whiteName)
            }
            HStack(spacing: 9) {
                ShareLink(item: sgfFile, preview: SharePreview("Ván cờ \(store.config.size)×\(store.config.size)")) {
                    Text("Chia sẻ SGF")
                }.buttonStyle(SecondaryButton())
                Button("Ván mới") { confirmNew = true }.buttonStyle(PrimaryButton())
            }
        }
        .padding(.top, 6)
        .foregroundStyle(Tokens.ink)
    }

    private var title: String {
        guard let result = store.result else { return "" }
        guard let winner = result.winner else { return LS(localized: "Ván vô hiệu") }
        return LS(localized: "\(store.name(of: winner)) thắng")
    }

    private var reasonLabel: String {
        switch store.result?.reason {
        case .counting: LS(localized: "Đếm điểm · nước \(store.moveNumber)")
        case .resignation: LS(localized: "Xin thua")
        case .repetition: LS(localized: "Lặp thế cờ")
        default: ""
        }
    }

    @ViewBuilder private var toastView: some View {
        if let toast = store.toast {
            Text(toast)
                .font(.footnote.weight(.medium))
                .padding(.horizontal, 14).padding(.vertical, 10)
                .background(Tokens.ink.opacity(0.9), in: Capsule())
                .foregroundStyle(Tokens.paper)
                .padding(.bottom, 8)
                .onTapGesture { store.dismissToast() }
                .task(id: toast) { try? await Task.sleep(for: .seconds(3)); store.dismissToast() }
        }
    }
}

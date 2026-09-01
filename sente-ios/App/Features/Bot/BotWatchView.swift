import SwiftUI
import GoKit
import SenteUI

/// Two bots play each other; the person just watches, or pauses to look.
struct BotWatchView: View {
    @Environment(AppSession.self) private var session
    @State private var store: LocalGameStore?
    @State private var blackLevel: BotLevel = .greedy
    @State private var whiteLevel: BotLevel = .thoughtful

    var body: some View {
        Group {
            if let store {
                match(store)
            } else {
                setup
            }
        }
        .navigationTitle(store == nil ? LS(localized: "Máy đấu máy") : "")
        .navigationBarTitleDisplayMode(.inline)
        // `-autoWatch 1` starts a default match on launch, for screenshots.
        .onAppear {
            if UserDefaults.standard.bool(forKey: "autoWatch"), store == nil {
                var config = LocalGameConfig(size: 9)
                config.blackBot = blackLevel.rawValue
                config.whiteBot = whiteLevel.rawValue
                config.blackName = LS(localized: "Máy") + " · " + blackLevel.title
                config.whiteName = LS(localized: "Máy") + " · " + whiteLevel.title
                store = LocalGameStore(config: config, storage: NullGameStorage())
            }
        }
    }

    @State private var size = 9

    private var setup: some View {
        Form {
            Section("Bàn cờ") {
                Picker("Cỡ bàn", selection: $size) {
                    ForEach([9, 13, 19], id: \.self) { Text("\($0)×\($0)").tag($0) }
                }
                .pickerStyle(.segmented)
            }
            Section {
                Picker("Đen", selection: $blackLevel) {
                    ForEach(BotLevel.allCases) { Text($0.title).tag($0) }
                }
                Picker("Trắng", selection: $whiteLevel) {
                    ForEach(BotLevel.allCases) { Text($0.title).tag($0) }
                }
            } header: { Text("Cấp độ từng máy") } footer: {
                Text("Xem hai cấp độ chơi với nhau là cách nhanh để thấy chúng khác gì nhau.")
            }
        }
        .scrollContentBackground(.hidden)
        .background(Tokens.paper.ignoresSafeArea())
        .safeAreaInset(edge: .bottom) {
            Button("Bắt đầu") {
                var config = LocalGameConfig(size: size)
                config.blackBot = blackLevel.rawValue
                config.whiteBot = whiteLevel.rawValue
                config.blackName = LS(localized: "Máy") + " · " + blackLevel.title
                config.whiteName = LS(localized: "Máy") + " · " + whiteLevel.title
                store = LocalGameStore(config: config, storage: NullGameStorage())
            }
            .buttonStyle(PrimaryButton()).padding(16)
        }
    }

    private func match(_ store: LocalGameStore) -> some View {
        VStack(spacing: 10) {
            row(store, .white)
            BoardView(
                snapshot: store.snapshot,
                interactive: false,
                showsCoordinates: session.settings.showCoordinates,
                colourBlindSymbols: session.settings.colourBlindSymbols,
                legality: { _ in nil },
                onPlace: { _ in })
            .accessibilityLabel("Bàn cờ")
            row(store, .black)
            HStack {
                Text("NƯỚC \(store.moveNumber)").font(.system(.caption, design: .monospaced))
                Spacer()
                if store.phase == .finished, let result = store.result {
                    Text(result.winner.map { "\(store.name(of: $0)) thắng" } ?? LS(localized: "Ván vô hiệu"))
                        .font(.caption.weight(.semibold))
                }
            }
            .foregroundStyle(Tokens.inkSecondary).padding(.horizontal, 8)
            controls(store)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .background(Tokens.paper.ignoresSafeArea())
    }

    private func row(_ store: LocalGameStore, _ player: Player) -> some View {
        let active = store.phase == .playing && store.toPlay == player
        let prisoners = player == .black ? store.captures.black : store.captures.white
        return HStack(spacing: 10) {
            Circle().fill(player == .black ? Color.black : Color.white)
                .frame(width: 14, height: 14).overlay(Circle().stroke(.black.opacity(0.2)))
            Text(store.name(of: player)).font(.callout.weight(.semibold))
            if active && store.thinking { ProgressView().controlSize(.small) }
            Spacer()
            Text("\(prisoners)").font(.subheadline.weight(.semibold)).foregroundStyle(Tokens.inkSecondary)
                .accessibilityLabel("\(prisoners) tù binh")
        }
        .padding(.horizontal, 14).padding(.vertical, 9)
        .background(active ? Tokens.sealSoft : .clear, in: RoundedRectangle(cornerRadius: 12))
        .foregroundStyle(Tokens.ink)
    }

    @ViewBuilder private func controls(_ store: LocalGameStore) -> some View {
        if store.phase == .finished, let score = store.finalScore {
            VStack(spacing: 8) {
                Text("\(store.config.blackName) \(score.black.formatted()) · \(store.config.whiteName) \(score.white.formatted())")
                    .font(.system(.footnote, design: .monospaced)).foregroundStyle(Tokens.inkSecondary)
                Button("Ván mới") { self.store = nil }.buttonStyle(PrimaryButton())
            }
        } else {
            HStack(spacing: 9) {
                Button {
                    store.paused.toggle()
                } label: {
                    Label(store.paused ? LS(localized: "Chạy tiếp") : LS(localized: "Tạm dừng"),
                          systemImage: store.paused ? "play.fill" : "pause.fill")
                }
                .buttonStyle(PrimaryButton())
                Button("Ván mới") { self.store = nil }.buttonStyle(SecondaryButton())
            }
        }
    }
}

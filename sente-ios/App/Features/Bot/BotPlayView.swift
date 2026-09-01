import SwiftUI
import GoKit
import SenteUI

/// A person against the bot. Its own save file, so it does not clobber the
/// pass-and-play game.
struct BotPlayView: View {
    @State private var store: LocalGameStore?
    @State private var confirmResign = false
    @State private var confirmNew = false

    private static let storage = FileLocalGameStorage(name: "bot-game")

    var body: some View {
        Group {
            if let store {
                LocalBoardView(store: store, confirmResign: $confirmResign, confirmNew: $confirmNew)
                    .onChange(of: store.phase) { _, phase in
                        guard phase == .finished, let winner = store.result?.winner,
                              store.config.botLevel(for: winner) == nil,
                              let botSide = [Player.black, .white].first(where: { store.config.botLevel(for: $0) != nil }),
                              let level = store.config.botLevel(for: botSide) else { return }
                        BotLadder.recordWin(over: level)
                    }
            } else {
                BotSetupView { config in store = LocalGameStore(config: config, storage: Self.storage) }
            }
        }
        .navigationTitle(store == nil ? LS(localized: "Đấu với máy") : "")
        .navigationBarTitleDisplayMode(.inline)
        .onAppear {
            if store == nil, let record = Self.storage.load(), !record.moves.isEmpty {
                store = LocalGameStore(record: record, storage: Self.storage)
            }
        }
        .confirmationDialog("Xin thua ván này?", isPresented: $confirmResign, titleVisibility: .visible) {
            Button("Xin thua", role: .destructive) { store?.resign() }
        }
        .confirmationDialog("Bỏ ván này và bắt đầu ván mới?", isPresented: $confirmNew, titleVisibility: .visible) {
            Button("Ván mới", role: .destructive) { Self.storage.clear(); store = nil }
        }
    }
}

struct BotSetupView: View {
    let onStart: (LocalGameConfig) -> Void
    @State private var size = 9
    @State private var level: BotLevel = .greedy
    @State private var myColor: Player = .black
    @State private var rules: RuleSet = .japanese
    @State private var handicap = 0

    private func label(for option: BotLevel) -> String {
        if BotLadder.highestBeaten() >= option.rawValue { return option.title + " ✓" }
        if !BotLadder.isUnlocked(option) { return option.title + " 🔒" }
        return option.title
    }

    var body: some View {
        Form {
            Section("Bàn cờ") {
                Picker("Cỡ bàn", selection: $size) {
                    ForEach([9, 13, 19], id: \.self) { Text("\($0)×\($0)").tag($0) }
                }
                .pickerStyle(.segmented)
                Picker("Hệ luật", selection: $rules) {
                    Text("Nhật Bản").tag(RuleSet.japanese); Text("Trung Quốc").tag(RuleSet.chinese)
                }
                Picker("Chấp quân", selection: $handicap) {
                    Text("Không").tag(0)
                    ForEach(2...9, id: \.self) { Text("\($0) quân").tag($0) }
                }
            }
            Section {
                Picker("Cấp độ máy", selection: $level) {
                    ForEach(BotLevel.allCases) { option in
                        Text(label(for: option)).tag(option)
                    }
                }
                .onChange(of: level) { _, chosen in
                    if !BotLadder.isUnlocked(chosen) {
                        level = BotLevel(rawValue: max(BotLadder.highestBeaten() + 1, BotLevel.greedy.rawValue)) ?? .greedy
                    }
                }
                Picker("Màu quân của bạn", selection: $myColor) {
                    Text("Đen").tag(Player.black); Text("Trắng").tag(Player.white)
                }
            } header: { Text("Đối thủ") } footer: {
                Text("Máy chạy ngay trên điện thoại, không cần mạng. Thắng một cấp để mở cấp tiếp theo.")
            }
        }
        .scrollContentBackground(.hidden)
        .background(Tokens.paper.ignoresSafeArea())
        .safeAreaInset(edge: .bottom) {
            Button("Bắt đầu") {
                var config = LocalGameConfig(size: size, rules: rules, handicap: handicap)
                let botName = LS(localized: "Máy") + " · " + level.title
                if myColor == .black {
                    config.blackName = LS(localized: "Bạn"); config.whiteName = botName
                    config.whiteBot = level.rawValue
                } else {
                    config.whiteName = LS(localized: "Bạn"); config.blackName = botName
                    config.blackBot = level.rawValue
                }
                onStart(config)
            }
            .buttonStyle(PrimaryButton()).padding(16)
        }
    }
}

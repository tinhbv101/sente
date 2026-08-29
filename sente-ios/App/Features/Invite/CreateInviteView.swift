import SwiftUI
import GoKit
import SenteNet
import SenteUI

/// Board and clock are the player's to choose (docs/01 FR-M1); the presets are
/// shortcuts that fill the same controls. The clock's ceiling follows the board
/// (docs/07 §8): three hours on 9×9, nine on 13×13, a day on 19×19.
struct CreateInviteView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.dismiss) private var dismiss

    private enum Preset: String, CaseIterable, Identifiable {
        case quick, standard, correspondence
        var id: String { rawValue }
        var title: String { switch self { case .quick: "Nhanh"; case .standard: "Chuẩn"; case .correspondence: "Thư tín" } }
        var detail: String {
            switch self {
            case .quick: "9×9 · 10 phút"
            case .standard: "19×19 · 20 phút + 3×30 giây"
            case .correspondence: "19×19 · 2 ngày mỗi nước"
            }
        }
    }

    private enum Pace: String, CaseIterable, Identifiable {
        case live, correspondence
        var id: String { rawValue }
        var title: String { self == .live ? "Tính giờ" : "Thư tín" }
    }

    /// Byo-yomi choices as (periods, seconds); nil periods means none.
    private static let byoyomiChoices: [(periods: Int, seconds: Int)?] = [nil, (3, 30), (5, 30), (3, 60), (5, 60)]

    @State private var boardSize = 19
    @State private var pace: Pace = .live
    @State private var mainTimeMs = 20 * 60_000
    @State private var byoyomi = 1
    @State private var daysPerMove = 2
    @State private var rules: RuleSet = .japanese
    @State private var handicap = 0
    @State private var colour = "random"
    @State private var created: Challenge?
    @State private var error: String?
    @State private var busy = false

    private var timeChoices: [Int] { TimeLimits.mainTimeChoicesMs(boardSize: boardSize) }

    private var timeControl: TimeControl {
        switch pace {
        case .correspondence:
            return TimeControl(kind: .correspondence, daysPerMove: daysPerMove)
        case .live:
            if let extra = Self.byoyomiChoices[byoyomi] {
                return TimeControl(kind: .byoyomi, mainTimeMs: mainTimeMs, periods: extra.periods, periodTimeMs: extra.seconds * 1000)
            }
            return TimeControl(kind: .absolute, mainTimeMs: mainTimeMs)
        }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Chọn nhanh") {
                    ForEach(Preset.allCases) { option in
                        Button { apply(option) } label: {
                            VStack(alignment: .leading, spacing: 1) {
                                Text(option.title).font(.callout.weight(.semibold))
                                Text(option.detail).font(.caption).foregroundStyle(Tokens.inkSecondary)
                            }
                        }.foregroundStyle(Tokens.ink)
                    }
                }
                Section("Bàn cờ") {
                    Picker("Cỡ bàn", selection: $boardSize) {
                        ForEach([9, 13, 19], id: \.self) { Text("\($0)×\($0)").tag($0) }
                    }
                    .pickerStyle(.segmented)
                }
                Section {
                    Picker("Thể thức", selection: $pace) {
                        ForEach(Pace.allCases) { Text($0.title).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    switch pace {
                    case .live:
                        Picker("Mỗi bên", selection: $mainTimeMs) {
                            ForEach(timeChoices, id: \.self) { Text(TimeLimits.format(ms: $0)).tag($0) }
                        }
                        Picker("Byo-yomi", selection: $byoyomi) {
                            ForEach(Self.byoyomiChoices.indices, id: \.self) { index in
                                Text(Self.byoyomiChoices[index].map { "\($0.periods) × \($0.seconds) giây" } ?? "Không").tag(index)
                            }
                        }
                    case .correspondence:
                        Picker("Mỗi nước", selection: $daysPerMove) {
                            ForEach(TimeLimits.daysPerMoveChoices, id: \.self) { Text("\($0) ngày").tag($0) }
                        }
                    }
                } header: { Text("Thời gian") } footer: {
                    if pace == .live {
                        Text("Tối đa \(TimeLimits.format(ms: TimeLimits.maxMainTimeMs(boardSize: boardSize))) mỗi bên cho bàn \(boardSize)×\(boardSize). Hết giờ chính thì dùng byo-yomi, nếu có.")
                    } else {
                        Text("Mỗi nước có bấy nhiêu ngày; hết hạn là thua. Có thông báo khi đến lượt.")
                    }
                }
                Section("Tùy chỉnh") {
                    Picker("Hệ luật", selection: $rules) {
                        Text("Nhật Bản").tag(RuleSet.japanese); Text("Trung Quốc").tag(RuleSet.chinese)
                    }
                    Picker("Chấp quân", selection: $handicap) {
                        Text("Không").tag(0)
                        ForEach(2...9, id: \.self) { Text("\($0) quân").tag($0) }
                    }
                    Picker("Màu quân của bạn", selection: $colour) {
                        Text("Ngẫu nhiên").tag("random"); Text("Đen").tag("black"); Text("Trắng").tag("white")
                    }
                    LabeledContent("Komi", value: GameEngine.defaultKomi(rules: rules, handicap: handicap).formatted())
                }
                if let error { Section { Text(error).foregroundStyle(.red) } }
            }
            .navigationTitle("Mời bạn chơi")
            .navigationBarTitleDisplayMode(.inline)
            // A smaller board has a lower ceiling; a choice above it snaps to the cap.
            .onChange(of: boardSize) { _, size in
                let cap = TimeLimits.maxMainTimeMs(boardSize: size)
                if mainTimeMs > cap { mainTimeMs = cap }
            }
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Đóng") { dismiss() } } }
            .safeAreaInset(edge: .bottom) {
                Button { Task { await create() } } label: {
                    if busy { ProgressView().tint(.white) } else { Text("Tạo lời mời · \(boardSize)×\(boardSize) · \(timeControl.summary)") }
                }
                .buttonStyle(PrimaryButton()).disabled(busy)
                .padding(16)
            }
            .sheet(item: $created) { invite in
                ShareInviteView(invite: invite) { dismiss() }
                    .presentationDetents([.medium])
            }
        }
    }

    private func apply(_ preset: Preset) {
        switch preset {
        case .quick: boardSize = 9; pace = .live; mainTimeMs = 10 * 60_000; byoyomi = 0
        case .standard: boardSize = 19; pace = .live; mainTimeMs = 20 * 60_000; byoyomi = 1
        case .correspondence: boardSize = 19; pace = .correspondence; daysPerMove = 2
        }
    }

    private func create() async {
        busy = true; defer { busy = false }
        do {
            created = try await session.api.createChallenge(GameConfigRequest(
                boardSize: boardSize, rules: rules, handicap: handicap,
                timeControl: timeControl, creatorColor: colour))
            await session.refreshQuietly()
        } catch let apiError as APIError {
            error = apiError.userMessage
        } catch {
            self.error = error.localizedDescription
        }
    }
}

struct ShareInviteView: View {
    let invite: Challenge
    let onDone: () -> Void

    private var shareText: String {
        let link = invite.shareUrl ?? "sente://j/\(invite.code)"
        return "Chơi cờ vây với mình nhé! Mở link hoặc nhập mã \(invite.code) trong Sente: \(link)"
    }

    var body: some View {
        VStack(spacing: 18) {
            Text("Gửi lời mời").font(.title3.weight(.semibold))
            Text(invite.code)
                .font(.system(size: 34, weight: .medium, design: .monospaced)).tracking(4)
                .padding(.vertical, 8).padding(.horizontal, 20)
                .background(Tokens.sheetSecondary, in: RoundedRectangle(cornerRadius: 12))
                .accessibilityLabel("Mã lời mời \(invite.code.map(String.init).joined(separator: " "))")
            Text("\(invite.config.boardSize)×\(invite.config.boardSize) · \(invite.config.timeControl.summary) · hết hạn sau 7 ngày")
                .font(.footnote).foregroundStyle(Tokens.inkSecondary)
            ShareLink(item: shareText) { Label("Chia sẻ", systemImage: "square.and.arrow.up") }
                .buttonStyle(PrimaryButton())
            Button("Xong") { onDone() }.buttonStyle(SecondaryButton())
        }
        .padding(24)
        .foregroundStyle(Tokens.ink)
        .presentationBackground(Tokens.sheet)
    }
}

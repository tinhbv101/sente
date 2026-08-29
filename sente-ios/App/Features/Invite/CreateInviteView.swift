import SwiftUI
import GoKit
import SenteNet
import SenteUI

/// Three presets cover almost every real request; the custom section is for people
/// who know what they want (docs/07 §8).
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
        var size: Int { self == .quick ? 9 : 19 }
        var timeControl: TimeControl {
            switch self { case .quick: .quick; case .standard: .standard; case .correspondence: .correspondence }
        }
    }

    @State private var preset: Preset = .standard
    @State private var rules: RuleSet = .japanese
    @State private var handicap = 0
    @State private var colour = "random"
    @State private var created: Challenge?
    @State private var error: String?
    @State private var busy = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Chọn nhanh") {
                    ForEach(Preset.allCases) { option in
                        Button { preset = option } label: {
                            HStack {
                                Image(systemName: preset == option ? "largecircle.fill.circle" : "circle")
                                    .foregroundStyle(Tokens.indigo)
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(option.title).font(.callout.weight(.semibold))
                                    Text(option.detail).font(.caption).foregroundStyle(Tokens.inkSecondary)
                                }
                            }
                        }.foregroundStyle(Tokens.ink)
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
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Đóng") { dismiss() } } }
            .safeAreaInset(edge: .bottom) {
                Button { Task { await create() } } label: {
                    if busy { ProgressView().tint(.white) } else { Text("Tạo lời mời") }
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

    private func create() async {
        busy = true; defer { busy = false }
        do {
            created = try await session.api.createChallenge(GameConfigRequest(
                boardSize: preset.size, rules: rules, handicap: handicap,
                timeControl: preset.timeControl, creatorColor: colour))
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

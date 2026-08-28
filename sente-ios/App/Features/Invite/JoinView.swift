import SwiftUI
import SenteNet
import SenteUI

/// Enter a code, see who is inviting you and to what, then accept (docs/01 J1).
struct JoinView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.dismiss) private var dismiss
    let onJoined: (GameSummary) -> Void

    @State private var code: String
    @State private var preview: Challenge?
    @State private var error: String?
    @State private var busy = false
    @FocusState private var focused: Bool

    init(initialCode: String, onJoined: @escaping (GameSummary) -> Void) {
        _code = State(initialValue: initialCode)
        self.onJoined = onJoined
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 20) {
                if let preview {
                    previewCard(preview)
                } else {
                    codeEntry
                }
                if let error { Text(error).font(.footnote).foregroundStyle(.red).multilineTextAlignment(.center) }
                Spacer()
            }
            .padding(20)
            .background(Tokens.paper.ignoresSafeArea())
            .navigationTitle(preview == nil ? "Nhập mã lời mời" : "Lời mời")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Đóng") { dismiss() } } }
            .task { if code.count == 8 { await lookUp() } else { focused = true } }
        }
    }

    private var codeEntry: some View {
        VStack(spacing: 14) {
            TextField("R4TN8KMP", text: $code)
                .font(.system(size: 30, weight: .medium, design: .monospaced))
                .multilineTextAlignment(.center)
                .textInputAutocapitalization(.characters).autocorrectionDisabled()
                .focused($focused)
                .onChange(of: code) { _, new in
                    code = String(new.uppercased().filter { "ABCDEFGHJKLMNPQRSTUVWXYZ23456789".contains($0) }.prefix(8))
                }
                .onSubmit { Task { await lookUp() } }
                .padding(.vertical, 12)
                .background(.white, in: RoundedRectangle(cornerRadius: 14))
            Text("Mã gồm 8 chữ, không có I, O, 0 hay 1.").font(.caption).foregroundStyle(Tokens.inkTertiary)
            Button { Task { await lookUp() } } label: {
                if busy { ProgressView().tint(.white) } else { Text("Xem lời mời") }
            }
            .buttonStyle(PrimaryButton()).disabled(code.count != 8 || busy)
        }
    }

    private func previewCard(_ invite: Challenge) -> some View {
        VStack(spacing: 16) {
            VStack(spacing: 6) {
                Text("\(invite.creatorName ?? "Ai đó") mời bạn\nmột ván cờ vây")
                    .font(.system(size: 28, design: .serif)).multilineTextAlignment(.center)
                Text(invite.status == "pending" ? "Hết hạn \(invite.expiresAt.formatted(.relative(presentation: .named)))" : "Lời mời này không còn hiệu lực")
                    .font(.footnote).foregroundStyle(invite.status == "pending" ? Tokens.inkSecondary : .red)
            }
            VStack(spacing: 0) {
                detail("Cỡ bàn", "\(invite.config.boardSize) × \(invite.config.boardSize)")
                Divider()
                detail("Hệ luật", invite.config.rules == "japanese" ? "Nhật Bản" : "Trung Quốc")
                Divider()
                detail("Komi", invite.config.komi.formatted())
                Divider()
                detail("Thời gian", invite.config.timeControl.summary)
                Divider()
                detail("Màu của bạn", yourColour(invite))
            }
            .padding(.horizontal, 14).background(.white, in: RoundedRectangle(cornerRadius: 16))

            if invite.isMine {
                Text("Đây là lời mời của bạn — gửi mã cho một người bạn.").font(.footnote).foregroundStyle(Tokens.inkSecondary)
                Button("Hủy lời mời", role: .destructive) { Task { await cancel(invite) } }.buttonStyle(SecondaryButton())
            } else if invite.status == "pending" {
                Button { Task { await accept(invite) } } label: {
                    if busy { ProgressView().tint(.white) } else { Text("Chấp nhận") }
                }.buttonStyle(PrimaryButton()).disabled(busy)
                Button("Từ chối") { Task { await decline(invite) } }.buttonStyle(SecondaryButton())
            }
        }
    }

    private func detail(_ label: String, _ value: String) -> some View {
        HStack { Text(label).foregroundStyle(Tokens.inkSecondary); Spacer(); Text(value).fontWeight(.semibold) }
            .font(.subheadline).padding(.vertical, 11)
    }

    private func yourColour(_ invite: Challenge) -> String {
        switch invite.creatorColor { case "black": "Trắng"; case "white": "Đen"; default: "Ngẫu nhiên" }
    }

    private func lookUp() async {
        guard code.count == 8 else { return }
        busy = true; defer { busy = false }
        error = nil
        do { preview = try await session.api.challenge(code: code) }
        catch let apiError as APIError { error = apiError.userMessage }
        catch { self.error = error.localizedDescription }
    }

    private func accept(_ invite: Challenge) async {
        busy = true; defer { busy = false }
        do {
            let accepted = try await session.api.acceptChallenge(code: invite.code)
            try await session.refresh()
            if let game = session.games.first(where: { $0.gameId == accepted.gameId }) {
                onJoined(game)
            } else {
                dismiss()
            }
        } catch let apiError as APIError { error = apiError.userMessage }
        catch { self.error = error.localizedDescription }
    }

    private func decline(_ invite: Challenge) async {
        try? await session.api.declineChallenge(code: invite.code)
        await session.refreshQuietly()
        dismiss()
    }

    private func cancel(_ invite: Challenge) async {
        try? await session.api.cancelChallenge(code: invite.code)
        await session.refreshQuietly()
        dismiss()
    }
}

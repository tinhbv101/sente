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
    @State private var scanning = false
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
            .foregroundStyle(Tokens.ink)
            .background(Tokens.paper.ignoresSafeArea())
            .navigationTitle(preview == nil ? String(localized: "Nhập mã lời mời") : String(localized: "Lời mời"))
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
                    // A pasted link is the common case from a chat; a typed code the other.
                    if let parsed = InviteCode.parse(new) {
                        code = parsed
                    } else {
                        code = String(new.uppercased().filter(InviteCode.alphabet.contains).prefix(InviteCode.length))
                    }
                }
                .onSubmit { Task { await lookUp() } }
                .padding(.vertical, 12)
                .background(Tokens.sheet, in: RoundedRectangle(cornerRadius: 14))
            Text("Mã gồm 8 chữ, không có I, O, 0 hay 1.").font(.caption).foregroundStyle(Tokens.inkTertiary)
            Button { Task { await lookUp() } } label: {
                if busy { ProgressView().tint(.white) } else { Text("Xem lời mời") }
            }
            .buttonStyle(PrimaryButton()).disabled(code.count != 8 || busy)
            Button { scanning = true } label: { Label("Quét mã QR", systemImage: "qrcode.viewfinder") }
                .buttonStyle(SecondaryButton())
        }
        .sheet(isPresented: $scanning) {
            QRScanSheet { scanned in
                scanning = false
                code = scanned
                Task { await lookUp() }
            }
        }
    }

    private func previewCard(_ invite: Challenge) -> some View {
        VStack(spacing: 16) {
            VStack(spacing: 6) {
                Text("\(invite.creatorName ?? String(localized: "Ai đó")) mời bạn\nmột ván cờ vây")
                    .font(.system(size: 28, design: .serif)).multilineTextAlignment(.center)
                Text(invite.status == "pending" ? expiry(invite.expiresAt) : String(localized: "Lời mời này không còn hiệu lực"))
                    .font(.footnote).foregroundStyle(invite.status == "pending" ? Tokens.inkSecondary : .red)
            }
            VStack(spacing: 0) {
                detail("Cỡ bàn", "\(invite.config.boardSize) × \(invite.config.boardSize)")
                Divider()
                detail("Hệ luật", invite.config.rules == "japanese" ? String(localized: "Nhật Bản") : String(localized: "Trung Quốc"))
                Divider()
                detail("Komi", invite.config.komi.formatted())
                Divider()
                detail("Thời gian", invite.config.timeControl.summary)
                Divider()
                detail("Màu của bạn", yourColour(invite))
            }
            .padding(.horizontal, 14).background(Tokens.sheet, in: RoundedRectangle(cornerRadius: 16))

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

    /// Spelled out rather than through a relative date formatter, which follows the
    /// device locale and produced "Hết hạn next week" on an English phone.
    private func expiry(_ date: Date) -> String {
        let seconds = date.timeIntervalSinceNow
        guard seconds > 0 else { return String(localized: "Đã hết hạn") }
        let hours = Int(seconds / 3600)
        if hours < 1 { return String(localized: "Hết hạn trong chưa đầy một giờ") }
        if hours < 48 { return String(localized: "Hết hạn sau \(hours) giờ") }
        return String(localized: "Hết hạn sau \(hours / 24) ngày")
    }

    private func detail(_ label: LocalizedStringKey, _ value: String) -> some View {
        HStack { Text(label).foregroundStyle(Tokens.inkSecondary); Spacer(); Text(value).fontWeight(.semibold) }
            .font(.subheadline).padding(.vertical, 11)
    }

    private func yourColour(_ invite: Challenge) -> String {
        switch invite.creatorColor { case "black": String(localized: "Trắng"); case "white": String(localized: "Đen"); default: String(localized: "Ngẫu nhiên") }
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

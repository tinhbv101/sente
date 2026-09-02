import SwiftUI
import SenteUI

/// First-launch sheet: three doors in, no form to fill.
struct WelcomeView: View {
    let onLearn: () -> Void
    let onBot: () -> Void
    let onInvite: () -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 13) {
            Text("⚫︎ ⚪︎").font(.system(size: 40)).padding(.top, 10)
            Text("Chào mừng đến với Sente").font(.title2.weight(.semibold))
            Text("Cờ vây: vây đất và bắt quân. Luật học trong năm phút — chơi được cả đời.")
                .font(.subheadline).foregroundStyle(Tokens.inkSecondary)
                .multilineTextAlignment(.center)
            Spacer(minLength: 4)
            door("graduationcap.fill", LS(localized: "Học luật trong 5 phút"),
                 LS(localized: "Bài học tương tác, bắt đầu từ số 0")) { dismiss(); onLearn() }
            door("cpu", LS(localized: "Đấu với máy"),
                 LS(localized: "Bốn cấp độ, chạy trên máy, không cần mạng")) { dismiss(); onBot() }
            door("qrcode", LS(localized: "Mời bạn chơi online"),
                 LS(localized: "Gửi link hoặc mã QR là vào ván")) { dismiss(); onInvite() }
            Button("Để sau") { dismiss() }
                .font(.callout.weight(.medium)).foregroundStyle(Tokens.inkSecondary)
                .padding(.top, 2)
        }
        .padding(22)
        .foregroundStyle(Tokens.ink)
        .presentationBackground(Tokens.sheet)
        // Large only: at medium the three doors squeeze the intro into ellipses.
        .presentationDetents([.large])
    }

    private func door(_ icon: String, _ title: String, _ subtitle: String,
                      action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 13) {
                Image(systemName: icon).font(.title3).foregroundStyle(Tokens.indigo)
                    .frame(width: 34)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.callout.weight(.semibold))
                    Text(subtitle).font(.caption).foregroundStyle(Tokens.inkSecondary)
                }
                Spacer()
                Image(systemName: "chevron.right").font(.caption.weight(.semibold))
                    .foregroundStyle(Tokens.inkTertiary)
            }
            .padding(14)
            .background(Tokens.sheetSecondary, in: RoundedRectangle(cornerRadius: 14))
        }
        .buttonStyle(.plain)
    }
}

import SwiftUI
import SenteUI

struct SettingsView: View {
    @Environment(AppSession.self) private var session
    @State private var draft = Settings.load()
    @State private var serverText = Settings.load().serverURL.absoluteString

    var body: some View {
        Form {
            if let user = session.user {
                Section("Tài khoản") {
                    LabeledContent("Tên", value: user.displayName)
                    LabeledContent("Mã bạn bè", value: user.friendCode)
                    Text("Tài khoản khách gắn với máy này. Đăng nhập Apple sẽ có ở bản sau.")
                        .font(.caption).foregroundStyle(Tokens.inkSecondary)
                }
            }
            Section("Bàn cờ") {
                Toggle("Hiện tọa độ", isOn: $draft.showCoordinates)
                Toggle("Ký hiệu cho người mù màu", isOn: $draft.colourBlindSymbols)
            }
            Section {
                TextField("https://…", text: $serverText)
                    .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                    .font(.system(.body, design: .monospaced))
            } header: { Text("Máy chủ") } footer: {
                Text("Đổi máy chủ sẽ tạo tài khoản khách mới trên máy chủ đó.")
            }
        }
        .navigationTitle("Cài đặt")
        .onChange(of: draft) { _, new in new.save(); Task { session.settings = new } }
        .onSubmit { applyServer() }
        .toolbar {
            if URL(string: serverText) != session.settings.serverURL {
                ToolbarItem(placement: .confirmationAction) { Button("Đổi máy chủ") { applyServer() } }
            }
        }
    }

    private func applyServer() {
        guard let url = URL(string: serverText), url.scheme?.hasPrefix("http") == true else { return }
        var next = draft
        next.serverURL = url
        Task { await session.apply(settings: next) }
    }
}

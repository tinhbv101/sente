import SwiftUI
import SenteNet
import SenteUI

struct SettingsView: View {
    @Environment(AppSession.self) private var session
    @State private var draft = Settings.load()
    @State private var serverText = Settings.load().serverURL.absoluteString
    @State private var confirmDelete = false
    @State private var deleteError: String?

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
            Section("Giao diện") {
                Picker("Chế độ màu", selection: $draft.appearance) {
                    ForEach(Settings.Appearance.allCases) { Text($0.title).tag($0) }
                }
                .pickerStyle(.segmented)
            }
            Section {
                Toggle("Hiện tọa độ", isOn: $draft.showCoordinates)
                Toggle("Ký hiệu cho người mù màu", isOn: $draft.colourBlindSymbols)
                Toggle("Đặt quân phía trên ngón tay", isOn: $draft.offsetPlacement)
            } header: { Text("Bàn cờ") } footer: {
                Text("Tắt: quân rơi đúng chỗ bạn chạm. Bật: quân ma hiện cao hơn ngón tay một chút để không bị che — hữu ích trên bàn 19×19, kéo xuống dưới mép bàn để đặt hàng cuối.")
            }
            Section {
                Button("Xóa tài khoản", role: .destructive) { confirmDelete = true }
                if let deleteError { Text(deleteError).font(.footnote).foregroundStyle(.red) }
            } footer: {
                Text("Tên và mã bạn bè của bạn bị xóa vĩnh viễn. Các ván đã chơi vẫn còn trong lịch sử của đối thủ, dưới tên \"Người chơi đã xóa\".")
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
        .confirmationDialog("Xóa tài khoản này?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Xóa vĩnh viễn", role: .destructive) {
                Task {
                    do { try await session.deleteAccount() }
                    catch let error as APIError { deleteError = error.userMessage }
                    catch { deleteError = error.localizedDescription }
                }
            }
        } message: {
            Text("Không thể khôi phục. Bạn sẽ được tạo một tài khoản khách mới.")
        }
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

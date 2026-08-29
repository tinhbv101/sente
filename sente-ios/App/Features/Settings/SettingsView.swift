import AuthenticationServices
import CryptoKit
import SwiftUI
import SenteNet
import SenteUI

struct SettingsView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.colorScheme) private var colorScheme
    @State private var draft = Settings.load()
    @State private var serverText = Settings.load().serverURL.absoluteString
    @State private var confirmDelete = false
    @State private var deleteError: String?
    @State private var appleError: String?
    // Fresh per screen; Apple echoes its hash back inside the identity token so
    // the server can tell this sign-in from a replayed one.
    @State private var rawNonce = SettingsView.freshNonce()
    @State private var pushStatus: PushRegistrar.Status?

    var body: some View {
        Form {
            if let user = session.user {
                Section {
                    LabeledContent("Tên", value: user.displayName)
                    LabeledContent("Mã bạn bè", value: user.friendCode)
                    if user.isGuest {
                        SignInWithAppleButton(.continue, onRequest: prepareAppleRequest, onCompletion: handleApple)
                            .signInWithAppleButtonStyle(colorScheme == .dark ? .white : .black)
                            .frame(height: 44)
                            .listRowInsets(EdgeInsets(top: 8, leading: 16, bottom: 8, trailing: 16))
                        if let appleError { Text(appleError).font(.footnote).foregroundStyle(.red) }
                    } else {
                        Label("Đã liên kết với Apple ID", systemImage: "checkmark.seal.fill")
                            .foregroundStyle(Tokens.inkSecondary)
                    }
                } header: { Text("Tài khoản") } footer: {
                    if user.isGuest {
                        Text("Tài khoản khách gắn với máy này. Đăng nhập Apple để giữ ván cờ và mã bạn bè khi đổi máy.")
                    }
                }
                notificationsSection
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

    @ViewBuilder private var notificationsSection: some View {
        Section {
            switch pushStatus {
            case .granted:
                Label("Đã bật", systemImage: "bell.badge.fill").foregroundStyle(Tokens.inkSecondary)
            case .denied:
                Button("Mở Cài đặt để bật thông báo") {
                    if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
                }
            case .notAsked:
                Button("Bật thông báo") {
                    Task { _ = await session.enablePush(); pushStatus = await PushRegistrar.status() }
                }
            case nil:
                ProgressView()
            }
        } header: { Text("Thông báo") } footer: {
            Text("Báo khi đến lượt bạn trong ván chậm, khi ván kết thúc, và khi bạn nhận lời mời.")
        }
        .task { pushStatus = await PushRegistrar.status() }
    }

    private func prepareAppleRequest(_ request: ASAuthorizationAppleIDRequest) {
        request.requestedScopes = [.fullName]
        request.nonce = Self.sha256(rawNonce)
    }

    private func handleApple(_ result: Result<ASAuthorization, Error>) {
        switch result {
        case .success(let authorization):
            guard let credential = authorization.credential as? ASAuthorizationAppleIDCredential else { return }
            let nonce = rawNonce
            rawNonce = Self.freshNonce()
            Task {
                do { try await session.signInWithApple(credential: credential, rawNonce: nonce) }
                catch let error as APIError { appleError = error.userMessage }
                catch { appleError = error.localizedDescription }
            }
        case .failure(let error):
            // Dismissing the sheet is not an error worth showing.
            if (error as? ASAuthorizationError)?.code != .canceled { appleError = error.localizedDescription }
        }
    }

    static func freshNonce() -> String {
        (0..<32).map { _ in String(format: "%02x", UInt8.random(in: .min ... .max)) }.joined()
    }

    static func sha256(_ input: String) -> String {
        SHA256.hash(data: Data(input.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    private func applyServer() {
        guard let url = URL(string: serverText), url.scheme?.hasPrefix("http") == true else { return }
        var next = draft
        next.serverURL = url
        Task { await session.apply(settings: next) }
    }
}

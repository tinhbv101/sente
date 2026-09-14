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
    @State private var nameDraft = ""
    @State private var nameError: String?
    // Fresh per screen; Apple echoes its hash back inside the identity token so
    // the server can tell this sign-in from a replayed one.
    @State private var rawNonce = SettingsView.freshNonce()
    @State private var pushStatus: PushRegistrar.Status?

    var body: some View {
        Form {
            if let user = session.user {
                Section {
                    HStack {
                        Text("Tên")
                        TextField("Tên hiển thị", text: $nameDraft)
                            .multilineTextAlignment(.trailing)
                            .submitLabel(.done)
                            .onSubmit { saveName() }
                        if nameDraft.trimmingCharacters(in: .whitespaces) != user.displayName {
                            Button("Lưu") { saveName() }.buttonStyle(.borderless)
                        } else {
                            // A plain trailing value reads as a label; the pencil says "tap me".
                            Image(systemName: "pencil").foregroundStyle(Tokens.inkSecondary)
                        }
                    }
                    if let nameError { Text(nameError).font(.footnote).foregroundStyle(.red) }
                    LabeledContent("Mã bạn bè", value: user.friendCode)
                    if !user.isGuest {
                        NavigationLink { FriendsView() } label: { Label("Bạn bè", systemImage: "person.2") }
                    }
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
                recordSection
            }
            Section {
                Picker("Chế độ màu", selection: $draft.appearance) {
                    ForEach(Settings.Appearance.allCases) { Text($0.title).tag($0) }
                }
                .pickerStyle(.segmented)
                Picker("Ngôn ngữ", selection: $draft.language) {
                    ForEach(AppLanguage.allCases) { Text($0.title).tag($0) }
                }
            } header: { Text("Giao diện") } footer: {
                Text("Mặc định theo ngôn ngữ máy. Đổi ở đây có hiệu lực ngay, chỉ với Sente.")
            }
            Section {
                Toggle("Hiện tọa độ", isOn: $draft.showCoordinates)
                Toggle("Ký hiệu cho người mù màu", isOn: $draft.colourBlindSymbols)
                Toggle("Đặt quân phía trên ngón tay", isOn: $draft.offsetPlacement)
            } header: { Text("Bàn cờ") } footer: {
                Text("Tắt: quân rơi đúng chỗ bạn chạm. Bật: quân ma hiện cao hơn ngón tay một chút để không bị che — hữu ích trên bàn 19×19, kéo xuống dưới mép bàn để đặt hàng cuối.")
            }
            Section {
                Toggle("Âm thanh", isOn: $soundOn)
                Toggle("Rung phản hồi", isOn: $hapticsOn)
            } header: { Text("Âm thanh & rung") } footer: {
                Text("Tiếng đặt quân và rung nhẹ khi có nước đi, bắt quân hoặc tin nhắn nhanh. Âm thanh theo công tắc im lặng của máy.")
            }
            Section {
                Button("Xóa tài khoản", role: .destructive) { confirmDelete = true }
                if let deleteError { Text(deleteError).font(.footnote).foregroundStyle(.red) }
            } footer: {
                Text("Tên và mã bạn bè của bạn bị xóa vĩnh viễn. Các ván đã chơi vẫn còn trong lịch sử của đối thủ, dưới tên \"Người chơi đã xóa\".")
            }
            Section {
                Link(destination: session.settings.serverURL.appending(path: "/privacy")
                        .appending(queryItems: [URLQueryItem(name: "lang", value: LanguageManager.isVietnamese ? "vi" : "en")])) {
                    Label("Chính sách quyền riêng tư", systemImage: "hand.raised")
                }
                .foregroundStyle(Tokens.ink)
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
        // The field follows the account: a fresh guest or a just-linked Apple ID.
        .task(id: session.user?.displayName) { nameDraft = session.user?.displayName ?? "" }
        .task { stats = try? await session.api.stats() }
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
        .onChange(of: draft) { _, new in
            new.save()
            LanguageManager.apply(new.language)
            Task { session.settings = new }
        }
        .onSubmit { applyServer() }
        .toolbar {
            if URL(string: serverText) != session.settings.serverURL {
                ToolbarItem(placement: .confirmationAction) { Button("Đổi máy chủ") { applyServer() } }
            }
        }
    }

    @AppStorage("soundOn") private var soundOn = true
    @AppStorage("hapticsOn") private var hapticsOn = true

    /// Kept on-device and pushed as a patch; the server filters per kind.
    @AppStorage("pushPrefTurn") private var prefTurn = true
    @AppStorage("pushPrefLowTime") private var prefLowTime = true
    @AppStorage("pushPrefGameEnd") private var prefGameEnd = true
    @AppStorage("pushPrefInvite") private var prefInvite = true
    @AppStorage("pushPrefFriend") private var prefFriend = true

    private func pushPref(_ key: String, _ value: Bool) {
        guard let token = session.deviceToken else { return }
        Task { _ = try? await session.api.setDevicePrefs(token: token, [key: value]) }
    }

    @State private var stats: PlayerStats?

    @ViewBuilder private var recordSection: some View {
        if let stats, stats.games > 0 {
            Section("Thành tích") {
                LabeledContent("Số ván online", value: "\(stats.games)")
                LabeledContent("Thắng / Thua", value: "\(stats.wins) / \(stats.losses)")
                ForEach(stats.bySize.keys.sorted(), id: \.self) { size in
                    if let line = stats.bySize[size] {
                        LabeledContent("Bàn \(size)×\(size)", value: "\(line.wins)/\(line.games) thắng")
                    }
                }
            }
        }
    }

    @ViewBuilder private var notificationsSection: some View {
        Section {
            switch pushStatus {
            case .granted:
                Toggle("Đến lượt bạn (ván chậm)", isOn: $prefTurn)
                    .onChange(of: prefTurn) { _, new in pushPref("turn", new) }
                Toggle("Sắp hết giờ", isOn: $prefLowTime)
                    .onChange(of: prefLowTime) { _, new in pushPref("low_time", new) }
                Toggle("Ván kết thúc", isOn: $prefGameEnd)
                    .onChange(of: prefGameEnd) { _, new in pushPref("game_end", new) }
                Toggle("Lời mời", isOn: $prefInvite)
                    .onChange(of: prefInvite) { _, new in pushPref("invite", new) }
                Toggle("Kết bạn", isOn: $prefFriend)
                    .onChange(of: prefFriend) { _, new in pushPref("friend", new) }
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
            Text("Báo khi đến lượt bạn trong ván chậm, khi ván kết thúc, khi bạn nhận lời mời và khi có người kết bạn.")
        }
        .task { pushStatus = await PushRegistrar.status() }
    }

    private func saveName() {
        let name = nameDraft.trimmingCharacters(in: .whitespaces)
        guard name != session.user?.displayName else { return }
        Task {
            do { try await session.rename(name); nameError = nil }
            catch let error as APIError { nameError = error.userMessage }
            catch { nameError = error.localizedDescription }
        }
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

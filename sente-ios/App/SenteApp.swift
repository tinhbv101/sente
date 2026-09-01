import SwiftUI

@main
struct SenteApp: App {
    @State private var session = AppSession()
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    init() {
        // Before any view resolves a string.
        LanguageManager.apply(Settings.load().language)
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(session)
                // nil follows the system; a choice in Settings overrides it.
                .preferredColorScheme(session.settings.appearance.colorScheme)
                // A language change rebuilds the tree, so every visible string
                // re-resolves at once; formatters follow through the locale.
                .id(session.settings.language)
                .environment(\.locale, session.settings.language.locale ?? .current)
                .task {
                    AppDelegate.onDeviceToken = { [session] in session.deviceTokenReceived($0) }
                    AppDelegate.onOpenGame = { [session] in session.pendingGameID = $0 }
                    AppDelegate.onOpenInvite = { [session] in session.pendingInviteCode = $0 }
                    await session.start()
                }
                // sente://j/<code> from a shared link, or the universal-link path once
                // the AASA file is in place.
                .onOpenURL { url in session.handle(url: url) }
        }
    }
}

struct RootView: View {
    @Environment(AppSession.self) private var session

    var body: some View {
        switch session.phase {
        case .starting:
            ProgressView("Đang khởi động…").tint(.secondary)
        case .failed(let message):
            ContentUnavailableView {
                Label("Không kết nối được", systemImage: "wifi.exclamationmark")
            } description: {
                Text(message)
            } actions: {
                Button("Thử lại") { Task { await session.start() } }
                    .buttonStyle(.borderedProminent)
                NavigationLink("Đổi máy chủ") { SettingsView() }
            }
        case .ready:
            HomeView()
        }
    }
}

import SwiftUI

@main
struct SenteApp: App {
    @State private var session = AppSession()
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @Environment(\.scenePhase) private var scenePhase

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
                    AppDelegate.onOpenFriends = { [session] in session.openFriends = true }
                    // A push while the app is open means the list is stale right now.
                    AppDelegate.onForegroundPush = { [session] in Task { await session.refreshQuietly() } }
                    await session.start()
                }
                // Whatever happened while the app was away (an accepted invitation,
                // a move) shows up without a pull-to-refresh.
                .onChange(of: scenePhase) { _, phase in
                    if phase == .active, session.phase == .ready {
                        Task { await session.refreshQuietly() }
                    }
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

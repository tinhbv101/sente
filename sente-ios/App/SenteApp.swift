import SwiftUI

@main
struct SenteApp: App {
    @State private var session = AppSession()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(session)
                .task { await session.start() }
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

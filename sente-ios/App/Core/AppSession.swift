import Foundation
import Observation
import SenteNet
import SwiftUI

/// Everything the app knows outside a game: who I am, where the server is, and
/// which games and invitations I have. Guest sign-up happens silently on first
/// launch so the first screen is the home screen, not a form (docs/03 ADR-008).
@MainActor @Observable
final class AppSession {
    enum Phase: Equatable { case starting, ready, failed(String) }

    private(set) var phase: Phase = .starting
    private(set) var user: GuestSignUp.User?
    private(set) var games: [GameSummary] = []
    private(set) var invitations: [Challenge] = []
    private(set) var isRefreshing = false
    var pendingInviteCode: String?
    /// A game to open as soon as the home screen can navigate: from a push, or a
    /// `sente://g/<id>` link (docs/07 §9.1).
    var pendingGameID: String?
    private(set) var keychainUnavailable = false
    var settings = Settings.load()
    /// Push permission is asked for once per launch, and only once there is a
    /// game worth being told about (docs/07 §3.1).
    var pushAttempted = false
    var deviceToken: String?

    private(set) var api: APIClient
    private let tokens = TokenStore()
    private let refreshTokens = TokenStore.refresh

    init() {
        api = APIClient(baseURL: Settings.load().serverURL, accessToken: TokenStore().load(),
                        refreshToken: TokenStore.refresh.load())
    }

    func start() async {
        phase = .starting
        api = APIClient(baseURL: settings.serverURL, accessToken: tokens.load(),
                        refreshToken: refreshTokens.load())
        // Every rotation lands in the Keychain, or the next launch is a stranger.
        // Local names must not shadow refresh(), which is called just below.
        let accessStore = tokens, refreshStore = refreshTokens
        await api.onTokensChanged { newAccess, newRefresh in
            accessStore.save(newAccess)
            if let newRefresh { refreshStore.save(newRefresh) }
        }
        do {
            // Order of preference: the access token we have; a refresh of it; and
            // only then a brand-new guest -- which is a different person.
            if await api.token == nil, await api.hasRefreshToken {
                _ = await api.refresh()
            }
            if await api.token == nil {
                try await becomeNewGuest()
            }
            try await loadProfile()
            try await refresh()
            phase = .ready
            consumeLaunchArguments()
        } catch APIError.unauthorized {
            // Both tokens are dead: expired after 30 days away, revoked, or the
            // account was deleted. Start over rather than show a login screen.
            tokens.clear(); refreshTokens.clear()
            await api.setTokens(access: nil, refresh: nil)
            do {
                try await becomeNewGuest()
                try await refresh()
                phase = .ready
            } catch let error as APIError {
                phase = .failed(error.userMessage)
            } catch {
                phase = .failed(error.localizedDescription)
            }
        } catch let error as APIError {
            phase = .failed(error.userMessage)
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    private func becomeNewGuest() async throws {
        adopt(try await api.signUpGuest())
    }

    /// Takes over a session the server just issued: a fresh guest, or a guest
    /// promoted by Sign in with Apple.
    func adopt(_ signUp: GuestSignUp) {
        if !tokens.save(signUp.accessToken) {
            // Without the token the next launch becomes a new guest, and this
            // account's games vanish with it. Loud in debug, visible in logs.
            assertionFailure("Keychain refused the access token")
            keychainUnavailable = true
        }
        if let refreshToken = signUp.refreshToken { refreshTokens.save(refreshToken) }
        user = signUp.user
    }

    /// Who am I, from the server: after a refresh the app has a token but no name.
    private func loadProfile() async throws {
        if user == nil { user = try await api.me() }
    }

    func rename(_ name: String) async throws {
        user = try await api.rename(name)
    }

    func deleteAccount() async throws {
        try await api.deleteAccount()
        tokens.clear(); refreshTokens.clear()
        user = nil; games = []; invitations = []
        await start()
    }

    func refresh() async throws {
        isRefreshing = true
        defer { isRefreshing = false }
        async let games = api.myGames()
        async let invitations = api.myChallenges()
        self.games = try await games
        self.invitations = try await invitations
        await registerForPushIfUseful()
    }

    func refreshQuietly() async { try? await refresh() }

    var myTurnGames: [GameSummary] { games.filter { $0.isActive && $0.yourTurn } }
    var waitingGames: [GameSummary] { games.filter { $0.isActive && !$0.yourTurn } }
    var finishedGames: [GameSummary] { games.filter { !$0.isActive } }

    func handle(url: URL) {
        // sente://j/CODE, sente://g/ID and https://<host>/{j,g}/… all put the kind
        // and the value in the last two path components; the custom scheme puts
        // the kind in the host instead.
        var parts = url.pathComponents.filter { $0 != "/" }
        if let host = url.host, host == "j" || host == "g" { parts.insert(host, at: 0) }
        guard parts.count >= 2 else { return }
        switch parts[parts.count - 2] {
        case "j": pendingInviteCode = parts[parts.count - 1].uppercased()
        case "g": pendingGameID = parts[parts.count - 1]
        default: break
        }
    }

    /// `-openGame <id>` and `-inviteCode <code>` on the launch command line, for UI
    /// tests and for driving the simulator from a script (docs/07 §12.2). Xcode
    /// maps `-key value` launch arguments straight into UserDefaults.
    private func consumeLaunchArguments() {
        let defaults = UserDefaults.standard
        if let id = defaults.string(forKey: "openGame") {
            defaults.removeObject(forKey: "openGame")
            pendingGameID = id
        }
        if let code = defaults.string(forKey: "inviteCode") {
            defaults.removeObject(forKey: "inviteCode")
            pendingInviteCode = code.uppercased()
        }
    }

    func apply(settings: Settings) async {
        self.settings = settings
        settings.save()
        await start()
    }
}

struct Settings: Equatable {
    var serverURL: URL
    var showCoordinates: Bool
    var colourBlindSymbols: Bool
    /// Place the stone above the fingertip instead of under it. Off by default;
    /// useful on 19×19 where a finger hides the intersection.
    var offsetPlacement: Bool
    var appearance: Appearance

    enum Appearance: String, CaseIterable, Identifiable {
        case system, light, dark
        var id: String { rawValue }
        var title: String {
            switch self { case .system: String(localized: "Theo hệ thống"); case .light: String(localized: "Sáng"); case .dark: String(localized: "Tối") }
        }
        /// nil defers to the system setting.
        var colorScheme: ColorScheme? {
            switch self { case .system: nil; case .light: .light; case .dark: .dark }
        }
    }

    static let defaultServer = URL(string: "https://sente.devlord.net")!

    static func load() -> Settings {
        let defaults = UserDefaults.standard
        return Settings(
            serverURL: defaults.string(forKey: "serverURL").flatMap(URL.init) ?? defaultServer,
            showCoordinates: defaults.object(forKey: "showCoordinates") as? Bool ?? true,
            colourBlindSymbols: defaults.bool(forKey: "colourBlindSymbols"),
            offsetPlacement: defaults.bool(forKey: "offsetPlacement"),
            appearance: Appearance(rawValue: defaults.string(forKey: "appearance") ?? "") ?? .system)
    }

    func save() {
        let defaults = UserDefaults.standard
        defaults.set(serverURL.absoluteString, forKey: "serverURL")
        defaults.set(showCoordinates, forKey: "showCoordinates")
        defaults.set(colourBlindSymbols, forKey: "colourBlindSymbols")
        defaults.set(offsetPlacement, forKey: "offsetPlacement")
        defaults.set(appearance.rawValue, forKey: "appearance")
    }
}

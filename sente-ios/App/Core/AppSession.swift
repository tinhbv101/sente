import Foundation
import Observation
import SenteNet

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

    private(set) var api: APIClient
    private let tokens = TokenStore()

    init() {
        api = APIClient(baseURL: Settings.load().serverURL, accessToken: TokenStore().load())
    }

    func start() async {
        phase = .starting
        api = APIClient(baseURL: settings.serverURL, accessToken: tokens.load())
        do {
            if await api.token == nil {
                let signUp = try await api.signUpGuest()
                if !tokens.save(signUp.accessToken) {
                    // Without the token the next launch becomes a new guest, and this
                    // account's games vanish with it. Loud in debug, visible in logs.
                    assertionFailure("Keychain refused the access token")
                    keychainUnavailable = true
                }
                user = signUp.user
            }
            try await refresh()
            phase = .ready
            consumeLaunchArguments()
        } catch APIError.unauthorized {
            // The token expired (guest tokens live 15 minutes until refresh tokens
            // exist). Sign up again rather than showing a login screen.
            tokens.clear()
            await api.setAccessToken(nil)
            await start()
        } catch let error as APIError {
            phase = .failed(error.userMessage)
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    func refresh() async throws {
        isRefreshing = true
        defer { isRefreshing = false }
        async let games = api.myGames()
        async let invitations = api.myChallenges()
        self.games = try await games
        self.invitations = try await invitations
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

    static let defaultServer = URL(string: "https://sente.devlord.net")!

    static func load() -> Settings {
        let defaults = UserDefaults.standard
        return Settings(
            serverURL: defaults.string(forKey: "serverURL").flatMap(URL.init) ?? defaultServer,
            showCoordinates: defaults.object(forKey: "showCoordinates") as? Bool ?? true,
            colourBlindSymbols: defaults.bool(forKey: "colourBlindSymbols"),
            offsetPlacement: defaults.bool(forKey: "offsetPlacement"))
    }

    func save() {
        let defaults = UserDefaults.standard
        defaults.set(serverURL.absoluteString, forKey: "serverURL")
        defaults.set(showCoordinates, forKey: "showCoordinates")
        defaults.set(colourBlindSymbols, forKey: "colourBlindSymbols")
        defaults.set(offsetPlacement, forKey: "offsetPlacement")
    }
}

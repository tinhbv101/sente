import SwiftUI
import GoKit
import SenteNet
import SenteUI

/// The app's shell: four tabs around one raised scan button.
///
/// The centre slot is a real tab item so VoiceOver and the bar's own layout
/// treat it like the others; its content is never shown, because selecting it
/// opens the scanner and bounces the selection back. The visible circle is an
/// overlay on top, which is why the router has to hide it explicitly on board
/// screens -- `.toolbar(.hidden, for: .tabBar)` does not reach an overlay.
struct RootTabsView: View {
    @Environment(AppSession.self) private var session
    @Environment(AppRouter.self) private var router
    /// Versioned so a future, richer onboarding can show itself again.
    @AppStorage("welcomedV1") private var welcomed = false
    @State private var launchArgumentsRead = false

    private enum Slot: Hashable { case play, friends, scan, learn, me }

    var body: some View {
        @Bindable var router = router
        TabView(selection: slot) {
            NavigationStack(path: $router.play) { HomeView().withSenteRoutes() }
                .tabItem { Label("Ván", systemImage: "circle.grid.3x3.fill") }
                .tag(Slot.play)

            NavigationStack(path: $router.friends) { FriendsView().withSenteRoutes() }
                .tabItem { Label("Bạn bè", systemImage: "person.2.fill") }
                .tag(Slot.friends)

            // Never seen: selecting this tab opens the scanner instead. No title,
            // because the raised circle sits exactly here and would cover one.
            // The label is set inside the closure so it reaches the bar item
            // rather than the (invisible) content behind it.
            Color.clear
                .tabItem {
                    // An empty image reserves the slot without drawing anything
                    // under the circle that covers it.
                    Image(uiImage: UIImage()).accessibilityLabel("Quét mã QR")
                }
                .tag(Slot.scan)

            NavigationStack(path: $router.learn) { LearnHubView().withSenteRoutes() }
                .tabItem { Label("Học", systemImage: "graduationcap.fill") }
                .tag(Slot.learn)

            NavigationStack(path: $router.me) { SettingsView().withSenteRoutes() }
                .tabItem { Label("Tôi", systemImage: "person.crop.circle") }
                .tag(Slot.me)
        }
        .tint(Tokens.indigo)
        .overlay(alignment: .bottom) { scanButton }
        .sheet(isPresented: welcomePresented) {
            WelcomeView(
                onLearn: { router.push(LearnRoute.list, on: .learn) },
                onBot: { router.push(BotRoute.play, on: .learn) },
                // The welcome sheet is still animating out; presenting the next
                // sheet immediately would be silently dropped.
                onInvite: { Task { try? await Task.sleep(for: .milliseconds(450)); router.sheet = .createInvite } })
        }
        .sheet(item: $router.sheet) { sheet(for: $0) }
        .onAppear(perform: consumeLaunchArguments)
        .onChange(of: session.pendingGameID) { _, _ in openPendingGame() }
        .onChange(of: session.games) { _, _ in openPendingGame() }
        .onChange(of: session.pendingInviteCode) { _, _ in openPendingInvite() }
        .onChange(of: session.pendingFriendCode) { _, _ in openPendingFriend() }
        .onChange(of: session.openFriends) { _, _ in openPendingFriends() }
        .onChange(of: session.pendingSGF) { _, _ in openPendingSGF() }
        // Coming back to a tab's root is the moment its list should be current.
        .task(id: router.play.count) { if router.play.isEmpty { await session.refreshQuietly() } }
    }

    /// Selecting the centre slot is an action, not a destination: the scanner
    /// opens and the selection springs back to where it was.
    private var slot: Binding<Slot> {
        Binding(
            get: {
                switch router.tab {
                case .play: .play
                case .friends: .friends
                case .learn: .learn
                case .me: .me
                }
            },
            set: { new in
                switch new {
                case .scan: router.sheet = .scan
                case .play: router.tab = .play
                case .friends: router.tab = .friends
                case .learn: router.tab = .learn
                case .me: router.tab = .me
                }
            })
    }

    @ViewBuilder private var scanButton: some View {
        if !router.barHidden {
            Button { router.sheet = .scan } label: {
                Image(systemName: "qrcode.viewfinder")
                    .font(.system(size: 26, weight: .semibold))
                    .foregroundStyle(Tokens.onIndigo)
                    .frame(width: 58, height: 58)
                    .background(Tokens.indigo, in: Circle())
                    .overlay(Circle().stroke(Tokens.paper, lineWidth: 4))
                    .shadow(color: .black.opacity(0.18), radius: 8, y: 3)
            }
            .contentShape(Circle())
            .accessibilityLabel("Quét mã QR")
            // Sits over the centre slot rather than straddling the bar by a fixed
            // number: the bar's own height differs between iOS versions.
            .padding(.bottom, 14)
        }
    }

    @ViewBuilder private func sheet(for sheet: AppRouter.Sheet) -> some View {
        switch sheet {
        case .createInvite:
            CreateInviteView()
        case .join(let code):
            JoinView(initialCode: code) { game in
                router.sheet = nil
                router.push(game, on: .play)
            }
            // A new code means a new view: the old one's typed state must not linger.
            .id(code)
        case .scan:
            ScanView { scanned in
                router.sheet = nil
                // The sheet is still animating out; the next one has to wait or
                // SwiftUI drops it.
                Task {
                    try? await Task.sleep(for: .milliseconds(450))
                    act(on: scanned)
                }
            }
        case .addFriend(let code):
            AddFriendView(initialCode: code).id(code)
        case .sgf(let record, _):
            NavigationStack { LocalReplayView(record: record) }
        }
    }

    private func act(on scanned: InviteCode.Scanned) {
        switch scanned {
        case .invitation(let code):
            router.sheet = .join(code: code)
        case .friend(let code):
            router.show(.friends)
            router.sheet = .addFriend(code: code)
        case .game(let id):
            session.pendingGameID = id
            openPendingGame()
        case .malformed, .foreign:
            break
        }
    }

    // MARK: - Arrivals from outside the app

    /// Launch arguments drive screenshots and UI tests (docs/07 §12.2). Read once
    /// and cleared: under tabs an unread key would fire again on every return.
    private func consumeLaunchArguments() {
        guard !launchArgumentsRead else { return }
        launchArgumentsRead = true
        let defaults = UserDefaults.standard
        func flag(_ key: String) -> Bool {
            guard defaults.bool(forKey: key) else { return false }
            defaults.removeObject(forKey: key)
            return true
        }
        if flag("createInvite") { router.sheet = .createInvite }
        if flag("openScan") { router.sheet = .scan }
        if flag("openSettings") { router.show(.me) }
        if flag("openLocal") { router.push(LocalRoute.board, on: .play) }
        if flag("openLearn") { router.push(LearnRoute.list, on: .learn) }
        if flag("openBot") { router.push(BotRoute.play, on: .learn) }
        if flag("openWatch") { router.push(BotRoute.watch, on: .learn) }
        if flag("openPuzzle") { router.push(PuzzleRoute.today, on: .learn) }
        if flag("openFriends") { router.show(.friends) }
        if let id = defaults.string(forKey: "openLesson"),
           let lesson = LessonLibrary.shared.chapters.flatMap(\.lessons).first(where: { $0.id == id }) {
            defaults.removeObject(forKey: "openLesson")
            router.push(LearnRoute.list, on: .learn)
            router.push(lesson, on: .learn)
        }
        openPendingGame()
        openPendingInvite()
        openPendingFriend()
        openPendingFriends()
        openPendingSGF()
    }

    /// Navigates once the game is known; if the list has not loaded yet the
    /// `games` observer retries when it does.
    private func openPendingGame() {
        guard let id = session.pendingGameID,
              let game = session.games.first(where: { $0.gameId == id }) else { return }
        session.pendingGameID = nil
        router.show(.play)
        router.push(game, on: .play)
    }

    private func openPendingInvite() {
        guard let code = session.pendingInviteCode else { return }
        session.pendingInviteCode = nil
        router.sheet = .join(code: code)
    }

    /// A shared friend link opens the friends tab with the code ready to send.
    /// This is the only place that clears it, so nothing re-presents the sheet.
    private func openPendingFriend() {
        guard let code = session.pendingFriendCode else { return }
        session.pendingFriendCode = nil
        router.show(.friends)
        router.sheet = .addFriend(code: code)
    }

    /// A friend push carries no code: it just opens the screen.
    private func openPendingFriends() {
        guard session.openFriends else { return }
        session.openFriends = false
        router.show(.friends)
    }

    private func openPendingSGF() {
        guard let record = session.pendingSGF else { return }
        session.pendingSGF = nil
        router.presentSGF(record)
    }

    private var welcomePresented: Binding<Bool> {
        Binding(get: { !welcomed }, set: { if !$0 { welcomed = true } })
    }
}

/// Every tab registers the same destinations, so a route pushed from anywhere
/// resolves wherever it lands.
private struct SenteRoutes: ViewModifier {
    func body(content: Content) -> some View {
        content
            .navigationDestination(for: GameSummary.self) { game in
                if game.isActive { GameView(summary: game) } else { ReplayView(summary: game) }
            }
            .navigationDestination(for: LearnRoute.self) { _ in LearnView() }
            .navigationDestination(for: PuzzleRoute.self) { _ in DailyPuzzleView() }
            .navigationDestination(for: BotRoute.self) { route in
                switch route {
                case .play: BotPlayView()
                case .watch: BotWatchView()
                }
            }
            .navigationDestination(for: Lesson.self) { LessonPlayerView(lesson: $0) }
            .navigationDestination(for: KifuRoute.self) { _ in KifuLibraryView() }
            .navigationDestination(for: FriendsRoute.self) { _ in FriendsView() }
            .navigationDestination(for: LocalRoute.self) { _ in LocalGameView() }
            .navigationDestination(for: SettingsRoute.self) { _ in SettingsView() }
            .navigationDestination(for: KifuEntry.self) { entry in
                if let record = try? SGF.decode(entry.sgf) {
                    LocalReplayView(record: record)
                } else {
                    ContentUnavailableView("Không đọc được ván", systemImage: "exclamationmark.triangle")
                }
            }
    }
}

extension View {
    func withSenteRoutes() -> some View { modifier(SenteRoutes()) }
}

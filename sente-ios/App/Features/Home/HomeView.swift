import SwiftUI
import GoKit
import SenteNet
import SenteUI

struct HomeView: View {
    @Environment(AppSession.self) private var session
    @State private var sheet: HomeSheet?
    /// Versioned so a future, richer onboarding can show itself again.
    @AppStorage("welcomedV1") private var welcomed = false
    @State private var showSettings = false
    @State private var showLocal = false
    @State private var path = NavigationPath()

    var body: some View {
        NavigationStack(path: $path) {
            List {
                gameSections
                inviteSection
                friendsSection
                localSection
                kifuSection
                learnSection
                emptyState
            }
            .listStyle(.insetGrouped)
            .scrollContentBackground(.hidden)
            .background(Tokens.paper.ignoresSafeArea())
            .foregroundStyle(Tokens.ink)
            .navigationTitle("Sente")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    NavigationLink { SettingsView() } label: { Image(systemName: "person.crop.circle") }
                }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(spacing: 8) {
                    Button("Mời bạn chơi") { sheet = .create }.buttonStyle(PrimaryButton())
                    Button("Nhập mã lời mời") { sheet = .join(code: "") }.buttonStyle(SecondaryButton())
                }
                .padding(.horizontal, 16).padding(.vertical, 10)
                .background(Tokens.paper)
            }
            .refreshable { await session.refreshQuietly() }
            .navigationDestination(for: GameSummary.self) { game in
                if game.isActive { GameView(summary: game) } else { ReplayView(summary: game) }
            }
            // One sheet for both, so an invitation arriving while another sheet is up
            // replaces it instead of being silently dropped (SwiftUI presents one).
            .sheet(isPresented: sgfPresented) {
                if let record = session.pendingSGF {
                    NavigationStack { LocalReplayView(record: record) }
                }
            }
            .sheet(isPresented: welcomePresented) {
                WelcomeView(
                    onLearn: { path.append(LearnRoute.list) },
                    onBot: { path.append(BotRoute.play) },
                    // The welcome sheet is still animating out; presenting the next
                    // sheet immediately would be silently dropped.
                    onInvite: { Task { try? await Task.sleep(for: .milliseconds(450)); sheet = .create } })
            }
            .sheet(item: $sheet) { item in
                switch item {
                case .create:
                    CreateInviteView()
                case .join(let code):
                    JoinView(initialCode: code) { game in
                        sheet = nil
                        path.append(game)
                    }
                    // A new code means a new view: the old one's typed state must not linger.
                    .id(code)
                }
            }
            .navigationDestination(isPresented: $showSettings) { SettingsView() }
            .navigationDestination(isPresented: $showLocal) { LocalGameView() }
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
            .navigationDestination(for: KifuEntry.self) { entry in
                if let record = try? SGF.decode(entry.sgf) {
                    LocalReplayView(record: record)
                } else {
                    ContentUnavailableView("Không đọc được ván", systemImage: "exclamationmark.triangle")
                }
            }
            .onChange(of: session.pendingInviteCode) { _, code in openPendingInvite(code) }
            // The id may already be set when this view first appears (a launch
            // argument, or a link opened while the app was starting), and onChange
            // only fires for later changes.
            .onAppear {
                // `-createInvite 1` opens the invitation sheet on launch, for screenshots
                // and UI tests (docs/07 §12.2).
                if UserDefaults.standard.bool(forKey: "createInvite") { sheet = .create }
                if UserDefaults.standard.bool(forKey: "openSettings") { showSettings = true }
                if UserDefaults.standard.bool(forKey: "openLocal") { showLocal = true }
                if UserDefaults.standard.bool(forKey: "openLearn") { path.append(LearnRoute.list) }
                if UserDefaults.standard.bool(forKey: "openBot") { path.append(BotRoute.play) }
                if UserDefaults.standard.bool(forKey: "openPuzzle") { path.append(PuzzleRoute.today) }
                if UserDefaults.standard.bool(forKey: "openWatch") { path.append(BotRoute.watch) }
                if UserDefaults.standard.bool(forKey: "openFriends") { path.append(FriendsRoute.list) }
                openPendingFriends()
                if let id = UserDefaults.standard.string(forKey: "openLesson"),
                   let lesson = LessonLibrary.shared.chapters.flatMap(\.lessons).first(where: { $0.id == id }) {
                    path.append(LearnRoute.list)
                    path.append(lesson)
                }
                openPendingGame(session.pendingGameID)
                openPendingInvite(session.pendingInviteCode)
            }
            .onChange(of: session.pendingGameID) { _, id in openPendingGame(id) }
            .onChange(of: session.openFriends) { _, _ in openPendingFriends() }
            .onChange(of: session.pendingFriendCode) { _, _ in openPendingFriends() }
            .onChange(of: session.games) { _, _ in openPendingGame(session.pendingGameID) }
            .task(id: path.count) { if path.isEmpty { await session.refreshQuietly() } }
            // While one of my invitations is open, someone may accept it any moment:
            // keep the list fresh so the new game appears without a pull-to-refresh.
            .task(id: session.invitations.contains(where: \.isMine)) {
                guard session.invitations.contains(where: \.isMine) else { return }
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(5))
                    await session.refreshQuietly()
                }
            }
        }
    }

    @ViewBuilder
    private var gameSections: some View {
        if !session.myTurnGames.isEmpty {
            Section("Đến lượt bạn · \(session.myTurnGames.count)") {
                ForEach(session.myTurnGames) { game in row(game) }
            }
        }
        if !session.waitingGames.isEmpty {
            Section("Đang chờ đối thủ · \(session.waitingGames.count)") {
                ForEach(session.waitingGames) { game in row(game) }
            }
        }
        if !session.finishedGames.isEmpty {
            Section("Đã kết thúc") {
                ForEach(Array(session.finishedGames.prefix(10))) { game in row(game) }
            }
        }
    }

    @ViewBuilder
    private var inviteSection: some View {
        if !session.invitations.isEmpty {
            Section("Lời mời đang mở") {
                ForEach(session.invitations) { invite in inviteRow(invite) }
            }
        }
    }

    @ViewBuilder
    private var emptyState: some View {
        if session.games.isEmpty && session.invitations.isEmpty {
            ContentUnavailableView {
                Label("Chưa có ván nào", systemImage: "circle.grid.3x3")
            } description: {
                Text("Mời một người bạn, hoặc nhập mã lời mời bạn nhận được.")
            }
            .listRowBackground(Color.clear)
        }
    }

    /// Extracted bindings: inline closures in the body send the type-checker
    /// into the weeds once the modifier chain gets this long.
    private var sgfPresented: Binding<Bool> {
        Binding(get: { session.pendingSGF != nil },
                set: { if !$0 { session.pendingSGF = nil } })
    }

    private var welcomePresented: Binding<Bool> {
        Binding(get: { !welcomed }, set: { if !$0 { welcomed = true } })
    }

    /// A friend push, or a shared sente://f/<code> link, lands on the friends
    /// screen; the code itself is consumed there.
    private func openPendingFriends() {
        guard session.openFriends || session.pendingFriendCode != nil else { return }
        session.openFriends = false
        if !path.isEmpty { path = NavigationPath() }
        path.append(FriendsRoute.list)
    }

    private func openPendingInvite(_ code: String?) {
        guard let code else { return }
        session.pendingInviteCode = nil
        sheet = .join(code: code)
    }

    /// Navigates once the game is known; if the list has not loaded yet the
    /// `games` observer retries when it does.
    private func openPendingGame(_ id: String?) {
        guard let id, let game = session.games.first(where: { $0.gameId == id }) else { return }
        session.pendingGameID = nil
        path = NavigationPath()
        path.append(game)
    }

    private func row(_ game: GameSummary) -> some View {
        NavigationLink(value: game) {
            HStack(spacing: 13) {
                thumbnail(game)
                VStack(alignment: .leading, spacing: 2) {
                    Text(game.opponentName.isEmpty ? LS(localized: "Đang chờ người chơi") : game.opponentName)
                        .font(.callout.weight(.semibold))
                    Text("\(game.boardSize)×\(game.boardSize) · \(game.rules == "japanese" ? "Nhật" : "Trung") · nước \(game.moveNo)")
                        .font(.caption).foregroundStyle(Tokens.inkSecondary)
                }
                Spacer()
                trailing(game)
            }
        }
    }

    private func thumbnail(_ game: GameSummary) -> some View {
        RoundedRectangle(cornerRadius: 10)
            .fill(LinearGradient(colors: [Tokens.Board.woodLight, Tokens.Board.woodDark], startPoint: .topLeading, endPoint: .bottomTrailing))
            .frame(width: 48, height: 48)
            .overlay {
                Circle().fill(game.myColor == "black" ? .black : .white)
                    .frame(width: 16, height: 16).overlay(Circle().stroke(.black.opacity(0.2)))
            }
    }

    @ViewBuilder
    private func trailing(_ game: GameSummary) -> some View {
        if game.isActive {
            VStack(alignment: .trailing, spacing: 1) {
                ClockLabel(deadline: game.moveDeadline, frozenMs: 0, active: game.isActive, offset: 0)
                    .font(.subheadline)
                Text(game.yourTurn ? LS(localized: "còn lại") : LS(localized: "hạn của đối thủ"))
                    .font(.system(size: 10, weight: .semibold)).textCase(.uppercase)
                    .foregroundStyle(Tokens.inkTertiary)
            }
            .foregroundStyle(game.yourTurn ? Tokens.seal : Tokens.inkTertiary)
        } else if let result = game.result {
            Text(verdict(result, myColor: game.myColor))
                .font(.subheadline.weight(.semibold)).foregroundStyle(Tokens.inkSecondary)
        }
    }

    private func verdict(_ result: GameResultPayload, myColor: String) -> String {
        guard let winner = result.winner else { return LS(localized: "Vô hiệu") }
        return winner == myColor ? LS(localized: "Thắng") : LS(localized: "Thua")
    }

    private func inviteRow(_ invite: Challenge) -> some View {
        Button { sheet = .join(code: invite.code) } label: {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(invite.isMine ? LS(localized: "Bạn đã mời") : LS(localized: "\(invite.creatorName ?? LS(localized: "Ai đó")) mời bạn"))
                        .font(.callout.weight(.semibold))
                    Text("\(invite.config.boardSize)×\(invite.config.boardSize) · \(invite.config.timeControl.summary) · mã \(invite.code)")
                        .font(.caption).foregroundStyle(Tokens.inkSecondary)
                }
                Spacer()
                Image(systemName: "chevron.right").font(.caption).foregroundStyle(Tokens.inkTertiary)
            }
        }
    }

    private var learnSection: some View {
        Section {
            Button { path.append(LearnRoute.list) } label: {
                HStack(spacing: 12) {
                    Image(systemName: "graduationcap.fill").foregroundStyle(Tokens.indigo)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Học cờ vây").font(.callout.weight(.semibold))
                        Text("Từ luật cơ bản đến sống chết · \(LessonProgress().done.count)/\(LessonLibrary.shared.lessonCount) bài")
                            .font(.caption).foregroundStyle(Tokens.inkSecondary)
                    }
                    Spacer()
                    Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(Tokens.inkTertiary)
                }
            }
            .foregroundStyle(Tokens.ink)
            Button { path.append(PuzzleRoute.today) } label: {
                HStack(spacing: 12) {
                    Image(systemName: "flame.fill").foregroundStyle(Tokens.seal)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Tsumego hôm nay").font(.callout.weight(.semibold))
                        puzzleSubtitle
                    }
                    Spacer()
                    Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(Tokens.inkTertiary)
                }
            }
            .foregroundStyle(Tokens.ink)
            Button { path.append(BotRoute.play) } label: {
                HStack(spacing: 12) {
                    Image(systemName: "cpu").foregroundStyle(Tokens.indigo)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Đấu với máy").font(.callout.weight(.semibold))
                        Text("Bốn cấp độ, chạy trên máy, không cần mạng").font(.caption).foregroundStyle(Tokens.inkSecondary)
                    }
                    Spacer()
                    Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(Tokens.inkTertiary)
                }
            }
            .foregroundStyle(Tokens.ink)
            Button { path.append(BotRoute.watch) } label: {
                HStack(spacing: 12) {
                    Image(systemName: "eye").foregroundStyle(Tokens.indigo)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Máy đấu máy").font(.callout.weight(.semibold))
                        Text("Xem hai bot chơi, chỉnh cấp từng bên").font(.caption).foregroundStyle(Tokens.inkSecondary)
                    }
                    Spacer()
                    Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(Tokens.inkTertiary)
                }
            }
            .foregroundStyle(Tokens.ink)
        }
    }

    @ViewBuilder private var puzzleSubtitle: some View {
        let status = DailyPuzzles.status(day: DailyPuzzles.dayNumber())
        if status.doneToday {
            Text("Hôm nay đã giải ✓ · chuỗi \(status.streak) ngày").font(.caption).foregroundStyle(Tokens.inkSecondary)
        } else if status.streak > 0 {
            Text("Giữ chuỗi \(status.streak) ngày!").font(.caption).foregroundStyle(Tokens.inkSecondary)
        } else {
            Text("Một bài mỗi ngày, giải để tạo chuỗi").font(.caption).foregroundStyle(Tokens.inkSecondary)
        }
    }

    /// Friends, with the number of requests waiting for an answer -- the only
    /// thing here that someone else is waiting on. Shown to guests too: the
    /// screen behind it is what explains why signing in is worth it.
    @ViewBuilder private var friendsSection: some View {
        if session.user != nil {
            let waiting = session.friends.filter { $0.isPending && $0.incoming }.count
            Section {
                Button { path.append(FriendsRoute.list) } label: {
                    HStack(spacing: 12) {
                        Image(systemName: "person.2.badge.plus").foregroundStyle(Tokens.indigo)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Bạn bè").font(.callout.weight(.semibold))
                            Text(friendsSubtitle(waiting: waiting))
                                .font(.caption).foregroundStyle(Tokens.inkSecondary)
                        }
                        Spacer()
                        if waiting > 0 {
                            Text("\(waiting)")
                                .font(.caption.weight(.bold)).foregroundStyle(Tokens.onIndigo)
                                .padding(.horizontal, 7).padding(.vertical, 3)
                                .background(Tokens.seal, in: Capsule())
                        }
                        Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(Tokens.inkTertiary)
                    }
                }
                .foregroundStyle(Tokens.ink)
            }
        }
    }

    private func friendsSubtitle(waiting: Int) -> String {
        if waiting > 0 { return LS(localized: "\(waiting) lời mời kết bạn đang chờ") }
        if session.user?.isGuest == true { return LS(localized: "Đăng nhập Apple để kết bạn") }
        let count = session.friends.filter(\.isAccepted).count
        return count > 0 ? LS(localized: "\(count) người bạn · mời chơi nhanh")
                         : LS(localized: "Thêm bạn bằng mã để mời chơi nhanh")
    }

    /// Finished on-device games: replayable, analysable, exportable.
    @ViewBuilder private var kifuSection: some View {
        let count = FileKifuLibrary().load().count
        if count > 0 {
            Section {
                Button { path.append(KifuRoute.list) } label: {
                    HStack(spacing: 12) {
                        Image(systemName: "books.vertical.fill").foregroundStyle(Tokens.indigo)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Ván đã lưu").font(.callout.weight(.semibold))
                            Text("\(count) ván trên máy này · xem lại và phân tích")
                                .font(.caption).foregroundStyle(Tokens.inkSecondary)
                        }
                        Spacer()
                        Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(Tokens.inkTertiary)
                    }
                }
                .foregroundStyle(Tokens.ink)
            }
        }
    }

    /// Pass-and-play needs no account: it is offered even when the list is empty.
    private var localSection: some View {
        Section("Trên máy này") {
            Button { showLocal = true } label: {
                HStack(spacing: 12) {
                    Image(systemName: "person.2.fill").foregroundStyle(Tokens.indigo)
                    VStack(alignment: .leading, spacing: 2) {
                        if let saved = FileLocalGameStorage().load(), !saved.moves.isEmpty {
                            Text("Tiếp tục ván trên máy này").font(.callout.weight(.semibold))
                            Text("\(saved.config.size)×\(saved.config.size) · nước \(saved.moves.count) · \(saved.config.blackName) vs \(saved.config.whiteName)")
                                .font(.caption).foregroundStyle(Tokens.inkSecondary)
                        } else {
                            Text("Hai người, một máy").font(.callout.weight(.semibold))
                            Text("Chơi offline, không cần tài khoản").font(.caption).foregroundStyle(Tokens.inkSecondary)
                        }
                    }
                    Spacer()
                    Image(systemName: "chevron.right").font(.caption.weight(.semibold)).foregroundStyle(Tokens.inkTertiary)
                }
            }
            .foregroundStyle(Tokens.ink)
        }
    }
}

/// The lesson catalogue as a pushable route, so Learn and its lessons travel
/// through the same NavigationPath as everything else.
enum LearnRoute: Hashable { case list }

/// The two bot screens, pushed through the same path.
enum BotRoute: Hashable { case play, watch }

enum PuzzleRoute: Hashable { case today }

/// What the home screen can present. `Identifiable` by content, so switching from
/// one invitation code to another re-presents rather than reuses.
private enum HomeSheet: Identifiable, Equatable {
    case create
    case join(code: String)

    var id: String {
        switch self {
        case .create: "create"
        case .join(let code): "join:\(code)"
        }
    }
}

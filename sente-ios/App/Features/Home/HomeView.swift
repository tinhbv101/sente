import SwiftUI
import SenteNet
import SenteUI

struct HomeView: View {
    @Environment(AppSession.self) private var session
    @State private var sheet: HomeSheet?
    @State private var showSettings = false
    @State private var showLocal = false
    @State private var path = NavigationPath()

    var body: some View {
        NavigationStack(path: $path) {
            List {
                gameSections
                inviteSection
                localSection
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
                openPendingGame(session.pendingGameID)
                openPendingInvite(session.pendingInviteCode)
            }
            .onChange(of: session.pendingGameID) { _, id in openPendingGame(id) }
            .onChange(of: session.games) { _, _ in openPendingGame(session.pendingGameID) }
            .task(id: path.count) { if path.isEmpty { await session.refreshQuietly() } }
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
                    Text(game.opponentName.isEmpty ? "Đang chờ người chơi" : game.opponentName)
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
                Text(game.yourTurn ? "còn lại" : "hạn của đối thủ")
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
        guard let winner = result.winner else { return "Vô hiệu" }
        return winner == myColor ? "Thắng" : "Thua"
    }

    private func inviteRow(_ invite: Challenge) -> some View {
        Button { sheet = .join(code: invite.code) } label: {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(invite.isMine ? "Bạn đã mời" : "\(invite.creatorName ?? "Ai đó") mời bạn")
                        .font(.callout.weight(.semibold))
                    Text("\(invite.config.boardSize)×\(invite.config.boardSize) · \(invite.config.timeControl.summary) · mã \(invite.code)")
                        .font(.caption).foregroundStyle(Tokens.inkSecondary)
                }
                Spacer()
                Image(systemName: "chevron.right").font(.caption).foregroundStyle(Tokens.inkTertiary)
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

import SwiftUI
import SenteNet
import SenteUI

struct HomeView: View {
    @Environment(AppSession.self) private var session
    @State private var showCreate = false
    @State private var joinCode: String?
    @State private var path = NavigationPath()

    var body: some View {
        NavigationStack(path: $path) {
            List {
                gameSections
                inviteSection
                emptyState
            }
            .listStyle(.insetGrouped)
            .scrollContentBackground(.hidden)
            .background(Tokens.paper.ignoresSafeArea())
            .navigationTitle("Sente")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    NavigationLink { SettingsView() } label: { Image(systemName: "person.crop.circle") }
                }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(spacing: 8) {
                    Button("Mời bạn chơi") { showCreate = true }.buttonStyle(PrimaryButton())
                    Button("Nhập mã lời mời") { joinCode = "" }.buttonStyle(SecondaryButton())
                }
                .padding(.horizontal, 16).padding(.vertical, 10)
                .background(Tokens.paper)
            }
            .refreshable { await session.refreshQuietly() }
            .navigationDestination(for: GameSummary.self) { GameView(summary: $0) }
            .sheet(isPresented: $showCreate) { CreateInviteView() }
            .sheet(item: joinBinding) { target in
                JoinView(initialCode: target.code) { game in
                    joinCode = nil
                    path.append(game)
                }
            }
            .onChange(of: session.pendingInviteCode) { _, code in
                if let code { joinCode = code; session.pendingInviteCode = nil }
            }
            // The id may already be set when this view first appears (a launch
            // argument, or a link opened while the app was starting), and onChange
            // only fires for later changes.
            .onAppear { openPendingGame(session.pendingGameID) }
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

    /// Navigates once the game is known; if the list has not loaded yet the
    /// `games` observer retries when it does.
    private func openPendingGame(_ id: String?) {
        guard let id, let game = session.games.first(where: { $0.gameId == id }) else { return }
        session.pendingGameID = nil
        path = NavigationPath()
        path.append(game)
    }

    private var joinBinding: Binding<JoinTarget?> {
        Binding(get: { joinCode.map(JoinTarget.init) }, set: { joinCode = $0?.code })
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
        Button { joinCode = invite.code } label: {
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
        .foregroundStyle(Tokens.ink)
    }
}

private struct JoinTarget: Identifiable {
    let code: String
    var id: String { code }
}

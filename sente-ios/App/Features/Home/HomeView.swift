import SwiftUI
import GoKit
import SenteNet
import SenteUI

/// The Play tab: games in progress, invitations, and the two ways to play
/// without an opponent online. Everything that teaches lives in the Learn tab,
/// and the account in the Me tab.
struct HomeView: View {
    @Environment(AppSession.self) private var session
    @Environment(AppRouter.self) private var router

    var body: some View {
        List {
            gameSections
            inviteSection
            localSection
            kifuSection
            emptyState
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .background(Tokens.paper.ignoresSafeArea())
        .foregroundStyle(Tokens.ink)
        .navigationTitle("Sente")
        .safeAreaInset(edge: .bottom) {
            VStack(spacing: 8) {
                Button("Mời bạn chơi") { router.sheet = .createInvite }.buttonStyle(PrimaryButton())
                Button("Nhập mã lời mời") { router.sheet = .join(code: "") }.buttonStyle(SecondaryButton())
            }
            .padding(.horizontal, 16).padding(.vertical, 10)
            .background(Tokens.paper)
        }
        .refreshable { await session.refreshQuietly() }
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
                Text("Mời một người bạn, quét mã QR của họ, hoặc chơi ngay trên máy này.")
            }
            .listRowBackground(Color.clear)
        }
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
        Button { router.sheet = .join(code: invite.code) } label: {
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

    /// Finished on-device games: replayable, analysable, exportable.
    @ViewBuilder private var kifuSection: some View {
        let count = FileKifuLibrary().load().count
        if count > 0 {
            Section {
                NavigationLink(value: KifuRoute.list) {
                    HStack(spacing: 12) {
                        Image(systemName: "books.vertical.fill").foregroundStyle(Tokens.indigo)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Ván đã lưu").font(.callout.weight(.semibold))
                            Text("\(count) ván trên máy này · xem lại và phân tích")
                                .font(.caption).foregroundStyle(Tokens.inkSecondary)
                        }
                    }
                }
                .foregroundStyle(Tokens.ink)
            }
        }
    }

    /// Pass-and-play needs no account: it is offered even when the list is empty.
    private var localSection: some View {
        Section("Trên máy này") {
            NavigationLink(value: LocalRoute.board) {
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

/// Pass-and-play as a value route. It used to be reached by
/// navigationDestination(isPresented:), which cannot share a path with value
/// pushes -- the mix push-then-pops (docs/07, and the router now pushes here).
enum LocalRoute: Hashable { case board }

enum SettingsRoute: Hashable { case main }

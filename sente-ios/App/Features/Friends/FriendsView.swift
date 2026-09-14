import SwiftUI
import SenteNet
import SenteUI

enum FriendsRoute: Hashable { case list }

/// Friends (docs/01 FR-A3). Only an account signed in with Apple can have them:
/// a guest account belongs to a phone, so a friendship made from one would not
/// survive a reinstall.
struct FriendsView: View {
    @Environment(AppSession.self) private var session
    @State private var addingCode = false
    @State private var busy: String?
    @State private var error: String?
    @State private var invitee: FriendSummary?

    private var incoming: [FriendSummary] { session.friends.filter { $0.isPending && $0.incoming } }
    private var outgoing: [FriendSummary] { session.friends.filter { $0.isPending && !$0.incoming } }
    private var accepted: [FriendSummary] { session.friends.filter(\.isAccepted) }

    var body: some View {
        List {
            if session.user?.isGuest == true {
                appleRequired
                blockedSection
            } else {
                if !incoming.isEmpty {
                    Section("Lời mời kết bạn · \(incoming.count)") {
                        ForEach(incoming) { friend in incomingRow(friend) }
                    }
                }
                if !accepted.isEmpty {
                    Section("Bạn bè · \(accepted.count)") {
                        ForEach(accepted) { friend in friendRow(friend) }
                    }
                }
                if !outgoing.isEmpty {
                    Section("Đã gửi") {
                        ForEach(outgoing) { friend in outgoingRow(friend) }
                    }
                }
                myCodeSection
                blockedSection
                if accepted.isEmpty && incoming.isEmpty && outgoing.isEmpty { emptyState }
            }
            if let error {
                Section { Text(error).font(.footnote).foregroundStyle(.red) }
            }
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .background(Tokens.paper.ignoresSafeArea())
        .foregroundStyle(Tokens.ink)
        .navigationTitle("Bạn bè")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if session.user?.isGuest == false {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { addingCode = true } label: { Image(systemName: "person.badge.plus") }
                        .accessibilityLabel("Thêm bạn bằng mã")
                }
            }
        }
        .refreshable { await load() }
        .task { await load() }
        // A shared sente://f/<code> link opens the add sheet with the code ready.
        // Both paths wait for the profile: a cold start from a link reaches the
        // screen before it is known whether this account may add anyone.
        .onChange(of: session.pendingFriendCode) { _, _ in openAddSheetIfAllowed() }
        .onChange(of: session.user?.isGuest) { _, _ in openAddSheetIfAllowed() }
        .onAppear { openAddSheetIfAllowed() }
        .sheet(isPresented: $addingCode) {
            AddFriendView(initialCode: session.pendingFriendCode ?? "")
                .onDisappear { session.pendingFriendCode = nil }
        }
        .sheet(item: $invitee) { friend in
            CreateInviteView(invitee: friend)
        }
    }

    /// Friends come with the session refresh; blocks are only needed here, so
    /// they are fetched by the screen that shows them.
    private func load() async {
        await session.refreshQuietly()
        if session.user != nil {
            blocked = (try? await session.api.blockedPlayers()) ?? blocked
        }
        if session.user?.isGuest == false {
            await session.registerForPushIfUseful(force: true)
        }
    }

    private func openAddSheetIfAllowed() {
        guard session.pendingFriendCode != nil, session.user?.isGuest == false else { return }
        addingCode = true
    }

    private var appleRequired: some View {
        Section {
            ContentUnavailableView {
                Label("Cần đăng nhập Apple", systemImage: "person.crop.circle.badge.exclamationmark")
            } description: {
                Text("Kết bạn cần một tài khoản thật. Đăng nhập bằng Apple để bạn bè tìm được bạn kể cả khi bạn đổi máy.")
            } actions: {
                // Telling someone to go to Settings without taking them there is
                // how a dead end is written.
                NavigationLink { SettingsView() } label: { Text("Mở Cài đặt") }
                    .buttonStyle(.borderedProminent).tint(Tokens.indigo)
            }
            .listRowBackground(Color.clear)
        }
    }

    private var emptyState: some View {
        Section {
            ContentUnavailableView {
                Label("Chưa có bạn nào", systemImage: "person.2")
            } description: {
                Text("Đưa mã bạn bè của bạn cho bạn bè, hoặc nhập mã của họ để gửi lời mời.")
            }
            .listRowBackground(Color.clear)
        }
    }

    private var myCodeSection: some View {
        Section {
            if let user = session.user {
                let link = InviteCode.friendLink(for: user.friendCode)
                ShareLink(item: LS(localized: "Kết bạn với mình trên Sente nhé! Mã của mình là \(user.friendCode): \(link)")) {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Mã bạn bè của bạn").font(.callout.weight(.semibold))
                            Text(user.friendCode)
                                .font(.system(.body, design: .monospaced)).tracking(3)
                                .foregroundStyle(Tokens.inkSecondary)
                        }
                        Spacer()
                        Image(systemName: "square.and.arrow.up").foregroundStyle(Tokens.indigo)
                    }
                }
                .foregroundStyle(Tokens.ink)
            }
        } footer: {
            Text("Chỉ người có mã này mới gửi được lời mời kết bạn cho bạn.")
        }
    }

    @ViewBuilder private var blockedSection: some View {
        if !blocked.isEmpty {
            Section("Đã chặn") {
                ForEach(blocked) { person in
                    HStack {
                        Text(person.displayName)
                        Spacer()
                        Button("Bỏ chặn") { Task { await unblock(person) } }
                            .buttonStyle(.borderless).font(.caption.weight(.semibold))
                            .disabled(busy == person.userId)
                    }
                }
            }
        }
    }

    @State private var blocked: [BlockedPlayer] = []

    private func incomingRow(_ friend: FriendSummary) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            nameLine(friend)
            HStack(spacing: 8) {
                Button("Đồng ý") { Task { await act(friend) { try await session.api.acceptFriend(userID: $0) } } }
                    .buttonStyle(.borderedProminent).font(.caption.weight(.semibold))
                Button("Từ chối") { Task { await act(friend) { try await session.api.declineFriend(userID: $0) } } }
                    .buttonStyle(.bordered).font(.caption.weight(.semibold))
            }
            .disabled(busy == friend.userId)
        }
        .padding(.vertical, 2)
    }

    private func friendRow(_ friend: FriendSummary) -> some View {
        HStack {
            nameLine(friend)
            Spacer()
            Button("Mời chơi") { invitee = friend }
                .buttonStyle(.borderless).font(.caption.weight(.semibold))
        }
        .swipeActions(edge: .trailing) {
            Button("Bỏ kết bạn", role: .destructive) {
                Task { await act(friend) { try await session.api.removeFriend(userID: $0) } }
            }
            Button("Chặn") { Task { await block(friend) } }.tint(.orange)
        }
    }

    private func outgoingRow(_ friend: FriendSummary) -> some View {
        HStack {
            nameLine(friend)
            Spacer()
            Text("Đang chờ").font(.caption).foregroundStyle(Tokens.inkTertiary)
        }
        .swipeActions(edge: .trailing) {
            Button("Thu hồi", role: .destructive) {
                Task { await act(friend) { try await session.api.removeFriend(userID: $0) } }
            }
        }
    }

    private func nameLine(_ friend: FriendSummary) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(friend.displayName).font(.callout.weight(.semibold))
            Text(friend.friendCode).font(.caption.monospaced()).foregroundStyle(Tokens.inkTertiary)
        }
    }

    /// Every mutation ends in a refresh, so the list is the server's answer
    /// rather than a guess made on this phone.
    private func act(_ friend: FriendSummary,
                     _ call: @escaping (String) async throws -> Void) async {
        busy = friend.userId
        defer { busy = nil }
        do {
            try await call(friend.userId)
            error = nil
            await session.refreshQuietly()
        } catch let apiError as APIError {
            error = apiError.userMessage
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func block(_ friend: FriendSummary) async {
        await act(friend) { try await session.api.block(userID: $0) }
        blocked = (try? await session.api.blockedPlayers()) ?? blocked
    }

    private func unblock(_ person: BlockedPlayer) async {
        busy = person.userId
        defer { busy = nil }
        do {
            try await session.api.unblock(userID: person.userId)
            blocked = try await session.api.blockedPlayers()
        } catch let apiError as APIError {
            error = apiError.userMessage
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// Add by code: look the code up first, so the person confirms a name rather
/// than firing a request at eight characters they may have mistyped.
struct AddFriendView: View {
    let initialCode: String
    @Environment(AppSession.self) private var session
    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    @State private var found: FoundPlayer?
    @State private var error: String?
    @State private var busy = false
    @State private var outcome: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Mã bạn bè", text: $code)
                        .font(.system(.title3, design: .monospaced)).tracking(3)
                        .textInputAutocapitalization(.characters)
                        .autocorrectionDisabled()
                        .onChange(of: code) { _, _ in found = nil; outcome = nil; error = nil }
                } footer: {
                    Text("Mã gồm 8 ký tự, ví dụ ABCD2345. Hỏi bạn của bạn trong mục Bạn bè trên máy của họ.")
                }
                if let found {
                    Section("Tìm thấy") {
                        VStack(alignment: .leading, spacing: 3) {
                            Text(found.displayName).font(.callout.weight(.semibold))
                            Text(found.friendCode).font(.caption.monospaced())
                                .foregroundStyle(Tokens.inkSecondary)
                        }
                    }
                }
                if let outcome {
                    Section {
                        // They had already asked us, so asking back is agreement.
                        Label(outcome == "accepted" ? LS(localized: "Đã là bạn bè")
                                                    : LS(localized: "Đã gửi lời mời kết bạn"),
                              systemImage: "checkmark")
                            .foregroundStyle(.green).font(.callout.weight(.semibold))
                    }
                }
                if let error {
                    Section { Text(error).font(.footnote).foregroundStyle(.red) }
                }
            }
            .navigationTitle("Thêm bạn")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Đóng") { dismiss() } } }
            .safeAreaInset(edge: .bottom) {
                Button {
                    Task { found == nil ? await lookUp() : await send() }
                } label: {
                    if busy { ProgressView().tint(.white) }
                    else { Text(found == nil ? LS(localized: "Tìm") : LS(localized: "Gửi lời mời")) }
                }
                .buttonStyle(PrimaryButton())
                .disabled(busy || outcome != nil || code.trimmingCharacters(in: .whitespaces).count != 8)
                .padding(16)
            }
            .onAppear { if code.isEmpty { code = initialCode } }
        }
    }

    private func lookUp() async {
        busy = true
        defer { busy = false }
        do {
            found = try await session.api.findPlayer(code: code)
            error = nil
        } catch let apiError as APIError {
            found = nil
            error = apiError.userMessage
        } catch {
            found = nil
            self.error = error.localizedDescription
        }
    }

    private func send() async {
        guard let found else { return }
        busy = true
        defer { busy = false }
        do {
            outcome = try await session.api.addFriend(userID: found.userId)
            error = nil
            await session.refreshQuietly()
        } catch let apiError as APIError {
            error = apiError.userMessage
        } catch {
            self.error = error.localizedDescription
        }
    }
}

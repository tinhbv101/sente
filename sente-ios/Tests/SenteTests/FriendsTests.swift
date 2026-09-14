import XCTest
import SenteNet
@testable import Sente

/// The client half of friends: what the app decides on its own, without a
/// server — link routing, push routing, and how a list is split into sections.
@MainActor
final class FriendsTests: XCTestCase {
    private func friend(_ id: String, status: String, incoming: Bool) -> FriendSummary {
        Fixture.decode(FriendSummary.self, """
        {"user_id":"\(id)","display_name":"Người \(id)","friend_code":"ABCD234\(id)",
         "status":"\(status)","incoming":\(incoming),"created_at":"2026-09-11T08:00:00Z"}
        """)
    }

    func testAFriendLinkOpensTheFriendsScreenWithTheCode() {
        let session = AppSession()
        session.handle(url: URL(string: "sente://f/ABCD2345")!)
        XCTAssertEqual(session.pendingFriendCode, "ABCD2345")
        XCTAssertNil(session.pendingInviteCode, "a friend code is not an invitation code")

        // The universal-link form carries the kind in the path instead.
        let other = AppSession()
        other.handle(url: URL(string: "https://sente.devlord.net/f/wxyz6789")!)
        XCTAssertEqual(other.pendingFriendCode, "WXYZ6789")
    }

    func testAnInvitationLinkStillRoutesToTheInvitation() {
        let session = AppSession()
        session.handle(url: URL(string: "sente://j/ABCD2345")!)
        XCTAssertEqual(session.pendingInviteCode, "ABCD2345")
        XCTAssertNil(session.pendingFriendCode)
    }

    func testFriendLinkCarriesItsOwnPathSoCodesCannotBeConfused() {
        let link = InviteCode.friendLink(for: "ABCD2345")
        XCTAssertTrue(link.contains("/f/"), link)
        // Both kinds of code look identical, so an invitation parser must refuse
        // a friend link outright rather than reading the code out of it.
        XCTAssertNil(InviteCode.parse(link))
    }

    func testTheListSplitsIntoWaiting_FriendsAndSent() {
        let all = [
            friend("1", status: "accepted", incoming: false),
            friend("2", status: "pending", incoming: true),
            friend("3", status: "pending", incoming: false),
        ]
        XCTAssertEqual(all.filter { $0.isPending && $0.incoming }.map(\.userId), ["2"])
        XCTAssertEqual(all.filter { $0.isPending && !$0.incoming }.map(\.userId), ["3"])
        XCTAssertEqual(all.filter(\.isAccepted).map(\.userId), ["1"])
    }

    func testAGuestHasNoFriendsList() async {
        let session = AppSession()
        XCTAssertTrue(session.friends.isEmpty)
        // A guest cannot reach the endpoints at all; the screen says so instead
        // of showing an empty list that looks like a bug.
        XCTAssertNil(session.user)
    }
}

/// Push routing: a friend notification has no game, and that used to be exactly
/// the case the foreground handler swallowed.
@MainActor
final class FriendPushTests: XCTestCase {
    func testAFriendPushOpensTheFriendsScreen() {
        var openedFriends = false
        var openedGame: String?
        AppDelegate.onOpenFriends = { openedFriends = true }
        AppDelegate.onOpenGame = { openedGame = $0 }
        defer { AppDelegate.onOpenFriends = nil; AppDelegate.onOpenGame = nil }

        AppDelegate.route(["kind": "friend", "friend_id": "u1"])
        XCTAssertTrue(openedFriends)
        XCTAssertNil(openedGame)
    }

    func testAGamePushStillOpensTheGame() {
        var openedFriends = false
        var openedGame: String?
        AppDelegate.onOpenFriends = { openedFriends = true }
        AppDelegate.onOpenGame = { openedGame = $0 }
        defer { AppDelegate.onOpenFriends = nil; AppDelegate.onOpenGame = nil }

        AppDelegate.route(["kind": "turn", "game_id": "g1"])
        XCTAssertEqual(openedGame, "g1")
        XCTAssertFalse(openedFriends)
    }

}

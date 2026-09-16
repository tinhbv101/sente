import XCTest
import GoKit
@testable import Sente

/// One scanner serves two flows, so everything rests on telling the two kinds of
/// code apart. They are the same length from the same alphabet: only the link's
/// path can say which is which.
final class ScanClassificationTests: XCTestCase {
    func testInvitationLinksInEveryForm() {
        for payload in ["sente://j/ABCD2345",
                        "https://sente.devlord.net/j/ABCD2345",
                        "https://sente.devlord.net/j/abcd2345"] {
            XCTAssertEqual(InviteCode.classify(payload), .invitation("ABCD2345"), payload)
        }
    }

    func testFriendLinksInEveryForm() {
        for payload in ["sente://f/ABCD2345",
                        "https://sente.devlord.net/f/ABCD2345",
                        "https://sente.devlord.net/f/abcd2345"] {
            XCTAssertEqual(InviteCode.classify(payload), .friend("ABCD2345"), payload)
        }
    }

    /// The same eight characters mean different things under /j/ and /f/. If this
    /// ever collapses, scanning a friend would join a game with a stranger.
    func testTheSameCodeMeansDifferentThingsUnderEachPath() {
        XCTAssertEqual(InviteCode.classify("sente://j/ABCD2345"), .invitation("ABCD2345"))
        XCTAssertEqual(InviteCode.classify("sente://f/ABCD2345"), .friend("ABCD2345"))
    }

    /// A game link carries a UUID, which the eight-character rule would reject.
    func testGameLinksSurviveTheCodeNormaliser() {
        let id = "018f4c2a-1b2c-7d3e-9f00-1a2b3c4d5e6f"
        XCTAssertEqual(InviteCode.classify("sente://g/\(id)"), .game(id))
        XCTAssertEqual(InviteCode.classify("https://sente.devlord.net/g/\(id)"), .game(id))
    }

    /// A bare code predates the friend format, so it stays an invitation.
    func testABareCodeIsStillAnInvitation() {
        XCTAssertEqual(InviteCode.classify("ABCD2345"), .invitation("ABCD2345"))
        XCTAssertEqual(InviteCode.classify(" abcd2345 "), .invitation("ABCD2345"))
    }

    func testAnythingElseIsRefused() {
        for payload in ["https://example.com", "WIFI:S:home;T:WPA;P:secret;;",
                        "hello world", "", "https://sente.devlord.net/f/TOOSHORT1"] {
            let verdict = InviteCode.classify(payload)
            XCTAssertTrue(verdict == .foreign || verdict == .malformed,
                          "\(payload) classified as \(verdict)")
        }
    }

    /// The code and the letters barred from it (I, O, 0, 1) must not slip through.
    func testAmbiguousLettersAreRejected() {
        XCTAssertEqual(InviteCode.classify("sente://f/ABCD234I"), .malformed)
        XCTAssertEqual(InviteCode.classify("sente://j/ABCD2340"), .malformed)
    }

    /// A friend link minted against a test server must point at that server, or
    /// the lookup runs against an account that does not exist there.
    func testFriendLinkFollowsTheServer() {
        let custom = InviteCode.friendLink(for: "ABCD2345",
                                           server: URL(string: "https://go.example.test")!)
        XCTAssertEqual(custom, "https://go.example.test/f/ABCD2345")
        XCTAssertTrue(InviteCode.friendLink(for: "ABCD2345").contains("sente.devlord.net"))
        // And it still classifies as a friend code wherever it was minted.
        XCTAssertEqual(InviteCode.classify(custom), .friend("ABCD2345"))
    }
}

/// The router is what every arrival steers, so its own rules are worth pinning.
@MainActor
final class AppRouterTests: XCTestCase {
    func testShowingATabClearsWhatWasPushedOnIt() {
        let router = AppRouter()
        router.push(LearnRoute.list, on: .learn)
        router.push(BotRoute.play, on: .learn)
        XCTAssertEqual(router.learn.count, 2)
        XCTAssertEqual(router.tab, .learn)

        router.show(.learn)
        XCTAssertTrue(router.learn.isEmpty, "an arriving link lands on the screen, not under it")
    }

    func testPushingSwitchesTabAndLeavesTheOthersAlone() {
        let router = AppRouter()
        router.push(LearnRoute.list, on: .learn)
        router.push(KifuRoute.list, on: .play)
        XCTAssertEqual(router.tab, .play)
        XCTAssertEqual(router.play.count, 1)
        XCTAssertEqual(router.learn.count, 1, "the other tab keeps its stack")
    }

    /// Two files opened in a row must both present; a constant id would drop the
    /// second silently.
    func testEachSGFPresentationHasItsOwnIdentity() {
        let router = AppRouter()
        let record = GameRecord(size: 9)
        router.presentSGF(record)
        let first = router.sheet?.id
        router.presentSGF(record)
        XCTAssertNotEqual(first, router.sheet?.id)
    }
}

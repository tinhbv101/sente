import XCTest
import SenteNet
@testable import Sente

/// The "waiting for a friend" poll: the share sheet watches its invitation and
/// hands the home screen the game id the moment someone accepts.
@MainActor
final class InviteWatchTests: XCTestCase {
    private func challenge(status: String, gameID: String? = nil) -> Challenge {
        Fixture.decode(Challenge.self, """
        {"code":"AB12CD","creator_color":"black","status":"\(status)",
         \(gameID.map { "\"game_id\":\"\($0)\"," } ?? "")
         "config":{"board_size":9,"rules":"japanese","komi":6.5,"handicap":0,
                   "time_control":{"kind":"absolute","main_time_ms":600000}},
         "expires_at":"2026-12-01T00:00:00Z","is_mine":true}
        """)
    }

    func testAcceptanceEndsThePollWithTheGame() async {
        let session = AppSession()
        var polls = 0
        let gameID = await session.waitForAcceptance(code: "AB12CD", interval: .milliseconds(1)) { _ in
            polls += 1
            return self.challenge(status: polls < 3 ? "pending" : "accepted",
                                  gameID: polls < 3 ? nil : "g42")
        }
        XCTAssertEqual(gameID, "g42")
        XCTAssertEqual(polls, 3)
        XCTAssertEqual(session.pendingGameID, "g42", "the home screen navigates from this")
    }

    func testADeadInvitationStopsThePoll() async {
        let session = AppSession()
        let gameID = await session.waitForAcceptance(code: "AB12CD", interval: .milliseconds(1)) { _ in
            self.challenge(status: "cancelled")
        }
        XCTAssertNil(gameID)
        XCTAssertNil(session.pendingGameID)
    }

    func testServerHiccupsAreRetriedNotFatal() async {
        let session = AppSession()
        var polls = 0
        let gameID = await session.waitForAcceptance(code: "AB12CD", interval: .milliseconds(1)) { _ in
            polls += 1
            if polls == 1 { throw APIError.transport("offline") }
            return self.challenge(status: "accepted", gameID: "g7")
        }
        XCTAssertEqual(gameID, "g7")
    }
}

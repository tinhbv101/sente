import XCTest
import GoKit
import SenteNet
@testable import Sente

/// Quick chat on the client: an incoming line becomes a bubble, sending goes
/// out over the transport, and the sounds the feedback layer needs ship.
@MainActor
final class QuickChatTests: XCTestCase {
    func testChatEventBecomesABubbleAndSendingGoesOut() async {
        let store = GameStore(gameID: "g1", myColor: .black)
        let transport = FakeTransport()
        await store.connect(transport: transport)

        await store.handle(.chat(by: "white", code: "gg"))
        XCTAssertEqual(store.incomingChat?.code, "gg")
        XCTAssertEqual(store.incomingChat?.by, .white)

        store.sendChat("hi")
        try? await Task.sleep(for: .milliseconds(80))
        guard case .chat(let code)? = transport.sent.last else {
            return XCTFail("the chat command never reached the transport")
        }
        XCTAssertEqual(code, "hi")

        store.dismissChat()
        XCTAssertNil(store.incomingChat)
        await store.disconnect()
    }

    func testTheVocabularyIsLocalisedAndDecodes() throws {
        for code in QuickChat.codes {
            XCTAssertFalse(QuickChat.text(code).isEmpty, code)
            XCTAssertNotEqual(QuickChat.text(code), code, "\(code) must map to a phrase")
        }
        // The wire event decodes into the enum case the store consumes.
        let event = try ProtocolDecoder.event(from: Fixture.decode(Message.self, """
        {"type":"chat","ts":"2026-09-02T08:00:00Z","payload":{"game_id":"g1","by":"black","code":"hi"}}
        """))
        guard case .chat(let by, let code)? = event else { return XCTFail("not a chat event") }
        XCTAssertEqual(by, "black")
        XCTAssertEqual(code, "hi")
    }

    func testStoneSoundsShipInTheBundle() {
        XCTAssertNotNil(Bundle.main.url(forResource: "stone", withExtension: "wav"))
        XCTAssertNotNil(Bundle.main.url(forResource: "capture", withExtension: "wav"))
    }
}

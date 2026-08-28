import Foundation
import XCTest
@testable import SenteNet

/// Fixtures copied from what the Go server emits (internal/httpapi/protocol.go).
/// If a field name changes on either side, one of these stops decoding.
final class ProtocolTests: XCTestCase {
    private func message(_ json: String) throws -> Message {
        try ProtocolDecoder.json.decode(Message.self, from: Data(json.utf8))
    }

    func testGameStateDecodes() throws {
        let raw = """
        {"id":"s-1","type":"game_state","ts":"2026-08-28T09:14:03.221Z","payload":{
          "game_id":"01a0","phase":"playing","rules":"japanese","rules_version":"1.0.0",
          "board_size":9,"komi":6.5,"handicap":0,
          "board":"\(String(repeating: ".", count: 81))",
          "board_hash":"0x0000000000000000","to_play":"black","move_no":0,"ko_point":null,
          "captures":{"black":0,"white":0},"consecutive_passes":0,
          "clock":{"black":{"main_ms":600000},"white":{"main_ms":600000},
                   "move_deadline":"2026-08-28T09:24:03.221Z"},
          "server_time":"2026-08-28T09:14:03.221847913Z"}}
        """
        guard case .gameState(let state) = try ProtocolDecoder.event(from: try message(raw)) else {
            return XCTFail("expected game_state")
        }
        XCTAssertEqual(state.boardSize, 9)
        XCTAssertEqual(state.board.count, 81)
        XCTAssertEqual(state.toPlay, "black")
        XCTAssertEqual(state.clock.black.mainMs, 600_000)
        XCTAssertNotNil(state.clock.moveDeadline)
        XCTAssertNil(state.koPoint)
        // Go writes nanoseconds; the decoder must cope.
        XCTAssertEqual(Calendar(identifier: .gregorian).component(.year, from: state.serverTime), 2026)
    }

    func testMoveMadeDecodes() throws {
        let raw = """
        {"type":"move_made","ts":"2026-08-28T09:14:03Z","payload":{
          "game_id":"g","move_no":5,"color":"white","kind":"play","point":"Q16",
          "captured":["R16","R15"],"board_hash":"0x71bc0e0000000000",
          "clock":{"black":{"main_ms":1,"periods_left":3,"period_ms":30000},
                   "white":{"main_ms":2},"move_deadline":"2026-08-28T09:14:33Z"}}}
        """
        guard case .moveMade(let made) = try ProtocolDecoder.event(from: try message(raw)) else {
            return XCTFail("expected move_made")
        }
        XCTAssertEqual(made.moveNo, 5)
        XCTAssertEqual(made.point, "Q16")
        XCTAssertEqual(made.captured, ["R16", "R15"])
        XCTAssertEqual(made.clock.black.periodsLeft, 3)
    }

    func testPassHasNoPoint() throws {
        let raw = """
        {"type":"move_made","ts":"2026-08-28T09:14:03Z","payload":{
          "game_id":"g","move_no":6,"color":"black","kind":"pass","board_hash":"0x0",
          "clock":{"black":{"main_ms":1},"white":{"main_ms":2}}}}
        """
        guard case .moveMade(let made) = try ProtocolDecoder.event(from: try message(raw)) else {
            return XCTFail("expected move_made")
        }
        XCTAssertEqual(made.kind, "pass")
        XCTAssertNil(made.point)
        XCTAssertNil(made.captured)
    }

    func testScoringStateAndGameOver() throws {
        let scoring = """
        {"type":"scoring_state","ts":"2026-08-28T09:14:03Z","payload":{
          "game_id":"g","dead_points":["C7"],"black_accepted":true,"white_accepted":false,
          "score":{"black":40,"white":40.5,"margin":0.5}}}
        """
        guard case .scoringState(let state) = try ProtocolDecoder.event(from: try message(scoring)) else {
            return XCTFail("expected scoring_state")
        }
        XCTAssertEqual(state.deadPoints, ["C7"])
        XCTAssertTrue(state.blackAccepted)
        XCTAssertEqual(state.score?.margin, 0.5)

        let over = """
        {"type":"game_over","ts":"2026-08-28T09:14:03Z","payload":{
          "game_id":"g","result":{"winner":null,"reason":"repetition"}}}
        """
        guard case .gameOver(let payload) = try ProtocolDecoder.event(from: try message(over)) else {
            return XCTFail("expected game_over")
        }
        // A void game has no winner and must not decode as one.
        XCTAssertNil(payload.result.winner)
        XCTAssertEqual(payload.result.reason, "repetition")
    }

    func testErrorCarriesTheReplyId() throws {
        let raw = """
        {"id":"s-9","re":"c-17","type":"error","ts":"2026-08-28T09:14:03Z",
         "payload":{"code":"not_your_turn","message":"Chưa tới lượt bạn."}}
        """
        guard case .error(let error, let replyTo) = try ProtocolDecoder.event(from: try message(raw)) else {
            return XCTFail("expected error")
        }
        XCTAssertEqual(error.code, "not_your_turn")
        XCTAssertEqual(replyTo, "c-17")
    }

    /// Forward compatibility: unknown types are ignored, not fatal (docs/03 ADR-014).
    func testUnknownMessageTypesAreDropped() throws {
        let raw = #"{"type":"hologram","ts":"2026-08-28T09:14:03Z","payload":{}}"#
        XCTAssertNil(try ProtocolDecoder.event(from: try message(raw)))
    }

    func testOutgoingMoveMatchesTheServerShape() throws {
        let command = ClientCommand.move(clientMoveId: "abc", expectedMoveNo: 4, kind: "play", point: "D4")
        let payload = try command.payload()
        let data = try ProtocolDecoder.encoder.encode(Message(id: "c-1", type: command.type, payload: payload))
        let object = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        XCTAssertEqual(object?["type"] as? String, "move")
        let body = object?["payload"] as? [String: Any]
        XCTAssertEqual(body?["client_move_id"] as? String, "abc")
        XCTAssertEqual(body?["expected_move_no"] as? Int, 4)
        XCTAssertEqual(body?["kind"] as? String, "play")
        XCTAssertEqual(body?["point"] as? String, "D4")
    }

    func testRestPayloadsDecode() throws {
        let signUp = """
        {"user":{"id":"u1","display_name":"an-sao-1234","friend_code":"K7MPQ2XB","is_guest":true},
         "access_token":"tok","expires_in":899}
        """
        let decoded = try ProtocolDecoder.json.decode(GuestSignUp.self, from: Data(signUp.utf8))
        XCTAssertEqual(decoded.user.friendCode, "K7MPQ2XB")

        let summary = """
        {"items":[{"game_id":"g","board_size":9,"rules":"japanese","phase":"playing","to_play":"black",
          "my_color":"black","your_turn":true,"opponent_name":"binh","move_no":3,
          "move_deadline":"2026-08-28T09:24:03Z","last_activity_at":"2026-08-28T09:14:03Z"}]}
        """
        struct Envelope: Decodable { let items: [GameSummary] }
        let list = try ProtocolDecoder.json.decode(Envelope.self, from: Data(summary.utf8))
        XCTAssertTrue(list.items[0].yourTurn)
        XCTAssertTrue(list.items[0].isActive)

        let challenge = """
        {"code":"R4TN8KMP","share_url":"https://sente.test/j/R4TN8KMP","creator_name":"an",
         "creator_color":"random","status":"pending",
         "config":{"board_size":9,"rules":"japanese","komi":6.5,"handicap":0,
                   "time_control":{"kind":"byoyomi","main_time_ms":600000,"periods":3,"period_time_ms":30000}},
         "expires_at":"2026-09-04T09:14:03Z","is_mine":false}
        """
        let invite = try ProtocolDecoder.json.decode(Challenge.self, from: Data(challenge.utf8))
        XCTAssertEqual(invite.config.timeControl.summary, "10′ + 3×30″")
    }

    func testTimeControlSummaries() {
        XCTAssertEqual(TimeControl.quick.summary, "10′")
        XCTAssertEqual(TimeControl.standard.summary, "20′ + 3×30″")
        XCTAssertEqual(TimeControl.correspondence.summary, "2 ngày/nước")
        XCTAssertEqual(TimeControl(kind: .fischer, mainTimeMs: 300_000, incrementMs: 10_000).summary, "5′ + 10″")
    }
}

final class BackoffTests: XCTestCase {
    func testScheduleIsImmediateThenDoublingToACap() {
        var backoff = Backoff(cap: 30)
        let noJitter: (ClosedRange<Double>) -> Double = { _ in 1 }
        XCTAssertEqual(backoff.next(random: noJitter), 0, "first retry is immediate")
        XCTAssertEqual(backoff.next(random: noJitter), 1)
        XCTAssertEqual(backoff.next(random: noJitter), 2)
        XCTAssertEqual(backoff.next(random: noJitter), 4)
        for _ in 0..<10 { _ = backoff.next(random: noJitter) }
        XCTAssertEqual(backoff.next(random: noJitter), 30, "never exceeds the cap")
        backoff.reset()
        XCTAssertEqual(backoff.next(random: noJitter), 0, "reset starts over")
    }

    func testJitterStaysWithinTwentyFivePercent() {
        var backoff = Backoff()
        _ = backoff.next(); _ = backoff.next(); _ = backoff.next()
        for _ in 0..<50 {
            let delay = backoff.next()
            XCTAssertGreaterThanOrEqual(delay, 0.75 * 0.75, "\(delay)")
        }
    }
}

final class JSONValueTests: XCTestCase {
    func testRoundTripPreservesIntegers() throws {
        let original: JSONValue = .object(["n": .number(42), "f": .number(0.5), "s": .string("x"),
                                           "b": .bool(true), "z": .null, "a": .array([.number(1)])])
        let data = try JSONEncoder().encode(original)
        XCTAssertTrue(String(decoding: data, as: UTF8.self).contains("\"n\":42"), "integers must not become 42.0")
        XCTAssertEqual(try JSONDecoder().decode(JSONValue.self, from: data), original)
    }
}

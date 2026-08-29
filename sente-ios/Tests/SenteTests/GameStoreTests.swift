import XCTest
import GoKit
import SenteNet
@testable import Sente

/// The store's job is reconciliation: what the player sees is the server's last
/// word plus the one move they are waiting on. These tests drive it with decoded
/// server payloads and check both the board it shows and what it sent.
@MainActor
final class GameStoreTests: XCTestCase {
    private var transport: FakeTransport!
    private var store: GameStore!

    override func setUp() async throws {
        transport = FakeTransport()
        store = GameStore(gameID: "g1", myColor: .black)
        await store.connect(transport: transport)
    }

    override func tearDown() async throws {
        await store.disconnect()
    }

    /// Hashes come from GoKit itself: the store compares against the same engine.
    private func hash(black: [String] = [], white: [String] = []) -> String {
        let engine = GameEngine.position(size: 9,
                                         black: black.map { Coordinate.point($0, size: 9)! },
                                         white: white.map { Coordinate.point($0, size: 9)! })
        return String(format: "0x%016llx", engine.state.boardHash)
    }

    private func point(_ text: String) -> Point { Coordinate.point(text, size: 9)! }

    private func loadEmptyGame() async {
        await store.handle(.gameState(Fixture.gameState(hash: hash())))
    }

    // MARK: - Loading

    func testGameStateBecomesTheConfirmedPosition() async {
        let board = Fixture.board([
            ".........", ".........", ".........", ".........",
            "....b....", ".........", ".........", ".........", ".........",
        ])
        await store.handle(.gameState(Fixture.gameState(
            board: board, toPlay: "white", moveNo: 1, lastMove: "E5", hash: hash(black: ["E5"]))))

        XCTAssertEqual(store.phase, .playing)
        XCTAssertEqual(store.moveNumber, 1)
        XCTAssertEqual(store.toPlay, .white)
        XCTAssertEqual(store.snapshot.board[point("E5")], .black)
        XCTAssertEqual(store.lastMove, point("E5"), "the marker must be drawable on first load")
        XCTAssertEqual(store.clock?.white.mainMs, 540_000)
        XCTAssertFalse(store.isMyTurn, "I am black and it is white's move")
    }

    func testMyColourComesFromTheCallerNotTheSocket() async {
        let white = GameStore(gameID: "g1", myColor: .white)
        await white.handle(.gameState(Fixture.gameState(toPlay: "white", hash: hash())))
        XCTAssertTrue(white.isMyTurn)
        XCTAssertEqual(white.ghostPlayer, .white)
    }

    // MARK: - Optimistic play (ADR-007)

    func testPlacingShowsTheStoneAtOnceAndSendsTheMove() async throws {
        await loadEmptyGame()
        XCTAssertTrue(store.isMyTurn)

        store.place(point("D4"))

        XCTAssertEqual(store.snapshot.pending, point("D4"), "the stone is shown before the server answers")
        XCTAssertEqual(store.snapshot.board[point("D4")], .black, "the optimistic engine has it")
        XCTAssertFalse(store.isMyTurn, "no second move while one is in flight")

        try await waitUntil { !self.transport.sent.isEmpty }
        guard case .move(let id, let expected, let kind, let text) = transport.sent[0] else {
            return XCTFail("expected a move command, got \(transport.sent)")
        }
        XCTAssertFalse(id.isEmpty, "the idempotency key is generated client-side")
        XCTAssertEqual(expected, 0)
        XCTAssertEqual(kind, "play")
        XCTAssertEqual(text, "D4", "wire coordinates are display coordinates")
    }

    func testTheServersEchoConfirmsThePendingStone() async {
        await loadEmptyGame()
        store.place(point("D4"))

        await store.handle(.moveMade(Fixture.moveMade(moveNo: 1, color: "black", point: "D4",
                                                      hash: hash(black: ["D4"]))))

        XCTAssertNil(store.snapshot.pending, "confirmed, so no longer pending")
        XCTAssertEqual(store.snapshot.board[point("D4")], .black)
        XCTAssertEqual(store.moveNumber, 1)
        XCTAssertEqual(store.toPlay, .white)
        XCTAssertEqual(store.lastMove, point("D4"))
        XCTAssertEqual(store.clock?.black.mainMs, 590_000, "the clock rides on move_made")
    }

    /// The server's verdict wins: the stone comes off and the reason is shown.
    func testARejectionRollsTheStoneBack() async {
        await loadEmptyGame()
        store.place(point("D4"))
        XCTAssertNotNil(store.snapshot.pending)

        await store.handle(.error(Fixture.decode(ErrorPayload.self,
            #"{"code":"not_your_turn","message":"Chưa tới lượt bạn."}"#), replyTo: "c-1"))

        XCTAssertNil(store.snapshot.pending)
        XCTAssertNil(store.snapshot.board[point("D4")], "rollback is a single assignment back to confirmed")
        XCTAssertEqual(store.toast, "Chưa tới lượt bạn.")
        XCTAssertTrue(store.isMyTurn, "the turn is mine again")
    }

    func testIllegalMovesNeverLeaveTheDevice() async throws {
        let board = Fixture.board([
            ".........", ".........", ".........", "....w....",
            "...wbw...", "....w....", ".........", ".........", ".........",
        ])
        // Black at E5 is surrounded; D5/F5/E4/E6 are white. Black to play at... nothing
        // interesting -- but E5 is occupied, and a suicide point exists at a corner shape
        // below. Use the occupied point as the illegal move.
        await store.handle(.gameState(Fixture.gameState(
            board: board, toPlay: "black", moveNo: 5,
            hash: hash(black: ["E5"], white: ["E6", "D5", "F5", "E4"]))))

        XCTAssertEqual(store.legality(point("E5")), "Đã có quân")
        store.place(point("E5"))
        XCTAssertNil(store.snapshot.pending)
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertTrue(transport.sent.isEmpty, "a move the local engine refuses is not sent")
    }

    func testLegalityExplainsWhoseTurnItIs() async {
        await store.handle(.gameState(Fixture.gameState(toPlay: "white", hash: hash())))
        XCTAssertEqual(store.legality(point("D4")), "Chưa tới lượt bạn")
    }

    // MARK: - Ordering (docs/06 §3.5)

    func testTheOpponentsMoveArrivesAndItBecomesMyTurn() async {
        await store.handle(.gameState(Fixture.gameState(toPlay: "white", moveNo: 0, hash: hash())))
        await store.handle(.moveMade(Fixture.moveMade(moveNo: 1, color: "white", point: "E5",
                                                      hash: hash(white: ["E5"]))))
        XCTAssertEqual(store.snapshot.board[point("E5")], .white)
        XCTAssertTrue(store.isMyTurn)
    }

    func testDuplicateAndStaleMovesAreIgnored() async {
        await loadEmptyGame()
        let first = Fixture.moveMade(moveNo: 1, color: "black", point: "D4", hash: hash(black: ["D4"]))
        await store.handle(.moveMade(first))
        await store.handle(.moveMade(first)) // the echo of an already-applied move
        XCTAssertEqual(store.moveNumber, 1)
        XCTAssertEqual(store.snapshot.board.stones(of: .black).count, 1)
        XCTAssertEqual(store.phase, .playing, "a duplicate is not a desync")
    }

    func testAGapInMoveNumbersForcesAResync() async {
        await loadEmptyGame()
        // Move 1 never arrived; move 2 did.
        await store.handle(.moveMade(Fixture.moveMade(moveNo: 2, color: "white", point: "E5",
                                                      hash: hash(white: ["E5"]))))
        XCTAssertEqual(store.phase, .loading, "the client cannot patch a gap, it reloads")
    }

    /// The desync canary (docs/04 §4.2): a hash that disagrees means the engines
    /// have drifted, and the client must not keep showing its own idea of the board.
    func testAHashMismatchIsNeverPaperedOver() async {
        await loadEmptyGame()
        await store.handle(.moveMade(Fixture.moveMade(moveNo: 1, color: "black", point: "D4",
                                                      hash: "0xdeadbeefdeadbeef")))
        XCTAssertEqual(store.phase, .loading)
        XCTAssertNotNil(store.toast)
    }

    func testResyncRequiredDropsEverythingAndReloads() async {
        await loadEmptyGame()
        store.place(point("D4"))
        await store.handle(.resyncRequired(moveNo: 0))
        XCTAssertEqual(store.phase, .loading)
        XCTAssertNil(store.snapshot.pending)
    }

    // MARK: - Passing, resigning, undo

    func testPassAndResignAreSentWithTheCurrentMoveNumber() async throws {
        await store.handle(.gameState(Fixture.gameState(moveNo: 6, hash: hash())))
        store.pass()
        try await waitUntil { self.transport.sent.count == 1 }
        guard case .move(_, let expected, let kind, let text) = transport.sent[0] else { return XCTFail() }
        XCTAssertEqual(expected, 6)
        XCTAssertEqual(kind, "pass")
        XCTAssertNil(text)
        XCTAssertFalse(store.isMyTurn, "a pass is pending like any move")

        store.resign()
        try await waitUntil { self.transport.sent.count == 2 }
        guard case .move(_, _, let resignKind, _) = transport.sent[1] else { return XCTFail() }
        XCTAssertEqual(resignKind, "resign")
    }

    /// The bug seen in the field: white asked, black agreed, and both boards kept
    /// the undone stone. The client cannot rebuild the position itself; it must
    /// freeze input until the server's game_state arrives, then show that.
    func testAnAcceptedUndoFreezesInputUntilTheServersBoardArrives() async {
        let two = Fixture.board([
            ".........", ".........", "....w....", ".........",
            "....b....", ".........", ".........", ".........", ".........",
        ])
        await store.handle(.gameState(Fixture.gameState(board: two, toPlay: "black", moveNo: 2,
                                                        hash: hash(black: ["E5"], white: ["E7"]))))
        XCTAssertTrue(store.isMyTurn)

        await store.handle(.undoResult(accepted: true, moveNo: 1))
        XCTAssertFalse(store.isMyTurn, "the board on screen is stale; no move may be built on it")
        XCTAssertNotNil(store.legality(point("C3")), "placing is refused while waiting")
        XCTAssertEqual(store.snapshot.board.stones(of: .white).count, 1, "no spinner: the old board stays visible")
        XCTAssertEqual(store.phase, .playing)
        XCTAssertEqual(store.toast, "Đã hoãn một nước.")

        let one = Fixture.board([
            ".........", ".........", ".........", ".........",
            "....b....", ".........", ".........", ".........", ".........",
        ])
        await store.handle(.gameState(Fixture.gameState(board: one, toPlay: "white", moveNo: 1,
                                                        lastMove: "E5", hash: hash(black: ["E5"]))))
        XCTAssertEqual(store.moveNumber, 1)
        XCTAssertEqual(store.toPlay, .white)
        XCTAssertEqual(store.snapshot.board.stones(of: .white).count, 0)
        XCTAssertFalse(store.awaitingState)
        XCTAssertFalse(store.isMyTurn, "it is white's move again")
    }

    func testASelfDetectedDesyncAsksTheServerForThePosition() async throws {
        await loadEmptyGame()
        await store.handle(.moveMade(Fixture.moveMade(moveNo: 2, color: "white", point: "E5",
                                                      hash: hash(white: ["E5"]))))
        try await waitUntil { self.transport.sent.count == 1 }
        guard case .resume = transport.sent[0] else { return XCTFail("want a resume, got \(transport.sent[0])") }
        XCTAssertEqual(store.phase, .loading)
    }

    func testUndoRequestsFromTheOpponentRaiseTheFlag() async {
        await loadEmptyGame()
        await store.handle(.undoRequested(by: "white"))
        XCTAssertTrue(store.undoRequestedByOpponent)
        await store.handle(.undoRequested(by: "black"))
        XCTAssertFalse(store.undoRequestedByOpponent, "my own request is not a question for me")

        store.answerUndo(true)
        XCTAssertFalse(store.undoRequestedByOpponent)
        await store.handle(.undoResult(accepted: false, moveNo: 3))
        XCTAssertEqual(store.toast, "Đối thủ không đồng ý hoãn.")
    }

    // MARK: - Scoring

    func testScoringStateDrivesTheOverlayAndConsent() async {
        let board = Fixture.board([
            "...b.w...", "...b.w...", "...b.w...", "...b.w...",
            "...b.w...", "...b.w...", "...b.w...", "...b.w...", "...b.w...",
        ])
        let stones = (1...9).flatMap { row in ["D\(row)", "F\(row)"] }
        await store.handle(.gameState(Fixture.gameState(
            board: board, moveNo: 20, phase: "scoring",
            hash: hash(black: stones.filter { $0.hasPrefix("D") }, white: stones.filter { $0.hasPrefix("F") }))))
        XCTAssertEqual(store.phase, .scoring)

        await store.handle(.scoringState(Fixture.scoringState(
            dead: [], blackAccepted: true, whiteAccepted: false, black: 27, white: 33.5)))

        XCTAssertTrue(store.iAccepted)
        XCTAssertFalse(store.opponentAccepted)
        XCTAssertEqual(store.score?.white, 33.5)
        // Columns A-C are black's, G-J white's, E is dame: the overlay shows it.
        XCTAssertEqual(store.snapshot.territoryBlack.count, 27)
        XCTAssertEqual(store.snapshot.territoryWhite.count, 27)
        XCTAssertFalse(store.snapshot.territoryBlack.contains(point("E5")), "dame is nobody's")

        store.toggleDead(point("F5"))
        store.accept(true)
        store.resumePlay()
    }

    func testMarkingDeadIsOnlyPossibleWhileScoring() async throws {
        await loadEmptyGame()
        store.toggleDead(point("D4"))
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertTrue(transport.sent.isEmpty)
    }

    // MARK: - Ending

    func testGameOverFinishesAndClearsAnythingPending() async {
        await loadEmptyGame()
        store.place(point("D4"))
        await store.handle(.gameOver(Fixture.gameOver(winner: "white", reason: "resignation")))
        XCTAssertEqual(store.phase, .finished)
        XCTAssertEqual(store.result?.winner, "white")
        XCTAssertNil(store.snapshot.pending)
    }

    func testAVoidGameHasNoWinner() async {
        await loadEmptyGame()
        await store.handle(.gameOver(Fixture.gameOver(winner: nil, reason: "repetition")))
        XCTAssertNil(store.result?.winner)
        XCTAssertEqual(store.result?.reason, "repetition")
    }

    // MARK: - Clock and connection

    func testClockAdjustmentIsExplainedToThePlayer() async {
        await loadEmptyGame()
        await store.handle(.clockAdjusted(Fixture.decode(ClockAdjustedPayload.self,
            #"{"game_id":"g1","color":"black","delta_ms":21000,"reason":"server_interruption"}"#)))
        XCTAssertEqual(store.toast, "Máy chủ gián đoạn 21 giây — thời gian đó được trả lại cho Đen.")
    }

    func testRemainingTimeIsLiveOnlyForThePlayerOnMove() async {
        await store.handle(.gameState(Fixture.gameState(toPlay: "black", hash: hash())))
        let mine = store.remaining(for: .black)
        let theirs = store.remaining(for: .white)
        XCTAssertNotNil(mine.deadline, "the player on move counts down to the deadline")
        XCTAssertNil(theirs.deadline, "the other clock is frozen")
        XCTAssertEqual(theirs.frozenMs, 540_000)
        XCTAssertEqual(mine.periods, 3)
    }

    func testConnectionStatusTracksHowLongWeHaveBeenReconnecting() async {
        store.handle(status: .reconnecting(attempt: 1))
        XCTAssertNotNil(store.reconnectingSince)
        store.handle(status: .connected)
        XCTAssertNil(store.reconnectingSince)
        XCTAssertEqual(store.connection, .connected)
    }

    func testPongUpdatesTheClockOffset() async {
        transport.offset = 1.25
        await store.handle(.pong(Fixture.decode(PongPayload.self,
            #"{"client_time":"2026-08-28T09:14:03Z","server_time":"2026-08-28T09:14:04Z"}"#)))
        XCTAssertEqual(store.clockOffset, 1.25)
    }

    func testDisconnectClosesTheTransport() async {
        await store.disconnect()
        XCTAssertEqual(transport.closeCalls, 1)
        XCTAssertEqual(transport.connectCalls, 1)
    }

    // MARK: - Helpers

    private func waitUntil(_ condition: @escaping @MainActor () -> Bool) async throws {
        for _ in 0..<100 {
            if condition() { return }
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTFail("condition never became true")
    }
}

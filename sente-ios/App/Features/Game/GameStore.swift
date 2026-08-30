import Foundation
import Observation
import GoKit
import SenteNet
import SenteUI

/// One live game on the client (docs/07 §4).
///
/// Two engines run side by side: `confirmed` is what the server has acknowledged,
/// `optimistic` is `confirmed` plus the move the player just made and is waiting
/// on. Rollback is a single assignment -- which is the reason GoKit is immutable.
@MainActor @Observable
final class GameStore {
    enum Phase: Equatable { case loading, playing, scoring, finished }

    let gameID: String
    private(set) var phase: Phase = .loading
    private(set) var myColor: Player = .black
    private(set) var boardSize = 9
    private(set) var rules: RuleSet = .japanese
    private(set) var komi = 6.5
    private(set) var moveNumber = 0
    private(set) var toPlay: Player = .black
    private(set) var captures = (black: 0, white: 0)
    private(set) var lastMove: Point?
    private(set) var clock: ClockPayload?
    private(set) var clockOffset: TimeInterval = 0
    private(set) var connection: ConnectionStatus = .disconnected
    private(set) var reconnectingSince: Date?
    private(set) var toast: String?
    private(set) var result: GameResultPayload?
    private(set) var undoRequestedByOpponent = false
    /// Set between an accepted undo and the `game_state` that follows it: the
    /// board on screen is stale, so no move may be built on it.
    private(set) var awaitingState = false

    // Scoring phase
    private(set) var deadStones: Set<Point> = []
    private(set) var blackAccepted = false
    private(set) var whiteAccepted = false
    private(set) var score: ScorePayload?

    private var confirmed: GameEngine?
    private var optimistic: GameEngine?
    private var pending: (id: String, point: Point?)?
    private var transport: (any GameTransport)?
    private var eventTask: Task<Void, Never>?
    private var statusTask: Task<Void, Never>?

    /// `myColor` comes from the game list, not from the socket: `game_state`
    /// describes the game, not the viewer, so the store must be told who it is.
    init(gameID: String, myColor: Player) {
        self.gameID = gameID
        self.myColor = myColor
    }

    // MARK: - Derived for the view

    var snapshot: BoardSnapshot {
        guard let engine = optimistic ?? confirmed else { return BoardSnapshot(board: Board(size: boardSize)) }
        var snapshot = BoardSnapshot(board: engine.board, lastMove: lastMove, pending: pending?.point)
        if phase == .scoring, let confirmed {
            snapshot.deadStones = deadStones
            let territory = confirmed.territory(deadStones: deadStones)
            snapshot.territoryBlack = territory.black
            snapshot.territoryWhite = territory.white
        }
        return snapshot
    }

    var isMyTurn: Bool { phase == .playing && toPlay == myColor && pending == nil && !awaitingState }
    var iAccepted: Bool { myColor == .black ? blackAccepted : whiteAccepted }
    var opponentAccepted: Bool { myColor == .black ? whiteAccepted : blackAccepted }
    var ghostPlayer: StonePlayer { myColor == .black ? .black : .white }

    /// Why a point cannot be played right now, in words the player can read.
    func legality(_ point: Point) -> String? {
        guard isMyTurn, let engine = confirmed else { return String(localized: "Chưa tới lượt bạn") }
        switch engine.validate(.play(point), by: myColor) {
        case .success: return nil
        case .failure(let error):
            switch error {
            case .occupied: return String(localized: "Đã có quân")
            case .suicide: return String(localized: "Tự sát")
            case .ko: return String(localized: "Luật ko")
            case .superko: return String(localized: "Lặp thế cờ")
            case .notYourTurn: return String(localized: "Chưa tới lượt")
            case .outOfBounds: return String(localized: "Ngoài bàn")
            case .gameNotPlaying: return String(localized: "Ván đã kết thúc")
            }
        }
    }

    // MARK: - Lifecycle

    func connect(api: APIClient, token: String) async {
        let baseURL = await api.baseURL
        await connect(transport: GameConnection(baseURL: baseURL, gameID: gameID, token: token))
    }

    func connect(transport: any GameTransport) async {
        self.transport = transport
        eventTask = Task { [weak self] in
            for await event in transport.events { await self?.handle(event) }
        }
        statusTask = Task { [weak self] in
            for await status in transport.status { await self?.handle(status: status) }
        }
        await transport.connect()
    }

    func disconnect() async {
        eventTask?.cancel(); statusTask?.cancel()
        await transport?.close()
        transport = nil
    }

    // MARK: - Intents (docs/03 ADR-007)

    func place(_ point: Point) {
        guard isMyTurn, let engine = confirmed, legality(point) == nil else { return }
        guard let next = try? engine.apply(.play(point), by: myColor) else { return }
        let id = UUID().uuidString.lowercased()
        pending = (id, point)
        optimistic = next
        toast = nil
        send(.move(clientMoveId: id, expectedMoveNo: moveNumber, kind: "play", point: Coordinate.text(point, size: boardSize)))
    }

    func pass() {
        guard isMyTurn else { return }
        let id = UUID().uuidString.lowercased()
        pending = (id, nil)
        send(.move(clientMoveId: id, expectedMoveNo: moveNumber, kind: "pass", point: nil))
    }

    func resign() {
        send(.move(clientMoveId: UUID().uuidString.lowercased(), expectedMoveNo: moveNumber, kind: "resign", point: nil))
    }

    func toggleDead(_ point: Point) {
        guard phase == .scoring else { return }
        send(.markDead(point: Coordinate.text(point, size: boardSize)))
    }

    func accept(_ accepted: Bool) { send(.scoringAccept(accepted)) }
    func resumePlay() { send(.scoringResume) }
    func requestUndo() { send(.undoRequest) }
    func answerUndo(_ accept: Bool) { undoRequestedByOpponent = false; send(.undoResponse(accept: accept)) }
    func dismissToast() { toast = nil }

    private func send(_ command: ClientCommand) {
        guard let transport else { return }
        Task { await transport.send(command) }
    }

    // MARK: - Events from the server

    func handle(status: ConnectionStatus) {
        connection = status
        switch status {
        case .reconnecting: if reconnectingSince == nil { reconnectingSince = Date() }
        case .connected: reconnectingSince = nil
        default: break
        }
    }

    func handle(_ event: ServerEvent) async {
        switch event {
        case .hello:
            break
        case .pong:
            if let transport { clockOffset = await transport.clockOffset }
        case .gameState(let state):
            apply(state)
        case .moveMade(let made):
            apply(made)
        case .scoringState(let state):
            phase = .scoring
            deadStones = Set(state.deadPoints.compactMap { Coordinate.point($0, size: boardSize) })
            blackAccepted = state.blackAccepted
            whiteAccepted = state.whiteAccepted
            score = state.score
        case .gameOver(let over):
            phase = .finished
            result = over.result
            pending = nil
            optimistic = confirmed
        case .clockAdjusted(let adjusted):
            let seconds = adjusted.deltaMs / 1000
            toast = String(localized: "Máy chủ gián đoạn \(seconds) giây — thời gian đó được trả lại cho \(adjusted.color == "black" ? String(localized: "Đen") : String(localized: "Trắng")).")
        case .undoRequested(let by):
            undoRequestedByOpponent = by != myColor.rawValue
        case .undoResult(let accepted, _):
            undoRequestedByOpponent = false
            if accepted {
                // The position after a rewind cannot be rebuilt here (captures are
                // gone for good), so the server sends game_state right behind this.
                // Keep showing the old board rather than flashing a spinner, but
                // take no input on it.
                toast = String(localized: "Đã hoãn một nước.")
                pending = nil
                optimistic = confirmed
                awaitingState = true
            } else {
                toast = String(localized: "Đối thủ không đồng ý hoãn.")
            }
        case .resyncRequired:
            // A rewind changes more than the client can patch; the server follows
            // with game_state, so only drop what is on screen.
            phase = .loading
            confirmed = nil; optimistic = nil; pending = nil
        case .error(let error, _):
            // The server's verdict wins: drop the optimistic stone and say why.
            pending = nil
            optimistic = confirmed
            toast = error.message
            if error.code == "out_of_sync" { /* a full game_state follows */ }
        }
    }

    private func apply(_ state: GameStatePayload) {
        boardSize = state.boardSize
        rules = state.rules == "chinese" ? .chinese : .japanese
        komi = state.komi
        moveNumber = state.moveNo
        toPlay = state.toPlay == "white" ? .white : .black
        captures = (state.captures.black, state.captures.white)
        clock = state.clock
        result = state.result
        lastMove = state.lastMove.flatMap { Coordinate.point($0, size: state.boardSize) }

        guard let board = Board(size: state.boardSize, wireString: state.board) else { return }
        let engine = GameEngine.position(size: state.boardSize,
                                         black: board.stones(of: .black), white: board.stones(of: .white),
                                         toPlay: toPlay, rules: rules, komi: komi, handicap: state.handicap,
                                         captures: Captures(black: state.captures.black, white: state.captures.white),
                                         moveNumber: state.moveNo)
        confirmed = engine
        optimistic = engine
        pending = nil
        awaitingState = false
        verify(hash: state.boardHash, against: engine)

        switch state.phase {
        case "scoring": phase = .scoring
        case "finished": phase = .finished
        default: phase = .playing
        }
    }

    private func apply(_ made: MoveMadePayload) {
        // Echoes of our own pending move and out-of-order deliveries are both
        // possible (docs/06 §3.5): apply only the next move, resync on a gap.
        guard let engine = confirmed else { return }
        if made.moveNo <= engine.state.moveNumber { return }
        guard made.moveNo == engine.state.moveNumber + 1 else {
            resync()
            return
        }
        let move: Move
        switch made.kind {
        case "pass": move = .pass
        case "resign": move = .resign
        default:
            guard let text = made.point, let point = Coordinate.point(text, size: boardSize) else { return }
            move = .play(point)
        }
        let by: Player = made.color == "white" ? .white : .black
        guard let next = try? engine.apply(move, by: by) else { return }

        confirmed = next
        optimistic = next
        pending = nil
        moveNumber = next.state.moveNumber
        toPlay = next.state.toPlay
        captures = (next.state.captures.black, next.state.captures.white)
        if case .play(let point) = move { lastMove = point }
        clock = made.clock
        verify(hash: made.boardHash, against: next)
    }

    /// Desync canary (docs/04 §4.2): the hash must match at every move.
    private func verify(hash: String, against engine: GameEngine) {
        let mine = String(format: "0x%016llx", engine.state.boardHash)
        if mine != hash.lowercased() {
            toast = String(localized: "Bàn cờ lệch với máy chủ — đang đồng bộ lại.")
            resync()
        }
    }

    /// The client noticed it is out of step: drop its picture and ask for the
    /// server's. Without the ask, "loading" would last until the next reconnect.
    private func resync() {
        phase = .loading
        confirmed = nil; optimistic = nil; pending = nil
        send(.resume)
    }

    // MARK: - Helpers for the view

    func remaining(for player: Player) -> (deadline: Date?, frozenMs: Int, periods: Int?) {
        guard let clock else { return (nil, 0, nil) }
        let side = player == .black ? clock.black : clock.white
        let active = phase == .playing && toPlay == player
        return (active ? clock.moveDeadline : nil, side.mainMs, side.periodsLeft)
    }
}

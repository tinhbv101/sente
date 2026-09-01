import Foundation
import Observation
import GoKit
import SenteUI

/// Configuration of a pass-and-play game (docs/01 FR-M5). Names are only labels;
/// nothing here touches an account.
struct LocalGameConfig: Codable, Equatable {
    var size = 9
    var rules: RuleSet = .japanese
    var handicap = 0
    var blackName = LS(localized: "Đen")
    var whiteName = LS(localized: "Trắng")
    /// A seat can be a bot: the level's raw value, nil for a person. Both set =
    /// a bot-versus-bot game to watch.
    var blackBot: Int?
    var whiteBot: Int?

    var komi: Double { GameEngine.defaultKomi(rules: rules, handicap: handicap) }
    func botLevel(for player: Player) -> BotLevel? {
        BotLevel(rawValue: (player == .black ? blackBot : whiteBot) ?? 0)
    }
    var isWatch: Bool { blackBot != nil && whiteBot != nil }
}

/// What survives an app relaunch: the setup and every move, nothing derived.
struct LocalGameRecord: Codable, Equatable {
    struct StoredMove: Codable, Equatable {
        let player: Player
        let kind: String
        let point: Point?
    }
    var config: LocalGameConfig
    var moves: [StoredMove]
    var startedAt: Date
}

/// Where the in-progress local game lives. A protocol so tests stay in memory.
protocol LocalGameStorage: Sendable {
    func load() -> LocalGameRecord?
    func save(_ record: LocalGameRecord)
    func clear()
}

/// One JSON file in Application Support; there is at most one local game.
struct FileLocalGameStorage: LocalGameStorage {
    /// Pass-and-play and the bot game are separate saves; a bot match is not saved.
    var name = "local-game"
    private var url: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        try? FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        return base.appending(path: "\(name).json")
    }

    func load() -> LocalGameRecord? {
        guard let data = try? Data(contentsOf: url) else { return nil }
        return try? JSONDecoder().decode(LocalGameRecord.self, from: data)
    }

    func save(_ record: LocalGameRecord) {
        if let data = try? JSONEncoder().encode(record) { try? data.write(to: url, options: .atomic) }
    }

    func clear() { try? FileManager.default.removeItem(at: url) }
}

/// Watching bots needs no file behind it.
struct NullGameStorage: LocalGameStorage {
    func load() -> LocalGameRecord? { nil }
    func save(_ record: LocalGameRecord) {}
    func clear() {}
}

/// Two people, one phone. The engine alone is the arbiter: whoever is to move
/// plays, so turns alternate by construction and there is nobody to ask for an
/// undo -- both players are looking at the screen.
@MainActor @Observable
final class LocalGameStore {
    private(set) var config: LocalGameConfig
    private(set) var engine: GameEngine
    private(set) var moves: [RecordedMove] = []
    private(set) var deadStones: Set<Point> = []
    private(set) var toast: String?
    /// Set once the players agree on the count; the engine itself stays in scoring.
    private(set) var counted: (score: Score, result: GameResult)?
    private let startedAt: Date
    private let storage: any LocalGameStorage
    /// Bots keep their RNG state across moves, one per seat.
    private var bots: [Player: GoBot] = [:]
    private(set) var thinking = false
    var paused = false {
        didSet { if !paused { scheduleBot() } }
    }

    init(config: LocalGameConfig, storage: any LocalGameStorage = FileLocalGameStorage()) {
        self.config = config
        self.engine = GameEngine.newGame(size: config.size, rules: config.rules, handicap: config.handicap)
        self.startedAt = Date()
        self.storage = storage
        save()
        makeBots()
        scheduleBot()
    }

    /// Rebuilds a saved game by replaying it. A move that no longer applies ends
    /// the replay there rather than throwing the whole game away.
    init(record: LocalGameRecord, storage: any LocalGameStorage = FileLocalGameStorage()) {
        self.config = record.config
        self.engine = GameEngine.newGame(size: record.config.size, rules: record.config.rules, handicap: record.config.handicap)
        self.startedAt = record.startedAt
        self.storage = storage
        for stored in record.moves {
            let move: Move
            switch stored.kind {
            case "pass": move = .pass
            case "resign": move = .resign
            default:
                guard let point = stored.point else { continue }
                move = .play(point)
            }
            guard let next = try? engine.apply(move, by: stored.player) else { break }
            engine = next
            moves.append(RecordedMove(player: stored.player, move: move))
        }
        makeBots()
        scheduleBot()
    }

    private func makeBots() {
        for player in [Player.black, .white] {
            if let level = config.botLevel(for: player) { bots[player] = GoBot(level: level) }
        }
    }

    // MARK: - Derived

    var phase: Phase { counted == nil ? engine.state.phase : .finished }
    var toPlay: Player { engine.toPlay }
    var moveNumber: Int { engine.state.moveNumber }
    var captures: Captures { engine.state.captures }
    var result: GameResult? { counted?.result ?? engine.state.result }
    var finalScore: Score? { counted?.score }

    var lastMove: Point? {
        for recorded in moves.reversed() {
            if case .play(let point) = recorded.move { return point }
            return nil
        }
        return nil
    }

    /// The live count while dead stones are being marked.
    var score: Score? { phase == .scoring ? engine.score(deadStones: deadStones) : nil }

    var snapshot: BoardSnapshot {
        var snapshot = BoardSnapshot(board: engine.board, lastMove: lastMove)
        if phase == .scoring {
            snapshot.deadStones = deadStones
            let territory = engine.territory(deadStones: deadStones)
            snapshot.territoryBlack = territory.black
            snapshot.territoryWhite = territory.white
        }
        return snapshot
    }

    func name(of player: Player) -> String { player == .black ? config.blackName : config.whiteName }

    /// A person may act only on their own seat, and not while the bot thinks.
    var isHumanTurn: Bool { phase == .playing && config.botLevel(for: toPlay) == nil && !thinking }

    func legality(_ point: Point) -> String? {
        switch engine.validate(.play(point)) {
        case .success: return nil
        case .failure(let error):
            switch error {
            case .occupied: return LS(localized: "Đã có quân")
            case .suicide: return LS(localized: "Tự sát")
            case .ko: return LS(localized: "Luật ko")
            case .superko: return LS(localized: "Lặp thế cờ")
            case .notYourTurn: return LS(localized: "Chưa tới lượt")
            case .outOfBounds: return LS(localized: "Ngoài bàn")
            case .gameNotPlaying: return LS(localized: "Ván đã kết thúc")
            }
        }
    }

    // MARK: - Intents

    func place(_ point: Point) {
        guard phase == .playing, config.botLevel(for: toPlay) == nil else { return }
        do {
            try apply(.play(point))
            scheduleBot()
        } catch let error as MoveError {
            toast = legalityText(error)
        } catch {
            toast = error.localizedDescription
        }
    }

    func pass() {
        guard phase == .playing, config.botLevel(for: toPlay) == nil else { return }
        try? apply(.pass)
        if phase == .scoring { toast = LS(localized: "Hai bên cùng nhường lượt — đánh dấu quân chết rồi đếm điểm.") }
        scheduleBot()
    }

    /// The player to move gives up.
    func resign() {
        guard phase == .playing else { return }
        try? apply(.resign)
    }

    /// Takes back the last move — and keeps unwinding until it is a person's
    /// turn again, so "undo" against the bot reverts your move, not just its reply.
    func undo() {
        guard !moves.isEmpty, counted == nil else { return }
        deadStones = []
        thinking = false
        replay(Array(moves.dropLast()))
        while config.botLevel(for: toPlay) != nil, !moves.isEmpty {
            replay(Array(moves.dropLast()))
        }
        save()
    }

    func toggleDead(_ point: Point) {
        guard phase == .scoring, engine.board[point] != nil else { return }
        let chain = engine.chain(at: point)
        if deadStones.isSuperset(of: chain) { deadStones.subtract(chain) } else { deadStones.formUnion(chain) }
    }

    /// Both players are satisfied with the marking: the count is the result.
    func finishCounting() {
        guard phase == .scoring else { return }
        let score = engine.score(deadStones: deadStones)
        counted = (score, GameResult(winner: score.winner, reason: .counting))
        storage.clear()
    }

    /// Disagreement over life and death: drop the passes and play it out.
    func resumePlay() {
        guard phase == .scoring else { return }
        deadStones = []
        var kept = moves
        while let last = kept.last, last.move == .pass { kept.removeLast() }
        replay(kept)
        save()
    }

    func dismissToast() { toast = nil }

    // MARK: - Bots

    /// Lets the bot on turn move, off the main thread, after a small beat so the
    /// game feels played rather than instantaneous.
    func scheduleBot(after delay: Duration = .milliseconds(600)) {
        guard phase == .playing, !paused, !thinking, bots[toPlay] != nil else { return }
        thinking = true
        let side = toPlay
        let snapshot = engine
        guard let bot = bots[side] else { thinking = false; return }
        Task { [weak self] in
            try? await Task.sleep(for: delay)
            // Value copies cross into the detached task; nothing shared mutates.
            let result = await Task.detached(priority: .userInitiated) { [bot, snapshot] () -> (Move, GoBot) in
                var thinker = bot
                let move = thinker.chooseMove(snapshot)
                return (move, thinker)
            }.value
            guard let self else { return }
            self.bots[side] = result.1
            self.thinking = false
            // The position may have changed under the bot (undo, new game): drop the move.
            guard self.phase == .playing, self.toPlay == side,
                  self.engine.state.boardHash == snapshot.state.boardHash,
                  self.engine.state.moveNumber == snapshot.state.moveNumber else { return }
            try? self.apply(result.0)
            self.afterBotMove()
            self.scheduleBot()
        }
    }

    /// The synchronous variant tests drive; identical rules, no delay. Any move
    /// still brewing on the background task is orphaned by the position guards.
    func stepBotNow() {
        thinking = false
        guard phase == .playing, var bot = bots[toPlay] else { return }
        let side = toPlay
        let move = bot.chooseMove(engine)
        bots[side] = bot
        try? apply(move)
        afterBotMove()
    }

    /// Two bots reaching scoring have nobody to argue about dead stones: count
    /// the board as it stands.
    private func afterBotMove() {
        if phase == .scoring, config.isWatch { finishCounting() }
    }

    /// A finished record for sharing. Dead stones are not part of SGF's main line.
    var sgf: String {
        SGF.encode(GameRecord(
            size: config.size, rules: config.rules, komi: config.komi, handicap: config.handicap,
            handicapStones: Handicap.stones(count: config.handicap, size: config.size),
            blackPlayer: config.blackName, whitePlayer: config.whiteName,
            date: startedAt.formatted(.iso8601.year().month().day()),
            result: result, moves: moves))
    }

    // MARK: - Internals

    private func apply(_ move: Move) throws {
        let player = engine.toPlay
        engine = try engine.apply(move, by: player)
        moves.append(RecordedMove(player: player, move: move))
        toast = nil
        if engine.state.phase == .finished { storage.clear() } else { save() }
    }

    private func replay(_ kept: [RecordedMove]) {
        engine = GameEngine.newGame(size: config.size, rules: config.rules, handicap: config.handicap)
        moves = []
        for recorded in kept {
            guard let next = try? engine.apply(recorded.move, by: recorded.player) else { break }
            engine = next
            moves.append(recorded)
        }
    }

    private func save() {
        storage.save(LocalGameRecord(config: config, moves: moves.map { recorded in
            switch recorded.move {
            case .play(let point): LocalGameRecord.StoredMove(player: recorded.player, kind: "play", point: point)
            case .pass: LocalGameRecord.StoredMove(player: recorded.player, kind: "pass", point: nil)
            case .resign: LocalGameRecord.StoredMove(player: recorded.player, kind: "resign", point: nil)
            }
        }, startedAt: startedAt))
    }

    private func legalityText(_ error: MoveError) -> String {
        switch error {
        case .occupied: LS(localized: "Đã có quân ở đó.")
        case .suicide: LS(localized: "Nước tự sát.")
        case .ko: LS(localized: "Luật ko: phải đi chỗ khác trước.")
        case .superko: LS(localized: "Lặp lại thế cờ.")
        case .notYourTurn: LS(localized: "Chưa tới lượt.")
        case .outOfBounds: LS(localized: "Ngoài bàn.")
        case .gameNotPlaying: LS(localized: "Ván đã kết thúc.")
        }
    }
}

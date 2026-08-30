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
    var blackName = "Đen"
    var whiteName = "Trắng"

    var komi: Double { GameEngine.defaultKomi(rules: rules, handicap: handicap) }
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
    private var url: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        try? FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        return base.appending(path: "local-game.json")
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

    init(config: LocalGameConfig, storage: any LocalGameStorage = FileLocalGameStorage()) {
        self.config = config
        self.engine = GameEngine.newGame(size: config.size, rules: config.rules, handicap: config.handicap)
        self.startedAt = Date()
        self.storage = storage
        save()
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

    func legality(_ point: Point) -> String? {
        switch engine.validate(.play(point)) {
        case .success: return nil
        case .failure(let error):
            switch error {
            case .occupied: return "Đã có quân"
            case .suicide: return "Tự sát"
            case .ko: return "Luật ko"
            case .superko: return "Lặp thế cờ"
            case .notYourTurn: return "Chưa tới lượt"
            case .outOfBounds: return "Ngoài bàn"
            case .gameNotPlaying: return "Ván đã kết thúc"
            }
        }
    }

    // MARK: - Intents

    func place(_ point: Point) {
        guard phase == .playing else { return }
        do {
            try apply(.play(point))
        } catch let error as MoveError {
            toast = legalityText(error)
        } catch {
            toast = error.localizedDescription
        }
    }

    func pass() {
        guard phase == .playing else { return }
        try? apply(.pass)
        if phase == .scoring { toast = "Hai bên cùng nhường lượt — đánh dấu quân chết rồi đếm điểm." }
    }

    /// The player to move gives up.
    func resign() {
        guard phase == .playing else { return }
        try? apply(.resign)
    }

    /// Takes back the last move. Also the way out of scoring by mistake.
    func undo() {
        guard !moves.isEmpty, counted == nil else { return }
        deadStones = []
        replay(Array(moves.dropLast()))
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
        case .occupied: "Đã có quân ở đó."
        case .suicide: "Nước tự sát."
        case .ko: "Luật ko: phải đi chỗ khác trước."
        case .superko: "Lặp lại thế cờ."
        case .notYourTurn: "Chưa tới lượt."
        case .outOfBounds: "Ngoài bàn."
        case .gameNotPlaying: "Ván đã kết thúc."
        }
    }
}

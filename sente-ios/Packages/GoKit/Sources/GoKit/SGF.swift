import Foundation

public struct RecordedMove: Equatable, Sendable {
    public let player: Player
    public let move: Move

    public init(player: Player, move: Move) {
        self.player = player
        self.move = move
    }
}

/// Everything needed to write a game out and read it back. Deliberately flat:
/// a record is transport, not a live game.
public struct GameRecord: Equatable, Sendable {
    public var size: Int
    public var rules: RuleSet
    public var komi: Double
    public var handicap: Int
    public var handicapStones: [Point]
    public var blackPlayer: String
    public var whitePlayer: String
    public var date: String?
    public var timeControl: String?
    public var result: GameResult?
    public var moves: [RecordedMove]

    public init(
        size: Int,
        rules: RuleSet = .japanese,
        komi: Double = 6.5,
        handicap: Int = 0,
        handicapStones: [Point] = [],
        blackPlayer: String = "",
        whitePlayer: String = "",
        date: String? = nil,
        timeControl: String? = nil,
        result: GameResult? = nil,
        moves: [RecordedMove] = []
    ) {
        self.size = size
        self.rules = rules
        self.komi = komi
        self.handicap = handicap
        self.handicapStones = handicapStones
        self.blackPlayer = blackPlayer
        self.whitePlayer = whitePlayer
        self.date = date
        self.timeControl = timeControl
        self.result = result
        self.moves = moves
    }
}

/// SGF FF[4] reader and writer for the main line of a game (docs/02 §9).
/// Variations are skipped: Sente records one line of play per game.
public enum SGF {
    public enum DecodeError: Error, Equatable {
        case malformed(String)
        case unsupportedSize(Int)
    }

    public static let application = "Sente:1.0"

    // MARK: - Encoding

    public static func encode(_ record: GameRecord) -> String {
        var root = "GM[1]FF[4]CA[UTF-8]AP[\(application)]"
        root += "SZ[\(record.size)]"
        root += "KM[\(number(record.komi))]"
        root += "RU[\(record.rules == .japanese ? "Japanese" : "Chinese")]"
        if !record.blackPlayer.isEmpty { root += "PB[\(escape(record.blackPlayer))]" }
        if !record.whitePlayer.isEmpty { root += "PW[\(escape(record.whitePlayer))]" }
        if let date = record.date { root += "DT[\(escape(date))]" }
        if let time = record.timeControl { root += "TM[\(escape(time))]" }
        if record.handicap > 0 {
            root += "HA[\(record.handicap)]"
            root += "AB" + record.handicapStones.map { "[\(Coordinate.sgfText($0))]" }.joined()
        }
        if let result = record.result { root += "RE[\(resultText(result))]" }

        let body = record.moves.map { recorded -> String in
            let tag = recorded.player == .black ? "B" : "W"
            switch recorded.move {
            case .play(let point): return ";\(tag)[\(Coordinate.sgfText(point))]"
            // An empty value is the FF[4] spelling of a pass.
            case .pass, .resign: return ";\(tag)[]"
            }
        }.joined()

        return "(;\(root)\(body))"
    }

    static func resultText(_ result: GameResult) -> String {
        switch result.reason {
        case .repetition: return "Void"
        case .resignation: return "\(winnerTag(result))+R"
        case .timeout: return "\(winnerTag(result))+T"
        case .abandonment: return "\(winnerTag(result))+F"
        case .mutualDraw: return "0"
        case .counting:
            guard let score = result.score, let winner = result.winner else { return "0" }
            return "\(winner == .black ? "B" : "W")+\(number(score.margin))"
        }
    }

    private static func winnerTag(_ result: GameResult) -> String {
        result.winner == .black ? "B" : "W"
    }

    /// Points are always multiples of 0.5, so a single decimal is exact.
    static func number(_ value: Double) -> String {
        value == value.rounded() ? String(Int(value)) : String(format: "%.1f", value)
    }

    private static func escape(_ text: String) -> String {
        text.replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "]", with: "\\]")
    }

    // MARK: - Decoding

    public static func decode(_ text: String) throws -> GameRecord {
        let nodes = try parseNodes(text)
        guard let root = nodes.first else { throw DecodeError.malformed("no root node") }

        let size = Int(root["SZ"]?.first ?? "19") ?? 19
        guard BoardSize.isSupported(size) else { throw DecodeError.unsupportedSize(size) }

        var record = GameRecord(size: size)
        record.komi = Double(root["KM"]?.first ?? "") ?? 6.5
        record.rules = (root["RU"]?.first ?? "").lowercased().hasPrefix("chinese") ? .chinese : .japanese
        record.blackPlayer = root["PB"]?.first ?? ""
        record.whitePlayer = root["PW"]?.first ?? ""
        record.date = root["DT"]?.first
        record.timeControl = root["TM"]?.first
        record.handicap = Int(root["HA"]?.first ?? "0") ?? 0
        record.handicapStones = (root["AB"] ?? []).compactMap { Coordinate.sgfPoint($0, size: size) }

        record.moves = nodes.dropFirst().compactMap { node -> RecordedMove? in
            for (tag, player) in [("B", Player.black), ("W", Player.white)] {
                guard let values = node[tag], let raw = values.first else { continue }
                // "" and the legacy "tt" both mean pass.
                if raw.isEmpty || (raw == "tt" && size <= 19) {
                    return RecordedMove(player: player, move: .pass)
                }
                guard let point = Coordinate.sgfPoint(raw, size: size) else { return nil }
                return RecordedMove(player: player, move: .play(point))
            }
            return nil
        }
        return record
    }

    /// Minimal scanner: property identifiers followed by bracketed values, nodes
    /// separated by `;`. A nested `(` starts a variation, which ends the main line.
    private static func parseNodes(_ text: String) throws -> [[String: [String]]] {
        let characters = Array(text)
        var index = 0
        func skipSpace() { while index < characters.count, characters[index].isWhitespace { index += 1 } }

        skipSpace()
        guard index < characters.count, characters[index] == "(" else {
            throw DecodeError.malformed("expected '('")
        }
        index += 1

        var nodes: [[String: [String]]] = []
        var current: [String: [String]] = [:]
        var started = false

        while index < characters.count {
            let character = characters[index]
            if character.isWhitespace { index += 1; continue }
            if character == ")" { break }
            if character == "(" { break }
            if character == ";" {
                if started { nodes.append(current) }
                current = [:]
                started = true
                index += 1
                continue
            }
            guard character.isUppercase else { index += 1; continue }

            var identifier = ""
            while index < characters.count, characters[index].isUppercase {
                identifier.append(characters[index])
                index += 1
            }
            var values: [String] = []
            skipSpace()
            while index < characters.count, characters[index] == "[" {
                index += 1
                var value = ""
                while index < characters.count, characters[index] != "]" {
                    if characters[index] == "\\", index + 1 < characters.count {
                        index += 1
                    }
                    value.append(characters[index])
                    index += 1
                }
                guard index < characters.count else { throw DecodeError.malformed("unterminated value") }
                index += 1
                values.append(value)
                skipSpace()
            }
            current[identifier, default: []].append(contentsOf: values)
        }
        if started { nodes.append(current) }
        return nodes
    }
}

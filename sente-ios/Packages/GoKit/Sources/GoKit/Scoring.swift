import Foundation

/// Which player each empty region belongs to once dead stones are removed.
public struct TerritoryMap: Equatable, Sendable {
    public let black: Set<Point>
    public let white: Set<Point>
    /// Points bordered by both colours -- dame. This is also what makes seki score
    /// correctly under Japanese rules (docs/02 §6.2).
    public let neutral: Set<Point>
}

public struct Score: Equatable, Sendable, Codable {
    public struct Side: Equatable, Sendable, Codable {
        public let territory: Int
        /// Chinese scoring only; zero under Japanese rules.
        public let area: Int
        /// Prisoners taken while the game was played.
        public let captures: Int
        /// Opponent stones agreed dead during the scoring phase.
        public let deadStones: Int
        public let komi: Double
        public let handicapAdjustment: Int
        public let total: Double
    }

    public let rules: RuleSet
    public let black: Double
    public let white: Double
    public let detail: Detail

    public struct Detail: Equatable, Sendable, Codable {
        public let black: Side
        public let white: Side
    }

    public var winner: Player? {
        if black > white { return .black }
        if white > black { return .white }
        return nil
    }

    public var margin: Double { abs(black - white) }
}

extension GameEngine {
    /// Flood-fills the empty regions of the board with dead stones removed.
    public func territory(deadStones: Set<Point> = []) -> TerritoryMap {
        let working = boardRemovingDead(deadStones)
        var black = Set<Point>(), white = Set<Point>(), neutral = Set<Point>()
        var visited = Set<Point>()

        for start in working.allPoints where working.isEmpty(start) && !visited.contains(start) {
            var stack = [start]
            var region: [Point] = []
            var borders = Set<Player>()
            visited.insert(start)

            while let current = stack.popLast() {
                region.append(current)
                working.forEachNeighbour(working.index(current)) { neighbourIndex in
                    let cell = working.cells[neighbourIndex]
                    if let player = cell.player {
                        borders.insert(player)
                        return
                    }
                    guard cell == .empty else { return }
                    let neighbour = working.point(at: neighbourIndex)
                    if visited.insert(neighbour).inserted { stack.append(neighbour) }
                }
            }

            // A region touching exactly one colour is that player's territory;
            // anything else scores for nobody.
            switch borders.count == 1 ? borders.first : nil {
            case .black: black.formUnion(region)
            case .white: white.formUnion(region)
            default: neutral.formUnion(region)
            }
        }
        return TerritoryMap(black: black, white: white, neutral: neutral)
    }

    /// Final score for the agreed set of dead stones. Japanese counts territory
    /// plus prisoners; Chinese counts area and ignores prisoners (docs/02 §6.2-6.3).
    public func score(deadStones: Set<Point> = []) -> Score {
        let working = boardRemovingDead(deadStones)
        let map = territory(deadStones: deadStones)

        // A dead stone becomes a prisoner of the opponent of its own colour.
        var deadFor = Captures()
        for point in deadStones {
            guard let owner = state.board[point] else { continue }
            deadFor[owner.opponent] += 1
        }

        func side(_ player: Player) -> Score.Side {
            let territoryCount = (player == .black ? map.black : map.white).count
            let stoneCount = working.stones(of: player).count
            let komiValue = player == .white ? komi : 0
            let handicapAdjustment = (rules == .chinese && player == .black) ? -handicap : 0

            let total: Double
            let area: Int
            switch rules {
            case .japanese:
                area = 0
                total = Double(territoryCount + state.captures[player] + deadFor[player]) + komiValue
            case .chinese:
                area = stoneCount + territoryCount
                total = Double(area + handicapAdjustment) + komiValue
            }
            return Score.Side(
                territory: territoryCount,
                area: area,
                captures: rules == .japanese ? state.captures[player] : 0,
                deadStones: rules == .japanese ? deadFor[player] : 0,
                komi: komiValue,
                handicapAdjustment: handicapAdjustment,
                total: total
            )
        }

        let blackSide = side(.black)
        let whiteSide = side(.white)
        return Score(
            rules: rules,
            black: blackSide.total,
            white: whiteSide.total,
            detail: Score.Detail(black: blackSide, white: whiteSide)
        )
    }

    func boardRemovingDead(_ deadStones: Set<Point>) -> Board {
        deadStones.isEmpty ? state.board : state.board.clearing(Array(deadStones))
    }
}

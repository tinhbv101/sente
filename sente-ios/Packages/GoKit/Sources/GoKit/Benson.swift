import Foundation

/// Benson's algorithm for unconditional life.
///
/// A chain is pass-alive when the opponent cannot capture it even if the owner
/// never answers. The result is exact, so these chains must never be suggested as
/// dead during scoring (docs/02 §6.4).
extension Board {
    public func passAliveChains(for player: Player) -> [Set<Point>] {
        let playerCell = Cell(player)

        // 1. Every chain of the player.
        var chains: [[Int]] = []
        var chainOf = [Int](repeating: -1, count: cells.count)
        for index in 0..<cells.count where cells[index] == playerCell && chainOf[index] == -1 {
            let (stones, _) = chainAndLiberties(at: index)
            for stone in stones { chainOf[stone] = chains.count }
            chains.append(stones)
        }

        // 2. Player-enclosed regions: maximal connected sets containing no player stone.
        struct Region {
            var points: [Int] = []
            var emptyPoints: [Int] = []
            var borderingChains: Set<Int> = []
        }
        var regions: [Region] = []
        var seen = [Bool](repeating: false, count: cells.count)
        for start in 0..<cells.count
        where !seen[start] && (cells[start] == .empty || cells[start] == Cell(player.opponent)) {
            var region = Region()
            var stack = [start]
            seen[start] = true
            while let current = stack.popLast() {
                region.points.append(current)
                if cells[current] == .empty { region.emptyPoints.append(current) }
                forEachNeighbour(current) { neighbour in
                    if cells[neighbour] == playerCell {
                        region.borderingChains.insert(chainOf[neighbour])
                    } else if !seen[neighbour], cells[neighbour] == .empty || cells[neighbour] == Cell(player.opponent) {
                        seen[neighbour] = true
                        stack.append(neighbour)
                    }
                }
            }
            regions.append(region)
        }

        // 3. A region is vital to a chain when every empty point in it is a liberty
        //    of that chain -- that is what makes it an eye the opponent cannot fill.
        var vitalRegions: [Set<Int>] = Array(repeating: [], count: chains.count)
        for (regionIndex, region) in regions.enumerated() {
            for chainIndex in region.borderingChains {
                let isVital = region.emptyPoints.allSatisfy { emptyPoint in
                    var touchesChain = false
                    forEachNeighbour(emptyPoint) { neighbour in
                        if cells[neighbour] == playerCell && chainOf[neighbour] == chainIndex {
                            touchesChain = true
                        }
                    }
                    return touchesChain
                }
                if isVital { vitalRegions[chainIndex].insert(regionIndex) }
            }
        }

        // 4. Alternately drop chains with fewer than two vital regions and regions
        //    that border a dropped chain, until the sets stop shrinking.
        var liveChains = Set(chains.indices)
        var liveRegions = Set(regions.indices)
        var changed = true
        while changed {
            changed = false
            for chainIndex in liveChains
            where vitalRegions[chainIndex].intersection(liveRegions).count < 2 {
                liveChains.remove(chainIndex)
                changed = true
            }
            for regionIndex in liveRegions
            where !regions[regionIndex].borderingChains.isSubset(of: liveChains) {
                liveRegions.remove(regionIndex)
                changed = true
            }
        }

        return liveChains.map { Set(chains[$0].map { point(at: $0) }) }
    }
}

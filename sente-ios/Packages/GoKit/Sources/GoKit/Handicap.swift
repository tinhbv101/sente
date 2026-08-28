import Foundation

/// Star-point placement for handicap stones (docs/02 §8).
public enum Handicap {
    /// The three star lines for a board size, low to high. Corners, edge midpoints
    /// and the centre are all combinations of these.
    static func starLines(size: Int) -> [Int] {
        switch size {
        case 19: [3, 9, 15]
        case 13: [3, 6, 9]
        case 9: [2, 4, 6]
        default: []
        }
    }

    public static func starPoints(size: Int) -> [Point] {
        let lines = starLines(size: size)
        return lines.flatMap { row in lines.map { Point(col: $0, row: row) } }
    }

    /// Stones in the conventional order: two corners first, then the remaining two,
    /// then edges, with the centre always taken last on odd counts.
    public static func stones(count: Int, size: Int) -> [Point] {
        guard count >= 2, (2...9).contains(count) else { return [] }
        let lines = starLines(size: size)
        guard lines.count == 3 else { return [] }
        let (low, mid, high) = (lines[0], lines[1], lines[2])

        let upperRight = Point(col: high, row: low)
        let lowerLeft = Point(col: low, row: high)
        let lowerRight = Point(col: high, row: high)
        let upperLeft = Point(col: low, row: low)
        let leftEdge = Point(col: low, row: mid)
        let rightEdge = Point(col: high, row: mid)
        let bottomEdge = Point(col: mid, row: high)
        let topEdge = Point(col: mid, row: low)
        let centre = Point(col: mid, row: mid)

        let corners = [upperRight, lowerLeft, lowerRight, upperLeft]
        switch count {
        case 2: return [upperRight, lowerLeft]
        case 3: return [upperRight, lowerLeft, lowerRight]
        case 4: return corners
        case 5: return corners + [centre]
        case 6: return corners + [leftEdge, rightEdge]
        case 7: return corners + [leftEdge, rightEdge, centre]
        case 8: return corners + [leftEdge, rightEdge, bottomEdge, topEdge]
        default: return corners + [leftEdge, rightEdge, bottomEdge, topEdge, centre]
        }
    }
}

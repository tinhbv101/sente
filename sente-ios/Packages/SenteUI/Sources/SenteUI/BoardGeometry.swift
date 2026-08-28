import CoreGraphics
import GoKit

/// Maps between points on the board and points on the screen. Pure and testable:
/// the view only draws and asks.
public struct BoardGeometry: Equatable, Sendable {
    public let size: Int
    public let side: CGFloat
    public let showsCoordinates: Bool

    /// Distance from the board edge to the first line.
    public var padding: CGFloat { side * (showsCoordinates ? 0.072 : 0.055) }
    /// Distance between neighbouring lines.
    public var cell: CGFloat { (side - padding * 2) / CGFloat(size - 1) }
    public var stoneRadius: CGFloat { cell * 0.465 }

    public init(size: Int, side: CGFloat, showsCoordinates: Bool = true) {
        self.size = size
        self.side = side
        self.showsCoordinates = showsCoordinates
    }

    public func center(of point: Point) -> CGPoint {
        CGPoint(x: padding + CGFloat(point.col) * cell, y: padding + CGFloat(point.row) * cell)
    }

    /// The intersection nearest to a touch, or nil when the touch is too far from
    /// any line to be a deliberate placement -- more than 70% of a cell away, which
    /// stops taps just off the edge from dropping a stone on the first line.
    public func point(at location: CGPoint) -> Point? {
        let col = Int(((location.x - padding) / cell).rounded())
        let row = Int(((location.y - padding) / cell).rounded())
        guard (0..<size).contains(col), (0..<size).contains(row) else { return nil }
        let candidate = Point(col: col, row: row)
        let centre = center(of: candidate)
        let distance = hypot(location.x - centre.x, location.y - centre.y)
        return distance <= cell * 0.7 ? candidate : nil
    }

    /// Star points for the size, in board coordinates.
    public var starPoints: [Point] { Handicap.starPoints(size: size) }

    /// Column letter for the coordinate strip.
    public func columnLabel(_ col: Int) -> String { String(Coordinate.columnLetters[col]) }

    /// Row number for the coordinate strip: rows count from the bottom.
    public func rowLabel(_ row: Int) -> String { String(size - row) }
}

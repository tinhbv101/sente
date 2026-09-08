import Foundation

/// The stone occupying an intersection, plus an off-board sentinel.
///
/// The sentinel lets neighbour loops run without bounds checks: the board is
/// stored with a one-cell border on every side.
enum Cell: UInt8, Sendable {
    case empty = 0
    case black = 1
    case white = 2
    case offBoard = 3

    init(_ player: Player) { self = player == .black ? .black : .white }

    var player: Player? {
        switch self {
        case .black: .black
        case .white: .white
        default: nil
        }
    }
}

/// An immutable position. Every mutation returns a new `Board`.
public struct Board: Equatable, Sendable {
    public let size: Int
    let width: Int
    var cells: [Cell]

    public init(size: Int) {
        precondition(BoardSize.isSupported(size), "unsupported board size \(size)")
        self.size = size
        self.width = size + 2
        self.cells = [Cell](repeating: .offBoard, count: width * width)
        for row in 0..<size {
            for col in 0..<size {
                cells[(row + 1) * width + (col + 1)] = .empty
            }
        }
    }

    // MARK: - Indexing

    @inline(__always)
    func index(col: Int, row: Int) -> Int { (row + 1) * width + (col + 1) }

    @inline(__always)
    func index(_ point: Point) -> Int { index(col: point.col, row: point.row) }

    @inline(__always)
    func point(at index: Int) -> Point {
        Point(col: index % width - 1, row: index / width - 1)
    }

    @inline(__always)
    func forEachNeighbour(_ index: Int, _ body: (Int) -> Void) {
        body(index - width)
        body(index - 1)
        body(index + 1)
        body(index + width)
    }

    public func contains(_ point: Point) -> Bool {
        point.col >= 0 && point.row >= 0 && point.col < size && point.row < size
    }

    // MARK: - Reading

    public subscript(_ point: Point) -> Player? {
        contains(point) ? cells[index(point)].player : nil
    }

    public func isEmpty(_ point: Point) -> Bool {
        contains(point) && cells[index(point)] == .empty
    }

    /// Every on-board intersection, top-left to bottom-right.
    public var allPoints: [Point] {
        (0..<size).flatMap { row in (0..<size).map { Point(col: $0, row: row) } }
    }

    public func stones(of player: Player) -> [Point] {
        allPoints.filter { self[$0] == player }
    }

    // MARK: - Chains

    /// The maximal connected group containing `point`, or an empty set if the
    /// intersection is empty or off the board.
    public func chain(at point: Point) -> Set<Point> {
        guard contains(point), cells[index(point)].player != nil else { return [] }
        let (stones, _) = chainAndLiberties(at: index(point))
        return Set(stones.map { self.point(at: $0) })
    }

    public func liberties(at point: Point) -> Int {
        guard contains(point), cells[index(point)].player != nil else { return 0 }
        return chainAndLiberties(at: index(point)).liberties
    }

    /// Flood fill from `start`, collecting the chain and counting distinct liberties.
    func chainAndLiberties(at start: Int) -> (stones: [Int], liberties: Int) {
        let colour = cells[start]
        guard colour == .black || colour == .white else { return ([], 0) }

        var visited = [Bool](repeating: false, count: cells.count)
        var libertySeen = [Bool](repeating: false, count: cells.count)
        var stack = [start]
        var stones: [Int] = []
        var liberties = 0
        visited[start] = true

        while let current = stack.popLast() {
            stones.append(current)
            forEachNeighbour(current) { neighbour in
                switch cells[neighbour] {
                case .empty:
                    if !libertySeen[neighbour] {
                        libertySeen[neighbour] = true
                        liberties += 1
                    }
                case colour:
                    if !visited[neighbour] {
                        visited[neighbour] = true
                        stack.append(neighbour)
                    }
                default:
                    break
                }
            }
        }
        return (stones, liberties)
    }

    // MARK: - Writing (internal; the public API always returns new values)

    func placing(_ player: Player, at point: Point) -> Board {
        var copy = self
        copy.cells[index(point)] = Cell(player)
        return copy
    }

    public func clearing(_ points: [Point]) -> Board {
        var copy = self
        for point in points { copy.cells[index(point)] = .empty }
        return copy
    }

    /// Compact wire form: one character per intersection, read left-to-right,
    /// top-to-bottom. See docs/06 §3.4.
    public var wireString: String {
        var out = String()
        out.reserveCapacity(size * size)
        for row in 0..<size {
            for col in 0..<size {
                switch cells[index(col: col, row: row)] {
                case .black: out.append("b")
                case .white: out.append("w")
                default: out.append(".")
                }
            }
        }
        return out
    }

    public init?(size: Int, wireString: String) {
        guard BoardSize.isSupported(size), wireString.count == size * size else { return nil }
        self.init(size: size)
        for (offset, character) in wireString.enumerated() {
            let point = Point(col: offset % size, row: offset / size)
            switch character {
            case "b": cells[index(point)] = .black
            case "w": cells[index(point)] = .white
            case ".": break
            default: return nil
            }
        }
    }
}

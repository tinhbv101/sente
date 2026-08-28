import Foundation

/// Board hashing. The constants live in `Zobrist+Table.swift`, generated from
/// `rules-spec/zobrist_table.json` so the Swift and Go engines hash identically.
public enum Zobrist {
    /// Table index. Boards smaller than 19x19 use the leading slice of the table.
    @inline(__always)
    static func tableIndex(size: Int, col: Int, row: Int) -> Int { row * size + col }

    @inline(__always)
    public static func value(size: Int, col: Int, row: Int, player: Player) -> UInt64 {
        let index = tableIndex(size: size, col: col, row: row)
        return player == .black ? blackTable[index] : whiteTable[index]
    }

    /// XOR of every occupied intersection. Colour to move is deliberately excluded:
    /// positional superko compares board positions only (docs/02 §4.2).
    public static func hash(of board: Board) -> UInt64 {
        var hash: UInt64 = 0
        for row in 0..<board.size {
            for col in 0..<board.size {
                guard let player = board.cells[board.index(col: col, row: row)].player else { continue }
                hash ^= value(size: board.size, col: col, row: row, player: player)
            }
        }
        return hash
    }
}

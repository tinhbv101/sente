import Foundation

/// Conversion between board points and the human coordinates used on the wire,
/// in logs and in bug reports: column letters A-T with I omitted, rows numbered
/// from the bottom. See docs/06 §1.
public enum Coordinate {
    public static let columnLetters = Array("ABCDEFGHJKLMNOPQRST")

    public static func text(_ point: Point, size: Int) -> String {
        "\(columnLetters[point.col])\(size - point.row)"
    }

    /// Parses `"d4"` / `"Q16"`. Returns nil for anything off the board or malformed.
    public static func point(_ text: String, size: Int) -> Point? {
        let trimmed = text.trimmingCharacters(in: .whitespaces).uppercased()
        guard let letter = trimmed.first,
              let col = columnLetters.firstIndex(of: letter),
              col < size,
              let number = Int(trimmed.dropFirst()),
              number >= 1, number <= size
        else { return nil }
        return Point(col: col, row: size - number)
    }

    /// SGF uses `a`-`s` for both axes with the origin at the top left -- a different
    /// system from the display coordinates above, and a classic source of off-by-ones.
    public static func sgfText(_ point: Point) -> String {
        let colScalar = UnicodeScalar(UInt8(97 + point.col))
        let rowScalar = UnicodeScalar(UInt8(97 + point.row))
        return "\(colScalar)\(rowScalar)"
    }

    public static func sgfPoint(_ text: String, size: Int) -> Point? {
        let scalars = Array(text.unicodeScalars)
        guard scalars.count == 2 else { return nil }
        let col = Int(scalars[0].value) - 97
        let row = Int(scalars[1].value) - 97
        guard col >= 0, row >= 0, col < size, row < size else { return nil }
        return Point(col: col, row: row)
    }
}

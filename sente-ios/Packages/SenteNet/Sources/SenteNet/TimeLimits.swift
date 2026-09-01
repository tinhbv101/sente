import Foundation

/// The clock a player may ask for, by board size. Mirrors `game.MaxMainTime` on
/// the server, which is the arbiter; this only keeps the picker from offering
/// what the server would refuse. `/v1/config` publishes the same numbers.
public enum TimeLimits {
    public static func maxMainTimeMs(boardSize: Int) -> Int {
        switch boardSize {
        case ...9: return 3 * 3_600_000
        case ...13: return 9 * 3_600_000
        default: return 24 * 3_600_000
        }
    }

    /// Sensible steps up to and including the cap for the board.
    public static func mainTimeChoicesMs(boardSize: Int) -> [Int] {
        let minutes = [5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 240, 360, 540, 720, 1440]
        let cap = maxMainTimeMs(boardSize: boardSize)
        return minutes.map { $0 * 60_000 }.filter { $0 <= cap }
    }

    /// Days per move a correspondence game may use.
    public static let daysPerMoveChoices = Array(1...7)

    /// "10 phút", "1 giờ 30 phút", "3 giờ".
    public static func format(ms: Int) -> String {
        let totalMinutes = ms / 60_000
        let hours = totalMinutes / 60, minutes = totalMinutes % 60
        switch (hours, minutes) {
        case (0, _): return String(localized: "\(minutes) phút", bundle: SenteNetL10n.bundle())
        case (_, 0): return String(localized: "\(hours) giờ", bundle: SenteNetL10n.bundle())
        default: return String(localized: "\(hours) giờ \(minutes) phút", bundle: SenteNetL10n.bundle())
        }
    }
}

import Foundation

/// The bot as a single-player mode: the first two levels are open; beating a
/// level unlocks the next. Watching bots never counts.
enum BotLadder {
    static func highestBeaten(_ defaults: UserDefaults = .standard) -> Int {
        defaults.integer(forKey: "botBeaten")
    }

    static func isUnlocked(_ level: BotLevel, defaults: UserDefaults = .standard) -> Bool {
        level.rawValue <= max(highestBeaten(defaults) + 1, BotLevel.greedy.rawValue)
    }

    static func recordWin(over level: BotLevel, defaults: UserDefaults = .standard) {
        if level.rawValue > highestBeaten(defaults) {
            defaults.set(level.rawValue, forKey: "botBeaten")
        }
    }
}

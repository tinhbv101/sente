import Foundation

/// One tsumego a day, rotating through the pool; solving on consecutive days
/// builds a streak. Everything lives on the device.
enum DailyPuzzles {
    static let library = LessonLibrary.load(resource: "Puzzles")
    static var all: [Lesson] { library.chapters.flatMap(\.lessons) }

    /// Whole days since the Unix epoch, in the person's calendar.
    static func dayNumber(_ date: Date = Date(), calendar: Calendar = .current) -> Int {
        calendar.dateComponents([.day], from: Date(timeIntervalSince1970: 0),
                                to: calendar.startOfDay(for: date)).day ?? 0
    }

    static func puzzle(forDay day: Int) -> Lesson? {
        guard !all.isEmpty else { return nil }
        return all[((day % all.count) + all.count) % all.count]
    }

    static func status(day: Int, defaults: UserDefaults = .standard) -> (streak: Int, doneToday: Bool) {
        let last = defaults.integer(forKey: "puzzleLastDay")
        let streak = defaults.integer(forKey: "puzzleStreak")
        if last == day { return (streak, true) }
        if last == day - 1 { return (streak, false) }
        return (0, false)
    }

    /// Called when today's puzzle is completed. Same-day repeats change nothing.
    static func recordSolved(day: Int, defaults: UserDefaults = .standard) {
        let last = defaults.integer(forKey: "puzzleLastDay")
        guard last != day else { return }
        let streak = last == day - 1 ? defaults.integer(forKey: "puzzleStreak") + 1 : 1
        defaults.set(day, forKey: "puzzleLastDay")
        defaults.set(streak, forKey: "puzzleStreak")
    }
}

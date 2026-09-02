import Foundation

/// What the home-screen widget shows, written by the app after every refresh
/// and read in the widget process. Compiled into both targets; the app group
/// is the only channel between them.
struct WidgetSummary: Codable, Equatable {
    static let appGroup = "group.app.sente.go"
    static let key = "widgetSummary"
    static let widgetKind = "SenteTurn"

    var myTurn: Int
    var waiting: Int
    var nextDeadline: Date?
    var nextOpponent: String?

    static func load(defaults: UserDefaults? = UserDefaults(suiteName: appGroup)) -> WidgetSummary? {
        guard let data = defaults?.data(forKey: key) else { return nil }
        return try? JSONDecoder().decode(WidgetSummary.self, from: data)
    }

    func save(defaults: UserDefaults? = UserDefaults(suiteName: WidgetSummary.appGroup)) {
        guard let defaults, let data = try? JSONEncoder().encode(self) else { return }
        defaults.set(data, forKey: WidgetSummary.key)
    }
}

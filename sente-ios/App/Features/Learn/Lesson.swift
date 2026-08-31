import Foundation
import GoKit

/// Lessons are data, not code: App/Resources/Lessons.json holds every chapter,
/// in both languages. GoKit is the referee, and LessonContentTests replays the
/// whole file through it, so wrong content fails CI instead of teaching wrong Go.

/// A piece of text in both app languages.
struct LText: Decodable, Hashable {
    let vi: String
    let en: String
    var text: String { Bundle.main.preferredLocalizations.first == "vi" ? vi : en }
}

struct LessonLibrary: Decodable {
    let chapters: [LessonChapter]

    /// Loaded once; the file ships in the bundle and never changes at runtime.
    static let shared = load()

    static func load(bundle: Bundle = .main) -> LessonLibrary {
        guard let url = bundle.url(forResource: "Lessons", withExtension: "json"),
              let data = try? Data(contentsOf: url),
              let library = try? JSONDecoder().decode(LessonLibrary.self, from: data) else {
            assertionFailure("Lessons.json is missing or malformed")
            return LessonLibrary(chapters: [])
        }
        return library
    }

    var lessonCount: Int { chapters.reduce(0) { $0 + $1.lessons.count } }
}

struct LessonChapter: Decodable, Identifiable, Hashable {
    let id: String
    let icon: String
    let title: LText
    let lessons: [Lesson]
}

struct Lesson: Decodable, Identifiable, Hashable {
    let id: String
    let title: LText
    let steps: [LessonStep]
}

/// One screenful. `board` nil on a task means "continue from the previous step's
/// position", which is how multi-move sequences are written.
struct LessonStep: Decodable, Hashable {
    enum Kind: String, Decodable, Hashable { case info, task }
    let kind: Kind
    let text: LText
    var size: Int? = 9
    var board: [String]?
    var toPlay: String?
    var highlight: [String]?
    var correct: [String]?
    var reply: String?
    var captures: Int?
    var success: LText?
    var wrong: LText?
}

/// Which lessons are done, on this device.
struct LessonProgress {
    private static let key = "lessonsDone"
    private let defaults: UserDefaults
    init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    var done: Set<String> { Set(defaults.stringArray(forKey: Self.key) ?? []) }
    func isDone(_ id: String) -> Bool { done.contains(id) }
    func markDone(_ id: String) {
        defaults.set(Array(done.union([id])).sorted(), forKey: Self.key)
    }
}

enum LessonBoard {
    /// Rows top-down, 'b'/'w'/'.' per point — the same convention as the test
    /// fixtures, chosen because a diagram in a file should look like the board.
    static func engine(size: Int, rows: [String]?, toPlay: Player) -> GameEngine {
        var black: [Point] = [], white: [Point] = []
        for (index, row) in (rows ?? []).enumerated() {
            for (column, character) in row.enumerated() {
                let point = Point(col: column, row: index)
                if character == "b" { black.append(point) }
                if character == "w" { white.append(point) }
            }
        }
        return GameEngine.position(size: size, black: black, white: white, toPlay: toPlay)
    }
}

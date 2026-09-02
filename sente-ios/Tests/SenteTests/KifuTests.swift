import XCTest
import GoKit
@testable import Sente

/// The on-device game shelf: storage round-trips with a cap, and finished games
/// archive themselves — except bot-versus-bot scenery.
@MainActor
final class KifuTests: XCTestCase {
    final class MemoryLibrary: KifuStoring, @unchecked Sendable {
        private let lock = NSLock()
        private var entries: [KifuEntry] = []
        func load() -> [KifuEntry] { lock.withLock { entries } }
        func add(_ entry: KifuEntry) { lock.withLock { entries.insert(entry, at: 0) } }
        func remove(id: UUID) { lock.withLock { entries.removeAll { $0.id == id } } }
    }

    private func entry(_ label: String) -> KifuEntry {
        KifuEntry(id: UUID(), date: Date(), mode: "local", blackName: label, whiteName: "w",
                  size: 9, resultText: "Đen thắng", sgf: "(;GM[1]SZ[9])")
    }

    func testFileLibraryRoundTripsNewestFirstAndCaps() {
        let library = FileKifuLibrary(name: "kifu-test-\(UUID().uuidString)")
        defer { library.clear() }

        library.add(entry("first"))
        library.add(entry("second"))
        XCTAssertEqual(library.load().map(\.blackName), ["second", "first"])

        let doomed = library.load()[0]
        library.remove(id: doomed.id)
        XCTAssertEqual(library.load().map(\.blackName), ["first"])

        for index in 0..<(FileKifuLibrary.cap + 5) { library.add(entry("g\(index)")) }
        XCTAssertEqual(library.load().count, FileKifuLibrary.cap)
        XCTAssertEqual(library.load().first?.blackName, "g\(FileKifuLibrary.cap + 4)")
    }

    func testAFinishedGameArchivesButWatchDoesNot() {
        let library = MemoryLibrary()
        var config = LocalGameConfig()
        config.whiteBot = BotLevel.novice.rawValue
        let store = LocalGameStore(config: config, storage: NullGameStorage(), library: library)
        store.resign()
        XCTAssertEqual(library.load().count, 1)
        XCTAssertEqual(library.load().first?.mode, "bot")
        XCTAssertTrue(library.load().first!.sgf.contains("RE["), "the SGF must carry the result")

        var watch = LocalGameConfig()
        watch.blackBot = BotLevel.novice.rawValue
        watch.whiteBot = BotLevel.novice.rawValue
        let watchStore = LocalGameStore(config: watch, storage: NullGameStorage(), library: library)
        watchStore.paused = true
        watchStore.resign()
        XCTAssertEqual(library.load().count, 1, "watch games are scenery, not records")
    }
}

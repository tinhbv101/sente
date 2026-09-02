import Foundation
import GoKit
import SwiftUI
import SenteUI

/// One finished on-device game, kept as SGF so replay, analysis and export all
/// share a single format.
struct KifuEntry: Codable, Identifiable, Equatable, Hashable {
    let id: UUID
    let date: Date
    /// "local" (two people, one phone) or "bot".
    let mode: String
    let blackName: String
    let whiteName: String
    let size: Int
    let resultText: String
    let sgf: String

    static func verdict(_ result: GameResult) -> String {
        guard let winner = result.winner else { return LS(localized: "Vô hiệu") }
        let name = winner == .black ? LS(localized: "Đen thắng") : LS(localized: "Trắng thắng")
        switch result.reason {
        case .resignation: return LS(localized: "\(name) (xin thua)")
        default: return name
        }
    }
}

protocol KifuStoring: Sendable {
    func load() -> [KifuEntry]
    func add(_ entry: KifuEntry)
    func remove(id: UUID)
}

/// Newest first, capped so the file cannot grow without bound.
struct FileKifuLibrary: KifuStoring {
    var name = "kifu-library"
    static let cap = 200

    private var url: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        try? FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        return base.appending(path: "\(name).json")
    }

    func load() -> [KifuEntry] {
        guard let data = try? Data(contentsOf: url) else { return [] }
        return (try? JSONDecoder().decode([KifuEntry].self, from: data)) ?? []
    }

    func add(_ entry: KifuEntry) {
        var all = load()
        all.insert(entry, at: 0)
        write(Array(all.prefix(Self.cap)))
    }

    func remove(id: UUID) { write(load().filter { $0.id != id }) }

    func clear() { try? FileManager.default.removeItem(at: url) }

    private func write(_ entries: [KifuEntry]) {
        if let data = try? JSONEncoder().encode(entries) { try? data.write(to: url, options: .atomic) }
    }
}

enum KifuRoute: Hashable { case list }

/// The saved-games shelf: every finished local or bot game, newest first.
struct KifuLibraryView: View {
    @State private var entries: [KifuEntry] = []
    private let library = FileKifuLibrary()

    var body: some View {
        List {
            ForEach(entries) { entry in
                NavigationLink(value: entry) {
                    HStack(spacing: 12) {
                        Image(systemName: entry.mode == "bot" ? "cpu" : "person.2")
                            .foregroundStyle(Tokens.indigo)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("\(entry.blackName) vs \(entry.whiteName)")
                                .font(.callout.weight(.semibold))
                            Text("\(entry.size)×\(entry.size) · \(entry.resultText) · \(entry.date.formatted(date: .abbreviated, time: .shortened))")
                                .font(.caption).foregroundStyle(Tokens.inkSecondary)
                        }
                    }
                }
            }
            .onDelete { offsets in
                for offset in offsets { library.remove(id: entries[offset].id) }
                entries.remove(atOffsets: offsets)
            }
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .background(Tokens.paper.ignoresSafeArea())
        .foregroundStyle(Tokens.ink)
        .navigationTitle("Ván đã lưu")
        .navigationBarTitleDisplayMode(.inline)
        .overlay {
            if entries.isEmpty {
                ContentUnavailableView {
                    Label("Chưa có ván nào được lưu", systemImage: "books.vertical")
                } description: {
                    Text("Ván chơi trên máy này và ván đấu bot sẽ tự lưu vào đây khi kết thúc.")
                }
            }
        }
        .onAppear { entries = library.load() }
    }
}

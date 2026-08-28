import Foundation
import XCTest
@testable import GoKit

/// Shape of a conformance vector. See rules-spec/SCHEMA.md.
struct Vector: Decodable {
    struct Setup: Decodable {
        var black: [String] = []
        var white: [String] = []
    }

    struct ResultExpect: Decodable {
        let winner: String?
        let reason: String
    }

    struct Expect: Decodable {
        var legal: Bool?
        var reason: String?
        var captured: [String]?
        var capturesAfter: [String: Int]?
        var koPoint: String?
        var koPointAbsent: Bool?
        var phase: String?
        var result: ResultExpect?
        var territory: [String: Int]?
        var neutral: Int?
        var area: [String: Int]?
        var score: [String: Double]?
        var winner: String?
        var margin: Double?
        var passAlive: [String: [String]]?
        var handicapStones: [String]?
        var toPlay: String?
        var komi: Double?
        var moveNumber: Int?
        var historyContainsStart: Bool?
        var historySize: Int?
        var sgfContains: [String]?
        var roundTrip: Bool?
    }

    let id: String
    var description: String?
    var rules: String?
    var boardSize: Int?
    var komi: Double?
    var handicap: Int?
    var setup: Setup?
    var toPlay: String?
    var moves: [String]?
    var move: String?
    var moveBy: String?
    var captures: [String: Int]?
    var deadStones: [String]?
    let expect: Expect

    /// Filled in by the loader, never present in the JSON.
    var group: String?
}

enum VectorLoader {
    /// Walks up from this source file until it finds the shared spec directory, so
    /// the Swift and Go suites provably read the same files.
    static let vectorsURL: URL = {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<10 {
            url = url.deletingLastPathComponent()
            let candidate = url.appendingPathComponent("rules-spec/vectors")
            if FileManager.default.fileExists(atPath: candidate.path) { return candidate }
        }
        fatalError("rules-spec/vectors not found above \(#filePath)")
    }()

    static func load(group: String) throws -> [Vector] {
        let directory = vectorsURL.appendingPathComponent(group)
        let files = try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "json" }
            .sorted { $0.lastPathComponent < $1.lastPathComponent }

        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try files.map { url in
            var vector = try decoder.decode(Vector.self, from: Data(contentsOf: url))
            vector.group = group
            return vector
        }
    }

    static var allGroups: [String] {
        (try? FileManager.default.contentsOfDirectory(atPath: vectorsURL.path).sorted()) ?? []
    }
}

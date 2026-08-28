import Foundation

/// Reconnect schedule from docs/06 §3.11: immediate, then 1s doubling to a 30s
/// cap, each with ±25% jitter so a fleet of clients does not stampede.
public struct Backoff: Sendable {
    public private(set) var attempt = 0
    public let cap: TimeInterval

    public init(cap: TimeInterval = 30) { self.cap = cap }

    /// The delay before the next attempt, advancing the schedule.
    public mutating func next(random: (ClosedRange<Double>) -> Double = { Double.random(in: $0) }) -> TimeInterval {
        defer { attempt += 1 }
        if attempt == 0 { return 0 }
        let base = min(cap, pow(2, Double(attempt - 1)))
        return base * random(0.75...1.25)
    }

    public mutating func reset() { attempt = 0 }
}

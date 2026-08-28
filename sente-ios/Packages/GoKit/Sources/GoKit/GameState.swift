import Foundation

/// A snapshot of a game. Plain data: no history, no rule configuration, so it is
/// cheap to copy, compare and encode. `GameEngine` owns the parts needed to
/// decide legality.
public struct GameState: Equatable, Sendable {
    public let board: Board
    public let toPlay: Player
    public let moveNumber: Int
    /// Set only for basic ko (Japanese); superko is checked against the history.
    public let koPoint: Point?
    public let captures: Captures
    public let consecutivePasses: Int
    public let phase: Phase
    public let result: GameResult?
    public let boardHash: UInt64

    init(
        board: Board,
        toPlay: Player,
        moveNumber: Int,
        koPoint: Point?,
        captures: Captures,
        consecutivePasses: Int,
        phase: Phase,
        result: GameResult?,
        boardHash: UInt64
    ) {
        self.board = board
        self.toPlay = toPlay
        self.moveNumber = moveNumber
        self.koPoint = koPoint
        self.captures = captures
        self.consecutivePasses = consecutivePasses
        self.phase = phase
        self.result = result
        self.boardHash = boardHash
    }

    public var size: Int { board.size }
}

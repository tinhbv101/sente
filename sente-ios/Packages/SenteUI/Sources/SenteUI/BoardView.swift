import SwiftUI
import GoKit

/// What the board needs to draw, independent of where it came from.
public struct BoardSnapshot: Equatable, Sendable {
    public var board: Board
    public var lastMove: Point?
    /// Stone placed locally but not yet acknowledged by the server (ADR-007).
    public var pending: Point?
    public var deadStones: Set<Point>
    public var territoryBlack: Set<Point>
    public var territoryWhite: Set<Point>

    public init(board: Board, lastMove: Point? = nil, pending: Point? = nil,
                deadStones: Set<Point> = [], territoryBlack: Set<Point> = [],
                territoryWhite: Set<Point> = []) {
        self.board = board; self.lastMove = lastMove; self.pending = pending
        self.deadStones = deadStones; self.territoryBlack = territoryBlack; self.territoryWhite = territoryWhite
    }
}

/// The goban. Drawn in three Canvas layers so the static wood and grid are not
/// repainted while stones animate (docs/07 §6.1), with a drag gesture that shows
/// a ghost stone offset above the finger so the intersection stays visible (§6.2).
public struct BoardView: View {
    public var snapshot: BoardSnapshot
    public var interactive: Bool
    public var showsCoordinates: Bool
    public var colourBlindSymbols: Bool
    /// Returns nil when the point is legal, or a short reason when it is not.
    public var legality: (Point) -> String?
    public var onPlace: (Point) -> Void
    public var onTapChain: ((Point) -> Void)?
    public var ghostPlayer: StonePlayer
    /// How far above the finger the target intersection sits. Zero means the
    /// stone lands exactly where you touch. Off by default: on a 9×9 board a cell
    /// is about 40pt, so any offset moves a plain tap onto the next row, and the
    /// bottom row can only be reached by touching below the board.
    public var fingerOffset: CGFloat

    @State private var drag: DragState?
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private struct DragState: Equatable {
        var target: Point?
        var reason: String?
    }

    public init(snapshot: BoardSnapshot, ghostPlayer: StonePlayer = .black, interactive: Bool = true,
                showsCoordinates: Bool = true, colourBlindSymbols: Bool = false,
                fingerOffset: CGFloat = 0,
                legality: @escaping (Point) -> String? = { _ in nil },
                onPlace: @escaping (Point) -> Void = { _ in },
                onTapChain: ((Point) -> Void)? = nil) {
        self.snapshot = snapshot; self.ghostPlayer = ghostPlayer; self.interactive = interactive
        self.showsCoordinates = showsCoordinates; self.colourBlindSymbols = colourBlindSymbols
        self.fingerOffset = fingerOffset
        self.legality = legality; self.onPlace = onPlace; self.onTapChain = onTapChain
    }

    /// The offset used when the player opts in: enough to clear a fingertip.
    public static let defaultFingerOffset: CGFloat = 44

    public var body: some View {
        GeometryReader { proxy in
            let side = min(proxy.size.width, proxy.size.height)
            let geometry = BoardGeometry(size: snapshot.board.size, side: side, showsCoordinates: showsCoordinates)
            ZStack {
                Canvas { context, _ in drawStatic(geometry, in: &context) }
                    .drawingGroup()
                Canvas { context, _ in drawStones(geometry, in: &context) }
                Canvas { context, _ in drawOverlay(geometry, in: &context) }
                accessibilityLayer(geometry)
            }
            .frame(width: side, height: side)
            .clipShape(RoundedRectangle(cornerRadius: side * 0.024, style: .continuous))
            .shadow(color: .black.opacity(0.25), radius: 10, y: 4)
            .gesture(interactive ? placementGesture(geometry) : nil)
            .onTapGesture { location in
                guard let onTapChain, let point = geometry.point(at: location),
                      snapshot.board[point] != nil else { return }
                onTapChain(point)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .aspectRatio(1, contentMode: .fit)
    }

    // MARK: - Gesture

    private func placementGesture(_ geometry: BoardGeometry) -> some Gesture {
        DragGesture(minimumDistance: 0)
            .onChanged { value in
                let raw = CGPoint(x: value.location.x, y: value.location.y - fingerOffset)
                let target = geometry.point(at: raw).flatMap { snapshot.board.isEmpty($0) ? $0 : nil }
                let next = DragState(target: target, reason: target.flatMap(legality))
                if next != drag {
                    if next.target != drag?.target { Haptics.selection() }
                    drag = next
                }
            }
            .onEnded { _ in
                defer { drag = nil }
                guard let target = drag?.target else { return }
                if drag?.reason == nil {
                    Haptics.impact()
                    onPlace(target)
                } else {
                    Haptics.warning()
                }
            }
    }

    // MARK: - Drawing

    private var isDark: Bool { colorScheme == .dark }

    private func drawStatic(_ g: BoardGeometry, in context: inout GraphicsContext) {
        let rect = CGRect(x: 0, y: 0, width: g.side, height: g.side)
        let wood = Gradient(colors: isDark
            ? [Tokens.Board.woodLightDim, Tokens.Board.woodDarkDim, Tokens.Board.woodLightDim]
            : [Tokens.Board.woodLight, Tokens.Board.woodDark, Tokens.Board.woodLight])
        context.fill(Path(rect), with: .linearGradient(wood, startPoint: .zero,
                                                       endPoint: CGPoint(x: g.side, y: g.side)))

        // Faint grain: deterministic, so the board does not shimmer on redraw.
        var grain = Path()
        for i in 0..<48 {
            let y = CGFloat(i) * g.side / 48 + CGFloat((i * 37) % 11) * 0.3
            grain.move(to: CGPoint(x: 0, y: y))
            grain.addLine(to: CGPoint(x: g.side, y: y + CGFloat((i * 13) % 5) - 2))
        }
        context.stroke(grain, with: .color(.white.opacity(0.06)), lineWidth: 1)

        var lines = Path()
        for i in 0..<g.size {
            let offset = g.padding + CGFloat(i) * g.cell
            lines.move(to: CGPoint(x: g.padding, y: offset))
            lines.addLine(to: CGPoint(x: g.side - g.padding, y: offset))
            lines.move(to: CGPoint(x: offset, y: g.padding))
            lines.addLine(to: CGPoint(x: offset, y: g.side - g.padding))
        }
        context.stroke(lines, with: .color(Tokens.Board.line.opacity(0.85)), lineWidth: max(0.6, g.cell * 0.026))
        let border = CGRect(x: g.padding, y: g.padding, width: g.side - g.padding * 2, height: g.side - g.padding * 2)
        context.stroke(Path(border), with: .color(Tokens.Board.line), lineWidth: max(1, g.cell * 0.05))

        let starRadius = max(1.4, g.cell * 0.075)
        for star in g.starPoints {
            let centre = g.center(of: star)
            context.fill(Path(ellipseIn: CGRect(x: centre.x - starRadius, y: centre.y - starRadius,
                                                width: starRadius * 2, height: starRadius * 2)),
                         with: .color(Tokens.Board.line))
        }

        if showsCoordinates {
            let font = Font.system(size: max(7, g.cell * 0.5), weight: .semibold, design: .monospaced)
            for i in 0..<g.size {
                let along = g.padding + CGFloat(i) * g.cell
                context.draw(Text(g.columnLabel(i)).font(font).foregroundStyle(Tokens.Board.line.opacity(0.6)),
                             at: CGPoint(x: along, y: g.padding * 0.46))
                context.draw(Text(g.rowLabel(i)).font(font).foregroundStyle(Tokens.Board.line.opacity(0.6)),
                             at: CGPoint(x: g.padding * 0.44, y: along))
            }
        }
    }

    private func drawStones(_ g: BoardGeometry, in context: inout GraphicsContext) {
        for point in snapshot.board.allPoints {
            guard let player = snapshot.board[point] else { continue }
            let dead = snapshot.deadStones.contains(point)
            StoneShape.draw(player == .black ? .black : .white, at: g.center(of: point),
                            radius: g.stoneRadius, in: &context, opacity: dead ? 0.3 : 1,
                            symbol: colourBlindSymbols)
        }
        if let pending = snapshot.pending, snapshot.board[pending] == nil {
            StoneShape.draw(ghostPlayer, at: g.center(of: pending), radius: g.stoneRadius,
                            in: &context, opacity: 0.55, symbol: colourBlindSymbols)
        }
    }

    private func drawOverlay(_ g: BoardGeometry, in context: inout GraphicsContext) {
        let square = g.cell * 0.30
        for point in snapshot.territoryBlack {
            let c = g.center(of: point)
            context.fill(Path(CGRect(x: c.x - square / 2, y: c.y - square / 2, width: square, height: square)),
                         with: .color(Tokens.territoryBlack))
        }
        for point in snapshot.territoryWhite {
            let c = g.center(of: point)
            let rect = CGRect(x: c.x - square / 2, y: c.y - square / 2, width: square, height: square)
            context.fill(Path(rect), with: .color(Tokens.territoryWhite))
            context.stroke(Path(rect), with: .color(Tokens.Board.line.opacity(0.34)), lineWidth: 0.7)
        }

        // Seal-red dot on the last move: the only hot colour on the board.
        if let last = snapshot.lastMove, snapshot.board[last] != nil {
            let c = g.center(of: last)
            let r = g.stoneRadius * 0.30
            context.fill(Path(ellipseIn: CGRect(x: c.x - r, y: c.y - r, width: r * 2, height: r * 2)),
                         with: .color(Tokens.seal))
        }

        if let drag, let target = drag.target {
            let c = g.center(of: target)
            var cross = Path()
            cross.move(to: CGPoint(x: g.padding, y: c.y)); cross.addLine(to: CGPoint(x: g.side - g.padding, y: c.y))
            cross.move(to: CGPoint(x: c.x, y: g.padding)); cross.addLine(to: CGPoint(x: c.x, y: g.side - g.padding))
            let tint: Color = drag.reason == nil ? Tokens.seal : .red
            context.stroke(cross, with: .color(tint.opacity(0.75)), style: StrokeStyle(lineWidth: 1.2, dash: [3, 3]))
            StoneShape.draw(ghostPlayer, at: c, radius: g.stoneRadius, in: &context,
                            opacity: drag.reason == nil ? 0.6 : 0.35, symbol: colourBlindSymbols)
            if let reason = drag.reason {
                context.draw(Text(reason).font(.caption.weight(.semibold)).foregroundStyle(.white),
                             at: CGPoint(x: c.x, y: max(g.cell, c.y - g.cell * 1.6)))
            }
        }
    }

    // MARK: - Accessibility (docs/07 §6.4)

    /// Canvas produces no accessibility elements, so every intersection gets one.
    @ViewBuilder
    private func accessibilityLayer(_ g: BoardGeometry) -> some View {
        Color.clear
            .accessibilityElement(children: .contain)
            .accessibilityLabel("Bàn cờ \(g.size) × \(g.size)")
            .accessibilityChildren {
                ForEach(snapshot.board.allPoints, id: \.self) { point in
                    Rectangle()
                        .fill(.clear)
                        .frame(width: g.cell, height: g.cell)
                        .position(g.center(of: point))
                        .accessibilityLabel(accessibilityLabel(for: point, size: g.size))
                        .accessibilityHint(interactive && snapshot.board.isEmpty(point) && legality(point) == nil
                                           ? String(localized: "Chạm hai lần để đặt quân", bundle: .main) : "")
                        .accessibilityAddTraits(snapshot.lastMove == point ? .isSelected : [])
                        .accessibilityAction { if interactive { onPlace(point) } }
                }
            }
    }

    private func accessibilityLabel(for point: Point, size: Int) -> String {
        let name = Coordinate.text(point, size: size)
        switch snapshot.board[point] {
        case .black: return String(localized: "\(name), quân đen", bundle: .main)
        case .white: return String(localized: "\(name), quân trắng", bundle: .main)
        case nil: return String(localized: "\(name), trống", bundle: .main)
        }
    }
}

/// Thin wrapper so the board can be unit-built on macOS, where UIKit haptics do
/// not exist.
enum Haptics {
    static func selection() {
        #if canImport(UIKit)
        UISelectionFeedbackGenerator().selectionChanged()
        #endif
    }
    static func impact() {
        #if canImport(UIKit)
        UIImpactFeedbackGenerator(style: .medium).impactOccurred()
        #endif
    }
    static func warning() {
        #if canImport(UIKit)
        UINotificationFeedbackGenerator().notificationOccurred(.warning)
        #endif
    }
}

#if canImport(UIKit)
import UIKit
#endif

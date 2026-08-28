import SwiftUI

/// Draws one stone into a Canvas context. Shared by the board, the ghost stone
/// and the game-list thumbnails, so a stone looks the same everywhere.
public enum StoneShape {
    public static func draw(_ player: StonePlayer, at centre: CGPoint, radius: CGFloat,
                            in context: inout GraphicsContext, opacity: Double = 1,
                            symbol: Bool = false) {
        let rect = CGRect(x: centre.x - radius, y: centre.y - radius, width: radius * 2, height: radius * 2)
        var layer = context
        layer.opacity = opacity
        layer.addFilter(.shadow(color: .black.opacity(0.35), radius: radius * 0.3, x: 0, y: radius * 0.12))

        let gradient: Gradient
        let edge: Color
        switch player {
        case .black:
            gradient = Gradient(stops: [
                .init(color: Tokens.Stone.blackHighlight, location: 0),
                .init(color: Tokens.Stone.blackBody, location: 0.42),
                .init(color: Tokens.Stone.blackEdge, location: 1),
            ])
            edge = .black.opacity(0.5)
        case .white:
            gradient = Gradient(stops: [
                .init(color: Tokens.Stone.whiteHighlight, location: 0),
                .init(color: Tokens.Stone.whiteBody, location: 0.5),
                .init(color: Tokens.Stone.whiteEdge, location: 1),
            ])
            edge = Color(red: 0.47, green: 0.42, blue: 0.31).opacity(0.32)
        }
        let highlight = CGPoint(x: centre.x - radius * 0.34, y: centre.y - radius * 0.4)
        layer.fill(Path(ellipseIn: rect), with: .radialGradient(gradient, center: highlight,
                                                                  startRadius: radius * 0.05,
                                                                  endRadius: radius * 1.08))
        layer.stroke(Path(ellipseIn: rect), with: .color(edge), lineWidth: max(0.5, radius * 0.05))

        // Colour-blind mode: a mark that does not rely on hue (NFR-A11Y3).
        if symbol {
            switch player {
            case .black:
                let dot = CGRect(x: centre.x - radius * 0.3, y: centre.y - radius * 0.3, width: radius * 0.6, height: radius * 0.6)
                layer.fill(Path(ellipseIn: dot), with: .color(.white.opacity(0.95)))
            case .white:
                let ring = CGRect(x: centre.x - radius * 0.46, y: centre.y - radius * 0.46, width: radius * 0.92, height: radius * 0.92)
                layer.stroke(Path(ellipseIn: ring), with: .color(.black.opacity(0.8)), lineWidth: radius * 0.2)
            }
        }
    }
}

public enum StonePlayer: Sendable { case black, white }

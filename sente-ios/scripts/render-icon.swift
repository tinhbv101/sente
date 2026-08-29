// Renders the app icon: a black stone with an off-centre highlight on kaya wood,
// with a faint grid and the seal-red last-move dot (docs/README "Tên sản phẩm").
//
//   swift scripts/render-icon.swift <out-dir>
//
// Drawn in code so it can be regenerated at any size and in both appearances
// without a design tool in the loop.
import Foundation
import CoreGraphics
import ImageIO
import UniformTypeIdentifiers

func render(size: CGFloat, dark: Bool) -> CGImage {
    let space = CGColorSpace(name: CGColorSpace.sRGB)!
    let ctx = CGContext(data: nil, width: Int(size), height: Int(size), bitsPerComponent: 8,
                        bytesPerRow: 0, space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    let s = size
    func rgb(_ hex: UInt32, _ a: CGFloat = 1) -> CGColor {
        CGColor(srgbRed: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                blue: CGFloat(hex & 0xFF) / 255, alpha: a)
    }

    // Wood: the same two-stop gradient as the board, dimmed for dark.
    let woodA = dark ? rgb(0xAC7F41) : rgb(0xE6BC7E)
    let woodB = dark ? rgb(0x8A6231) : rgb(0xC7924A)
    let wood = CGGradient(colorsSpace: space, colors: [woodA, woodB, woodA] as CFArray, locations: [0, 0.55, 1])!
    ctx.drawLinearGradient(wood, start: .zero, end: CGPoint(x: s, y: s), options: [])

    // Grain
    ctx.setStrokeColor(rgb(0xFFF3DA, 0.07)); ctx.setLineWidth(s / 512)
    for i in 0..<70 {
        let y = CGFloat(i) * s / 70 + CGFloat((i * 37) % 11) / 11 * s / 140
        ctx.move(to: CGPoint(x: 0, y: y)); ctx.addLine(to: CGPoint(x: s, y: y + s / 300)); ctx.strokePath()
    }

    // Grid lines through the centre, so the stone sits on an intersection and the
    // tile reads as a goban rather than a pattern.
    let line = rgb(0x5C4321, dark ? 0.7 : 0.85)
    ctx.setStrokeColor(line); ctx.setLineWidth(s / 110)
    let cell = s / 4
    for i in 1...3 {
        let at = CGFloat(i) * cell
        ctx.move(to: CGPoint(x: 0, y: at)); ctx.addLine(to: CGPoint(x: s, y: at)); ctx.strokePath()
        ctx.move(to: CGPoint(x: at, y: 0)); ctx.addLine(to: CGPoint(x: at, y: s)); ctx.strokePath()
    }
    let centre = CGPoint(x: s / 2, y: s / 2)

    // Stone: shadow, body, highlight. Just under a cell so the lines stay visible.
    let r = cell * 0.95
    ctx.saveGState()
    ctx.setShadow(offset: CGSize(width: 0, height: -r * 0.12), blur: r * 0.35, color: rgb(0x000000, 0.45))
    ctx.setFillColor(rgb(0x15161A))
    ctx.fillEllipse(in: CGRect(x: centre.x - r, y: centre.y - r, width: r * 2, height: r * 2))
    ctx.restoreGState()

    let body = CGGradient(colorsSpace: space,
                          colors: [rgb(0x6E6F7A), rgb(0x2B2C33), rgb(0x15161A), rgb(0x08080B)] as CFArray,
                          locations: [0, 0.34, 0.82, 1])!
    ctx.saveGState()
    ctx.addEllipse(in: CGRect(x: centre.x - r, y: centre.y - r, width: r * 2, height: r * 2)); ctx.clip()
    // Highlight sits high and left: the "off-centre shine" from the brand note.
    let hi = CGPoint(x: centre.x - r * 0.34, y: centre.y + r * 0.40)
    // Both options, or the disc inside startRadius is left unpainted and the dark
    // shadow fill shows through as a spot on the highlight.
    ctx.drawRadialGradient(body, startCenter: hi, startRadius: 0, endCenter: centre, endRadius: r * 1.08,
                           options: [.drawsBeforeStartLocation, .drawsAfterEndLocation])
    ctx.restoreGState()
    ctx.setStrokeColor(rgb(0x000000, 0.5)); ctx.setLineWidth(r * 0.03)
    ctx.strokeEllipse(in: CGRect(x: centre.x - r, y: centre.y - r, width: r * 2, height: r * 2))

    // Seal-red marker: the one hot colour, as on the board's last move.
    let dot = r * 0.22
    ctx.setFillColor(rgb(dark ? 0xE07268 : 0xC0453A))
    ctx.fillEllipse(in: CGRect(x: centre.x - dot, y: centre.y - dot, width: dot * 2, height: dot * 2))

    return ctx.makeImage()!
}

func write(_ image: CGImage, to url: URL) {
    let dest = CGImageDestinationCreateWithURL(url as CFURL, UTType.png.identifier as CFString, 1, nil)!
    CGImageDestinationAddImage(dest, image, nil)
    CGImageDestinationFinalize(dest)
}

let out = URL(fileURLWithPath: CommandLine.arguments[1])
write(render(size: 1024, dark: false), to: out.appendingPathComponent("AppIcon.png"))
write(render(size: 1024, dark: true), to: out.appendingPathComponent("AppIcon-Dark.png"))
print("rendered AppIcon.png and AppIcon-Dark.png in \(out.path)")

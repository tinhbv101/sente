import SwiftUI

/// Design tokens from docs/07 §10. Two rules: the board keeps its wood tone in
/// dark mode -- players recognise a goban by its colour -- and everything around
/// it inverts. Seal red is the one hot colour and is never used on a button.
public enum Tokens {
    public enum Board {
        public static let woodLight = Color(red: 0.902, green: 0.737, blue: 0.494)   // #E6BC7E
        public static let woodDark = Color(red: 0.780, green: 0.573, blue: 0.290)    // #C7924A
        public static let woodLightDim = Color(red: 0.675, green: 0.498, blue: 0.255)
        public static let woodDarkDim = Color(red: 0.541, green: 0.384, blue: 0.192)
        public static let line = Color(red: 0.361, green: 0.263, blue: 0.129)        // #5C4321
    }

    public enum Stone {
        public static let blackHighlight = Color(red: 0.431, green: 0.435, blue: 0.478)
        public static let blackBody = Color(red: 0.106, green: 0.110, blue: 0.129)
        public static let blackEdge = Color(red: 0.031, green: 0.031, blue: 0.043)
        public static let whiteHighlight = Color.white
        public static let whiteBody = Color(red: 0.969, green: 0.949, blue: 0.902)
        public static let whiteEdge = Color(red: 0.776, green: 0.741, blue: 0.663)
    }

    // Chrome. Each pair is (light, dark); the dark values come from the design
    // mockup's `ui-dark` palette.
    public static let paper = adaptive(light: 0xF4F1EA, dark: 0x0D0C0A)
    public static let sheet = adaptive(light: 0xFFFFFF, dark: 0x1A1815)
    public static let sheetSecondary = adaptive(light: 0xFAF7F0, dark: 0x211E1A)
    public static let ink = adaptive(light: 0x17150F, dark: 0xF2ECE0)
    public static let inkSecondary = adaptive(light: 0x7B7364, dark: 0x918977)
    public static let inkTertiary = adaptive(light: 0xA69E8D, dark: 0x6B6456)
    public static let separator = adaptive(light: 0xE5DFD2, dark: 0x2A2621)
    public static let indigo = adaptive(light: 0x274A73, dark: 0x8CADD4)
    /// Text on an indigo button: white on the deep light-mode tint, near-black on
    /// the pale dark-mode one.
    public static let onIndigo = adaptive(light: 0xFFFFFF, dark: 0x0D0C0A)
    public static let indigoSoft = adaptive(light: 0xE7EDF5, dark: 0x1B2938)
    /// Seal red: "your turn" and the last-move marker only.
    public static let seal = adaptive(light: 0xB8453A, dark: 0xE07268)
    public static let sealSoft = adaptive(light: 0xF7E9E6, dark: 0x2E1D1B)
    public static let territoryBlack = Color.black.opacity(0.62)
    public static let territoryWhite = Color(red: 1, green: 0.988, blue: 0.957).opacity(0.86)

    /// One colour that resolves per appearance. On macOS (package tests) there is
    /// no trait collection, so the light value is used.
    static func adaptive(light: UInt32, dark: UInt32) -> Color {
        #if canImport(UIKit)
        return Color(uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark ? UIColor(hex: dark) : UIColor(hex: light)
        })
        #else
        return Color(hex: light)
        #endif
    }
}

#if canImport(UIKit)
import UIKit

extension UIColor {
    convenience init(hex: UInt32) {
        self.init(red: CGFloat((hex >> 16) & 0xFF) / 255,
                  green: CGFloat((hex >> 8) & 0xFF) / 255,
                  blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}
#endif

extension Color {
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255,
                  green: Double((hex >> 8) & 0xFF) / 255,
                  blue: Double(hex & 0xFF) / 255)
    }
}

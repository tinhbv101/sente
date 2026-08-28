import SwiftUI

/// Design tokens from docs/07 §10. The board keeps its wood tone in dark mode --
/// players recognise a goban by its colour -- while the chrome around it inverts.
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

    /// Seal red: the one hot colour on the screen, used only for "last move" and
    /// "your turn". No button borrows it (docs/07 §10).
    public static let seal = Color(red: 0.722, green: 0.271, blue: 0.227)            // #B8453A
    public static let indigo = Color(red: 0.153, green: 0.290, blue: 0.451)          // #274A73
    public static let indigoSoft = Color(red: 0.906, green: 0.929, blue: 0.961)
    public static let paper = Color(red: 0.957, green: 0.945, blue: 0.918)           // #F4F1EA
    public static let ink = Color(red: 0.090, green: 0.082, blue: 0.059)
    public static let inkSecondary = Color(red: 0.482, green: 0.451, blue: 0.392)
    public static let inkTertiary = Color(red: 0.651, green: 0.620, blue: 0.553)
    public static let territoryBlack = Color.black.opacity(0.62)
    public static let territoryWhite = Color(red: 1, green: 0.988, blue: 0.957).opacity(0.86)
}

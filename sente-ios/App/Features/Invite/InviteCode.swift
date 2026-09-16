import Foundation

/// One place that knows what an invitation code looks like, whether typed, pasted
/// as a link, or read off a QR code.
enum InviteCode {
    /// No I, O, 0, 1: the code is read aloud and typed by hand (docs/06 §2.4).
    static let alphabet = Set("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")
    static let length = 8

    /// Accepts `https://<host>/j/CODE`, `sente://j/CODE`, or the bare code in any
    /// case with stray spaces or dashes. Returns the normalised code, or nil.
    static func parse(_ text: String) -> String? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if let url = URL(string: trimmed), url.scheme != nil {
            var parts = url.pathComponents.filter { $0 != "/" }
            if let host = url.host, host == "j" { parts.insert(host, at: 0) }
            guard parts.count >= 2, parts[parts.count - 2] == "j" else { return nil }
            return normalise(parts[parts.count - 1])
        }
        return normalise(trimmed)
    }

    /// Internal so the scanner's classifier reuses this exact rule instead of
    /// growing a second copy that can drift from it.
    static func normalise(_ raw: String) -> String? {
        let code = raw.uppercased().filter { $0 != " " && $0 != "-" }
        guard code.count == length, code.allSatisfy(alphabet.contains) else { return nil }
        return code
    }

    /// The link a QR code carries. A universal link, so the iOS Camera app opens
    /// Sente directly; the custom scheme is the fallback when the server has no
    /// public URL to offer.
    static func link(for code: String, shareUrl: String?) -> String {
        shareUrl ?? "sente://j/\(code)"
    }

    /// A friend code shared as a link. Deliberately a different path from an
    /// invitation: both are eight characters from the same alphabet, so only the
    /// prefix can say which kind of code this is.
    ///
    /// The host follows the server the account actually lives on -- a code minted
    /// against a test server must not point at production, where that account
    /// does not exist.
    static func friendLink(for code: String, server: URL? = nil) -> String {
        let host = server?.host() ?? "sente.devlord.net"
        return "https://\(host)/f/\(code)"
    }

    /// What a scanned QR code turned out to be. One scanner serves joining a game
    /// and adding a friend, so the payload has to say which it is -- and a bare
    /// code cannot, since both kinds are eight characters from the same alphabet.
    enum Scanned: Equatable {
        case invitation(String)
        case friend(String)
        case game(String)
        /// A readable Sente link whose code is malformed.
        case malformed
        /// Not a Sente code at all.
        case foreign
    }

    /// Classifies a scanned payload. Only the path segment distinguishes an
    /// invitation from a friend code, so a bare code is treated as an invitation:
    /// that is the older format, the one printed on screens already out there.
    static func classify(_ payload: String) -> Scanned {
        let trimmed = payload.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), url.scheme != nil else {
            return normalise(trimmed).map(Scanned.invitation) ?? .foreign
        }
        var parts = url.pathComponents.filter { $0 != "/" }
        if let host = url.host, ["j", "g", "f"].contains(host) { parts.insert(host, at: 0) }
        guard parts.count >= 2 else { return .foreign }
        let value = parts[parts.count - 1]
        switch parts[parts.count - 2] {
        case "j": return normalise(value).map(Scanned.invitation) ?? .malformed
        case "f": return normalise(value).map(Scanned.friend) ?? .malformed
        // A game id is a UUID, not a friend code: it must not go through the
        // eight-character normaliser, which would reject every one of them.
        case "g": return value.count == 36 ? .game(value) : .malformed
        default: return .foreign
        }
    }
}

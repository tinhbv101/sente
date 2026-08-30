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

    private static func normalise(_ raw: String) -> String? {
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
}

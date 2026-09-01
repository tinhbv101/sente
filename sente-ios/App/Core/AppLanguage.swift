import Foundation
import SenteNet
import SenteUI

/// The in-app language choice. `.system` — the default — follows the device,
/// so someone who never opens this setting sees exactly what iOS decides.
enum AppLanguage: String, Codable, CaseIterable, Identifiable {
    case system, vi, en
    var id: String { rawValue }

    var title: String {
        switch self {
        case .system: LS(localized: "Theo hệ thống")
        case .vi: "Tiếng Việt"   // language names stay in their own tongue
        case .en: "English"
        }
    }

    var locale: Locale? {
        switch self {
        case .system: nil
        case .vi: Locale(identifier: "vi")
        case .en: Locale(identifier: "en")
        }
    }
}

/// How the override reaches each kind of string (verified by runtime probes):
/// SwiftUI `Text` follows `\.locale` set at the root; `String(localized:)` needs
/// an explicit bundle, so those call sites go through `LS(localized:)`; the
/// packages resolve through their injected bundle providers.
enum LanguageManager {
    private(set) nonisolated(unsafe) static var current: AppLanguage = .system
    private nonisolated(unsafe) static var overrideBundle: Bundle?

    /// Where `String(localized:)` should look right now.
    static var lookupBundle: Bundle { overrideBundle ?? .main }

    /// The language the UI is actually showing.
    static var effectiveCode: String {
        current.locale?.language.languageCode?.identifier
            ?? Bundle.main.preferredLocalizations.first ?? "vi"
    }

    static var isVietnamese: Bool { effectiveCode == "vi" }

    /// Called once at launch with the saved choice, then on every change.
    static func apply(_ language: AppLanguage) {
        current = language
        if let code = language.locale?.language.languageCode?.identifier,
           let path = Bundle.main.path(forResource: code, ofType: "lproj"),
           let bundle = Bundle(path: path) {
            overrideBundle = bundle
            // Keeps the next launch — and system-rendered UI — consistent.
            UserDefaults.standard.set([code], forKey: "AppleLanguages")
        } else {
            overrideBundle = nil
            UserDefaults.standard.removeObject(forKey: "AppleLanguages")
        }
        SenteNetL10n.bundle = { lookupBundle }
        SenteUIL10n.bundle = { lookupBundle }
    }
}

/// Drop-in for `String(localized:)` that honours the in-app language.
func LS(localized key: String.LocalizationValue) -> String {
    String(localized: key, bundle: LanguageManager.lookupBundle)
}

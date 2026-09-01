import Foundation

/// Where this package's user-facing strings are resolved. The app points it at
/// the in-app language choice; standalone (tests), it stays on the main bundle.
public enum SenteNetL10n {
    nonisolated(unsafe) public static var bundle: @Sendable () -> Bundle = { .main }
}

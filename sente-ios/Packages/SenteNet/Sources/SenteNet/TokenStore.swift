import Foundation
import Security

/// Keychain-backed storage for the access token. `kSecAttrAccessibleAfterFirstUnlock`
/// so a background reconnect after a reboot can still read it; not synchronised to
/// iCloud, because a guest identity belongs to one device (docs/03 ADR-008).
public struct TokenStore: Sendable {
    public let service: String
    public let account: String

    public init(service: String = "app.sente.go", account: String = "access_token") {
        self.service = service
        self.account = account
    }

    private var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    public func load() -> String? {
        var query = query
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    /// Returns false when the Keychain refused the write. Callers must not ignore
    /// it: a token that was never saved means a fresh guest account on the next
    /// launch, and every game silently lost with the old one.
    @discardableResult
    public func save(_ token: String) -> Bool {
        let data = Data(token.utf8)
        var attributes = query
        attributes[kSecValueData as String] = data
        attributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        // Delete-then-add is simpler than update-or-add and the item is tiny.
        SecItemDelete(query as CFDictionary)
        return SecItemAdd(attributes as CFDictionary, nil) == errSecSuccess
    }

    public func clear() { SecItemDelete(query as CFDictionary) }
}

import AuthenticationServices
import Foundation
import SenteNet
import UIKit

enum AppleSignInError: LocalizedError {
    case noIdentityToken
    var errorDescription: String? { "Apple không trả về thông tin đăng nhập. Thử lại sau." }
}

extension AppSession {
    /// Links the Apple ID to this account. The guest's games, name and friend
    /// code all stay; only the way back in changes (docs/01 FR-A2).
    func signInWithApple(credential: ASAuthorizationAppleIDCredential, rawNonce: String) async throws {
        guard let data = credential.identityToken, let identityToken = String(data: data, encoding: .utf8) else {
            throw AppleSignInError.noIdentityToken
        }
        // Apple gives the name once, on the first sign-in, and never again.
        let name = credential.fullName.flatMap { AppleName.displayName($0) }
        let signUp = try await api.signInWithApple(identityToken: identityToken, nonce: rawNonce, fullName: name)
        adopt(signUp)
    }

    // MARK: - Push

    func registerForPushIfUseful() async {
        guard !pushAttempted, !games.isEmpty else { return }
        pushAttempted = true
        // Never re-prompt: once denied, Settings is the only way back.
        if await PushRegistrar.status() != .denied { await PushRegistrar.register() }
    }

    /// The system prompt, on the user's request from Settings.
    func enablePush() async -> Bool {
        pushAttempted = true
        return await PushRegistrar.register()
    }

    /// Called by the app delegate with the APNs token; sent to the server so it
    /// knows where this account lives. Re-sent on every launch: tokens rotate.
    func deviceTokenReceived(_ token: String) {
        deviceToken = token
        let version = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String
        Task { try? await api.registerDevice(token: token, environment: PushRegistrar.environment, appVersion: version) }
    }
}

/// Vietnamese names read family–middle–given; the system formatter puts the
/// given name first for Latin script. Other locales keep the system's order.
enum AppleName {
    static func displayName(_ components: PersonNameComponents, locale: Locale = .current) -> String? {
        let text: String
        if locale.language.languageCode?.identifier == "vi" {
            text = [components.familyName, components.middleName, components.givenName]
                .compactMap { $0?.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty }
                .joined(separator: " ")
        } else {
            text = PersonNameComponentsFormatter().string(from: components)
        }
        return text.isEmpty ? nil : text
    }
}

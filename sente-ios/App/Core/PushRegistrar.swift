import SwiftUI
import UserNotifications

/// UIKit delegate for the two things SwiftUI cannot do: receive the APNs device
/// token and hear about a tapped notification (docs/07 §9.1).
@MainActor
final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    static var onDeviceToken: ((String) -> Void)?
    static var onOpenGame: ((String) -> Void)?
    static var onOpenInvite: ((String) -> Void)?
    /// A friend request or acceptance; there is no game to open.
    static var onOpenFriends: (() -> Void)?
    /// A push arriving while the app is in the foreground.
    static var onForegroundPush: (() -> Void)?

    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        let hex = deviceToken.map { String(format: "%02x", $0) }.joined()
        Self.onDeviceToken?(hex)
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        // Common on the simulator without an Apple Silicon host; nothing to fix.
        #if DEBUG
        print("push registration failed: \(error.localizedDescription)")
        #endif
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        // A push for the game currently on screen would only be noise. A push
        // with no game at all -- an invitation, a friend request -- is never
        // that: comparing two nils used to suppress every one of them.
        let gameID = notification.request.content.userInfo["game_id"] as? String
        return await MainActor.run { () -> UNNotificationPresentationOptions in
            Self.onForegroundPush?()
            let isOnScreen = gameID != nil && PushRegistrar.visibleGameID == gameID
            return isOnScreen ? [] : [.banner, .sound, .badge]
        }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            didReceive response: UNNotificationResponse) async {
        // The payload itself is not Sendable, so only the strings cross over.
        let info = response.notification.request.content.userInfo
        let inviteCode = info["invite_code"] as? String
        let kind = info["kind"] as? String
        let gameID = info["game_id"] as? String
        await MainActor.run { Self.route(inviteCode: inviteCode, kind: kind, gameID: gameID) }
    }

    /// Where a tapped notification goes. Named so the tests exercise this exact
    /// branch rather than a copy of it that cannot regress with it.
    @MainActor
    static func route(inviteCode: String?, kind: String?, gameID: String?) {
        if let inviteCode {
            onOpenInvite?(inviteCode)
        } else if kind == "friend" {
            onOpenFriends?()
        } else if let gameID {
            onOpenGame?(gameID)
        }
    }

    @MainActor
    static func route(_ info: [AnyHashable: Any]) {
        route(inviteCode: info["invite_code"] as? String,
              kind: info["kind"] as? String,
              gameID: info["game_id"] as? String)
    }
}

/// Asks for permission and registers with APNs. Kept out of AppSession so the
/// session stays free of UIKit.
@MainActor
enum PushRegistrar {
    /// The game the user is looking at right now; set by GameView.
    static var visibleGameID: String?

    enum Status { case notAsked, granted, denied }

    static func status() async -> Status {
        switch await UNUserNotificationCenter.current().notificationSettings().authorizationStatus {
        case .notDetermined: return .notAsked
        case .denied: return .denied
        default: return .granted
        }
    }

    /// Prompts once; afterwards registration is silent and only refreshes the token.
    @discardableResult
    static func register() async -> Bool {
        let center = UNUserNotificationCenter.current()
        let granted: Bool
        do {
            granted = try await center.requestAuthorization(options: [.alert, .sound, .badge])
        } catch {
            return false
        }
        guard granted else { return false }
        UIApplication.shared.registerForRemoteNotifications()
        return true
    }

    /// Which APNs environment this build's token belongs to. Debug builds carry a
    /// development provisioning profile; TestFlight and the App Store use production.
    static var environment: String {
        #if DEBUG
        return "sandbox"
        #else
        return "production"
        #endif
    }
}

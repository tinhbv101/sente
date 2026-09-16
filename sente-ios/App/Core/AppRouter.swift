import SwiftUI
import GoKit
import SenteNet

/// Where the app is. One object owns the selected tab, every tab's navigation
/// stack and the single sheet slot, so a push, a deep link or a tapped
/// notification can all steer the app from outside the view that shows it.
///
/// Held by SenteApp rather than by the tab view: a language change rebuilds the
/// whole view tree by id, and a router below that would lose every stack.
@MainActor @Observable
final class AppRouter {
    enum Tab: Hashable { case play, friends, learn, me }

    /// What the one sheet slot is showing. One slot because SwiftUI presents a
    /// single sheet per level: two would silently drop one.
    enum Sheet: Identifiable, Equatable {
        case createInvite
        case join(code: String)
        case scan
        case addFriend(code: String)
        /// Counted, so a second file opened while one is up still presents.
        case sgf(GameRecord, serial: Int)

        var id: String {
            switch self {
            case .createInvite: "create"
            case .join(let code): "join:\(code)"
            case .scan: "scan"
            case .addFriend(let code): "friend:\(code)"
            case .sgf(_, let serial): "sgf:\(serial)"
            }
        }
    }

    var tab: Tab = .play
    var play = NavigationPath()
    var friends = NavigationPath()
    var learn = NavigationPath()
    var me = NavigationPath()
    var sheet: Sheet?

    /// Board screens hide the tab bar. The raised scan button is an overlay, and
    /// `.toolbar(.hidden, for: .tabBar)` does not touch an overlay -- so without
    /// this flag the button floats over the board.
    var barHidden = false

    private var sgfSerial = 0

    /// Jumps to a tab and clears whatever was pushed on it, so an arriving link
    /// lands on the screen itself rather than under three others.
    func show(_ tab: Tab) {
        self.tab = tab
        switch tab {
        case .play: play = NavigationPath()
        case .friends: friends = NavigationPath()
        case .learn: learn = NavigationPath()
        case .me: me = NavigationPath()
        }
    }

    func push<V: Hashable>(_ value: V, on tab: Tab) {
        self.tab = tab
        switch tab {
        case .play: play.append(value)
        case .friends: friends.append(value)
        case .learn: learn.append(value)
        case .me: me.append(value)
        }
    }

    func presentSGF(_ record: GameRecord) {
        sgfSerial += 1
        sheet = .sgf(record, serial: sgfSerial)
    }
}

/// Board screens call this: it hides the tab bar and the raised scan button
/// together, which two separate modifiers could not do.
struct HidesTabBar: ViewModifier {
    @Environment(AppRouter.self) private var router

    func body(content: Content) -> some View {
        content
            .toolbar(.hidden, for: .tabBar)
            .onAppear { router.barHidden = true }
            .onDisappear { router.barHidden = false }
    }
}

extension View {
    func hidesSenteTabBar() -> some View { modifier(HidesTabBar()) }
}

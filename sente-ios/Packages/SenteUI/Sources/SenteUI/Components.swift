import SwiftUI

/// A player's clock as text, counting down from a server-supplied deadline. The
/// device clock is never trusted directly: `offset` is the measured skew.
public struct ClockLabel: View {
    public var deadline: Date?
    public var frozenMs: Int
    public var periodsLeft: Int?
    public var active: Bool
    public var offset: TimeInterval

    public init(deadline: Date?, frozenMs: Int, periodsLeft: Int? = nil, active: Bool, offset: TimeInterval = 0) {
        self.deadline = deadline; self.frozenMs = frozenMs; self.periodsLeft = periodsLeft
        self.active = active; self.offset = offset
    }

    public var body: some View {
        TimelineView(.periodic(from: .now, by: active ? 0.25 : 60)) { context in
            VStack(alignment: .trailing, spacing: 4) {
                Text(display(at: context.date))
                    .font(.system(size: 20, weight: .medium, design: .monospaced))
                    .monospacedDigit()
                    .foregroundStyle(active ? Tokens.seal : Tokens.inkSecondary)
                if let periodsLeft, periodsLeft > 0 {
                    HStack(spacing: 3.5) {
                        ForEach(0..<periodsLeft, id: \.self) { _ in
                            Circle().fill(active ? Tokens.seal : Tokens.inkTertiary.opacity(0.5))
                                .frame(width: 5, height: 5)
                        }
                    }
                }
            }
        }
    }

    private func display(at now: Date) -> String {
        var remaining: TimeInterval
        if active, let deadline {
            remaining = deadline.timeIntervalSince(now.addingTimeInterval(offset))
        } else {
            remaining = TimeInterval(frozenMs) / 1000
        }
        remaining = max(0, remaining)
        if remaining >= 86_400 { return String(format: "%dn %dg", Int(remaining) / 86_400, (Int(remaining) % 86_400) / 3600) }
        if remaining >= 3600 { return String(format: "%d:%02d:%02d", Int(remaining) / 3600, (Int(remaining) % 3600) / 60, Int(remaining) % 60) }
        return String(format: "%02d:%02d", Int(remaining) / 60, Int(remaining) % 60)
    }
}

/// Connection state as the player sees it (docs/07 §8.2). Nothing is shown for a
/// healthy connection or a reconnect shorter than three seconds.
public struct ConnectionBanner: View {
    public enum Kind: Equatable { case reconnecting, offline, syncing }
    public var kind: Kind

    public init(kind: Kind) { self.kind = kind }

    public var body: some View {
        HStack(spacing: 8) {
            ProgressView().controlSize(.small).tint(.white)
            Text(text).font(.footnote.weight(.semibold))
        }
        .foregroundStyle(.white)
        .padding(.horizontal, 14).padding(.vertical, 8)
        .background(background, in: Capsule())
        .transition(.move(edge: .top).combined(with: .opacity))
    }

    private var text: String {
        switch kind {
        case .reconnecting: "Đang kết nối lại…"
        case .offline: "Mất kết nối. Nước đi sẽ được gửi khi có mạng."
        case .syncing: "Đang đồng bộ ván…"
        }
    }
    private var background: Color {
        switch kind {
        case .reconnecting: Color(red: 0.62, green: 0.46, blue: 0.10)
        case .offline: Color(red: 0.55, green: 0.20, blue: 0.16)
        case .syncing: Color(red: 0.153, green: 0.290, blue: 0.451)
        }
    }
}

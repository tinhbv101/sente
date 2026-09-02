import AVFoundation
import UIKit

/// Stone clicks and haptics, honouring the Settings toggles. Sounds use the
/// ambient category so the mute switch and any playing audio win.
@MainActor
enum Feedback {
    static var soundOn: Bool { UserDefaults.standard.object(forKey: "soundOn") as? Bool ?? true }
    static var hapticsOn: Bool { UserDefaults.standard.object(forKey: "hapticsOn") as? Bool ?? true }

    private static var players: [String: AVAudioPlayer] = [:]
    private static var configured = false

    static func stone() { impact(.light); play("stone") }
    static func capture() { impact(.medium); play("capture") }
    static func chat() { impact(.soft) }
    static func gameEnd() {
        guard hapticsOn else { return }
        UINotificationFeedbackGenerator().notificationOccurred(.success)
    }

    private static func impact(_ style: UIImpactFeedbackGenerator.FeedbackStyle) {
        guard hapticsOn else { return }
        UIImpactFeedbackGenerator(style: style).impactOccurred()
    }

    private static func play(_ name: String) {
        guard soundOn else { return }
        if !configured {
            try? AVAudioSession.sharedInstance().setCategory(.ambient, options: [.mixWithOthers])
            configured = true
        }
        if players[name] == nil,
           let url = Bundle.main.url(forResource: name, withExtension: "wav") {
            players[name] = try? AVAudioPlayer(contentsOf: url)
            players[name]?.prepareToPlay()
        }
        guard let player = players[name] else { return }
        player.currentTime = 0
        player.play()
    }
}

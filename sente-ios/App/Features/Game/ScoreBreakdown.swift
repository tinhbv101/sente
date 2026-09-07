import SwiftUI
import GoKit
import SenteUI

/// The full arithmetic of a score, so nobody has to wonder whether the dead
/// stones were counted: territory, in-game prisoners, marked dead stones and
/// komi per side under Japanese rules; area and adjustments under Chinese.
struct ScoreBreakdownView: View {
    let score: Score
    let blackName: String
    let whiteName: String

    var body: some View {
        Grid(alignment: .trailing, horizontalSpacing: 18, verticalSpacing: 4) {
            GridRow {
                Text("")
                Text(blackName).font(.caption.weight(.bold)).lineLimit(1)
                Text(whiteName).font(.caption.weight(.bold)).lineLimit(1)
            }
            if score.rules == .japanese {
                row(LS(localized: "Đất"), \.territory)
                row(LS(localized: "Tù binh trong ván"), \.captures)
                row(LS(localized: "Quân chết bị bắt"), \.deadStones)
            } else {
                row(LS(localized: "Vùng (quân + đất)"), \.area)
                if score.detail.black.handicapAdjustment != 0 {
                    row(LS(localized: "Chấp quân"), \.handicapAdjustment)
                }
            }
            GridRow {
                Text("Komi").gridColumnAlignment(.leading).foregroundStyle(Tokens.inkSecondary)
                Text(score.detail.black.komi.formatted()).monospacedDigit()
                Text(score.detail.white.komi.formatted()).monospacedDigit()
            }
            Divider().gridCellUnsizedAxes(.horizontal)
            GridRow {
                Text("Tổng").gridColumnAlignment(.leading).font(.caption.weight(.bold))
                Text(score.black.formatted()).monospacedDigit().font(.subheadline.weight(.semibold))
                Text(score.white.formatted()).monospacedDigit().font(.subheadline.weight(.semibold))
            }
        }
        .font(.caption)
        .foregroundStyle(Tokens.ink)
    }

    private func row(_ label: String, _ key: KeyPath<Score.Side, Int>) -> some View {
        GridRow {
            Text(label).gridColumnAlignment(.leading).foregroundStyle(Tokens.inkSecondary)
            Text("\(score.detail.black[keyPath: key])").monospacedDigit()
            Text("\(score.detail.white[keyPath: key])").monospacedDigit()
        }
    }
}

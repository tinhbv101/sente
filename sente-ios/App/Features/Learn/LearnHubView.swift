import SwiftUI
import SenteUI

/// The Learn tab: everything that teaches or plays without another person.
struct LearnHubView: View {
    var body: some View {
        List {
            Section {
                row(LearnRoute.list, icon: "graduationcap.fill", tint: Tokens.indigo,
                    title: LS(localized: "Học cờ vây"),
                    subtitle: LS(localized: "Từ luật cơ bản đến sống chết · \(LessonProgress().done.count)/\(LessonLibrary.shared.lessonCount) bài"))
                NavigationLink(value: PuzzleRoute.today) {
                    HStack(spacing: 12) {
                        Image(systemName: "flame.fill").foregroundStyle(Tokens.seal)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Tsumego hôm nay").font(.callout.weight(.semibold))
                            puzzleSubtitle
                        }
                    }
                }
                .foregroundStyle(Tokens.ink)
            }
            Section("Chơi với máy") {
                row(BotRoute.play, icon: "cpu", tint: Tokens.indigo,
                    title: LS(localized: "Đấu với máy"),
                    subtitle: LS(localized: "Bốn cấp độ, chạy trên máy, không cần mạng"))
                row(BotRoute.watch, icon: "eye", tint: Tokens.indigo,
                    title: LS(localized: "Máy đấu máy"),
                    subtitle: LS(localized: "Xem hai bot chơi, chỉnh cấp từng bên"))
            }
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .background(Tokens.paper.ignoresSafeArea())
        .foregroundStyle(Tokens.ink)
        .navigationTitle("Học")
    }

    private func row<Route: Hashable>(_ route: Route, icon: String, tint: Color,
                                      title: String, subtitle: String) -> some View {
        NavigationLink(value: route) {
            HStack(spacing: 12) {
                Image(systemName: icon).foregroundStyle(tint)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.callout.weight(.semibold))
                    Text(subtitle).font(.caption).foregroundStyle(Tokens.inkSecondary)
                }
            }
        }
        .foregroundStyle(Tokens.ink)
    }

    @ViewBuilder private var puzzleSubtitle: some View {
        let status = DailyPuzzles.status(day: DailyPuzzles.dayNumber())
        if status.doneToday {
            Text("Hôm nay đã giải ✓ · chuỗi \(status.streak) ngày").font(.caption).foregroundStyle(Tokens.inkSecondary)
        } else if status.streak > 0 {
            Text("Giữ chuỗi \(status.streak) ngày!").font(.caption).foregroundStyle(Tokens.inkSecondary)
        } else {
            Text("Một bài mỗi ngày, giải để tạo chuỗi").font(.caption).foregroundStyle(Tokens.inkSecondary)
        }
    }
}

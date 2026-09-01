import SwiftUI

/// Today's tsumego, played through the lesson player; finishing records the day.
struct DailyPuzzleView: View {
    @State private var store: LessonPlayerStore?

    var body: some View {
        Group {
            if let store {
                LessonPlayerView(store: store)
                    .onChange(of: store.completed) { _, done in
                        if done { DailyPuzzles.recordSolved(day: DailyPuzzles.dayNumber()) }
                    }
            } else {
                ContentUnavailableView("Không có bài hôm nay", systemImage: "flame")
            }
        }
        .onAppear {
            if store == nil, let puzzle = DailyPuzzles.puzzle(forDay: DailyPuzzles.dayNumber()) {
                store = LessonPlayerStore(lesson: puzzle)
            }
        }
    }
}

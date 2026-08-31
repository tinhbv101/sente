import SwiftUI
import GoKit
import SenteUI

/// The lesson catalogue: chapters from basics to advanced, ticks for what is done.
struct LearnView: View {
    private let library = LessonLibrary.shared
    @State private var progress = LessonProgress().done

    var body: some View {
        List {
            ForEach(library.chapters) { chapter in
                Section {
                    ForEach(chapter.lessons) { lesson in
                        NavigationLink(value: lesson) {
                            HStack(spacing: 12) {
                                Image(systemName: progress.contains(lesson.id) ? "checkmark.circle.fill" : "circle")
                                    .foregroundStyle(progress.contains(lesson.id) ? .green : Tokens.inkTertiary)
                                Text(lesson.title.text).font(.callout.weight(.medium))
                                Spacer()
                                Text("\(lesson.steps.count)").font(.caption).foregroundStyle(Tokens.inkTertiary)
                            }
                        }
                    }
                } header: {
                    Label(chapter.title.text, systemImage: chapter.icon)
                }
            }
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .background(Tokens.paper.ignoresSafeArea())
        .foregroundStyle(Tokens.ink)
        .navigationTitle("Học cờ vây")
        .onAppear { progress = LessonProgress().done }
    }
}

struct LessonPlayerView: View {
    @Environment(AppSession.self) private var session
    @Environment(\.dismiss) private var dismiss
    @State private var store: LessonPlayerStore

    init(lesson: Lesson) {
        _store = State(initialValue: LessonPlayerStore(lesson: lesson))
    }

    /// For tests and snapshots: drive the store from outside.
    init(store: LessonPlayerStore) {
        _store = State(initialValue: store)
    }

    var body: some View {
        VStack(spacing: 12) {
            progressDots
            BoardView(
                snapshot: store.snapshot,
                ghostPlayer: store.mySide == .black ? .black : .white,
                interactive: store.awaitingMove,
                showsCoordinates: session.settings.showCoordinates,
                colourBlindSymbols: session.settings.colourBlindSymbols,
                fingerOffset: 0,
                legality: { store.legality($0) },
                onPlace: { store.tap($0) })
            .accessibilityLabel("Bàn cờ")
            card
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .background(Tokens.paper.ignoresSafeArea())
        .navigationTitle(store.lesson.title.text)
        .navigationBarTitleDisplayMode(.inline)
    }

    private var progressDots: some View {
        HStack(spacing: 5) {
            ForEach(store.lesson.steps.indices, id: \.self) { index in
                Capsule()
                    .fill(index <= store.stepIndex ? Tokens.indigo : Tokens.inkTertiary.opacity(0.3))
                    .frame(height: 4)
            }
        }
        .padding(.top, 6)
        .accessibilityLabel(Text("Bước \(store.stepIndex + 1)/\(store.lesson.steps.count)"))
    }

    @ViewBuilder private var card: some View {
        VStack(alignment: .leading, spacing: 10) {
            if store.completed {
                Label("Hoàn thành!", systemImage: "checkmark.seal.fill")
                    .font(.headline).foregroundStyle(.green)
                Text("Bạn đã xong bài này. Quay lại danh sách để học bài tiếp theo.")
                    .font(.subheadline).foregroundStyle(Tokens.inkSecondary)
                Button("Xong") { dismiss() }.buttonStyle(PrimaryButton())
            } else {
                if store.awaitingMove {
                    Label("Đến lượt bạn — chạm vào bàn cờ", systemImage: "hand.tap")
                        .font(.caption.weight(.semibold)).foregroundStyle(Tokens.seal)
                }
                Text(store.step.text.text).font(.subheadline)
                if let feedback = store.feedback {
                    Text(feedback)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(store.feedbackIsPraise ? .green : Tokens.seal)
                }
                HStack(spacing: 9) {
                    if store.step.kind == .task && !store.solved {
                        Button("Làm lại") { store.retry() }.buttonStyle(SecondaryButton())
                    }
                    if store.step.kind == .info || store.solved {
                        Button("Tiếp tục") { store.advance() }.buttonStyle(PrimaryButton())
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(Tokens.sheet, in: RoundedRectangle(cornerRadius: 16))
        .foregroundStyle(Tokens.ink)
        .animation(.default, value: store.feedback)
    }
}

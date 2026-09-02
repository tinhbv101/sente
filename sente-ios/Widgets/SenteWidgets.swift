import SwiftUI
import WidgetKit

@main
struct SenteWidgetBundle: WidgetBundle {
    var body: some Widget { TurnWidget() }
}

struct TurnEntry: TimelineEntry {
    let date: Date
    let summary: WidgetSummary?
}

struct TurnProvider: TimelineProvider {
    func placeholder(in context: Context) -> TurnEntry {
        TurnEntry(date: .now, summary: WidgetSummary(
            myTurn: 2, waiting: 1, nextDeadline: .now.addingTimeInterval(3600), nextOpponent: nil))
    }

    func getSnapshot(in context: Context, completion: @escaping (TurnEntry) -> Void) {
        completion(TurnEntry(date: .now, summary: WidgetSummary.load()))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<TurnEntry>) -> Void) {
        // The app reloads this timeline after every refresh; between reloads a
        // half-hourly wake keeps the relative deadline text honest.
        completion(Timeline(entries: [TurnEntry(date: .now, summary: WidgetSummary.load())],
                            policy: .after(.now.addingTimeInterval(1800))))
    }
}

struct TurnWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: WidgetSummary.widgetKind, provider: TurnProvider()) { entry in
            TurnWidgetView(summary: entry.summary)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Đến lượt bạn")
        .description("Số ván đang chờ nước đi của bạn.")
        .supportedFamilies([.systemSmall])
    }
}

struct TurnWidgetView: View {
    let summary: WidgetSummary?

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 5) {
                Circle().fill(.black).frame(width: 10, height: 10)
                Circle().fill(.white).frame(width: 10, height: 10)
                    .overlay(Circle().stroke(.black.opacity(0.25)))
                Spacer()
            }
            Spacer()
            if let summary, summary.myTurn > 0 {
                Text("\(summary.myTurn)")
                    .font(.system(size: 36, weight: .bold, design: .rounded))
                Text("ván đến lượt bạn").font(.caption2)
                if let deadline = summary.nextDeadline, deadline > .now {
                    Text(deadline, style: .relative)
                        .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                }
            } else if let summary, summary.waiting > 0 {
                Text("\(summary.waiting)")
                    .font(.system(size: 36, weight: .bold, design: .rounded))
                Text("ván chờ đối thủ").font(.caption2)
            } else {
                Text("Sente").font(.headline)
                Text("Không có ván nào chờ bạn").font(.caption2).foregroundStyle(.secondary)
            }
        }
        .widgetURL(URL(string: "sente://home"))
    }
}

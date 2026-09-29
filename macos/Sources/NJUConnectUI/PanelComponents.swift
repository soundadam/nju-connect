import SwiftUI

/// A filled dot with a soft halo; `nil` draws a hollow ring for "no verdict".
struct StatusDot: View {
    let tint: Color?
    var size: CGFloat = 8

    var body: some View {
        Group {
            if let tint {
                Circle()
                    .fill(tint)
                    .background(Circle().fill(tint.opacity(0.22)).padding(-size * 0.375))
            } else {
                Circle()
                    .strokeBorder(.tertiary, lineWidth: 1.2)
            }
        }
        .frame(width: size, height: size)
        .animation(.easeOut(duration: 0.2), value: tint)
    }
}

/// The small title line that opens a section: a name on the left and a
/// quiet caption or control on the right.
struct PanelSectionTitle<Trailing: View>: View {
    let title: String
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(spacing: 6) {
            Text(title)
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(.primary)
            Spacer(minLength: 4)
            trailing
        }
    }
}

extension PanelSectionTitle where Trailing == AnyView {
    init(_ title: String, caption: String) {
        self.init(title: title) {
            AnyView(
                Text(caption)
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
            )
        }
    }
}

/// A sparkline sitting on a hairline baseline, or a placeholder when there is
/// nothing to plot yet.
struct PanelChart: View {
    let series: [MetricSparklineSeries]
    var height: CGFloat = 42
    var placeholder = "No samples yet"

    var body: some View {
        ZStack {
            if series.allSatisfy({ $0.samples.isEmpty }) {
                Text(placeholder)
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
            } else {
                MetricSparklineChart(
                    series: series,
                    minimum: 0,
                    maximum: max(series.compactMap { $0.samples.max() }.max() ?? 0, 1) * 1.1
                )
            }
        }
        .frame(maxWidth: .infinity)
        .frame(height: height)
        .background(alignment: .bottom) {
            Rectangle().fill(.quaternary).frame(height: 1)
        }
        .accessibilityHidden(true)
    }
}

/// One figure in a row of equal columns: a label, the value, and an optional
/// quiet detail line.
struct PanelStat: View {
    let label: String
    let value: String
    var tint: Color?
    var detail: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 4) {
                if let tint {
                    Circle().fill(tint).frame(width: 5, height: 5)
                }
                Text(label)
                    .foregroundStyle(.secondary)
            }
            .font(.system(size: 10))

            Text(value)
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(.primary)

            if let detail {
                Text(detail)
                    .font(.system(size: 10))
                    .foregroundStyle(.tertiary)
            }
        }
        .monospacedDigit()
        .lineLimit(1)
        .minimumScaleFactor(0.8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
    }
}

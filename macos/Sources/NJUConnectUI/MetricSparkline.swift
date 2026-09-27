import SwiftUI

struct MetricSparklineSeries: Identifiable {
    let id: String
    let samples: [Double]
    let color: Color
    var filled = false
}

struct MetricSparklineChart: View {
    let series: [MetricSparklineSeries]
    var minimum: Double?
    var maximum: Double?

    init(
        series: [MetricSparklineSeries],
        minimum: Double? = nil,
        maximum: Double? = nil
    ) {
        self.series = series
        self.minimum = minimum
        self.maximum = maximum
    }

    var body: some View {
        ZStack {
            ForEach(series) { item in
                MetricSparkline(
                    samples: item.samples,
                    color: item.color,
                    filled: item.filled,
                    minimum: minimum,
                    maximum: maximum
                )
            }
        }
    }
}

struct MetricSparkline: View {
    let samples: [Double]
    let color: Color
    var filled = false
    var minimum: Double?
    var maximum: Double?

    init(
        samples: [Double],
        color: Color,
        filled: Bool = false,
        minimum: Double? = nil,
        maximum: Double? = nil
    ) {
        self.samples = samples
        self.color = color
        self.filled = filled
        self.minimum = minimum
        self.maximum = maximum
    }

    var body: some View {
        GeometryReader { geometry in
            let points = plottedPoints(in: geometry.size)
            ZStack {
                if filled, let first = points.first, let last = points.last {
                    Path { path in
                        path.move(to: CGPoint(x: first.x, y: geometry.size.height))
                        points.forEach { path.addLine(to: $0) }
                        path.addLine(to: CGPoint(x: last.x, y: geometry.size.height))
                        path.closeSubpath()
                    }
                    .fill(color.opacity(0.16))
                }
                Path { path in
                    path.addLines(points)
                }
                .stroke(color, style: StrokeStyle(lineWidth: filled ? 1.6 : 1.5, lineCap: .round, lineJoin: .round))
            }
        }
    }

    private func plottedPoints(in size: CGSize) -> [CGPoint] {
        let lowerBound = minimum ?? samples.min() ?? 0
        let upperBound = maximum ?? samples.max() ?? (lowerBound + 1)
        let span = max(upperBound - lowerBound, 1)
        return samples.enumerated().map { index, sample in
            let x = samples.count <= 1
                ? size.width / 2
                : size.width * CGFloat(index) / CGFloat(samples.count - 1)
            let normalized = min(max((sample - lowerBound) / span, 0), 1)
            return CGPoint(x: x, y: size.height * (1 - CGFloat(normalized)))
        }
    }
}

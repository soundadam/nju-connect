import SwiftUI

struct MetricSparklineSeries: Identifiable {
    let id: String
    let samples: [Double]
    let color: Color
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
    var minimum: Double?
    var maximum: Double?

    init(
        samples: [Double],
        color: Color,
        minimum: Double? = nil,
        maximum: Double? = nil
    ) {
        self.samples = samples
        self.color = color
        self.minimum = minimum
        self.maximum = maximum
    }

    var body: some View {
        GeometryReader { geometry in
            let lowerBound = minimum ?? samples.min() ?? 0
            let upperBound = maximum ?? samples.max() ?? (lowerBound + 1)
            let span = max(upperBound - lowerBound, 1)

            Path { path in
                for (index, sample) in samples.enumerated() {
                    let x = samples.count <= 1
                        ? geometry.size.width / 2
                        : geometry.size.width * CGFloat(index) / CGFloat(samples.count - 1)
                    let normalized = min(max((sample - lowerBound) / span, 0), 1)
                    let y = geometry.size.height - (geometry.size.height * CGFloat(normalized))
                    if index == 0 {
                        path.move(to: CGPoint(x: x, y: y))
                    } else {
                        path.addLine(to: CGPoint(x: x, y: y))
                    }
                }
            }
            .stroke(color, style: StrokeStyle(lineWidth: 2, lineCap: .round, lineJoin: .round))
        }
    }
}

import SwiftUI

struct TrafficRow: View {
    @ObservedObject var model: DesignModel

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text("SOCKS5")
                    .fontWeight(.medium)
                Text(model.socksRouteSummary)
                    .fontDesign(.monospaced)
                Spacer(minLength: 0)
            }

            HStack {
                Text(uiText("Live traffic", "实时流量"))
                    .fontWeight(.medium)
                Spacer(minLength: 0)
                Text(uiText("Last 30 sec", "最近 30 秒"))
                    .font(.system(size: 9))
                    .foregroundStyle(.tertiary)
            }

            HStack(spacing: 6) {
                TrafficRateChart(
                    title: uiText("Download", "下载"),
                    systemImage: "arrow.down",
                    rate: model.rates.downloadBytesPerSecond,
                    samples: model.downloadRateSamples,
                    color: .blue
                )
                TrafficRateChart(
                    title: uiText("Upload", "上传"),
                    systemImage: "arrow.up",
                    rate: model.rates.uploadBytesPerSecond,
                    samples: model.uploadRateSamples,
                    color: .green
                )
            }

            HStack(spacing: 9) {
                Text(uiText("Session", "本次"))
                    .frame(width: 42, alignment: .leading)
                Label(formatBytes(model.downloadBytes), systemImage: "arrow.down")
                Label(formatBytes(model.uploadBytes), systemImage: "arrow.up")
                Spacer(minLength: 0)
            }

            HStack(spacing: 9) {
                Text(uiText("Conn.", "连接"))
                    .frame(width: 42, alignment: .leading)
                Text(uiText("\(model.activeConnections) active", "活跃 \(model.activeConnections)"))
                Spacer(minLength: 0)
            }
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
        .monospacedDigit()
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .help(
            uiText(
                "Downloaded \(formatBytes(model.downloadBytes)), uploaded \(formatBytes(model.uploadBytes)), \(model.activeConnections) active connections",
                "本次下载 \(formatBytes(model.downloadBytes))，上传 \(formatBytes(model.uploadBytes))，活跃连接 \(model.activeConnections)"
            )
        )
    }
}

private struct TrafficRateChart: View {
    let title: String
    let systemImage: String
    let rate: Double
    let samples: [Double]
    let color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 3) {
                Label(title, systemImage: systemImage)
                    .foregroundStyle(color)
                Spacer(minLength: 2)
                Text(formatRate(rate))
                    .fontWeight(.medium)
                    .foregroundStyle(.secondary)
            }
            MetricSparklineChart(
                series: [MetricSparklineSeries(id: title, samples: samples, color: color)],
                minimum: 0,
                maximum: max(samples.max() ?? 0, 1)
            )
            .frame(height: 40)
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 5)
        .background(.quaternary.opacity(0.28), in: RoundedRectangle(cornerRadius: 6))
        .accessibilityLabel("\(title), \(formatRate(rate))")
    }
}

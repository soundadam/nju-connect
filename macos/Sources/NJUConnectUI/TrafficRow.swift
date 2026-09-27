import SwiftUI

struct TrafficRow: View {
    @ObservedObject var model: DesignModel

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(uiText("Live traffic", "实时流量"))
                    .foregroundStyle(.primary)
                Spacer(minLength: 0)
                Text(uiText("Last 30 sec", "最近 30 秒"))
                    .foregroundStyle(.tertiary)
            }

            MetricSparklineChart(
                series: [
                    MetricSparklineSeries(id: "download", samples: model.downloadRateSamples, color: .brand, filled: true),
                    MetricSparklineSeries(id: "upload", samples: model.uploadRateSamples, color: .secondary),
                ],
                minimum: 0,
                maximum: max(model.downloadRateSamples.max() ?? 0, model.uploadRateSamples.max() ?? 0, 1)
            )
            .frame(height: 42)
            .background(alignment: .bottom) {
                Rectangle().fill(.quaternary).frame(height: 1)
            }
            .accessibilityHidden(true)

            Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 3) {
                GridRow {
                    stat(uiText("Download", "下载"), formatRate(model.rates.downloadBytesPerSecond), tint: .brand)
                    stat(uiText("Upload", "上传"), formatRate(model.rates.uploadBytesPerSecond), tint: .secondary)
                }
                GridRow {
                    stat(
                        uiText("Session", "本次"),
                        "↓ \(formatBytes(model.downloadBytes))  ↑ \(formatBytes(model.uploadBytes))"
                    )
                    stat(uiText("Conn.", "连接"), uiText("\(model.activeConnections) active", "\(model.activeConnections) 个"))
                }
            }
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
        .monospacedDigit()
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
    }

    private func stat(_ title: String, _ value: String, tint: Color? = nil) -> some View {
        HStack(spacing: 4) {
            if let tint {
                Circle().fill(tint).frame(width: 5, height: 5)
            }
            Text(title)
            Text(value)
                .fontWeight(.semibold)
                .foregroundStyle(.primary)
        }
        .lineLimit(1)
        .fixedSize()
        .accessibilityElement(children: .combine)
    }
}

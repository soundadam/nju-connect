import SwiftUI

struct TrafficRow: View {
    @ObservedObject var model: DesignModel

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            PanelSectionTitle("Live traffic", caption: "Last 30 s")

            PanelChart(series: [
                MetricSparklineSeries(id: "download", samples: model.downloadRateSamples, color: .brand, filled: true),
                MetricSparklineSeries(id: "upload", samples: model.uploadRateSamples, color: .secondary),
            ])

            HStack(alignment: .top, spacing: 8) {
                PanelStat(
                    label: "Download",
                    value: formatRate(model.rates.downloadBytesPerSecond),
                    tint: .brand,
                    detail: "\(formatBytes(model.downloadBytes)) total"
                )
                PanelStat(
                    label: "Upload",
                    value: formatRate(model.rates.uploadBytesPerSecond),
                    tint: .secondary,
                    detail: "\(formatBytes(model.uploadBytes)) total"
                )
                PanelStat(
                    label: "Connections",
                    value: "\(model.activeConnections)",
                    detail: "active"
                )
            }
        }
        .padding(.horizontal, 12)
        .padding(.top, 9)
        .padding(.bottom, 10)
    }
}

import Foundation
import SwiftUI

struct CampusSpeedInspector: View {
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Campus speed")
                        .font(.headline)
                    Text("speed.nju.edu.cn")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }

                Spacer()

                Text(speedTest.routeLabel)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            LatencySection(speedTest: speedTest)

            Divider()

            BandwidthSection(speedTest: speedTest)
        }
        .padding(14)
        .frame(width: 312)
    }
}

private struct LatencySection: View {
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 7) {
                Text("Ping")
                    .font(.caption.weight(.semibold))
                Text(speedTest.routeLabel)
                    .font(.caption2)
                    .foregroundStyle(
                        speedTest.reachabilityState == .failed
                            ? Color.secondary
                            : Color.green
                    )
                Spacer()
                Text(compactLatency)
                    .font(.caption.weight(.semibold))
                    .monospacedDigit()
                Button("Refresh") {
                    speedTest.beginLatencySampling(force: true)
                }
                .controlSize(.small)
                .disabled(speedTest.isLatencySampling || speedTest.isRunning)
            }

            ZStack {
                if speedTest.latencySamples.isEmpty {
                    Text("No successful samples yet.")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                } else {
                    MetricSparkline(samples: speedTest.latencySamples, color: .blue)
                        .padding(.horizontal, 5)
                }
            }
            .frame(height: 48)
            .background(.quaternary.opacity(0.35), in: RoundedRectangle(cornerRadius: 6))

            HStack {
                Text(latencyStatistics)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
                Spacer()
                Text("HTTP · last 10")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
        }
    }

    private var compactLatency: String {
        guard let latency = speedTest.latencyMs else { return "— ms" }
        return "\(String(format: "%.0f", latency)) ms"
    }

    private var latencyStatistics: String {
        guard let minimum = speedTest.latencySamples.min(),
              let maximum = speedTest.latencySamples.max(),
              let median = speedTest.latencyMs
        else {
            return "No samples"
        }
        return String(
            format: "min %.0f · median %.0f · max %.0f ms",
            minimum,
            median,
            maximum
        )
    }
}

private struct BandwidthSection: View {
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 7) {
                Text("Bandwidth")
                    .font(.caption.weight(.semibold))
                Text(bandwidthStatus)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
                Spacer()
                bandwidthAction
            }

            let maximum = max(
                max(
                    speedTest.downloadSamples.max() ?? 0,
                    speedTest.uploadSamples.max() ?? 0
                ),
                1
            )
            ZStack {
                if speedTest.downloadSamples.isEmpty && speedTest.uploadSamples.isEmpty {
                    Text("No bandwidth samples yet.")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                } else {
                    MetricSparklineChart(
                        series: [
                            MetricSparklineSeries(
                                id: "download",
                                samples: speedTest.downloadSamples,
                                color: .blue
                            ),
                            MetricSparklineSeries(
                                id: "upload",
                                samples: speedTest.uploadSamples,
                                color: .green
                            ),
                        ],
                        minimum: 0,
                        maximum: maximum
                    )
                    .padding(.horizontal, 5)
                }
            }
            .frame(height: 62)
            .background(.quaternary.opacity(0.35), in: RoundedRectangle(cornerRadius: 6))

            HStack(spacing: 12) {
                MetricValue(
                    label: "Download",
                    value: speedTest.downloadMbps ?? speedTest.lastResult?.downloadMbps,
                    color: .blue,
                    systemImage: "arrow.down"
                )
                MetricValue(
                    label: "Upload",
                    value: speedTest.uploadMbps ?? speedTest.lastResult?.uploadMbps,
                    color: .green,
                    systemImage: "arrow.up"
                )
                Spacer()
            }
        }
    }

    @ViewBuilder
    private var bandwidthAction: some View {
        if speedTest.isRunning {
            Button("Cancel", action: speedTest.cancel)
                .controlSize(.small)
        } else if speedTest.phase == .componentRequired {
            Button("Set Up", action: speedTest.confirmComponentDownload)
                .controlSize(.small)
                .disabled(speedTest.componentInstallSource.isEmpty)
                .help(componentDownloadDescription)
        } else if speedTest.phase == .connectionRequired, speedTest.canRetryAfterConnection {
            Button("Retry", action: speedTest.retryAfterConnection)
                .controlSize(.small)
        } else if speedTest.phase == .connectionRequired {
            Text("Waiting for VPN")
                .font(.caption2)
                .foregroundStyle(.secondary)
        } else {
            Button(
                campusSpeedResultText(for: speedTest) == nil
                    ? "Start"
                    : "Test Again",
                action: speedTest.start
            )
            .controlSize(.small)
        }
    }

    private var bandwidthStatus: String {
        if let live = campusLiveMeasurementText(for: speedTest) {
            return live
        }
        switch speedTest.phase {
        case .downloading:
            return "Installing helper"
        case .probing:
            return "Probing route"
        case .measuring:
            return "Measuring"
        case .componentRequired, .connectionRequired, .failed:
            return speedTest.message
        case .idle:
            return campusSpeedResultText(for: speedTest) ?? "Not tested"
        case .completed:
            return campusSpeedResultText(for: speedTest) ?? "Complete"
        }
    }

    private var componentDownloadDescription: String {
        guard !speedTest.componentInstallSource.isEmpty else {
            return "Install with: brew install soundadam/tap/librespeed-cli-nju-connect"
        }
        return "\(speedTest.componentVersion) · Homebrew"
    }
}

private struct MetricValue: View {
    let label: String
    let value: Double?
    let color: Color
    let systemImage: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Label(label, systemImage: systemImage)
                .foregroundStyle(color)
            Text(value.map { "\(String(format: "%.0f", $0)) Mbps" } ?? "—")
                .font(.caption.weight(.semibold))
                .monospacedDigit()
        }
        .font(.caption2)
    }
}

private extension SpeedTestController {
    var routeLabel: String {
        switch route {
        case "direct": return "→ Direct"
        case "nju-connect": return "→ Via VPN"
        default: return "Route pending"
        }
    }
}

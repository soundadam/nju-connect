import Foundation
import SwiftUI

/// The speed-test popover, built from the same rows as the panel: a status
/// header, then latency and bandwidth sections split by hairlines.
struct CampusSpeedInspector: View {
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        VStack(spacing: 0) {
            InspectorHeader(speedTest: speedTest)
            Divider()
            LatencySection(speedTest: speedTest)
            Divider()
            BandwidthSection(speedTest: speedTest)
        }
        .frame(width: 292)
        .tint(.brand)
    }
}

private struct InspectorHeader: View {
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        HStack(spacing: 8) {
            let summary = campusSpeedSummary(for: speedTest)
            Group {
                if summary.showsProgress {
                    ProgressView()
                        .controlSize(.mini)
                } else {
                    StatusDot(tint: summary.tint)
                }
            }
            .frame(width: 12)

            VStack(alignment: .leading, spacing: 1) {
                Text("Campus speed")
                    .font(.system(size: 13, weight: .semibold))
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 6)

            Button {
                speedTest.refreshReachability()
            } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 11, weight: .semibold))
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
            .disabled(speedTest.isLatencySampling || speedTest.isRunning)
            .help("Probe speed.nju.edu.cn again")
            .accessibilityLabel("Probe again")
        }
        .padding(.horizontal, 12)
        .padding(.top, 10)
        .padding(.bottom, 8)
    }

    private var subtitle: String {
        let route = speedTest.route.map(campusRouteTitle) ?? "Route pending"
        return "speed.nju.edu.cn · \(route)"
    }
}

private struct LatencySection: View {
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            PanelSectionTitle("Latency", caption: "HTTP · last 10")

            PanelChart(
                series: [
                    MetricSparklineSeries(id: "latency", samples: speedTest.latencySamples, color: .brand, filled: true),
                ],
                placeholder: "No successful probes yet"
            )

            HStack(alignment: .top, spacing: 8) {
                PanelStat(label: "Median", value: milliseconds(median(of: speedTest.latencySamples)))
                PanelStat(label: "Min", value: milliseconds(speedTest.latencySamples.min()))
                PanelStat(label: "Max", value: milliseconds(speedTest.latencySamples.max()))
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
    }

    private func milliseconds(_ value: Double?) -> String {
        value.map { String(format: "%.0f ms", $0) } ?? "—"
    }
}

private struct BandwidthSection: View {
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            PanelSectionTitle(title: "Bandwidth") {
                bandwidthAction
            }

            PanelChart(
                series: [
                    MetricSparklineSeries(id: "download", samples: speedTest.downloadSamples, color: .brand, filled: true),
                    MetricSparklineSeries(id: "upload", samples: speedTest.uploadSamples, color: .secondary),
                ],
                placeholder: "No bandwidth run yet"
            )

            HStack(alignment: .top, spacing: 8) {
                PanelStat(
                    label: "Download",
                    value: mbps(speedTest.downloadMbps ?? speedTest.lastResult?.downloadMbps),
                    tint: .brand
                )
                PanelStat(
                    label: "Upload",
                    value: mbps(speedTest.uploadMbps ?? speedTest.lastResult?.uploadMbps),
                    tint: .secondary
                )
                PanelStat(
                    label: "Ping",
                    value: speedTest.lastResult?.pingMs.map { String(format: "%.0f ms", $0) } ?? "—"
                )
            }

            if let status {
                Text(status)
                    .font(.system(size: 10))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 12)
        .padding(.top, 9)
        .padding(.bottom, 10)
    }

    private func mbps(_ value: Double?) -> String {
        value.map { String(format: "%.0f Mbps", $0) } ?? "—"
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
                .font(.system(size: 11))
                .foregroundStyle(.tertiary)
        } else {
            Button(
                campusSpeedResultText(for: speedTest) == nil ? "Start" : "Test Again",
                action: speedTest.start
            )
            .controlSize(.small)
        }
    }

    /// What the run is doing, or why it cannot run; nothing once it has a
    /// result, since the figures above already say it.
    private var status: String? {
        if let live = campusLiveMeasurementText(for: speedTest) {
            return "Measuring \(live)"
        }
        switch speedTest.phase {
        case .downloading:
            return "Installing helper…"
        case .probing:
            return "Probing route…"
        case .measuring:
            return "Measuring…"
        case .componentRequired, .connectionRequired, .failed:
            return speedTest.message
        case .idle, .completed:
            return nil
        }
    }

    private var componentDownloadDescription: String {
        guard !speedTest.componentInstallSource.isEmpty else {
            return "Install with: brew install soundadam/tap/librespeed-cli-nju-connect"
        }
        return "\(speedTest.componentVersion) · Homebrew"
    }
}

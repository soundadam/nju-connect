import Foundation
import SwiftUI

struct CampusSpeedSummary {
    let label: String
    let symbol: String
    let tint: Color
    let showsProgress: Bool
}

struct CampusSpeedSummaryRow: View {
    @ObservedObject var speedTest: SpeedTestController
    @State private var showsInspector = false

    var body: some View {
        Button {
            showsInspector.toggle()
        } label: {
            HStack(spacing: 7) {
                summaryIcon

                Text("speed.nju.edu.cn")
                    .font(.caption)
                    .fontWeight(.medium)
                    .lineLimit(1)

                Spacer(minLength: 4)

                Text(summary.label)
                    .font(.system(size: 10))
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
                    .lineLimit(1)

                Image(systemName: "chart.line.uptrend.xyaxis")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(.tertiary)
                    .frame(width: 14)
                    .accessibilityLabel("Open speed inspector")
            }
            .padding(.horizontal, 12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(height: 32)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .popover(
            isPresented: $showsInspector,
            attachmentAnchor: .rect(.bounds),
            arrowEdge: .leading
        ) {
            CampusSpeedInspector(speedTest: speedTest)
        }
        .help(campusSpeedHelp(for: speedTest))
    }

    private var summary: CampusSpeedSummary {
        campusSpeedSummary(for: speedTest)
    }

    @ViewBuilder
    private var summaryIcon: some View {
        if summary.showsProgress {
            ProgressView()
                .controlSize(.mini)
                .frame(width: 13)
        } else {
            Image(systemName: summary.symbol)
                .foregroundStyle(summary.tint)
                .frame(width: 13)
        }
    }
}

@MainActor
func campusSpeedSummary(for speedTest: SpeedTestController) -> CampusSpeedSummary {
    if speedTest.isRunning {
        let label: String
        switch speedTest.phase {
        case .downloading:
            label = "Installing"
        case .probing:
            label = "Probing"
        case .measuring:
            switch speedTest.activeMeasurementPhase {
            case "download":
                label = "Downloading"
            case "upload":
                label = "Uploading"
            default:
                label = "Measuring"
            }
        default:
            label = "Working"
        }
        return CampusSpeedSummary(
            label: label,
            symbol: "arrow.triangle.2.circlepath",
            tint: .secondary,
            showsProgress: true
        )
    }

    let label: String
    switch speedTest.phase {
    case .componentRequired:
        label = "Setup needed"
    case .connectionRequired:
        label = "VPN needed"
    case .failed:
        label = "Unavailable"
    default:
        label = campusSpeedResultText(for: speedTest)
            ?? (speedTest.latencyMs.map {
                String(format: "Ping %.0f ms", $0)
            } ?? "Not tested")
    }

    switch speedTest.reachabilityState {
    case .reachable:
        return CampusSpeedSummary(label: label, symbol: "checkmark.circle.fill", tint: .green, showsProgress: false)
    case .slow:
        return CampusSpeedSummary(label: label, symbol: "exclamationmark.circle.fill", tint: .orange, showsProgress: false)
    case .failed:
        return CampusSpeedSummary(label: label, symbol: "circle.fill", tint: .red, showsProgress: false)
    case .probing:
        return CampusSpeedSummary(label: label, symbol: "network", tint: .secondary, showsProgress: true)
    case .unknown:
        return CampusSpeedSummary(label: label, symbol: "questionmark.circle", tint: .secondary, showsProgress: false)
    }
}

@MainActor
func campusSpeedResultText(for speedTest: SpeedTestController) -> String? {
    let download = speedTest.downloadMbps ?? speedTest.lastResult?.downloadMbps
    let upload = speedTest.uploadMbps ?? speedTest.lastResult?.uploadMbps
    let values = [
        download.map { "↓\(String(format: "%.0f", $0))" },
        upload.map { "↑\(String(format: "%.0f", $0))" },
    ].compactMap { $0 }
    guard !values.isEmpty else { return nil }
    return values.joined(separator: " ") + " Mbps"
}

@MainActor
func campusLiveMeasurementText(for speedTest: SpeedTestController) -> String? {
    switch speedTest.activeMeasurementPhase {
    case "download":
        guard let download = speedTest.downloadMbps else { return nil }
        return "↓ \(String(format: "%.0f", download)) Mbps"
    case "upload":
        guard let upload = speedTest.uploadMbps else { return nil }
        return "↑ \(String(format: "%.0f", upload)) Mbps"
    default:
        return nil
    }
}

@MainActor
func campusSpeedHelp(for speedTest: SpeedTestController) -> String {
    var details = [speedTest.message]
    if let route = speedTest.route {
        details.append(
            route == "direct"
                ? "Direct campus route"
                : "Via nju-connect"
        )
    }
    if let latencyMs = speedTest.latencyMs {
        details.append(String(format: "Latency %.0f ms", latencyMs))
    }
    return details.joined(separator: " · ")
}

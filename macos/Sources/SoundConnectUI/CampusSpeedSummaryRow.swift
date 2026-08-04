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
                    .accessibilityLabel(uiText("Open speed inspector", "打开测速详情"))
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
            label = uiText("Installing", "安装中")
        case .probing:
            label = uiText("Probing", "探测中")
        case .measuring:
            switch speedTest.activeMeasurementPhase {
            case "download":
                label = uiText("Downloading", "下载中")
            case "upload":
                label = uiText("Uploading", "上传中")
            default:
                label = uiText("Measuring", "测量中")
            }
        default:
            label = uiText("Working", "处理中")
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
        label = uiText("Setup needed", "需要设置")
    case .connectionRequired:
        label = uiText("VPN needed", "需要 VPN")
    case .failed:
        label = uiText("Unavailable", "不可用")
    default:
        label = campusSpeedResultText(for: speedTest)
            ?? (speedTest.latencyMs.map {
                String(format: uiText("Ping %.0f ms", "延迟 %.0f ms"), $0)
            } ?? uiText("Not tested", "尚未测速"))
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
                ? uiText("Direct campus route", "校园网直连")
                : uiText("Via soundconnect", "经 soundconnect")
        )
    }
    if let latencyMs = speedTest.latencyMs {
        details.append(String(format: uiText("Latency %.0f ms", "延迟 %.0f ms"), latencyMs))
    }
    return details.joined(separator: " · ")
}

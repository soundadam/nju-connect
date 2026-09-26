import Foundation
import SwiftUI

struct CampusSpeedInspector: View {
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(uiText("Campus speed", "校园测速"))
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
                Text(uiText("Ping", "延迟"))
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
                Button(uiText("Refresh", "刷新")) {
                    speedTest.beginLatencySampling(force: true)
                }
                .controlSize(.small)
                .disabled(speedTest.isLatencySampling || speedTest.isRunning)
            }

            ZStack {
                if speedTest.latencySamples.isEmpty {
                    Text(uiText("No successful samples yet.", "尚无成功样本"))
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
                Text(uiText("HTTP · last 10", "HTTP · 最近 10 次"))
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
            return uiText("No samples", "暂无样本")
        }
        return String(
            format: uiText(
                "min %.0f · median %.0f · max %.0f ms",
                "最低 %.0f · 中位 %.0f · 最高 %.0f ms"
            ),
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
                Text(uiText("Bandwidth", "带宽"))
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
                    Text(uiText("No bandwidth samples yet.", "尚无带宽样本"))
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
                    label: uiText("Download", "下载"),
                    value: speedTest.downloadMbps ?? speedTest.lastResult?.downloadMbps,
                    color: .blue,
                    systemImage: "arrow.down"
                )
                MetricValue(
                    label: uiText("Upload", "上传"),
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
            Button(uiText("Cancel", "取消"), action: speedTest.cancel)
                .controlSize(.small)
        } else if speedTest.phase == .componentRequired {
            Button(uiText("Set Up", "设置"), action: speedTest.confirmComponentDownload)
                .controlSize(.small)
                .disabled(speedTest.componentInstallSource.isEmpty)
                .help(componentDownloadDescription)
        } else if speedTest.phase == .connectionRequired, speedTest.canRetryAfterConnection {
            Button(uiText("Retry", "重试"), action: speedTest.retryAfterConnection)
                .controlSize(.small)
        } else if speedTest.phase == .connectionRequired {
            Text(uiText("Waiting for VPN", "等待 VPN"))
                .font(.caption2)
                .foregroundStyle(.secondary)
        } else {
            Button(
                campusSpeedResultText(for: speedTest) == nil
                    ? uiText("Start", "开始")
                    : uiText("Test Again", "再测一次"),
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
            return uiText("Installing helper", "安装测速依赖")
        case .probing:
            return uiText("Probing route", "探测线路")
        case .measuring:
            return uiText("Measuring", "测量中")
        case .componentRequired, .connectionRequired, .failed:
            return speedTest.message
        case .idle:
            return campusSpeedResultText(for: speedTest) ?? uiText("Not tested", "尚未测速")
        case .completed:
            return campusSpeedResultText(for: speedTest) ?? uiText("Complete", "已完成")
        }
    }

    private var componentDownloadDescription: String {
        guard !speedTest.componentInstallSource.isEmpty else {
            return uiText(
                "Install with: brew install soundadam/local/librespeed-cli-soundconnect",
                "请运行：brew install soundadam/local/librespeed-cli-soundconnect"
            )
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
        case "direct": return uiText("→ Direct", "→ 直连")
        case "nju-connect": return uiText("→ Via VPN", "→ 经 VPN")
        default: return uiText("Route pending", "线路待定")
        }
    }
}

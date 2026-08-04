import SwiftUI

struct DashboardHeader: View {
    @ObservedObject var model: DesignModel

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(statusColor)
                .frame(width: 6, height: 6)

            VStack(alignment: .leading, spacing: 1) {
                Text("soundconnect")
                    .font(.system(size: 13, weight: .semibold))
                Text(model.statusTitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 6)

            if model.isPerformingAction {
                ProgressView()
                    .controlSize(.mini)
            }

            Toggle(
                uiText("VPN service", "VPN 服务"),
                isOn: Binding(
                    get: { model.isServiceEnabled },
                    set: { model.setServiceEnabled($0) }
                )
            )
            .labelsHidden()
            .toggleStyle(.switch)
            .controlSize(.small)
            .frame(minWidth: 44, minHeight: 32)
            .contentShape(Rectangle())
            .disabled(!model.canControlService || model.isPerformingAction)
            .help(
                model.isServiceEnabled
                    ? uiText("Stop soundconnect", "停止 soundconnect 服务")
                    : uiText("Start soundconnect", "启动 soundconnect 服务")
            )
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var statusColor: Color {
        if model.showsCredentialSetup {
            return .orange
        }
        switch model.phase {
        case .connected: return .green
        case .waitingMFA, .authenticating, .connecting, .reconnecting, .starting: return .orange
        case .degraded: return .red
        case .stopped: return .secondary
        }
    }
}

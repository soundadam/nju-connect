import SwiftUI

struct DashboardHeader: View {
    @ObservedObject var model: DesignModel

    var body: some View {
        HStack(spacing: 8) {
            StatusDot(tint: model.statusTint)
                .padding(.leading, 2)

            VStack(alignment: .leading, spacing: 1) {
                Text("nju-connect")
                    .font(.system(size: 13, weight: .semibold))
                Text(model.statusTitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 6)

            if model.isPerformingAction {
                ProgressView()
                    .controlSize(.small)
            } else if model.phase == .connected || model.phase == .reconnecting {
                Text("SOCKS5 :\(model.socksPort)")
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .help("Local SOCKS5 proxy \(model.socksEndpoint) → \(model.gatewayServer)")
            }
        }
        .padding(.horizontal, 12)
        .padding(.top, 10)
        .padding(.bottom, 8)
    }
}

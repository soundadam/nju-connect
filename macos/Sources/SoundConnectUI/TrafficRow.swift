import SwiftUI

struct TrafficRow: View {
    @ObservedObject var model: DesignModel

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text("SOCKS5")
                    .fontWeight(.medium)
                Text(model.socksEndpoint)
                    .fontDesign(.monospaced)
                Spacer(minLength: 0)
            }

            HStack(spacing: 9) {
                Text(uiText("Live", "实时"))
                    .frame(width: 42, alignment: .leading)
                Label(formatRate(model.rates.downloadBytesPerSecond), systemImage: "arrow.down")
                Label(formatRate(model.rates.uploadBytesPerSecond), systemImage: "arrow.up")
                Spacer(minLength: 0)
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

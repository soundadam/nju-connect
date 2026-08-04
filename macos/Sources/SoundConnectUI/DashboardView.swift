import SwiftUI

struct DashboardView: View {
    @ObservedObject var model: DesignModel
    @ObservedObject var speedTest: SpeedTestController
    @State private var oneTimeCode = ""
    @State private var schoolAccount = ""
    @State private var vpnPassword = ""
    @State private var setupValidationMessage: String?
    @State private var showsLatencyDetails = false
    @State private var isHoveringLatencyControl = false
    @State private var isHoveringLatencyDetails = false
    @FocusState private var codeFieldFocused: Bool
    @FocusState private var setupFieldFocused: SetupField?

    private enum SetupField: Hashable {
        case schoolAccount
        case vpnPassword
    }

    var body: some View {
        VStack(spacing: 0) {
            header

            Divider()
            campusSpeedTestRow

            if !model.statusDetail.isEmpty {
                Divider()
                statusDetailRow
            }

            if let message = model.actionMessage {
                Divider()
                actionMessageRow(message)
            }

            if let notice = model.serviceControlNotice {
                Divider()
                serviceControlNoticeRow(notice)
            }

            if model.showsCredentialSetup {
                Divider()
                setupRow
            }

            if !model.showsCredentialSetup, model.canSubmitAuthenticationCode {
                authenticationRow
            }

            if !model.showsCredentialSetup,
               model.phase == .connected || model.phase == .reconnecting
            {
                Divider()
                trafficRow
            }

            if !model.showsCredentialSetup, model.phase == .degraded {
                Divider()
                retryRow
            }
        }
        .frame(width: 292)
        .onAppear {
            speedTest.beginLatencySamplingIfNeeded()
        }
    }

    private var header: some View {
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
                    set: { enabled in model.setServiceEnabled(enabled) }
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

    private var statusDetailRow: some View {
        HStack(alignment: .top, spacing: 7) {
            Image(systemName: statusDetailSymbol)
                .foregroundStyle(statusColor)
                .frame(width: 13)

            Text(model.statusDetail)
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
    }

    private func actionMessageRow(_ message: String) -> some View {
        HStack(spacing: 7) {
            Image(systemName: "info.circle")
                .frame(width: 13)
            Text(message)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(2)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
    }

    private func serviceControlNoticeRow(_ notice: String) -> some View {
        HStack(alignment: .top, spacing: 7) {
            Image(systemName: "gearshape.2")
                .foregroundStyle(.orange)
                .frame(width: 13)
            Text(notice)
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
    }

    private var setupRow: some View {
        VStack(alignment: .leading, spacing: 7) {
            TextField(uiText("School account", "学校账号"), text: $schoolAccount)
                .textFieldStyle(.roundedBorder)
                .controlSize(.small)
                .focused($setupFieldFocused, equals: .schoolAccount)
                .onSubmit { setupFieldFocused = .vpnPassword }

            SecureField(uiText("VPN password", "VPN 长期密码"), text: $vpnPassword)
                .textFieldStyle(.roundedBorder)
                .controlSize(.small)
                .focused($setupFieldFocused, equals: .vpnPassword)
                .onSubmit(submitSetup)

            if let setupValidationMessage {
                Text(setupValidationMessage)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack(spacing: 8) {
                Spacer(minLength: 4)

                if model.isReconfiguringCredentials {
                    Button(uiText("Cancel", "取消")) {
                        model.cancelCredentialRecovery()
                    }
                    .controlSize(.small)
                    .disabled(model.isPerformingAction)
                }

                Button(uiText("Save & Connect", "保存并连接"), action: submitSetup)
                    .controlSize(.small)
                    .disabled(
                        schoolAccount.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                            || vpnPassword.isEmpty
                            || model.isPerformingAction
                    )
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .onAppear {
            setupFieldFocused = .schoolAccount
        }
        .help(
            uiText(
                "The account and VPN password are stored in Keychain. Verification codes are never saved.",
                "学校账号与 VPN 长期密码存储在钥匙串中；短信或动态口令不会保存"
            )
        )
    }

    private var authenticationRow: some View {
        HStack(spacing: 7) {
            SecureField(model.authenticationPlaceholder, text: $oneTimeCode)
                .textFieldStyle(.roundedBorder)
                .controlSize(.small)
                .focused($codeFieldFocused)
                .onSubmit(submitCode)

            Button(uiText("Submit", "提交"), action: submitCode)
                .controlSize(.small)
                .disabled(oneTimeCode.isEmpty || model.isPerformingAction)
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 9)
        .onAppear {
            codeFieldFocused = true
        }
        .help(uiText("The code is sent once through a private local socket and is never saved.", "验证码仅经本机私有 socket 发送一次，不会保存"))
    }

    private var trafficRow: some View {
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

    private var retryRow: some View {
        HStack {
            Text(model.retryDetail)
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Spacer()
            Button(model.retryTitle) {
                if model.retriesByReconfiguringCredentials {
                    model.beginCredentialRecovery()
                } else {
                    model.retry()
                }
            }
            .controlSize(.small)
            .frame(minWidth: 56, minHeight: 32)
            .contentShape(Rectangle())
            .disabled(model.isPerformingAction)
        }
        .padding(.horizontal, 12)
        .frame(minHeight: 36)
    }

    private var campusSpeedTestRow: some View {
        HStack(spacing: 7) {
            if speedTest.reachabilityState == .failed {
                Circle()
                    .fill(.red)
                    .frame(width: 6, height: 6)
                    .frame(width: 13)
            } else if speedTest.reachabilityState == .probing || speedTest.phase == .downloading,
               speedTest.activeMeasurementPhase == nil
            {
                ProgressView()
                    .controlSize(.mini)
                    .frame(width: 13)
            } else {
                Image(systemName: speedTestStatusSymbol)
                    .foregroundStyle(speedTestStatusColor)
                    .frame(width: 13)
            }

            Text("speed.nju.edu.cn")
                .font(.caption)
                .fontWeight(.medium)
                .lineLimit(1)

            Spacer(minLength: 4)

            HStack(spacing: 4) {
                if speedTest.isRunning {
                    if let liveSpeedResult {
                        Text(liveSpeedResult)
                            .font(.system(size: 10))
                            .foregroundStyle(.secondary)
                            .monospacedDigit()
                            .lineLimit(1)
                    } else {
                        Text(
                            speedTest.phase == .downloading
                                ? uiText("Installing", "安装组件")
                                : uiText("Probing", "探测中")
                        )
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }

                    Button(uiText("Cancel", "取消"), action: speedTest.cancel)
                        .controlSize(.small)
                } else if speedTest.phase == .componentRequired {
                    Button(uiText("Dependency Missing", "缺少依赖"), action: speedTest.confirmComponentDownload)
                        .controlSize(.small)
                        .disabled(speedTest.componentInstallSource.isEmpty)
                        .help(componentDownloadDescription)
                } else if speedTest.phase == .connectionRequired,
                          speedTest.canRetryAfterConnection
                {
                    Button(uiText("Retry", "重新测速"), action: speedTest.retryAfterConnection)
                        .controlSize(.small)
                } else if speedTest.phase == .connectionRequired {
                    Text(uiText("Waiting", "等待连接"))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                } else {
                    Button {
                        showsLatencyDetails = true
                    } label: {
                        HStack(spacing: 3) {
                            if speedTest.isLatencySampling, !speedTest.latencySamples.isEmpty {
                                ProgressView()
                                    .controlSize(.mini)
                            }
                            Text(uiText("Ping \(compactLatency)", "延迟 \(compactLatency)"))
                                .monospacedDigit()
                        }
                        .frame(minWidth: 58, alignment: .trailing)
                    }
                    .buttonStyle(.plain)
                    .onHover { hovering in
                        isHoveringLatencyControl = hovering
                        if hovering {
                            showsLatencyDetails = true
                        } else {
                            scheduleLatencyDetailDismissal()
                        }
                    }
                    .popover(isPresented: $showsLatencyDetails, arrowEdge: .bottom) {
                        latencyDetails
                    }
                    .help(uiText("Show recent latency", "查看近期延迟"))

                    Button(action: speedTest.start) {
                        Text(uiText("Speed", "测速"))
                    }
                    .controlSize(.small)
                    .help(compactSpeedResult == nil ? uiText("Run bandwidth test", "进行带宽测速") : uiText("Run bandwidth test again", "再次进行带宽测速"))
                }
            }
            .frame(width: 116, alignment: .trailing)
        }
        .padding(.horizontal, 12)
        .frame(height: 32)
        .help(speedTestHelp)
    }

    private var compactLatency: String {
        guard let latency = speedTest.latencyMs else { return "— ms" }
        return "\(String(format: "%.0f", latency)) ms"
    }

    private var latencyDetails: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Text(latencyRouteTitle)
                    .font(.headline)
                    .foregroundStyle(speedTest.reachabilityState == .failed ? Color.secondary : Color.green)
                Spacer(minLength: 12)
                Text(compactLatency)
                    .font(.headline)
                    .monospacedDigit()
            }

            LatencySparkline(samples: speedTest.latencySamples)
                .frame(height: 48)

            if let minimum = speedTest.latencySamples.min(),
               let maximum = speedTest.latencySamples.max(),
               let median = speedTest.latencyMs
            {
                Text(String(format: uiText("min %.0f · median %.0f · max %.0f ms", "最低 %.0f · 中位 %.0f · 最高 %.0f ms"), minimum, median, maximum))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            } else {
                Text(uiText("No successful samples yet.", "尚无成功样本"))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            if let compactSpeedResult {
                Text(uiText("Last speed test: \(compactSpeedResult)", "最近测速：\(compactSpeedResult)"))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            HStack {
                Text(uiText("Last 10 HTTP probes", "最近 10 次 HTTP 探测"))
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                Spacer()
                Button(uiText("Refresh", "刷新")) {
                    speedTest.beginLatencySampling(force: true)
                }
                .controlSize(.small)
                .disabled(speedTest.isLatencySampling || speedTest.isRunning)
            }
        }
        .padding(12)
        .frame(width: 244)
        .onHover { hovering in
            isHoveringLatencyDetails = hovering
            if !hovering {
                scheduleLatencyDetailDismissal()
            }
        }
    }

    private func scheduleLatencyDetailDismissal() {
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(600))
            guard !isHoveringLatencyControl, !isHoveringLatencyDetails else { return }
            showsLatencyDetails = false
        }
    }

    private var latencyRouteTitle: String {
        switch speedTest.route {
        case "direct": return uiText("→ Direct", "→ 直连")
        case "soundconnect": return uiText("→ Via VPN", "→ 经 VPN")
        default: return uiText("Latency", "延迟")
        }
    }

    private var liveSpeedResult: String? {
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

    private var compactSpeedResult: String? {
        let download = speedTest.downloadMbps ?? speedTest.lastResult?.downloadMbps
        let upload = speedTest.uploadMbps ?? speedTest.lastResult?.uploadMbps
        guard let download else { return nil }
        return "↓\(String(format: "%.0f", download)) ↑\(String(format: "%.0f", upload ?? 0)) Mbps"
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

    private var speedTestStatusColor: Color {
        switch speedTest.reachabilityState {
        case .reachable: return .green
        case .slow: return .orange
        case .failed: return .red
        case .probing, .unknown: return .secondary
        }
    }

    private var speedTestStatusSymbol: String {
        switch speedTest.activeMeasurementPhase {
        case "download": return "arrow.down.circle.fill"
        case "upload": return "arrow.up.circle.fill"
        default: break
        }
        switch speedTest.reachabilityState {
        case .reachable: return "checkmark.circle.fill"
        case .slow: return "exclamationmark.circle.fill"
        case .failed: return "circle.fill"
        case .probing: return "network"
        case .unknown: return "questionmark.circle"
        }
    }

    private var speedTestHelp: String {
        var details = [speedTest.message]
        if let route = speedTest.route {
            details.append(route == "direct" ? uiText("Direct campus route", "校园网直连") : uiText("Via soundconnect", "经 soundconnect"))
        }
        if let latencyMs = speedTest.latencyMs {
            details.append(String(format: uiText("Latency %.0f ms", "延迟 %.0f ms"), latencyMs))
        }
        return details.joined(separator: " · ")
    }

    private var statusDetailSymbol: String {
        if model.showsCredentialSetup {
            return "person.badge.key.fill"
        }
        switch model.phase {
        case .connected: return "checkmark.circle.fill"
        case .waitingMFA: return "ellipsis.message.fill"
        case .starting, .authenticating, .connecting, .reconnecting: return "arrow.triangle.2.circlepath"
        case .degraded: return "exclamationmark.triangle.fill"
        case .stopped: return "circle.slash"
        }
    }

    private func submitCode() {
        guard !oneTimeCode.isEmpty else { return }
        model.submitAuthenticationCode(oneTimeCode)
        oneTimeCode.removeAll(keepingCapacity: false)
    }

    private func submitSetup() {
        let account = schoolAccount.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !account.isEmpty, !account.contains("\n"), !account.contains("\r") else {
            setupValidationMessage = uiText("Enter a valid school account.", "请输入有效的学校账号")
            setupFieldFocused = .schoolAccount
            return
        }
        guard !vpnPassword.isEmpty else {
            setupValidationMessage = uiText("Enter the VPN password.", "请输入 VPN 长期密码")
            setupFieldFocused = .vpnPassword
            return
        }
        setupValidationMessage = nil
        model.completeSetup(schoolAccount: account, vpnPassword: vpnPassword)
        vpnPassword.removeAll(keepingCapacity: false)
    }
}

private struct LatencySparkline: View {
    let samples: [Double]

    var body: some View {
        GeometryReader { geometry in
            let minimum = samples.min() ?? 0
            let maximum = samples.max() ?? 1
            let span = max(maximum - minimum, 1)
            Path { path in
                for (index, sample) in samples.enumerated() {
                    let x = samples.count <= 1
                        ? geometry.size.width / 2
                        : geometry.size.width * CGFloat(index) / CGFloat(samples.count - 1)
                    let normalized = (sample - minimum) / span
                    let y = geometry.size.height - (geometry.size.height * CGFloat(normalized))
                    if index == 0 {
                        path.move(to: CGPoint(x: x, y: y))
                    } else {
                        path.addLine(to: CGPoint(x: x, y: y))
                    }
                }
            }
            .stroke(.blue, style: StrokeStyle(lineWidth: 2, lineCap: .round, lineJoin: .round))
        }
        .padding(.vertical, 5)
        .background(.quaternary.opacity(0.35), in: RoundedRectangle(cornerRadius: 6))
    }
}

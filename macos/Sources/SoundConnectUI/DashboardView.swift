import SwiftUI

struct DashboardView: View {
    @ObservedObject var model: DesignModel
    @ObservedObject var speedTest: SpeedTestController
    @State private var oneTimeCode = ""
    @State private var schoolAccount = ""
    @State private var vpnPassword = ""
    @State private var setupValidationMessage: String?
    @FocusState private var codeFieldFocused: Bool
    @FocusState private var setupFieldFocused: SetupField?

    private enum SetupField: Hashable {
        case schoolAccount
        case vpnPassword
    }

    var body: some View {
        VStack(spacing: 0) {
            header

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

            if !model.showsCredentialSetup, model.phase == .connected {
                Divider()
                accessStatusRow
            }

            if !model.showsCredentialSetup {
                Divider()
                campusSpeedTestRow
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
        .animation(.easeInOut(duration: 0.18), value: model.scenario)
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
                "VPN 服务",
                isOn: Binding(
                    get: { model.isServiceEnabled },
                    set: { enabled in model.setServiceEnabled(enabled) }
                )
            )
            .labelsHidden()
            .toggleStyle(.switch)
            .controlSize(.small)
            .disabled(!model.canControlService || model.isPerformingAction)
            .help(model.isServiceEnabled ? "停止 soundconnect 服务" : "启动 soundconnect 服务")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var statusDetailRow: some View {
        HStack(alignment: .top, spacing: 7) {
            Image(systemName: statusDetailSymbol)
                .foregroundStyle(statusColor)
                .frame(width: 13)

            if model.phase == .connected {
                VStack(alignment: .leading, spacing: 2) {
                    Text("VPN 数据通道已就绪")
                        .font(.caption)
                        .fontWeight(.medium)
                    Text(model.statusDetail)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            } else {
                Text(model.statusDetail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

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

    private var accessStatusRow: some View {
        HStack(alignment: .top, spacing: 7) {
            Image(systemName: accessStatusSymbol)
                .foregroundStyle(accessStatusColor)
                .frame(width: 13)

            VStack(alignment: .leading, spacing: 2) {
                Text(model.accessStatusTitle)
                    .font(.caption)
                    .fontWeight(.medium)
                Text(model.accessStatusDetail)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
    }

    private var setupRow: some View {
        VStack(alignment: .leading, spacing: 7) {
            TextField("学校账号", text: $schoolAccount)
                .textFieldStyle(.roundedBorder)
                .controlSize(.small)
                .focused($setupFieldFocused, equals: .schoolAccount)
                .onSubmit { setupFieldFocused = .vpnPassword }

            SecureField("VPN 长期密码", text: $vpnPassword)
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
                Text("密码仅写入当前用户 0600 本地文件")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                Spacer(minLength: 4)

                if model.isReconfiguringCredentials {
                    Button("取消") {
                        model.cancelCredentialRecovery()
                    }
                    .controlSize(.small)
                    .disabled(model.isPerformingAction)
                }

                Button("保存并连接", action: submitSetup)
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
        .help("学校账号与 VPN 长期密码在首启时一起设置；短信或动态口令不会保存")
    }

    private var authenticationRow: some View {
        HStack(spacing: 7) {
            SecureField(model.authenticationPlaceholder, text: $oneTimeCode)
                .textFieldStyle(.roundedBorder)
                .controlSize(.small)
                .focused($codeFieldFocused)
                .onSubmit(submitCode)

            Button("提交", action: submitCode)
                .controlSize(.small)
                .disabled(oneTimeCode.isEmpty || model.isPerformingAction)
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 9)
        .onAppear {
            codeFieldFocused = true
        }
        .help("验证码仅经本机私有 socket 发送一次，不会保存")
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
                Text("实时")
                    .frame(width: 26, alignment: .leading)
                Label(formatRate(model.rates.downloadBytesPerSecond), systemImage: "arrow.down")
                Label(formatRate(model.rates.uploadBytesPerSecond), systemImage: "arrow.up")
                Spacer(minLength: 0)
            }

            HStack(spacing: 9) {
                Text("本次")
                    .frame(width: 26, alignment: .leading)
                Label(formatBytes(model.downloadBytes), systemImage: "arrow.down")
                Label(formatBytes(model.uploadBytes), systemImage: "arrow.up")
                Spacer(minLength: 0)
            }

            HStack(spacing: 9) {
                Text("连接")
                    .frame(width: 26, alignment: .leading)
                Text("活跃 \(model.activeConnections)")
                Spacer(minLength: 0)
            }
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
        .monospacedDigit()
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .help("本次下载 \(formatBytes(model.downloadBytes))，上传 \(formatBytes(model.uploadBytes))，活跃连接 \(model.activeConnections)")
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
            .disabled(model.isPerformingAction)
        }
        .padding(.horizontal, 12)
        .frame(minHeight: 36)
    }

    private var campusSpeedTestRow: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 7) {
                Image(systemName: "speedometer")
                    .frame(width: 13)
                Text("校园测速")
                    .font(.caption)
                    .fontWeight(.medium)
                Spacer(minLength: 4)
                if speedTest.isRunning {
                    ProgressView()
                        .controlSize(.mini)
                    Button("取消", action: speedTest.cancel)
                        .controlSize(.small)
                } else if speedTest.phase == .connectionRequired,
                          speedTest.canRetryAfterConnection
                {
                    Button("重新测速", action: speedTest.retryAfterConnection)
                        .controlSize(.small)
                } else if speedTest.phase != .componentRequired {
                    Button(speedTest.lastResult == nil ? "开始" : "再测一次", action: speedTest.start)
                        .controlSize(.small)
                }
            }

            Text(speedTest.message)
                .font(.caption2)
                .foregroundStyle(speedTest.phase == .failed ? .red : .secondary)
                .fixedSize(horizontal: false, vertical: true)

            if speedTest.phase == .componentRequired {
                HStack(spacing: 8) {
                    Text(componentDownloadDescription)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 4)
                    Button("下载组件", action: speedTest.confirmComponentDownload)
                        .controlSize(.small)
                        .disabled(speedTest.componentSize <= 0)
                }
            }

            if speedTest.phase == .downloading {
                ProgressView(value: speedTest.componentProgress)
                    .progressViewStyle(.linear)
            }

            if let download = speedTest.downloadMbps {
                HStack(spacing: 10) {
                    Label(String(format: "%.2f Mbps", download), systemImage: "arrow.down")
                    if let upload = speedTest.uploadMbps {
                        Label(String(format: "%.2f Mbps", upload), systemImage: "arrow.up")
                    }
                    Spacer(minLength: 0)
                }
                .font(.system(size: 11))
                .monospacedDigit()
            } else if let result = speedTest.lastResult,
                      let download = result.downloadMbps
            {
                Text("最近结果  ↓ \(String(format: "%.2f Mbps", download))  ↑ \(String(format: "%.2f Mbps", result.uploadMbps ?? 0))")
                    .font(.system(size: 10))
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            }

            if let route = speedTest.route {
                Text(route == "soundconnect" ? "线路：经 soundconnect" : "线路：校内直连")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
    }

    private var componentDownloadDescription: String {
        guard speedTest.componentSize > 0 else { return "组件尚未发布" }
        return "\(speedTest.componentVersion) · \(ByteCountFormatter.string(fromByteCount: speedTest.componentSize, countStyle: .file))"
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

    private var accessStatusColor: Color {
        switch model.accessProbeState {
        case .passed: return .green
        case .failed: return .orange
        case .checking, .notRun: return .secondary
        }
    }

    private var accessStatusSymbol: String {
        switch model.accessProbeState {
        case .passed: return "checkmark.seal.fill"
        case .failed: return "exclamationmark.triangle.fill"
        case .checking: return "network"
        case .notRun: return "questionmark.circle"
        }
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
            setupValidationMessage = "请输入有效的学校账号"
            setupFieldFocused = .schoolAccount
            return
        }
        guard !vpnPassword.isEmpty else {
            setupValidationMessage = "请输入 VPN 长期密码"
            setupFieldFocused = .vpnPassword
            return
        }
        setupValidationMessage = nil
        model.completeSetup(schoolAccount: account, vpnPassword: vpnPassword)
        vpnPassword.removeAll(keepingCapacity: false)
    }
}

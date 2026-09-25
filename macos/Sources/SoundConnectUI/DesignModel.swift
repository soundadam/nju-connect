import Foundation
import SwiftUI

enum DesignScenario: String, CaseIterable, Identifiable, Hashable {
    case setup
    case stopped
    case connecting
    case waitingMFA
    case connected
    case reconnecting
    case credentialRejected
    case credentialAccessCancelled
    case transportFailed

    var id: String { rawValue }

    var title: String {
        switch self {
        case .setup: return uiText("Setup", "首次设置")
        case .stopped: return uiText("Offline", "服务离线")
        case .connecting: return uiText("Connecting", "连接中")
        case .waitingMFA: return uiText("Verification", "等待验证码")
        case .connected: return uiText("Connected", "已连接")
        case .reconnecting: return uiText("Reconnecting", "重连中")
        case .credentialRejected: return uiText("Authentication failed", "认证失败")
        case .credentialAccessCancelled: return uiText("Keychain cancelled", "钥匙串访问已取消")
        case .transportFailed: return uiText("Transport failed", "传输失败")
        }
    }
}

enum DesignPhase: Equatable {
    case starting
    case authenticating
    case waitingMFA
    case connecting
    case connected
    case reconnecting
    case degraded
    case stopped
}

enum MenuBarIconState {
    case connected
    case inProgress
    case needsAttention
    case inactive
    case unknown
}

struct TrafficRates {
    let uploadBytesPerSecond: Double
    let downloadBytesPerSecond: Double
}

@MainActor
final class DesignModel: ObservableObject {
    private static let maximumTrafficRateSamples = 30
    private let gatewayOverride: String?
    private let controller: SoundConnectControlling?
    private var pendingBackendStart = false
    private var pollingTask: Task<Void, Never>?
    private var isRefreshingStatus = false
	private var lastTrafficSample: (sampledAtUnixMilli: Int64, upload: UInt64, download: UInt64)?
	private var isTrafficMonitoringActive = false
    private var runtimeSOCKSEndpoint = "127.0.0.1:1081"
    private var runtimeRates = TrafficRates(uploadBytesPerSecond: 0, downloadBytesPerSecond: 0)
    private var runtimeUploadBytes: UInt64 = 0
    private var runtimeDownloadBytes: UInt64 = 0
    private var runtimeActiveConnections = 0

    @Published var scenario: DesignScenario {
        didSet {
            if oldValue != scenario {
                actionMessage = nil
            }
            if scenario == .setup || scenario == .stopped {
                isServiceEnabled = false
            } else if oldValue == .setup || oldValue == .stopped {
                isServiceEnabled = true
            }
            if scenario != .credentialRejected {
                isReconfiguringCredentials = false
            }
        }
    }
    @Published private(set) var actionMessage: String? = nil
    @Published private(set) var isPerformingAction = false
    @Published private(set) var isReconfiguringCredentials = false
    @Published private(set) var isServiceEnabled: Bool
    @Published private(set) var uploadRateSamples: [Double] = []
    @Published private(set) var downloadRateSamples: [Double] = []
    /// The selected protocol backend. The Go CLI owns protocol behavior; this
    /// is only the user's selection and the runtime's reported owner.
    @Published private(set) var backend: SoundConnectBackend
    @Published private(set) var backendCatalog: SoundConnectBackendCatalog?

    init(
        scenario: DesignScenario = .connected,
        gatewayServer: String? = nil,
        backend: SoundConnectBackend = .easyConnect,
        controller: SoundConnectControlling? = nil
    ) {
        self.scenario = scenario
        self.gatewayOverride = gatewayServer
        self.backend = backend
        self.controller = controller
        self.isServiceEnabled = scenario != .stopped && scenario != .setup
        if let controller {
            self.scenario = .stopped
            self.isServiceEnabled = false
            controller.loadBackendCatalog { [weak self] catalog in
                self?.backendCatalog = catalog
            }
            refreshRuntimeStatus()
            startPolling()
        }
    }

    /// Gateway identity for the selected backend: an explicit override, the
    /// Go-owned catalog default, or NJU's shared gateway before the catalog
    /// loads.
    var gatewayServer: String {
        gatewayOverride
            ?? backendCatalog?.descriptor(for: backend)?.defaultGateway
            ?? "vpn.nju.edu.cn"
    }

    var menuBarGatewayLabel: String {
        let serverURL = gatewayServer.contains("://")
            ? URL(string: gatewayServer)
            : URL(string: "https://\(gatewayServer)")
        let labels = (serverURL?.host ?? gatewayServer).split(separator: ".")

        guard let vpnIndex = labels.firstIndex(where: { $0.caseInsensitiveCompare("vpn") == .orderedSame }),
              labels.indices.contains(vpnIndex + 1)
        else {
            return "VPN"
        }
        return labels[vpnIndex + 1].uppercased()
    }

    var phase: DesignPhase {
        switch scenario {
        case .setup, .stopped: return .stopped
        case .connecting: return .connecting
        case .waitingMFA: return .waitingMFA
        case .connected: return .connected
        case .reconnecting: return .reconnecting
        case .credentialRejected, .credentialAccessCancelled, .transportFailed: return .degraded
        }
    }

    var statusTitle: String {
        if scenario == .setup {
            return uiText("Setup required", "需要完成初始设置")
        }
        if isReconfiguringCredentials {
            return uiText("Reset credentials", "重置账号与密码")
        }
        switch scenario {
        case .setup: return uiText("Setup required", "需要完成初始设置")
        case .stopped: return uiText("Service offline", "服务离线")
        case .connecting: return uiText("\(backend.title) · Connecting…", "\(backend.title) · 正在连接")
        case .waitingMFA: return uiText("Waiting for verification code", "等待短信验证码")
        case .connected: return uiText("\(backend.title) · Connected", "\(backend.title) · 已连接")
        case .reconnecting: return uiText("\(backend.title) · Reconnecting…", "\(backend.title) · 正在重连")
        case .credentialRejected: return uiText("Authentication failed", "认证失败")
        case .credentialAccessCancelled: return uiText("Keychain access cancelled", "钥匙串访问已取消")
        case .transportFailed: return uiText("VPN transport failed", "VPN 传输失败")
        }
    }

    var statusDetail: String {
        if scenario == .setup {
            return uiText(
                "Enter the NJU account and password shared by both backends. A verification code may be requested next.",
                "请输入两个后端共用的南大账号和密码；短信或动态口令将在网关随后要求时单独输入"
            )
        }
        if isReconfiguringCredentials {
            return uiText("Saving will start a new sign-in.", "更新账号与长期密码后，会显式重新发起登录")
        }
        switch scenario {
        case .setup:
            return ""
        case .stopped:
            return ""
        case .connecting:
            return uiText(
                "Opening the \(backend.title) data stream and local proxy.",
                "正在打开 \(backend.title) 数据流和本机代理"
            )
        case .waitingMFA:
            return uiText("The verification code is not saved or logged.", "验证码不会被保存或写入日志")
        case .connected:
            return ""
        case .reconnecting:
            return uiText(
                "Restoring the VPN session. Sign-in will restart if recovery takes over two minutes.",
                "后台服务仍在运行，正在恢复原 VPN 会话；连续两分钟未恢复会要求重新登录"
            )
        case .credentialRejected:
            return ""
        case .credentialAccessCancelled:
            return uiText(
                "Retry, then choose Allow when macOS asks to access the saved VPN password.",
                "请点击重试，并在 macOS 请求读取已保存 VPN 密码时选择“允许”"
            )
        case .transportFailed:
            return uiText("The proxy or VPN transport failed. Fix the issue and retry.", "账号、前置代理或 VPN 传输未能完成；修复后点击重试")
        }
    }

    var showsCredentialSetup: Bool {
        scenario == .setup || isReconfiguringCredentials
    }

    var canSubmitAuthenticationCode: Bool {
        scenario == .waitingMFA
    }

    var authenticationPlaceholder: String {
        uiText("Verification code", "短信验证码")
    }

    var serviceControlNotice: String? {
        if scenario == .setup || isReconfiguringCredentials {
            return nil
        }
        if scenario == .stopped {
            return uiText("Turn on to start the VPN service.", "开启后会启动后台 VPN 服务")
        }
        return nil
    }

    var canControlService: Bool {
        !showsCredentialSetup
    }

    var requiresServiceApproval: Bool {
        false
    }

    var socksEndpoint: String {
        controller == nil ? "127.0.0.1:1081" : runtimeSOCKSEndpoint
    }

    /// Listener port and the gateway it leads to, for example "1081 → vpn.nju.edu.cn".
    var socksRouteSummary: String {
        guard let port = socksEndpoint.split(separator: ":").last, !port.isEmpty else {
            return "— → \(gatewayServer)"
        }
        return "\(port) → \(gatewayServer)"
    }

    var rates: TrafficRates {
        if controller != nil {
            return runtimeRates
        }
        switch scenario {
        case .connected:
            return TrafficRates(uploadBytesPerSecond: 128 * 1024, downloadBytesPerSecond: 1.2 * 1024 * 1024)
        case .reconnecting:
            return TrafficRates(uploadBytesPerSecond: 0, downloadBytesPerSecond: 0)
        default:
            return TrafficRates(uploadBytesPerSecond: 0, downloadBytesPerSecond: 0)
        }
    }

    var uploadBytes: UInt64 {
        if controller != nil { return runtimeUploadBytes }
        return scenario == .connected ? 4_700_000 : 0
    }

    var downloadBytes: UInt64 {
        if controller != nil { return runtimeDownloadBytes }
        return scenario == .connected ? 82_400_000 : 0
    }

    var activeConnections: Int {
        if controller != nil { return runtimeActiveConnections }
        return scenario == .connected ? 3 : 0
    }

    var menuBarIconState: MenuBarIconState {
        switch scenario {
        case .setup, .credentialRejected, .credentialAccessCancelled, .transportFailed:
            return .needsAttention
        case .connecting, .waitingMFA, .reconnecting:
            return .inProgress
        case .connected:
            return .connected
        case .stopped:
            return .inactive
        }
    }

    var retryTitle: String {
        scenario == .credentialRejected ? uiText("Reset", "重置") : uiText("Retry", "重试")
    }

    var retryDetail: String {
        switch scenario {
        case .credentialRejected:
            return uiText("Gateway rejected the account or password.", "VPN 网关未接受账号或长期密码")
        case .credentialAccessCancelled:
            return uiText("Allow Keychain access on the next attempt.", "下次重试时请允许访问钥匙串")
        case .transportFailed:
            return uiText("Reconnect after fixing the issue.", "问题修复后可重新连接")
        default:
            return uiText("Reconnect after fixing the issue.", "问题修复后可重新连接")
        }
    }

    var retriesByReconfiguringCredentials: Bool {
        scenario == .credentialRejected
    }

    /// Selects a backend and turns the service on. A live runtime is never
    /// mutated in place: it is stopped, and the new backend starts after the
    /// stop completes, so both backends can share the one SOCKS listener.
    func activateBackend(_ selection: SoundConnectBackend) {
        guard canControlService, !isPerformingAction else { return }
        if selection == backend, isServiceEnabled { return }
        let shouldRestart = selection != backend && isServiceEnabled
        backend = selection
        guard shouldRestart, let controller else {
            setServiceEnabled(true)
            return
        }
        pendingBackendStart = true
        isPerformingAction = true
        scenario = .connecting
        actionMessage = uiText("Switching to \(selection.title)…", "正在切换到 \(selection.title)")
        controller.disconnect { [weak self] result in
            guard let self else { return }
            guard self.pendingBackendStart else { return }
            self.pendingBackendStart = false
            switch result {
            case .success:
                self.clearRuntimeTraffic()
                self.isServiceEnabled = true
                self.scenario = .connecting
                self.actionMessage = uiText("Starting \(selection.title)…", "正在启动 \(selection.title)")
                self.startConnection(using: controller)
            case .failure(let error):
                self.isPerformingAction = false
                self.isServiceEnabled = true
                self.scenario = .transportFailed
                self.actionMessage = error.localizedDescription
            }
        }
    }

    func setServiceEnabled(_ enabled: Bool) {
        guard canControlService else { return }
        if !enabled { pendingBackendStart = false }
        isServiceEnabled = enabled
        guard let controller else {
            scenario = enabled ? .connecting : .stopped
            actionMessage = enabled
                ? uiText("Starting service…", "已请求启动服务")
                : uiText("Stopping service…", "已请求停止服务")
            return
        }
        isPerformingAction = true
        if enabled {
            scenario = .connecting
            actionMessage = uiText("Starting service…", "正在启动服务")
            startConnection(using: controller)
        } else {
            actionMessage = uiText("Stopping service…", "正在停止服务")
            controller.disconnect { [weak self] result in
                guard let self else { return }
                self.isPerformingAction = false
                switch result {
                case .success:
                    self.scenario = .stopped
                    self.clearRuntimeTraffic()
                case .failure(let error):
                    self.isServiceEnabled = true
                    self.scenario = .transportFailed
                    self.actionMessage = error.localizedDescription
                }
            }
        }
    }

    func submitAuthenticationCode(_ code: String) {
        guard !code.isEmpty, canSubmitAuthenticationCode else { return }
        guard controller?.submitVerificationCode(code) ?? true else {
            actionMessage = uiText("No active verification request.", "当前没有等待中的验证码请求")
            return
        }
        scenario = .connecting
        actionMessage = uiText("Code submitted. Waiting for the gateway.", "验证码已提交，等待网关确认")
    }

    func completeSetup(schoolAccount: String, vpnPassword: String) {
        guard !schoolAccount.isEmpty, !vpnPassword.isEmpty else { return }
        guard let controller else {
            isReconfiguringCredentials = false
            isServiceEnabled = true
            scenario = .connecting
            actionMessage = uiText("Credentials saved. Starting connection.", "账号与密码已保存，正在启动连接")
            return
        }
        isPerformingAction = true
        isReconfiguringCredentials = false
        scenario = .connecting
        actionMessage = uiText("Saving credentials…", "正在保存账号与密码")
        controller.saveConfiguration(
            backend: backend,
            server: gatewayServer,
            account: schoolAccount,
            password: vpnPassword
        ) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success:
                self.isServiceEnabled = true
                self.actionMessage = uiText("Credentials saved. Starting connection.", "账号与密码已保存，正在启动连接")
                self.startConnection(using: controller)
            case .failure(let error):
                self.isPerformingAction = false
                self.isServiceEnabled = false
                self.scenario = .setup
                self.actionMessage = error.localizedDescription
            }
        }
    }

    func beginCredentialRecovery() {
        guard scenario == .credentialRejected else { return }
        isReconfiguringCredentials = true
        actionMessage = nil
    }

    /// Credential recovery on aTrust can also drop the saved session, so the
    /// next connection signs in from scratch.
    var canForgetSession: Bool {
        isReconfiguringCredentials && backend == .aTrust && controller != nil
    }

    func forgetSavedSession() {
        guard canForgetSession, !isPerformingAction, let controller else { return }
        isPerformingAction = true
        controller.forgetSession { [weak self] result in
            guard let self else { return }
            self.isPerformingAction = false
            switch result {
            case .success:
                self.actionMessage = uiText(
                    "Saved aTrust session forgotten. The next connection signs in again.",
                    "已清除保存的 aTrust 会话，下次连接将重新登录"
                )
            case .failure(let error):
                self.actionMessage = error.localizedDescription
            }
        }
    }

    func cancelCredentialRecovery() {
        guard isReconfiguringCredentials else { return }
        isReconfiguringCredentials = false
        actionMessage = nil
    }

    func retry() {
        scenario = .connecting
        isServiceEnabled = true
        actionMessage = uiText("Reconnecting…", "已请求重新连接")
        if let controller {
            isPerformingAction = true
            startConnection(using: controller)
        }
    }

	func refreshRuntimeStatus() {
        guard let controller, !isRefreshingStatus else { return }
        isRefreshingStatus = true
        controller.readStatus { [weak self] result in
            guard let self else { return }
            self.isRefreshingStatus = false
            switch result {
            case .success(let snapshot):
                self.apply(snapshot)
            case .failure(let error):
                guard !self.isPerformingAction else { return }
                self.isServiceEnabled = false
                self.scenario = .transportFailed
                self.actionMessage = error.localizedDescription
            }
        }
	}

	func setTrafficMonitoringActive(_ active: Bool) {
		guard isTrafficMonitoringActive != active else {
			if active { refreshRuntimeStatus() }
			return
		}
		isTrafficMonitoringActive = active
		lastTrafficSample = nil
		runtimeRates = TrafficRates(uploadBytesPerSecond: 0, downloadBytesPerSecond: 0)
		clearTrafficRateSamples()
		if active {
			pollingTask?.cancel()
			pollingTask = nil
			controller?.startStatusMonitoring { [weak self] result in
				guard let self else { return }
				switch result {
				case .success(let snapshot):
					self.apply(snapshot)
				case .failure(let error):
					guard !self.isPerformingAction else { return }
					self.isServiceEnabled = false
					self.scenario = .transportFailed
					self.actionMessage = error.localizedDescription
				}
			}
		} else {
			controller?.stopStatusMonitoring()
			startPolling()
			refreshRuntimeStatus()
		}
	}

	private func startPolling() {
		pollingTask?.cancel()
		pollingTask = Task { [weak self] in
			while !Task.isCancelled {
				guard let self else { return }
				try? await Task.sleep(for: .seconds(5))
				guard !Task.isCancelled else { return }
				self.refreshRuntimeStatus()
            }
        }
    }

    private func startConnection(using controller: SoundConnectControlling) {
        controller.connect(
            backend: backend,
            verificationRequested: { [weak self] in
                guard let self else { return }
                self.scenario = .waitingMFA
                self.actionMessage = uiText("Enter the verification code from the gateway.", "请输入网关发送的验证码")
            },
            completion: { [weak self] result in
                guard let self else { return }
                self.isPerformingAction = false
                switch result {
                case .success:
                    self.actionMessage = uiText("Connected", "已连接")
                    self.refreshRuntimeStatus()
                case .failure(let error):
                    self.isServiceEnabled = false
                    if error.isKeychainAccessCancellation {
                        self.scenario = .credentialAccessCancelled
                        self.actionMessage = uiText(
                            "Keychain access was cancelled. Retry and choose Allow.",
                            "钥匙串访问已取消；请重试并选择“允许”"
                        )
                        return
                    }
                    self.scenario = self.isCredentialFailure(error.localizedDescription)
                        ? .credentialRejected
                        : .transportFailed
                    self.actionMessage = error.localizedDescription
                }
            }
        )
    }

    private func apply(_ snapshot: SoundConnectRuntimeSnapshot) {
        if snapshot.running {
            if pendingBackendStart { return }
            if let runningBackend = snapshot.backend, runningBackend != backend {
                guard !isPerformingAction else { return }
                // Adopt a runtime that was already running (for example one
                // started from the CLI) instead of reporting a mismatch.
                backend = runningBackend
            }
            isServiceEnabled = true
            switch snapshot.state {
            case "connected": scenario = .connected
            case "reconnecting": scenario = .reconnecting
            default: scenario = .connecting
            }
            if !snapshot.socksListen.isEmpty {
                runtimeSOCKSEndpoint = snapshot.socksListen
            }
            updateTraffic(snapshot.traffic)
            return
        }
        guard !isPerformingAction else { return }
        isServiceEnabled = false
        scenario = snapshot.configured ? .stopped : .setup
        clearRuntimeTraffic()
    }

	private func updateTraffic(_ traffic: SoundConnectTrafficSnapshot?) {
        guard let traffic else {
            clearRuntimeTraffic()
            return
        }
		runtimeUploadBytes = traffic.uploadBytes
		runtimeDownloadBytes = traffic.downloadBytes
		runtimeActiveConnections = traffic.activeConnections
		guard isTrafficMonitoringActive else {
			lastTrafficSample = nil
			runtimeRates = TrafficRates(uploadBytesPerSecond: 0, downloadBytesPerSecond: 0)
			return
		}
		let sampledAtUnixMilli = traffic.sampledAtUnixMilli > 0
			? traffic.sampledAtUnixMilli
			: Int64(Date().timeIntervalSince1970 * 1_000)
		if let previous = lastTrafficSample {
			let elapsed = Double(sampledAtUnixMilli - previous.sampledAtUnixMilli) / 1_000
			if elapsed > 0, traffic.uploadBytes >= previous.upload, traffic.downloadBytes >= previous.download {
				runtimeRates = TrafficRates(
                    uploadBytesPerSecond: Double(traffic.uploadBytes - previous.upload) / elapsed,
                    downloadBytesPerSecond: Double(traffic.downloadBytes - previous.download) / elapsed
			)
			appendTrafficRateSamples(runtimeRates)
			}
		}
		lastTrafficSample = (sampledAtUnixMilli, traffic.uploadBytes, traffic.downloadBytes)
    }

    private func clearRuntimeTraffic() {
        lastTrafficSample = nil
        runtimeRates = TrafficRates(uploadBytesPerSecond: 0, downloadBytesPerSecond: 0)
        clearTrafficRateSamples()
        runtimeUploadBytes = 0
        runtimeDownloadBytes = 0
        runtimeActiveConnections = 0
    }

    private func appendTrafficRateSamples(_ rates: TrafficRates) {
        uploadRateSamples.append(rates.uploadBytesPerSecond)
        downloadRateSamples.append(rates.downloadBytesPerSecond)
        if uploadRateSamples.count > Self.maximumTrafficRateSamples {
            uploadRateSamples.removeFirst(uploadRateSamples.count - Self.maximumTrafficRateSamples)
        }
        if downloadRateSamples.count > Self.maximumTrafficRateSamples {
            downloadRateSamples.removeFirst(downloadRateSamples.count - Self.maximumTrafficRateSamples)
        }
    }

    private func clearTrafficRateSamples() {
        uploadRateSamples.removeAll(keepingCapacity: true)
        downloadRateSamples.removeAll(keepingCapacity: true)
    }

    private func isCredentialFailure(_ message: String) -> Bool {
        message.localizedCaseInsensitiveContains("authentication rejected")
            || message.localizedCaseInsensitiveContains("password authentication")
            || message.localizedCaseInsensitiveContains("credential")
            || message.localizedCaseInsensitiveContains("no saved VPN password")
    }
}

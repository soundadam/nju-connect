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
    case transportFailed

    var id: String { rawValue }

    var title: String {
        switch self {
        case .setup: return "Setup"
        case .stopped: return "Disconnected"
        case .connecting: return "Connecting"
        case .waitingMFA: return "Verification"
        case .connected: return "Connected"
        case .reconnecting: return "Reconnecting"
        case .credentialRejected: return "Authentication failed"
        case .transportFailed: return "Transport failed"
        }
    }
}

enum DesignPhase: Equatable {
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
}

struct TrafficRates {
    let uploadBytesPerSecond: Double
    let downloadBytesPerSecond: Double
}

@MainActor
final class DesignModel: ObservableObject {
    private static let maximumTrafficRateSamples = 30
    private let gatewayOverride: String?
    private let controller: NJUConnectControlling?
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
    @Published private(set) var backend: NJUConnectBackend
    @Published private(set) var backendCatalog: NJUConnectBackendCatalog?

    init(
        scenario: DesignScenario = .connected,
        gatewayServer: String? = nil,
        backend: NJUConnectBackend = .easyConnect,
        controller: NJUConnectControlling? = nil
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
        } else {
            // A deterministic curve so the design preview shows a live chart.
            downloadRateSamples = (0..<Self.maximumTrafficRateSamples).map { index in
                let x = Double(index)
                return 1_000_000 * (0.55 + 0.3 * sin(x * 0.7) + 0.1 * sin(x * 2.3))
            }
            uploadRateSamples = downloadRateSamples.map { $0 / 8 }
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
        case .credentialRejected, .transportFailed: return .degraded
        }
    }

    var statusTitle: String {
        if isReconfiguringCredentials {
            return "Reset credentials"
        }
        switch scenario {
        case .setup: return "Setup required"
        case .stopped: return "Disconnected"
        case .connecting: return "\(backend.title) · Connecting…"
        case .waitingMFA: return "Waiting for verification code"
        case .connected: return "\(backend.title) · Connected"
        case .reconnecting: return "\(backend.title) · Reconnecting…"
        case .credentialRejected: return "Authentication failed"
        case .transportFailed: return "VPN transport failed"
        }
    }

    var statusDetail: String {
        if isReconfiguringCredentials {
            return "Saving will start a new sign-in."
        }
        switch scenario {
        case .setup:
            return "Enter the NJU account and password shared by both backends. A verification code may be requested next."
        case .stopped, .connected, .credentialRejected:
            return ""
        case .connecting:
            return "Opening the \(backend.title) data stream and local proxy."
        case .waitingMFA:
            return "The verification code is not saved or logged."
        case .reconnecting:
            return "Restoring the VPN session. Sign-in will restart if recovery takes over two minutes."
        case .transportFailed:
            return "The proxy or VPN transport failed. Fix the issue and retry."
        }
    }

    var showsCredentialSetup: Bool {
        scenario == .setup || isReconfiguringCredentials
    }

    var canSubmitAuthenticationCode: Bool {
        scenario == .waitingMFA
    }

    var authenticationPlaceholder: String {
        "Verification code"
    }

    var serviceControlNotice: String? {
        guard scenario == .stopped, !isReconfiguringCredentials else { return nil }
        return "Choose EasyConnect or aTrust to connect."
    }

    var canControlService: Bool {
        !showsCredentialSetup
    }

    var socksEndpoint: String {
        controller == nil ? "127.0.0.1:1081" : runtimeSOCKSEndpoint
    }

    /// The listener port, for example "1081" from "127.0.0.1:1081".
    var socksPort: String {
        guard let port = socksEndpoint.split(separator: ":").last, !port.isEmpty else { return "—" }
        return String(port)
    }

    var rates: TrafficRates {
        if controller != nil {
            return runtimeRates
        }
        return scenario == .connected
            ? TrafficRates(uploadBytesPerSecond: 128 * 1024, downloadBytesPerSecond: 1.2 * 1024 * 1024)
            : TrafficRates(uploadBytesPerSecond: 0, downloadBytesPerSecond: 0)
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
        case .setup, .credentialRejected, .transportFailed:
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
        scenario == .credentialRejected ? "Reset" : "Retry"
    }

    var retryDetail: String {
        switch scenario {
        case .credentialRejected:
            return "Gateway rejected the account or password."
        default:
            return "Reconnect after fixing the issue."
        }
    }

    var retriesByReconfiguringCredentials: Bool {
        scenario == .credentialRejected
    }

    /// Selects a backend and turns the service on. A live runtime is never
    /// mutated in place: it is stopped, and the new backend starts after the
    /// stop completes, so both backends can share the one SOCKS listener.
    func activateBackend(_ selection: NJUConnectBackend) {
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
        actionMessage = "Switching to \(selection.title)…"
        controller.disconnect { [weak self] result in
            guard let self else { return }
            guard self.pendingBackendStart else { return }
            self.pendingBackendStart = false
            switch result {
            case .success:
                self.clearRuntimeTraffic()
                self.isServiceEnabled = true
                self.scenario = .connecting
                self.actionMessage = "Starting \(selection.title)…"
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
                ? "Starting service…"
                : "Stopping service…"
            return
        }
        isPerformingAction = true
        if enabled {
            scenario = .connecting
            actionMessage = "Starting service…"
            startConnection(using: controller)
        } else {
            actionMessage = "Stopping service…"
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
            actionMessage = "No active verification request."
            return
        }
        scenario = .connecting
        actionMessage = "Code submitted. Waiting for the gateway."
    }

    func completeSetup(schoolAccount: String, vpnPassword: String) {
        guard !schoolAccount.isEmpty, !vpnPassword.isEmpty else { return }
        guard let controller else {
            isReconfiguringCredentials = false
            isServiceEnabled = true
            scenario = .connecting
            actionMessage = "Credentials saved. Starting connection."
            return
        }
        isPerformingAction = true
        isReconfiguringCredentials = false
        scenario = .connecting
        actionMessage = "Saving credentials…"
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
                self.actionMessage = "Credentials saved. Starting connection."
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
                self.actionMessage = "Saved aTrust session forgotten. The next connection signs in again."
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
        actionMessage = "Reconnecting…"
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
        guard let controller else { return }
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
            controller.startStatusMonitoring { [weak self] result in
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
            controller.stopStatusMonitoring()
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

    private func startConnection(using controller: NJUConnectControlling) {
        controller.connect(
            backend: backend,
            verificationRequested: { [weak self] in
                guard let self else { return }
                self.scenario = .waitingMFA
                self.actionMessage = "Enter the verification code from the gateway."
            },
            completion: { [weak self] result in
                guard let self else { return }
                self.isPerformingAction = false
                switch result {
                case .success:
                    self.actionMessage = "Connected"
                    self.refreshRuntimeStatus()
                case .failure(let error):
                    self.isServiceEnabled = false
                    self.scenario = Self.isCredentialFailure(error.localizedDescription)
                        ? .credentialRejected
                        : .transportFailed
                    self.actionMessage = error.localizedDescription
                }
            }
        )
    }

    private func apply(_ snapshot: NJUConnectRuntimeSnapshot) {
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

    private func updateTraffic(_ traffic: NJUConnectTrafficSnapshot?) {
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

    /// Whether a failed connect was about the saved username or password.
    /// The CLI marks a rejected sign-in with the stable `credential_rejected:`
    /// token and a missing password with `no saved VPN password`.
    nonisolated static func isCredentialFailure(_ message: String) -> Bool {
        message.contains("credential_rejected:")
            || message.contains("no saved VPN password")
    }
}

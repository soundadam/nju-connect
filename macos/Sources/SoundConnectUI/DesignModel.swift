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
        case .setup: return uiText("Setup", "首次设置")
        case .stopped: return uiText("Offline", "服务离线")
        case .connecting: return uiText("Connecting", "连接中")
        case .waitingMFA: return uiText("Verification", "等待验证码")
        case .connected: return uiText("Connected", "已连接")
        case .reconnecting: return uiText("Reconnecting", "重连中")
        case .credentialRejected: return uiText("Auth failed", "账号失败")
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
    let gatewayServer: String

    @Published var scenario: DesignScenario {
        didSet {
            actionMessage = nil
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

    init(
        scenario: DesignScenario = .connected,
        gatewayServer: String = "vpn.nju.edu.cn"
    ) {
        self.scenario = scenario
        self.gatewayServer = gatewayServer
        self.isServiceEnabled = scenario != .stopped && scenario != .setup
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
        if scenario == .setup {
            return uiText("Setup required", "需要完成初始设置")
        }
        if isReconfiguringCredentials {
            return uiText("Reset credentials", "重置账号与密码")
        }
        switch scenario {
        case .setup: return uiText("Setup required", "需要完成初始设置")
        case .stopped: return uiText("Service offline", "服务离线")
        case .connecting: return uiText("Connecting to campus VPN", "正在建立校园 VPN")
        case .waitingMFA: return uiText("Waiting for verification code", "等待短信验证码")
        case .connected: return uiText("Campus VPN connected", "校园 VPN 已连接")
        case .reconnecting: return uiText("Reconnecting campus VPN", "校园 VPN 正在重连")
        case .credentialRejected: return uiText("Authentication failed", "认证失败")
        case .transportFailed: return uiText("VPN transport failed", "VPN 传输失败")
        }
    }

    var statusDetail: String {
        if scenario == .setup {
            return uiText(
                "Enter your school account and VPN password. A verification code may be requested next.",
                "请设置学校账号与 VPN 长期密码；短信或动态口令将在网关随后要求时单独输入"
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
            return uiText("Opening the VPN data stream and local proxy.", "认证已完成，正在打开双向 VPN 数据流和本机代理")
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
        "127.0.0.1:1081"
    }

    var rates: TrafficRates {
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
        scenario == .connected ? 4_700_000 : 0
    }

    var downloadBytes: UInt64 {
        scenario == .connected ? 82_400_000 : 0
    }

    var activeConnections: Int {
        scenario == .connected ? 3 : 0
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
        scenario == .credentialRejected ? uiText("Reset", "重置") : uiText("Retry", "重试")
    }

    var retryDetail: String {
        switch scenario {
        case .credentialRejected:
            return uiText("Gateway rejected the account or password.", "VPN 网关未接受账号或长期密码")
        case .transportFailed:
            return uiText("Reconnect after fixing the issue.", "问题修复后可重新连接")
        default:
            return uiText("Reconnect after fixing the issue.", "问题修复后可重新连接")
        }
    }

    var retriesByReconfiguringCredentials: Bool {
        scenario == .credentialRejected
    }

    func setServiceEnabled(_ enabled: Bool) {
        guard canControlService else { return }
        isServiceEnabled = enabled
        scenario = enabled ? .connecting : .stopped
        actionMessage = enabled
            ? uiText("Starting service", "已请求启动服务")
            : uiText("Stopping service", "已请求停止服务")
    }

    func submitAuthenticationCode(_ code: String) {
        guard !code.isEmpty, canSubmitAuthenticationCode else { return }
        scenario = .connecting
        actionMessage = uiText("Code submitted. Waiting for the gateway.", "验证码已提交，等待网关确认")
    }

    func completeSetup(schoolAccount: String, vpnPassword: String) {
        guard !schoolAccount.isEmpty, !vpnPassword.isEmpty else { return }
        isReconfiguringCredentials = false
        isServiceEnabled = true
        scenario = .connecting
        actionMessage = uiText("Credentials saved. Starting connection.", "账号与密码已保存，正在启动连接")
    }

    func beginCredentialRecovery() {
        guard scenario == .credentialRejected else { return }
        isReconfiguringCredentials = true
        actionMessage = nil
    }

    func cancelCredentialRecovery() {
        guard isReconfiguringCredentials else { return }
        isReconfiguringCredentials = false
        actionMessage = nil
    }

    func retry() {
        scenario = .connecting
        isServiceEnabled = true
        actionMessage = uiText("Reconnecting", "已请求重新连接")
    }
}

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
        case .setup: return "首次设置"
        case .stopped: return "服务离线"
        case .connecting: return "连接中"
        case .waitingMFA: return "等待验证码"
        case .connected: return "已连接"
        case .reconnecting: return "重连中"
        case .credentialRejected: return "账号失败"
        case .transportFailed: return "传输失败"
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

enum AccessProbeState {
    case notRun
    case checking
    case passed
    case failed
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
            return "需要完成初始设置"
        }
        if isReconfiguringCredentials {
            return "重新设置账号与密码"
        }
        switch scenario {
        case .setup: return "需要完成初始设置"
        case .stopped: return "服务离线"
        case .connecting: return "正在建立校园 VPN"
        case .waitingMFA: return "等待短信验证码"
        case .connected: return "校园 VPN 已连接"
        case .reconnecting: return "校园 VPN 正在重连"
        case .credentialRejected: return "学校账号或密码未通过"
        case .transportFailed: return "VPN 传输失败"
        }
    }

    var statusDetail: String {
        if scenario == .setup {
            return "请设置学校账号与 VPN 长期密码；短信或动态口令将在网关随后要求时单独输入"
        }
        if isReconfiguringCredentials {
            return "更新账号与长期密码后，会显式重新发起登录"
        }
        switch scenario {
        case .setup:
            return ""
        case .stopped:
            return "菜单栏 App 与 VPN 服务相互独立"
        case .connecting:
            return "认证已完成，正在打开双向 VPN 数据流和本机代理"
        case .waitingMFA:
            return "验证码不会被保存或写入日志"
        case .connected:
            return "账号认证已完成；双向 VPN 数据流和本机 SOCKS5 监听均正常"
        case .reconnecting:
            return "后台服务仍在运行，正在恢复原 VPN 会话；连续两分钟未恢复会要求重新登录"
        case .credentialRejected:
            return "VPN 网关未接受当前学校账号或长期密码；请重新设置后显式重新登录"
        case .transportFailed:
            return "账号、前置代理或 VPN 传输未能完成；修复后点击重试"
        }
    }

    var showsCredentialSetup: Bool {
        scenario == .setup || isReconfiguringCredentials
    }

    var canSubmitAuthenticationCode: Bool {
        scenario == .waitingMFA
    }

    var authenticationPlaceholder: String {
        "短信验证码"
    }

    var serviceControlNotice: String? {
        if scenario == .setup || isReconfiguringCredentials {
            return nil
        }
        if scenario == .stopped {
            return "开启后会启动后台 VPN 服务"
        }
        return nil
    }

    var canControlService: Bool {
        !showsCredentialSetup
    }

    var requiresServiceApproval: Bool {
        false
    }

    var accessProbeState: AccessProbeState {
        switch scenario {
        case .connected: return .passed
        case .reconnecting: return .checking
        case .transportFailed: return .failed
        default: return .notRun
        }
    }

    var accessStatusTitle: String {
        switch accessProbeState {
        case .notRun: return "尚未验证应用代理路径"
        case .checking: return "正在验证应用代理路径"
        case .passed: return "校内站点经 VPN 可达"
        case .failed: return "校内站点暂不可达"
        }
    }

    var accessStatusDetail: String {
        switch accessProbeState {
        case .notRun:
            return "等待通过 \(socksEndpoint) 执行无正文应用层测试"
        case .checking:
            return "正在通过 SOCKS5 \(socksEndpoint) 请求固定校内站点，不发送 Cookie 或读取正文；最近一次检查仍会保留"
        case .passed:
            return "已通过 SOCKS5 \(socksEndpoint) 收到固定校内站点的 HTTP 响应；这证明应用代理路径可用，不代表所有校内资源都可达"
        case .failed:
            return "VPN 数据流仍已连接，但固定校内站点暂未返回响应"
        }
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
        scenario == .credentialRejected ? "重新设置账号和密码" : "重试"
    }

    var retryDetail: String {
        switch scenario {
        case .credentialRejected: return "账号或长期密码未通过"
        case .transportFailed: return "问题修复后可重新连接"
        default: return "问题修复后可重新连接"
        }
    }

    var retriesByReconfiguringCredentials: Bool {
        scenario == .credentialRejected
    }

    func setServiceEnabled(_ enabled: Bool) {
        guard canControlService else { return }
        isServiceEnabled = enabled
        scenario = enabled ? .connecting : .stopped
        actionMessage = enabled ? "已请求启动服务" : "已请求停止服务"
    }

    func submitAuthenticationCode(_ code: String) {
        guard !code.isEmpty, canSubmitAuthenticationCode else { return }
        scenario = .connecting
        actionMessage = "验证码已提交，等待网关确认"
    }

    func completeSetup(schoolAccount: String, vpnPassword: String) {
        guard !schoolAccount.isEmpty, !vpnPassword.isEmpty else { return }
        isReconfiguringCredentials = false
        isServiceEnabled = true
        scenario = .connecting
        actionMessage = "账号与密码已保存，正在启动连接"
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
        actionMessage = "已请求重新连接"
    }
}

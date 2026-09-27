import SwiftUI

struct VPNContextRows: View {
    @ObservedObject var model: DesignModel
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

            if !model.showsCredentialSetup, model.phase == .degraded {
                Divider()
                retryRow
            }
        }
    }

    private var statusDetailRow: some View {
        HStack(alignment: .top, spacing: 7) {
            Image(systemName: statusDetailSymbol)
                .foregroundStyle(model.statusTint)
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
            Image(systemName: "power")
                .foregroundStyle(.secondary)
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
            TextField(uiText("NJU account", "南大账号"), text: $schoolAccount)
                .textFieldStyle(.roundedBorder)
                .controlSize(.small)
                .focused($setupFieldFocused, equals: .schoolAccount)
                .onSubmit { setupFieldFocused = .vpnPassword }

            SecureField(uiText("NJU password", "南大密码"), text: $vpnPassword)
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

                if model.canForgetSession {
                    Button(uiText("Forget session", "清除会话")) {
                        model.forgetSavedSession()
                    }
                    .controlSize(.small)
                    .disabled(model.isPerformingAction)
                    .help(uiText(
                        "Forget the saved aTrust session and browser sign-in; the password is kept.",
                        "清除保存的 aTrust 会话与浏览器登录状态，保留密码"
                    ))
                }

                if model.isReconfiguringCredentials {
                    Button(uiText("Cancel", "取消")) {
                        model.cancelCredentialRecovery()
                    }
                    .controlSize(.small)
                    .disabled(model.isPerformingAction)
                }

                Button(uiText("Save & Connect", "保存并连接"), action: submitSetup)
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
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
                "The account is saved in nju-connect's settings; the long-lived password stays in Keychain and is shared by both backends. Verification codes are never saved.",
                "账号保存在 nju-connect 设置中，长期密码存储在钥匙串，两个后端共用；短信或动态口令不会保存"
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
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .disabled(oneTimeCode.isEmpty || model.isPerformingAction)
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 9)
        .onAppear {
            codeFieldFocused = true
        }
        .help(
            uiText(
                "The code is sent once through a private local socket and is never saved.",
                "验证码仅经本机私有 socket 发送一次，不会保存"
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
            .buttonStyle(.borderedProminent)
            .controlSize(.small)
            .frame(minWidth: 56, minHeight: 32)
            .contentShape(Rectangle())
            .disabled(model.isPerformingAction)
        }
        .padding(.horizontal, 12)
        .frame(minHeight: 36)
    }

    private var statusDetailSymbol: String {
        if model.showsCredentialSetup {
            return "person.badge.key.fill"
        }
        switch model.phase {
        case .connected: return "checkmark.circle.fill"
        case .waitingMFA: return "ellipsis.message.fill"
        case .connecting, .reconnecting: return "arrow.triangle.2.circlepath"
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

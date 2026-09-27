import SwiftUI

/// Off, EasyConnect, or aTrust as one native segmented control. The binding
/// reads the model, so a failed start or stop snaps back to the real state.
struct BackendSwitch: View {
    @ObservedObject var model: DesignModel

    private enum Selection: Hashable {
        case off
        case backend(NJUConnectBackend)
    }

    var body: some View {
        Picker(uiText("VPN", "VPN"), selection: selection) {
            Text(uiText("Off", "关闭")).tag(Selection.off)
            ForEach(NJUConnectBackend.allCases) { backend in
                Text(backend.title).tag(Selection.backend(backend))
            }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .disabled(model.isPerformingAction || !model.canControlService)
        .padding(.horizontal, 12)
        .padding(.bottom, 10)
        .accessibilityLabel(uiText("VPN backend and service", "VPN 后端与服务开关"))
        .help(uiText("Choose Off, EasyConnect, or aTrust.", "选择关闭、EasyConnect 或 aTrust"))
    }

    private var selection: Binding<Selection> {
        Binding(
            get: { model.isServiceEnabled ? .backend(model.backend) : .off },
            set: { newValue in
                switch newValue {
                case .off: model.setServiceEnabled(false)
                case .backend(let backend): model.activateBackend(backend)
                }
            }
        )
    }
}

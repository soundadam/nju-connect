import SwiftUI

/// A compact three-position control layered onto the existing dashboard:
/// Off, EasyConnect, and aTrust.
struct BackendSwitch: View {
    @ObservedObject var model: DesignModel

    var body: some View {
        GeometryReader { geometry in
            let segmentWidth = geometry.size.width / 3
            let selectedIndex = model.isServiceEnabled
                ? (model.backend == .easyConnect ? 1 : 2)
                : 0
            let selectedOffset = CGFloat(selectedIndex - 1) * segmentWidth

            ZStack {
                Capsule()
                    .fill(.quaternary)

                Capsule()
                    .fill(model.isServiceEnabled ? Color.accentColor : Color.secondary)
                    .frame(width: segmentWidth - 4, height: geometry.size.height - 4)
                    .offset(x: selectedOffset)

                HStack(spacing: 0) {
                    singleLineLabel(uiText("Off", "关"), isSelected: selectedIndex == 0)
                    easyConnectLabel(isSelected: selectedIndex == 1)
                    singleLineLabel("aTrust", isSelected: selectedIndex == 2)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .contentShape(Capsule())
            .gesture(
                DragGesture(minimumDistance: 0)
                    .onEnded { value in
                        switch max(0, min(Int(value.location.x / segmentWidth), 2)) {
                        case 0:
                            model.setServiceEnabled(false)
                        case 1:
                            model.activateBackend(.easyConnect)
                        default:
                            model.activateBackend(.aTrust)
                        }
                    }
            )
        }
        .frame(width: 112, height: 28)
        .opacity(model.isPerformingAction ? 0.65 : 1)
        .allowsHitTesting(!model.isPerformingAction && model.canControlService)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(uiText("VPN backend and service", "VPN 后端与服务开关"))
        .accessibilityValue(
            uiText(
                "\(model.backend.title), \(model.isServiceEnabled ? "on" : "off")",
                "\(model.backend.title)，\(model.isServiceEnabled ? "开启" : "关闭")"
            )
        )
        .help(uiText("Choose Off, EasyConnect, or aTrust.", "选择关闭、EasyConnect 或 aTrust"))
    }

    private func singleLineLabel(_ title: String, isSelected: Bool) -> some View {
        Text(title)
            .font(.system(size: 8, weight: .semibold))
            .foregroundStyle(isSelected ? .white : .secondary)
            .frame(maxWidth: .infinity)
            .lineLimit(1)
            .minimumScaleFactor(0.75)
    }

    private func easyConnectLabel(isSelected: Bool) -> some View {
        VStack(spacing: -1) {
            Text("easy")
            Text("connect")
        }
        .font(.system(size: 7, weight: .semibold))
        .foregroundStyle(isSelected ? .white : .secondary)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .lineLimit(1)
        .minimumScaleFactor(0.75)
    }
}

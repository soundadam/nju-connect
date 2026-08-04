import SwiftUI

@main
struct SoundConnectApp: App {
    @StateObject private var model = DesignModel()
    @StateObject private var speedTest = SpeedTestController()

    var body: some Scene {
#if UI_DESIGN_PREVIEW
        WindowGroup("soundconnect UI preview") {
            DesignPreviewView()
        }
        .windowResizability(.contentSize)
#else
        MenuBarExtra {
            DashboardView(model: model, speedTest: speedTest)
        } label: {
            MenuBarLabel(model: model)
        }
        .menuBarExtraStyle(.window)
#endif
    }
}

#if !UI_DESIGN_PREVIEW
private struct MenuBarLabel: View {
    @ObservedObject var model: DesignModel

    var body: some View {
        HStack(spacing: 3) {
            MenuBarStatusIcon(state: model.menuBarIconState)
            Text(model.menuBarGatewayLabel)
                .font(.system(size: 11, weight: .semibold))
        }
        .fixedSize()
        .accessibilityLabel("soundconnect, \(model.statusTitle)")
    }
}
#endif

#if UI_DESIGN_PREVIEW
private struct DesignPreviewView: View {
    @StateObject private var model = DesignModel()
    @StateObject private var speedTest = SpeedTestController(previewState: .download)
    @State private var speedPreviewState: CampusSpeedTestPreviewState = .download

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VStack(alignment: .leading, spacing: 4) {
                Text("soundconnect macOS UI")
                    .font(.title2.weight(.semibold))
                Text(uiText("Phase 1: review the menu-bar layout with simulated VPN states.", "第一阶段：开发菜单栏面板结构，用模拟状态参与视觉设计"))
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 12) {
                Picker(uiText("State", "状态"), selection: $model.scenario) {
                    ForEach(DesignScenario.allCases) { scenario in
                        Text(scenario.title).tag(scenario)
                    }
                }
                .frame(width: 170)

                Picker(uiText("Speed test", "测速"), selection: $speedPreviewState) {
                    ForEach(CampusSpeedTestPreviewState.allCases) { state in
                        Text(state.title).tag(state)
                    }
                }
                .frame(width: 150)
                .onChange(of: speedPreviewState) { newState in
                    speedTest.applyPreviewState(newState)
                }

                Button(uiText("Reset to Connected", "恢复已连接")) {
                    model.scenario = .connected
                }

                Spacer()
            }

            Divider()

            HStack(alignment: .top, spacing: 24) {
                VStack(alignment: .leading, spacing: 8) {
                    Text(uiText("Panel Preview", "面板预览"))
                        .font(.headline)
                    DashboardView(model: model, speedTest: speedTest)
                        .background(.regularMaterial)
                        .clipShape(RoundedRectangle(cornerRadius: 12))
                        .overlay {
                            RoundedRectangle(cornerRadius: 12)
                                .stroke(.quaternary, lineWidth: 1)
                        }
                }

                reviewNotes
            }
        }
        .padding(24)
        .frame(minWidth: 720, minHeight: 500)
    }

    private var reviewNotes: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(uiText("Review these four details", "这一轮先看 4 个细节"))
                .font(.headline)

            reviewItem("01", uiText("Status hierarchy", "状态层级"), uiText("Are the status dot, product name, connection state, and service toggle clear?", "顶部圆点、产品名、连接状态和服务开关是否足够清楚？"))
            reviewItem("02", uiText("Information density", "信息密度"), uiText("Do the width, dividers, and supporting copy feel crowded?", "面板宽度、分隔线和辅助说明是否显得拥挤？"))
            reviewItem("03", uiText("Actions", "动作入口"), uiText("Are setup, verification, and retry actions prioritized correctly?", "首次设置、验证码、重试三个动作的优先级是否合理？"))
            reviewItem("04", uiText("Connection evidence", "连接证据"), uiText("Should SOCKS5, reachability, and traffic remain visible?", "SOCKS5 地址、应用层探测和实时/累计流量是否需要保留？"))

            Text(uiText("Switch states above, then review copy, spacing, color, and interaction.", "先在上方切换状态，再逐项讨论文案、间距、颜色与交互。"))
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(width: 280, alignment: .leading)
    }

    private func reviewItem(_ number: String, _ title: String, _ detail: String) -> some View {
            HStack(alignment: .top, spacing: 9) {
            Text(number)
                .font(.system(.caption, design: .monospaced, weight: .semibold))
                .foregroundStyle(.secondary)
                .frame(width: 22, alignment: .leading)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption.weight(.semibold))
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}
#endif

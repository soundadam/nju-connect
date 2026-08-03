import SwiftUI

@main
struct SoundConnectApp: App {
    @StateObject private var model = DesignModel()

    var body: some Scene {
#if UI_DESIGN_PREVIEW
        WindowGroup("soundconnect UI preview") {
            DesignPreviewView()
        }
        .windowResizability(.contentSize)
#else
        MenuBarExtra {
            DashboardView(model: model)
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
            Text("SC")
                .font(.system(size: 11, weight: .semibold))
        }
        .fixedSize()
        .accessibilityLabel("soundconnect，\(model.statusTitle)")
    }
}
#endif

#if UI_DESIGN_PREVIEW
private struct DesignPreviewView: View {
    @StateObject private var model = DesignModel()

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VStack(alignment: .leading, spacing: 4) {
                Text("soundconnect macOS UI")
                    .font(.title2.weight(.semibold))
                Text("第一阶段：开发菜单栏面板结构，用模拟状态参与视觉设计")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 12) {
                Picker("状态", selection: $model.scenario) {
                    ForEach(DesignScenario.allCases) { scenario in
                        Text(scenario.title).tag(scenario)
                    }
                }
                .frame(width: 170)

                Button("恢复已连接") {
                    model.scenario = .connected
                }

                Spacer()
            }

            Divider()

            HStack(alignment: .top, spacing: 28) {
                VStack(alignment: .leading, spacing: 8) {
                    Text("面板预览")
                        .font(.headline)
                    DashboardView(model: model)
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
        .frame(minWidth: 760, minHeight: 520)
    }

    private var reviewNotes: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("这一轮先看 4 个细节")
                .font(.headline)

            reviewItem("01", "状态层级", "顶部圆点、产品名、连接状态和服务开关是否足够清楚？")
            reviewItem("02", "信息密度", "面板宽度、分隔线和辅助说明是否显得拥挤？")
            reviewItem("03", "动作入口", "首次设置、验证码、重试三个动作的优先级是否合理？")
            reviewItem("04", "连接证据", "SOCKS5 地址、应用层探测和实时/累计流量是否需要保留？")

            Text("先在上方切换状态，再逐项讨论文案、间距、颜色与交互。")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(width: 300, alignment: .leading)
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

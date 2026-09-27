import SwiftUI

@main
struct NJUConnectApp: App {
#if UI_DESIGN_PREVIEW
    @StateObject private var model = DesignModel()
#else
    @StateObject private var model = DesignModel(controller: NJUConnectController())
#endif
    @StateObject private var speedTest = SpeedTestController()

    var body: some Scene {
#if UI_DESIGN_PREVIEW
        WindowGroup("nju-connect UI preview") {
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
        .accessibilityLabel("nju-connect, \(model.statusTitle)")
    }
}
#endif

#if UI_DESIGN_PREVIEW
private struct DesignPreviewView: View {
    @StateObject private var model = DesignModel()
    @StateObject private var speedTest = SpeedTestController(previewState: .idle)
    @State private var speedPreviewState: CampusSpeedTestPreviewState = .idle

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VStack(alignment: .leading, spacing: 4) {
                Text("nju-connect macOS UI")
                    .font(.title2.weight(.semibold))
                Text("Review the menu-bar layout with simulated VPN states.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 12) {
                Picker("State", selection: $model.scenario) {
                    ForEach(DesignScenario.allCases) { scenario in
                        Text(scenario.title).tag(scenario)
                    }
                }
                .frame(width: 170)

                Picker("Speed test", selection: $speedPreviewState) {
                    ForEach(CampusSpeedTestPreviewState.allCases) { state in
                        Text(state.title).tag(state)
                    }
                }
                .frame(width: 150)
                .onChange(of: speedPreviewState) { newState in
                    speedTest.applyPreviewState(newState)
                }

                Spacer()
            }

            Divider()

            DashboardView(model: model, speedTest: speedTest)
                .background(.regularMaterial)
                .clipShape(RoundedRectangle(cornerRadius: 12))
                .overlay {
                    RoundedRectangle(cornerRadius: 12)
                        .stroke(.quaternary, lineWidth: 1)
                }
        }
        .padding(24)
        .frame(minWidth: 340, alignment: .topLeading)
    }
}
#endif

import SwiftUI

struct DashboardView: View {
    @ObservedObject var model: DesignModel
    @ObservedObject var speedTest: SpeedTestController

    var body: some View {
        VStack(spacing: 0) {
            DashboardHeader(model: model)
            BackendSwitch(model: model)

            Divider()
            CampusSpeedSummaryRow(speedTest: speedTest)

            // Only the sections below the fixed rows animate; the switch and
            // the speed row never move under the pointer.
            VStack(spacing: 0) {
                VPNContextRows(model: model)

                if !model.showsCredentialSetup,
                   model.phase == .connected || model.phase == .reconnecting
                {
                    Divider()
                    TrafficRow(model: model)
                        .transition(.opacity)
                }
            }
            .animation(.easeOut(duration: 0.2), value: model.scenario)
            .animation(.easeOut(duration: 0.2), value: model.isReconfiguringCredentials)
        }
        .frame(width: 292)
        .tint(.brand)
        .onAppear {
            model.setTrafficMonitoringActive(true)
            speedTest.setVPNConnected(model.phase == .connected)
            speedTest.beginLatencySamplingIfNeeded()
        }
        .onDisappear {
            model.setTrafficMonitoringActive(false)
        }
        .task {
            // Keeps the speed row's verdict current while the panel stays
            // open, so a tunnel that drops shows up without reopening it.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                speedTest.beginLatencySamplingIfNeeded(maxAge: 30)
            }
        }
        .onChange(of: model.phase) { phase in
            speedTest.setVPNConnected(phase == .connected)
        }
    }
}

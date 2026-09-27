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

            VPNContextRows(model: model)

            if !model.showsCredentialSetup,
               model.phase == .connected || model.phase == .reconnecting
            {
                Divider()
                TrafficRow(model: model)
            }
        }
        .frame(width: 292)
        .tint(.brand)
        .onAppear {
            model.setTrafficMonitoringActive(true)
            speedTest.beginLatencySamplingIfNeeded()
        }
        .onDisappear {
            model.setTrafficMonitoringActive(false)
        }
        .onChange(of: model.phase) { phase in
            if phase == .connected {
                speedTest.beginLatencySampling(force: true)
            }
        }
    }
}

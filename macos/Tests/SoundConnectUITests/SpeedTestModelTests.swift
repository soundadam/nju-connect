import Foundation
import XCTest
@testable import soundconnect_ui

final class SpeedTestModelTests: XCTestCase {
    @MainActor
    private final class FakeSoundConnectController: SoundConnectControlling {
        var snapshot = SoundConnectRuntimeSnapshot(
            configured: true,
            running: true,
            state: "connected",
            socksListen: "127.0.0.1:1081",
            traffic: SoundConnectTrafficSnapshot(uploadBytes: 12, downloadBytes: 34, activeConnections: 2)
        )
        var savedAccount: String?
        var disconnectCalls = 0
		var statusMonitorCompletion: SoundConnectStatusCompletion?

        func readStatus(completion: @escaping SoundConnectStatusCompletion) {
            completion(.success(snapshot))
        }

		func startStatusMonitoring(completion: @escaping SoundConnectStatusCompletion) {
			statusMonitorCompletion = completion
			completion(.success(snapshot))
		}

		func stopStatusMonitoring() {
			statusMonitorCompletion = nil
		}

        func saveConfiguration(
            server: String,
            account: String,
            password: String,
            completion: @escaping SoundConnectActionCompletion
        ) {
            savedAccount = account
            snapshot = SoundConnectRuntimeSnapshot(
                configured: true, running: false, state: "stopped", socksListen: "", traffic: nil
            )
            completion(.success(()))
        }

        func connect(
            verificationRequested: @escaping @MainActor @Sendable () -> Void,
            completion: @escaping SoundConnectActionCompletion
        ) {
            snapshot = SoundConnectRuntimeSnapshot(
                configured: true, running: true, state: "connected",
                socksListen: "127.0.0.1:1081", traffic: nil
            )
            completion(.success(()))
        }

        func submitVerificationCode(_ code: String) -> Bool { !code.isEmpty }

        func disconnect(completion: @escaping SoundConnectActionCompletion) {
            disconnectCalls += 1
            snapshot = SoundConnectRuntimeSnapshot(
                configured: true, running: false, state: "stopped", socksListen: "", traffic: nil
            )
            completion(.success(()))
        }
    }

    func testHelperEnvironmentPreservesRequiredHomeWithRestrictedPath() {
        let environment = soundConnectHelperEnvironment(
            homeDirectory: URL(fileURLWithPath: "/Users/tester"),
            temporaryDirectory: "/private/tmp/tester/"
        )

        XCTAssertEqual(environment["HOME"], "/Users/tester")
        XCTAssertEqual(environment["PATH"], "/usr/bin:/bin:/usr/sbin:/sbin")
        XCTAssertEqual(environment["TMPDIR"], "/private/tmp/tester/")
        XCTAssertEqual(environment.count, 3)
    }

    func testLiveRateFormattingStartsAtKilobytesAndSwitchesAtPointOneMegabyte() {
        XCTAssertEqual(formatRate(0), "0.0 kB/s")
        XCTAssertEqual(formatRate(450), "0.5 kB/s")
        XCTAssertEqual(formatRate(12_345), "12.3 kB/s")
        XCTAssertEqual(formatRate(99_999), "100.0 kB/s")
        XCTAssertEqual(formatRate(100_000), "0.1 MB/s")
        XCTAssertEqual(formatRate(1_250_000), "1.2 MB/s")
    }

    @MainActor
    func testCredentialFailureUsesOneCompactResetRow() {
        let model = DesignModel(scenario: .credentialRejected)
        XCTAssertEqual(model.statusTitle, "Authentication failed")
        XCTAssertEqual(model.statusDetail, "")
        XCTAssertEqual(model.retryDetail, "Gateway rejected the account or password.")
        XCTAssertEqual(model.retryTitle, "Reset")
    }

    @MainActor
	func testLiveModelUsesRuntimeStatusAndDisconnectController() {
        let controller = FakeSoundConnectController()
        let model = DesignModel(controller: controller)

        XCTAssertEqual(model.scenario, .connected)
        XCTAssertEqual(model.socksEndpoint, "127.0.0.1:1081")
        XCTAssertEqual(model.downloadBytes, 34)
        XCTAssertEqual(model.activeConnections, 2)

        model.setServiceEnabled(false)
        XCTAssertEqual(controller.disconnectCalls, 1)
        XCTAssertEqual(model.scenario, .stopped)
        XCTAssertFalse(model.isServiceEnabled)
	}

	@MainActor
	func testLiveRatesUseBackendSampleTimeOnlyWhilePanelIsOpen() {
		let controller = FakeSoundConnectController()
		controller.snapshot = SoundConnectRuntimeSnapshot(
			configured: true,
			running: true,
			state: "connected",
			socksListen: "127.0.0.1:1081",
			traffic: SoundConnectTrafficSnapshot(
				uploadBytes: 1_000,
				downloadBytes: 2_000,
				activeConnections: 1,
				sampledAtUnixMilli: 1_000
			)
		)
		let model = DesignModel(controller: controller)
		model.setTrafficMonitoringActive(true)

		controller.snapshot = SoundConnectRuntimeSnapshot(
			configured: true,
			running: true,
			state: "connected",
			socksListen: "127.0.0.1:1081",
			traffic: SoundConnectTrafficSnapshot(
				uploadBytes: 2_000,
				downloadBytes: 4_000,
				activeConnections: 1,
				sampledAtUnixMilli: 2_000
			)
		)
		controller.statusMonitorCompletion?(.success(controller.snapshot))

		XCTAssertEqual(model.rates.uploadBytesPerSecond, 1_000)
		XCTAssertEqual(model.rates.downloadBytesPerSecond, 2_000)
		XCTAssertEqual(model.uploadRateSamples, [1_000])
		XCTAssertEqual(model.downloadRateSamples, [2_000])

		for second in 3...37 {
			controller.snapshot = SoundConnectRuntimeSnapshot(
				configured: true,
				running: true,
				state: "connected",
				socksListen: "127.0.0.1:1081",
				traffic: SoundConnectTrafficSnapshot(
					uploadBytes: UInt64(second * 1_000),
					downloadBytes: UInt64(second * 2_000),
					activeConnections: 1,
					sampledAtUnixMilli: Int64(second * 1_000)
				)
			)
			controller.statusMonitorCompletion?(.success(controller.snapshot))
		}
		XCTAssertEqual(model.uploadRateSamples.count, 30)
		XCTAssertEqual(model.downloadRateSamples.count, 30)

		model.setTrafficMonitoringActive(false)
		XCTAssertEqual(model.rates.uploadBytesPerSecond, 0)
		XCTAssertEqual(model.rates.downloadBytesPerSecond, 0)
		XCTAssertTrue(model.uploadRateSamples.isEmpty)
		XCTAssertTrue(model.downloadRateSamples.isEmpty)
	}

    @MainActor
    func testLiveSetupSavesThenStartsRealController() {
        let controller = FakeSoundConnectController()
        controller.snapshot = SoundConnectRuntimeSnapshot(
            configured: false, running: false, state: "stopped", socksListen: "", traffic: nil
        )
        let model = DesignModel(controller: controller)
        XCTAssertEqual(model.scenario, .setup)

        model.completeSetup(schoolAccount: "student", vpnPassword: "test-only")

        XCTAssertEqual(controller.savedAccount, "student")
        XCTAssertEqual(model.scenario, .connected)
        XCTAssertTrue(model.isServiceEnabled)
    }

    func testCampusSpeedTestResultDecodesStableSchema() throws {
        let payload = #"{"schema_version":1,"status":"success","started_at":"2026-08-04T00:00:00.123456789Z","ended_at":"2026-08-04T00:00:20.123456789Z","target":"speed.nju.edu.cn","family":"ipv4","route":"direct","server":"speed.nju.edu.cn","download_mbps":50,"upload_mbps":10,"helper_version":"v1.0.13-soundconnect.1"}"#.data(using: .utf8)!
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .iso8601
        let result = try decoder.decode(CampusSpeedTestResult.self, from: payload)
        XCTAssertEqual(result.schemaVersion, 1)
        XCTAssertEqual(result.route, "direct")
        XCTAssertEqual(result.downloadMbps, 50)
        XCTAssertNil(result.failure)
    }

    @MainActor
    func testControllerConsumesProgressAndFinalResultEvents() {
        let defaults = makeIsolatedDefaults()
        let controller = SpeedTestController(loadRuntimeState: false, userDefaults: defaults)
        let progress = #"{"schema_version":1,"type":"measurement_progress","phase":"download","test":"download","mbps":42.5,"route":"direct"}"# + "\n"
        controller.consume(Data(progress.utf8))
        XCTAssertEqual(controller.phase, .measuring)
        XCTAssertEqual(controller.reachabilityState, .reachable)
        XCTAssertEqual(controller.activeMeasurementPhase, "download")
        XCTAssertEqual(controller.downloadMbps, 42.5)
        XCTAssertEqual(controller.downloadSamples, [42.5])
        XCTAssertEqual(controller.route, "direct")

        let completed = #"{"schema_version":1,"type":"result","result":{"schema_version":1,"status":"success","started_at":"2026-08-04T00:00:00Z","ended_at":"2026-08-04T00:00:20Z","target":"speed.nju.edu.cn","family":"ipv4","route":"direct","server":"speed.nju.edu.cn","ping_ms":6,"download_mbps":50,"upload_mbps":10,"helper_version":"v1.0.13-soundconnect.1"}}"# + "\n"
        controller.consume(Data(completed.utf8))
        XCTAssertEqual(controller.phase, .completed)
        XCTAssertEqual(controller.reachabilityState, .reachable)
        XCTAssertNil(controller.activeMeasurementPhase)
        XCTAssertEqual(controller.lastResult?.downloadMbps, 50)
        XCTAssertEqual(controller.uploadMbps, 10)
    }

    @MainActor
    func testControllerRestoresBandwidthSamplesFromUserDefaults() {
        let defaults = makeIsolatedDefaults()
        let firstController = SpeedTestController(loadRuntimeState: false, userDefaults: defaults)
        let progress = """
        {"schema_version":1,"type":"measurement_progress","phase":"download","test":"download","mbps":42.5,"route":"direct"}
        {"schema_version":1,"type":"measurement_progress","phase":"download","test":"download","mbps":47.5,"route":"direct"}
        {"schema_version":1,"type":"measurement_progress","phase":"upload","test":"upload","mbps":7.5,"route":"direct"}
        """ + "\n"
        firstController.consume(Data(progress.utf8))
        let completed = #"{"schema_version":1,"type":"result","result":{"schema_version":1,"status":"success","started_at":"2026-08-04T00:00:00Z","ended_at":"2026-08-04T00:00:20Z","target":"speed.nju.edu.cn","family":"ipv4","route":"direct","server":"speed.nju.edu.cn","ping_ms":6,"download_mbps":50,"upload_mbps":10,"helper_version":"v1.0.13-soundconnect.1"}}"# + "\n"
        firstController.consume(Data(completed.utf8))
        let restoredController = SpeedTestController(loadRuntimeState: true, userDefaults: defaults)

        XCTAssertEqual(restoredController.downloadSamples, [42.5, 47.5])
        XCTAssertEqual(restoredController.uploadSamples, [7.5])
        XCTAssertEqual(restoredController.downloadMbps, 50)
        XCTAssertEqual(restoredController.uploadMbps, 10)
    }

    @MainActor
    func testMissingUploadDoesNotBecomeZero() {
        let controller = SpeedTestController(loadRuntimeState: false, userDefaults: makeIsolatedDefaults())
        let completed = #"{"schema_version":1,"type":"result","result":{"schema_version":1,"status":"success","started_at":"2026-08-04T00:00:00Z","ended_at":"2026-08-04T00:00:20Z","target":"speed.nju.edu.cn","family":"ipv4","route":"direct","server":"speed.nju.edu.cn","ping_ms":6,"download_mbps":50,"helper_version":"v1.0.13-soundconnect.1"}}"# + "\n"

        controller.consume(Data(completed.utf8))

        XCTAssertNil(controller.uploadMbps)
        XCTAssertTrue(controller.uploadSamples.isEmpty)
        XCTAssertEqual(campusSpeedResultText(for: controller), "↓50 Mbps")
    }

    @MainActor
    func testPreviewDownloadStateIsCompactAndRunning() {
        let controller = SpeedTestController(previewState: .download)
        XCTAssertEqual(controller.phase, .measuring)
        XCTAssertEqual(controller.activeMeasurementPhase, "download")
        XCTAssertEqual(controller.downloadMbps, 53)
        XCTAssertTrue(controller.isRunning)
    }

    @MainActor
    func testPreviewIdleStateShowsMedianLatencyHistory() {
        let controller = SpeedTestController(previewState: .idle)
        XCTAssertEqual(controller.phase, .idle)
        XCTAssertEqual(controller.latencySamples.count, 6)
        XCTAssertEqual(controller.latencyMs, 16)
        XCTAssertEqual(controller.route, "direct")
        XCTAssertEqual(controller.downloadSamples.count, 6)
        XCTAssertEqual(controller.uploadSamples.count, 6)
    }

    func testCampusProbeResultDecodesRouteAndLatency() throws {
        let payload = #"{"schema_version":1,"target":"speed.nju.edu.cn","route":"direct","latency_ms":6.25}"#.data(using: .utf8)!
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        let result = try decoder.decode(CampusProbeResult.self, from: payload)
        XCTAssertEqual(result.schemaVersion, 1)
        XCTAssertEqual(result.target, "speed.nju.edu.cn")
        XCTAssertEqual(result.route, "direct")
        XCTAssertEqual(result.latencyMs, 6.25)
    }

    private func makeIsolatedDefaults() -> UserDefaults {
        let suiteName = "SoundConnectUITests.SpeedTest.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suiteName)!
        addTeardownBlock {
            UserDefaults(suiteName: suiteName)?.removePersistentDomain(forName: suiteName)
        }
        return defaults
    }
}

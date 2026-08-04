import XCTest
@testable import soundconnect_ui

final class SpeedTestModelTests: XCTestCase {
    @MainActor
    func testCredentialFailureUsesOneCompactResetRow() {
        let model = DesignModel(scenario: .credentialRejected)
        XCTAssertEqual(model.statusTitle, "Authentication failed")
        XCTAssertEqual(model.statusDetail, "")
        XCTAssertEqual(model.retryDetail, "Gateway rejected the account or password.")
        XCTAssertEqual(model.retryTitle, "Reset")
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
        let controller = SpeedTestController(loadRuntimeState: false)
        let progress = #"{"schema_version":1,"type":"measurement_progress","phase":"download","test":"download","mbps":42.5,"route":"direct"}"# + "\n"
        controller.consume(Data(progress.utf8))
        XCTAssertEqual(controller.phase, .measuring)
        XCTAssertEqual(controller.reachabilityState, .reachable)
        XCTAssertEqual(controller.activeMeasurementPhase, "download")
        XCTAssertEqual(controller.downloadMbps, 42.5)
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
    func testPreviewDownloadStateIsCompactAndRunning() {
        let controller = SpeedTestController(previewState: .download)
        XCTAssertEqual(controller.phase, .measuring)
        XCTAssertEqual(controller.activeMeasurementPhase, "download")
        XCTAssertEqual(controller.downloadMbps, 53)
        XCTAssertTrue(controller.isRunning)
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
}

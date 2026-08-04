import XCTest
@testable import soundconnect_ui

final class SpeedTestModelTests: XCTestCase {
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
        let controller = SpeedTestController()
        let progress = #"{"schema_version":1,"type":"measurement_progress","phase":"download","test":"download","mbps":42.5,"route":"direct"}"# + "\n"
        controller.consume(Data(progress.utf8))
        XCTAssertEqual(controller.phase, .measuring)
        XCTAssertEqual(controller.downloadMbps, 42.5)
        XCTAssertEqual(controller.route, "direct")

        let completed = #"{"schema_version":1,"type":"result","result":{"schema_version":1,"status":"success","started_at":"2026-08-04T00:00:00Z","ended_at":"2026-08-04T00:00:20Z","target":"speed.nju.edu.cn","family":"ipv4","route":"direct","server":"speed.nju.edu.cn","download_mbps":50,"upload_mbps":10,"helper_version":"v1.0.13-soundconnect.1"}}"# + "\n"
        controller.consume(Data(completed.utf8))
        XCTAssertEqual(controller.phase, .completed)
        XCTAssertEqual(controller.lastResult?.downloadMbps, 50)
        XCTAssertEqual(controller.uploadMbps, 10)
    }
}

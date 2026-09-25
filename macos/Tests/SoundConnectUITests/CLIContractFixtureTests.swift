import Foundation
import XCTest
@testable import soundconnect_ui

/// Decodes the CLI output fixtures in `testdata/contract`, which the Go tests
/// in `cmd/soundconnect` regenerate from real command output. A field rename
/// on either side breaks both test suites at once.
final class CLIContractFixtureTests: XCTestCase {
    private static let fixtureDirectory = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent() // SoundConnectUITests
        .deletingLastPathComponent() // Tests
        .deletingLastPathComponent() // macos
        .deletingLastPathComponent() // repository root
        .appendingPathComponent("testdata/contract", isDirectory: true)

    /// Mirrors SoundConnectController's decoder for status and doctor.
    private func controllerDecoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return decoder
    }

    /// SpeedTestController's own decoder for speed-test payloads.
    private func speedTestDecoder() -> JSONDecoder {
        campusSpeedTestDecoder()
    }

    private func fixture(_ name: String) throws -> Data {
        try Data(contentsOf: Self.fixtureDirectory.appendingPathComponent(name))
    }

    private func fixtureLines(_ name: String) throws -> [Data] {
        let text = try XCTUnwrap(String(data: try fixture(name), encoding: .utf8))
        return text.split(separator: "\n").map { Data($0.utf8) }
    }

    func testBackendCatalog() throws {
        // The controller decodes the catalog with a default decoder and
        // explicit coding keys.
        let catalog = try JSONDecoder().decode(SoundConnectBackendCatalog.self, from: fixture("backends.json"))
        XCTAssertEqual(catalog.schemaVersion, 1)
        XCTAssertEqual(catalog.backends.map(\.id), [.easyConnect, .aTrust])
        XCTAssertFalse(catalog.socksListen.isEmpty)
        XCTAssertEqual(catalog.descriptor(for: .easyConnect)?.defaultGateway, "vpn.nju.edu.cn")
        XCTAssertTrue(catalog.descriptor(for: .aTrust)?.authentication.contains(.oauth) ?? false)
    }

    func testStoppedStatus() throws {
        let status = try controllerDecoder().decode(RuntimeStatusPayload.self, from: fixture("status_stopped.json"))
        XCTAssertFalse(status.running)
        XCTAssertEqual(status.state, "stopped")
        XCTAssertNil(status.traffic)
    }

    func testRunningEasyConnectStatus() throws {
        let status = try controllerDecoder().decode(RuntimeStatusPayload.self, from: fixture("status_running.json"))
        XCTAssertTrue(status.running)
        XCTAssertEqual(status.state, "connected")
        XCTAssertEqual(status.socksListen, "127.0.0.1:1080")
        XCTAssertEqual(status.accessEvidence, "available")
        let traffic = try XCTUnwrap(status.traffic)
        XCTAssertEqual(traffic.uploadBytes, 12_345)
        XCTAssertEqual(traffic.downloadBytes, 678_901)
        XCTAssertEqual(traffic.activeConnections, 2)
        XCTAssertNotNil(traffic.sampledAtUnixMilli)
        let snapshot = SoundConnectRuntimeSnapshot(
            configured: true, running: status.running, state: status.state,
            socksListen: status.socksListen ?? "", traffic: nil, profile: status.profile
        )
        XCTAssertEqual(snapshot.backend, .easyConnect)
    }

    func testRunningATrustStatusMapsToATrustBackend() throws {
        let status = try controllerDecoder().decode(RuntimeStatusPayload.self, from: fixture("status_running_atrust.json"))
        let snapshot = SoundConnectRuntimeSnapshot(
            configured: true, running: status.running, state: status.state,
            socksListen: status.socksListen ?? "", traffic: nil, profile: status.profile
        )
        XCTAssertEqual(snapshot.backend, .aTrust)
    }

    func testSpeedTestReadsRuntimeState() throws {
        let running = try speedTestDecoder().decode(RuntimeStatus.self, from: fixture("status_running.json"))
        XCTAssertTrue(running.running)
        XCTAssertEqual(running.state, "connected")
        let stopped = try speedTestDecoder().decode(RuntimeStatus.self, from: fixture("status_stopped.json"))
        XCTAssertFalse(stopped.running)
    }

    func testDoctorReadiness() throws {
        XCTAssertFalse(try controllerDecoder().decode(DoctorPayload.self, from: fixture("doctor_missing.json")).ready)
        XCTAssertTrue(try controllerDecoder().decode(DoctorPayload.self, from: fixture("doctor_ready.json")).ready)
    }

    func testComponentStatus() throws {
        let missing = try speedTestDecoder().decode(ComponentStatus.self, from: fixture("speedtest_component_status_missing.json"))
        XCTAssertFalse(missing.installed)
        XCTAssertTrue(missing.downloadReady)
        XCTAssertGreaterThan(missing.downloadSize, 0)
        let installed = try speedTestDecoder().decode(ComponentStatus.self, from: fixture("speedtest_component_status_installed.json"))
        XCTAssertTrue(installed.installed)
        XCTAssertEqual(installed.installSource, "download")
    }

    func testComponentInstallEvents() throws {
        let events = try fixtureLines("speedtest_component_install_events.ndjson").map {
            try speedTestDecoder().decode(CampusSpeedTestEvent.self, from: $0)
        }
        XCTAssertEqual(events.last?.type, "component_progress")
        XCTAssertEqual(events.last?.phase, "complete")
        XCTAssertEqual(events.last?.progress, 1)
    }

    func testProbeResult() throws {
        let probe = try speedTestDecoder().decode(CampusProbeResult.self, from: fixture("speedtest_probe.json"))
        XCTAssertEqual(probe.schemaVersion, 1)
        XCTAssertEqual(probe.target, "speed.nju.edu.cn")
        XCTAssertEqual(probe.route, "direct")
    }

    func testCampusEventStream() throws {
        let events = try fixtureLines("speedtest_campus_events.ndjson").map {
            try speedTestDecoder().decode(CampusSpeedTestEvent.self, from: $0)
        }
        XCTAssertTrue(events.allSatisfy { $0.schemaVersion == 1 })
        XCTAssertTrue(events.contains { $0.type == "measurement_progress" && $0.test == "download" && $0.mbps == 50 })
        let result = try XCTUnwrap(events.last?.result)
        XCTAssertEqual(events.last?.type, "result")
        XCTAssertEqual(result.status, "success")
        XCTAssertEqual(result.downloadMbps, 50)
        XCTAssertEqual(result.uploadMbps, 10)
    }

    func testLastResult() throws {
        let result = try speedTestDecoder().decode(CampusSpeedTestResult.self, from: fixture("speedtest_last.json"))
        XCTAssertEqual(result.status, "success")
        XCTAssertEqual(result.route, "direct")
        XCTAssertEqual(result.pingMs, 6)
    }
}

import Foundation

enum CampusSpeedTestPhase: Equatable {
    case idle
    case componentRequired
    case downloading
    case probing
    case measuring
    case connectionRequired
    case completed
    case failed
}

enum CampusReachabilityState: Equatable {
    case unknown
    case probing
    case reachable
    case slow
    case failed
}

enum CampusSpeedTestPreviewState: String, CaseIterable, Identifiable {
    case idle
    case probing
    case download
    case upload
    case completed
    case failed

    var id: String { rawValue }

    var title: String {
        switch self {
        case .idle: return "Idle"
        case .probing: return "Probing"
        case .download: return "Download"
        case .upload: return "Upload"
        case .completed: return "Complete"
        case .failed: return "Failed"
        }
    }
}

struct CampusSpeedTestFailure: Codable, Equatable {
    let stage: String
    let code: String
    let message: String
}

struct CampusSpeedTestResult: Codable, Equatable {
    let schemaVersion: Int
    let status: String
    let startedAt: Date
    let endedAt: Date
    let target: String
    let family: String
    let route: String
    let server: String?
    let pingMs: Double?
    let jitterMs: Double?
    let downloadMbps: Double?
    let uploadMbps: Double?
    let helperVersion: String?
    let failure: CampusSpeedTestFailure?
}

struct CampusSpeedTestHistory: Codable, Equatable {
    let endedAt: Date?
    let route: String?
    let latencyMs: Double?
    let downloadMbps: Double?
    let uploadMbps: Double?
    let latencySamples: [Double]
    let downloadSamples: [Double]
    let uploadSamples: [Double]
}

struct CampusSpeedTestEvent: Decodable {
    let schemaVersion: Int
    let type: String
    let phase: String?
    let test: String?
    let mbps: Double?
    let progress: Double?
    let message: String?
    let route: String?
    let result: CampusSpeedTestResult?
    let error: EventError?

    struct EventError: Decodable {
        let code: String
        let message: String
    }
}

struct CampusProbeResult: Decodable, Equatable {
    let schemaVersion: Int
    let target: String
    let route: String
    let latencyMs: Double
}

/// The decoder for speed-test JSON from the CLI and for the saved history.
func campusSpeedTestDecoder() -> JSONDecoder {
    let decoder = JSONDecoder()
    decoder.keyDecodingStrategy = .convertFromSnakeCase
    decoder.dateDecodingStrategy = .custom { decoder in
        let container = try decoder.singleValueContainer()
        let text = try container.decode(String.self)
        guard let date = parseRFC3339Timestamp(text) else {
            throw DecodingError.dataCorruptedError(
                in: container, debugDescription: "Expected an RFC 3339 timestamp, got \(text)."
            )
        }
        return date
    }
    return decoder
}

/// Parses the RFC 3339 timestamps Go writes for time.Time, which carry up to
/// nine fractional-second digits. The built-in .iso8601 strategy rejects any
/// fraction on macOS 15 and earlier, so the fraction is split off, the rest
/// is parsed as a plain internet date-time, and the fraction is added back.
func parseRFC3339Timestamp(_ text: String) -> Date? {
    var whole = Substring(text)
    var fraction: TimeInterval = 0
    if let dot = text.firstIndex(of: "."),
       let zone = text[dot...].firstIndex(where: { $0 == "Z" || $0 == "z" || $0 == "+" || $0 == "-" })
    {
        let digits = text[text.index(after: dot)..<zone]
        guard !digits.isEmpty, digits.allSatisfy(\.isASCII), digits.allSatisfy(\.isNumber),
              let value = TimeInterval("0." + digits)
        else { return nil }
        fraction = value
        whole = text[..<dot] + text[zone...]
    }
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime]
    return formatter.date(from: String(whole))?.addingTimeInterval(fraction)
}

// Internal for the shared CLI contract fixture tests.
struct ComponentStatus: Decodable {
    let installed: Bool
    let helperVersion: String
    let downloadSize: Int64
    let downloadReady: Bool
    let installSource: String?
}

struct RuntimeStatus: Decodable {
    let running: Bool
    let state: String
}

func njuConnectHelperEnvironment(
    homeDirectory: URL = FileManager.default.homeDirectoryForCurrentUser,
    temporaryDirectory: String = NSTemporaryDirectory(),
    configDirectory: String? = ProcessInfo.processInfo.environment["NJU_CONNECT_CONFIG_DIR"]
) -> [String: String] {
    var environment = [
        "HOME": homeDirectory.path,
        "PATH": "/usr/bin:/bin:/usr/sbin:/sbin",
        "TMPDIR": temporaryDirectory,
    ]
    // An isolated profile directory must reach the helper so it reads the
    // same runtime status socket as the app's own CLI calls.
    if let configDirectory, !configDirectory.isEmpty {
        environment["NJU_CONNECT_CONFIG_DIR"] = configDirectory
    }
    return environment
}

@MainActor
final class SpeedTestController: ObservableObject, @unchecked Sendable {
    @Published private(set) var phase: CampusSpeedTestPhase = .idle
    @Published private(set) var message = "speed.nju.edu.cn"
    @Published private(set) var componentVersion = ""
    @Published private(set) var componentSize: Int64 = 0
    @Published private(set) var componentInstallSource = ""
    @Published private(set) var componentProgress: Double = 0
    @Published private(set) var downloadMbps: Double?
    @Published private(set) var uploadMbps: Double?
    @Published private(set) var downloadSamples: [Double] = []
    @Published private(set) var uploadSamples: [Double] = []
    @Published private(set) var lastResult: CampusSpeedTestResult?
    @Published private(set) var route: String?
    @Published private(set) var reachabilityState: CampusReachabilityState = .unknown
    @Published private(set) var latencyMs: Double?
    @Published private(set) var latencySamples: [Double] = []
    @Published private(set) var isLatencySampling = false
    @Published private(set) var activeMeasurementPhase: String?
    @Published private(set) var canRetryAfterConnection = false

    private var process: Process?
    private var cancelRequested = false
    private var pollTask: Task<Void, Never>?
    private var latencySamplingTask: Task<Void, Never>?
    private var lastLatencySamplingAt: Date?
    /// `nju-connect` while the VPN is connected, so a dead tunnel reads as
    /// unreachable instead of silently falling back to the direct path.
    private var probeRoute = "auto"
    private var outputBuffer = Data()
    private let decoder: JSONDecoder
    private let historyEncoder: JSONEncoder
    private let historyDefaults: UserDefaults
    private let isPreviewMode: Bool
    private var restoredHistoryEndedAt: Date?

    private static let historyDefaultsKey = "campus-speed-test.history.v1"

    init(
        loadRuntimeState: Bool = true,
        previewState: CampusSpeedTestPreviewState? = nil,
        userDefaults: UserDefaults = .standard
    ) {
        isPreviewMode = previewState != nil
        historyDefaults = userDefaults
        decoder = campusSpeedTestDecoder()
        historyEncoder = JSONEncoder()
        historyEncoder.dateEncodingStrategy = .iso8601
        if let previewState {
            applyPreviewState(previewState)
        } else if loadRuntimeState {
            restorePersistedHistory()
            refreshInitialState()
        }
    }

    var isRunning: Bool {
        phase == .downloading || phase == .probing || phase == .measuring
    }

    func applyPreviewState(_ state: CampusSpeedTestPreviewState) {
        resetTransientState()
        latencySamples = [19, 17, 16, 15, 15, 16]
        latencyMs = 16
        route = "direct"
        downloadSamples = [41, 45, 49, 52, 50, 53]
        uploadSamples = [8, 10, 11, 12, 11, 12]
        downloadMbps = 53
        uploadMbps = 12
        switch state {
        case .idle:
            phase = .idle
            reachabilityState = .reachable
            message = "Ready to test over the direct campus route."
        case .probing:
            phase = .probing
            reachabilityState = .probing
            message = "Probing direct campus route…"
        case .download:
            phase = .measuring
            reachabilityState = .reachable
            activeMeasurementPhase = "download"
            downloadMbps = 53
            message = "Measuring download speed…"
        case .upload:
            phase = .measuring
            reachabilityState = .reachable
            activeMeasurementPhase = "upload"
            downloadMbps = 53
            uploadMbps = 12
            message = "Measuring upload speed…"
        case .completed:
            phase = .completed
            reachabilityState = .reachable
            downloadMbps = 53
            uploadMbps = 12
            message = "Campus speed test complete."
        case .failed:
            phase = .failed
            reachabilityState = .failed
            latencyMs = nil
            route = nil
            message = "Campus speed test failed."
        }
    }

    func start() {
        guard !isRunning else { return }
        resetTransientState()
        phase = .probing
        reachabilityState = .probing
        message = "Preparing campus speed test…"
        inspectComponent { [weak self] status in
            guard let self else { return }
            if status.installed {
                self.runMeasurement()
            } else {
                self.componentVersion = status.helperVersion
                self.componentSize = status.downloadSize
                self.componentInstallSource = status.installSource ?? ""
                self.phase = .componentRequired
                self.message = status.downloadReady
                    ? "The external speed-test helper is ready."
                    : "Install librespeed-cli-nju-connect with Homebrew first."
            }
        }
    }

    func confirmComponentDownload() {
        guard phase == .componentRequired else { return }
        phase = .downloading
        message = "Installing speed-test helper…"
        launch(arguments: ["speedtest", "component", "install", "--yes", "--json-events"]) { [weak self] code in
            guard let self else { return }
            if code == 0 {
                self.runMeasurement()
            } else if self.phase == .downloading {
                self.phase = .failed
                self.message = "Speed-test component installation failed."
            }
        }
    }

    func cancel() {
        cancelRequested = true
        process?.interrupt()
        pollTask?.cancel()
        pollTask = nil
        phase = .idle
        reachabilityState = .unknown
        activeMeasurementPhase = nil
        message = "Speed test cancelled."
    }

    func retryAfterConnection() {
        guard phase == .connectionRequired, canRetryAfterConnection else { return }
        runMeasurement()
    }

    func beginLatencySamplingIfNeeded(maxAge: TimeInterval = 10) {
        guard let lastLatencySamplingAt else {
            beginLatencySampling()
            return
        }
        guard Date().timeIntervalSince(lastLatencySamplingAt) >= maxAge else { return }
        beginLatencySampling()
    }

    func beginLatencySampling(force: Bool = false) {
        guard !isPreviewMode, !isRunning, !isLatencySampling else { return }
        if !force, let lastLatencySamplingAt,
           Date().timeIntervalSince(lastLatencySamplingAt) < 10
        {
            return
        }

        isLatencySampling = true
        if latencySamples.isEmpty || reachabilityState == .unknown {
            reachabilityState = .probing
        }
        let route = probeRoute
        latencySamplingTask = Task { [weak self] in
            guard let self else { return }
            var roundSamples: [Double] = []
            for sampleIndex in 0..<3 {
                guard !Task.isCancelled else { return }
                let (result, code) = await self.captureLatencyProbe(route: route)
                guard !Task.isCancelled else { return }
                if let result,
                   result.schemaVersion == 1,
                   result.target == "speed.nju.edu.cn"
                {
                    if self.route != nil, self.route != result.route {
                        self.latencySamples.removeAll(keepingCapacity: true)
                    }
                    self.route = result.route
                    self.latencySamples.append(result.latencyMs)
                    if self.latencySamples.count > 10 {
                        self.latencySamples.removeFirst(self.latencySamples.count - 10)
                    }
                    roundSamples.append(result.latencyMs)
                    self.latencyMs = self.medianLatency
                    self.reachabilityState = self.classifiedReachability(for: median(of: roundSamples))
                    self.message = "Campus speed-test route reachable."
                } else if code == 127, self.latencySamples.isEmpty {
                    self.reachabilityState = .unknown
                }
                if sampleIndex < 2 {
                    try? await Task.sleep(for: .milliseconds(400))
                }
            }
            if roundSamples.isEmpty {
                self.reachabilityState = .failed
                self.latencyMs = nil
                self.route = nil
                self.message = route == "nju-connect"
                    ? "speed.nju.edu.cn is unreachable through nju-connect."
                    : "Campus speed-test route unavailable."
            }
            self.persistHistory()
            self.lastLatencySamplingAt = Date()
            self.isLatencySampling = false
            self.latencySamplingTask = nil
        }
    }

    /// Follows the VPN: while it is connected, probes go through the tunnel.
    /// A change discards the current verdict and probes again at once.
    func setVPNConnected(_ connected: Bool) {
        let route = connected ? "nju-connect" : "auto"
        guard route != probeRoute else { return }
        probeRoute = route
        refreshReachability()
    }

    /// Drops the current verdict and probes again, restarting any round
    /// already in flight.
    func refreshReachability() {
        guard !isPreviewMode, !isRunning else { return }
        latencySamplingTask?.cancel()
        latencySamplingTask = nil
        isLatencySampling = false
        reachabilityState = .probing
        beginLatencySampling(force: true)
    }

    private func captureLatencyProbe(route: String) async -> (CampusProbeResult?, Int32) {
        await withCheckedContinuation { continuation in
            runCapture(arguments: ["speedtest", "probe", "--route", route, "--json"]) { [weak self] data, code in
                guard let self else {
                    continuation.resume(returning: (nil, code))
                    return
                }
                continuation.resume(returning: (try? self.decoder.decode(CampusProbeResult.self, from: data), code))
            }
        }
    }

    private func refreshInitialState() {
        runCapture(arguments: ["speedtest", "last", "--json"]) { [weak self] data, code in
            guard let self, code == 0,
                  let result = try? self.decoder.decode(CampusSpeedTestResult.self, from: data)
            else { return }
            self.lastResult = result
            self.route = result.route
            self.latencyMs = result.pingMs ?? self.latencyMs
            self.downloadMbps = result.downloadMbps ?? self.downloadMbps
            self.uploadMbps = result.uploadMbps ?? self.uploadMbps

            if result.status == "success" {
                self.phase = .completed
                self.message = "Campus speed test complete."

                if self.restoredHistoryEndedAt != result.endedAt {
                    self.downloadSamples.removeAll(keepingCapacity: true)
                    self.uploadSamples.removeAll(keepingCapacity: true)
                    self.appendMeasurement(result.downloadMbps, to: &self.downloadSamples)
                    self.appendMeasurement(result.uploadMbps, to: &self.uploadSamples)
                }
                self.persistHistory(endedAt: result.endedAt)
            } else {
                self.phase = .failed
                self.message = result.failure?.message
                    ?? "Campus speed test failed."
            }
        }
    }

    private func inspectComponent(completion: @escaping @MainActor (ComponentStatus) -> Void) {
        runCapture(arguments: ["speedtest", "component", "status", "--json"]) { [weak self] data, _ in
            guard let self,
                  let status = try? self.decoder.decode(ComponentStatus.self, from: data)
            else {
                self?.phase = .failed
                self?.message = "Unable to read component status."
                return
            }
            completion(status)
        }
    }

    private func runMeasurement() {
        pollTask?.cancel()
        pollTask = nil
        canRetryAfterConnection = false
        phase = .probing
        reachabilityState = .probing
        activeMeasurementPhase = nil
        message = "Probing campus speed-test route…"
        launch(arguments: ["speedtest", "campus", "--route", "auto", "--json-events"]) { [weak self] code in
            guard let self else { return }
            if code != 0, self.phase != .connectionRequired, self.phase != .completed {
                self.phase = .failed
                self.message = "Campus speed test did not complete."
            }
        }
    }

    private func launch(arguments: [String], completion: @escaping @MainActor (Int32) -> Void) {
        guard process == nil else { return }
        guard let executable = helperExecutable() else {
            phase = .failed
            message = missingHelperMessage
            return
        }
        let child = Process()
        let stdout = Pipe()
        let stderr = Pipe()
        child.executableURL = executable
        child.arguments = arguments
        child.standardOutput = stdout
        child.standardError = stderr
        child.environment = njuConnectHelperEnvironment()
        outputBuffer.removeAll(keepingCapacity: true)
        cancelRequested = false
        stdout.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else { return }
            Task { @MainActor in self?.consume(data) }
        }
        child.terminationHandler = { [weak self] finished in
            stdout.fileHandleForReading.readabilityHandler = nil
            Task { @MainActor in
                guard let self else { return }
                self.consume(Data("\n".utf8))
                self.process = nil
                if self.cancelRequested {
                    self.cancelRequested = false
                    self.phase = .idle
                    self.message = "Speed test cancelled."
                    return
                }
                completion(finished.terminationStatus)
            }
        }
        do {
            try child.run()
            process = child
        } catch {
            stdout.fileHandleForReading.readabilityHandler = nil
            phase = .failed
            message = "Unable to start nju-connect helper."
        }
    }

    func consume(_ data: Data) {
        outputBuffer.append(data)
        while let newline = outputBuffer.firstIndex(of: 0x0A) {
            let line = outputBuffer[..<newline]
            outputBuffer.removeSubrange(...newline)
            guard !line.isEmpty,
                  let event = try? decoder.decode(CampusSpeedTestEvent.self, from: Data(line)),
                  event.schemaVersion == 1
            else { continue }
            apply(event)
        }
    }

    func apply(_ event: CampusSpeedTestEvent) {
        switch event.type {
        case "component_progress":
            phase = .downloading
            componentProgress = event.progress ?? componentProgress
            message = event.phase == "installing"
                ? "Installing speed-test component…"
                : "Downloading speed-test component…"
        case "measurement_progress":
            route = event.route ?? route
            switch event.phase {
            case "probing", "route_selected":
                phase = .probing
                reachabilityState = .probing
                activeMeasurementPhase = nil
                message = event.route == "nju-connect"
                    ? "Probing nju-connect route…"
                    : "Probing direct campus route…"
            case "download":
                phase = .measuring
                reachabilityState = .reachable
                activeMeasurementPhase = "download"
                downloadMbps = event.mbps
                appendMeasurement(event.mbps, to: &downloadSamples)
                message = "Measuring download speed…"
            case "upload":
                phase = .measuring
                reachabilityState = .reachable
                activeMeasurementPhase = "upload"
                uploadMbps = event.mbps
                appendMeasurement(event.mbps, to: &uploadSamples)
                message = "Measuring upload speed…"
            default:
                phase = .measuring
            }
        case "result":
            guard let result = event.result else { return }
            lastResult = result
            route = result.route
            downloadMbps = result.downloadMbps
            uploadMbps = result.uploadMbps
            if downloadSamples.isEmpty { appendMeasurement(result.downloadMbps, to: &downloadSamples) }
            if uploadSamples.isEmpty { appendMeasurement(result.uploadMbps, to: &uploadSamples) }
            phase = result.status == "success" ? .completed : .failed
            reachabilityState = result.status == "success" ? classifiedReachability(for: result.pingMs) : .failed
            latencyMs = result.pingMs
            activeMeasurementPhase = nil
            message = result.status == "success"
                ? "Campus speed test complete."
                : (result.failure?.message ?? "Campus speed test failed.")
            if result.status == "success" {
                persistHistory(endedAt: result.endedAt)
            }
        case "error":
            reachabilityState = .failed
            activeMeasurementPhase = nil
            if event.error?.code == "nju_connect_required" {
                phase = .connectionRequired
                message = "Direct route unavailable. Connect nju-connect to retry."
                beginConnectionPolling()
            } else {
                phase = .failed
                message = event.error?.message ?? "Campus speed test failed."
            }
        default:
            break
        }
    }

    private func beginConnectionPolling() {
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                let connected = await self.readConnectedStatus()
                if connected {
                    self.canRetryAfterConnection = true
                    self.message = "nju-connect is connected. Ready to retry."
                    return
                }
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }

    private func readConnectedStatus() async -> Bool {
        await withCheckedContinuation { continuation in
            runCapture(arguments: ["status", "--json"]) { [weak self] data, _ in
                guard let self,
                      let status = try? self.decoder.decode(RuntimeStatus.self, from: data)
                else {
                    continuation.resume(returning: false)
                    return
                }
                continuation.resume(returning: status.running && status.state == "connected")
            }
        }
    }

    private func runCapture(arguments: [String], completion: @escaping @MainActor (Data, Int32) -> Void) {
        guard let executable = helperExecutable() else {
            completion(Data(), 127)
            return
        }
        let child = Process()
        let output = Pipe()
        child.executableURL = executable
        child.arguments = arguments
        child.standardOutput = output
        child.standardError = Pipe()
        child.environment = njuConnectHelperEnvironment()
        child.terminationHandler = { finished in
            let data = output.fileHandleForReading.readDataToEndOfFile()
            Task { @MainActor in completion(data, finished.terminationStatus) }
        }
        do {
            try child.run()
        } catch {
            completion(Data(), 127)
        }
    }

    private func helperExecutable() -> URL? {
        let bundled = Bundle.main.bundleURL
            .appendingPathComponent("Contents/Helpers/nju-connect", isDirectory: false)
        if FileManager.default.isExecutableFile(atPath: bundled.path) {
            return bundled
        }
        if let override = ProcessInfo.processInfo.environment["NJU_CONNECT_HELPER"],
           FileManager.default.isExecutableFile(atPath: override)
        {
            return URL(fileURLWithPath: override)
        }
        return nil
    }

    private func restorePersistedHistory() {
        guard let data = historyDefaults.data(forKey: Self.historyDefaultsKey),
              let history = try? decoder.decode(CampusSpeedTestHistory.self, from: data)
        else {
            return
        }

        restoredHistoryEndedAt = history.endedAt
        route = history.route
        latencyMs = history.latencyMs
        downloadMbps = history.downloadMbps
        uploadMbps = history.uploadMbps
        latencySamples = validatedSamples(history.latencySamples, maximumCount: 10)
        downloadSamples = validatedSamples(history.downloadSamples, maximumCount: 30)
        uploadSamples = validatedSamples(history.uploadSamples, maximumCount: 30)

        if history.endedAt != nil || downloadMbps != nil || uploadMbps != nil {
            phase = .completed
            message = "Previous campus speed test restored."
        }
    }

    private func persistHistory(endedAt: Date? = nil) {
        let history = CampusSpeedTestHistory(
            endedAt: endedAt ?? restoredHistoryEndedAt,
            route: route,
            latencyMs: latencyMs,
            downloadMbps: downloadMbps,
            uploadMbps: uploadMbps,
            latencySamples: validatedSamples(latencySamples, maximumCount: 10),
            downloadSamples: validatedSamples(downloadSamples, maximumCount: 30),
            uploadSamples: validatedSamples(uploadSamples, maximumCount: 30)
        )
        guard history.endedAt != nil
                || !history.latencySamples.isEmpty
                || !history.downloadSamples.isEmpty
                || !history.uploadSamples.isEmpty
        else {
            return
        }
        guard let data = try? historyEncoder.encode(history) else { return }
        historyDefaults.set(data, forKey: Self.historyDefaultsKey)
        restoredHistoryEndedAt = history.endedAt
    }

    private func validatedSamples(_ samples: [Double], maximumCount: Int) -> [Double] {
        Array(
            samples
                .filter { $0.isFinite && $0 >= 0 }
                .suffix(maximumCount)
        )
    }

    private func resetTransientState() {
        latencySamplingTask?.cancel()
        latencySamplingTask = nil
        isLatencySampling = false
        pollTask?.cancel()
        pollTask = nil
        canRetryAfterConnection = false
        componentProgress = 0
        downloadMbps = nil
        uploadMbps = nil
        downloadSamples.removeAll(keepingCapacity: true)
        uploadSamples.removeAll(keepingCapacity: true)
        route = nil
        activeMeasurementPhase = nil
    }

    private var medianLatency: Double? {
        median(of: latencySamples)
    }

    private func appendMeasurement(_ value: Double?, to samples: inout [Double]) {
        guard let value, value.isFinite, value >= 0 else { return }
        samples.append(value)
        if samples.count > 30 {
            samples.removeFirst(samples.count - 30)
        }
    }

    /// A probe times one cold HTTP request: connection setup plus the
    /// server's reply, which alone takes about 250 ms on a healthy path
    /// (direct or through the tunnel). Three times that is slow.
    private static let slowLatencyMs: Double = 750

    private func classifiedReachability(for latencyMs: Double?) -> CampusReachabilityState {
        guard let latencyMs else { return .reachable }
        return latencyMs >= Self.slowLatencyMs ? .slow : .reachable
    }
}

func median(of samples: [Double]) -> Double? {
    guard !samples.isEmpty else { return nil }
    let sorted = samples.sorted()
    let middle = sorted.count / 2
    if sorted.count.isMultiple(of: 2) {
        return (sorted[middle - 1] + sorted[middle]) / 2
    }
    return sorted[middle]
}

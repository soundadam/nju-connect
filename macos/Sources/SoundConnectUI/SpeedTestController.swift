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
        case .idle: return uiText("Idle", "待测速")
        case .probing: return uiText("Probing", "探测中")
        case .download: return uiText("Download", "下载中")
        case .upload: return uiText("Upload", "上传中")
        case .completed: return uiText("Complete", "已完成")
        case .failed: return uiText("Failed", "失败")
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

private struct ComponentStatus: Decodable {
    let installed: Bool
    let helperVersion: String
    let downloadSize: Int64
    let downloadReady: Bool
    let installSource: String?
}

private struct RuntimeStatus: Decodable {
    let running: Bool
    let state: String
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
    private var outputBuffer = Data()
    private let decoder: JSONDecoder
    private let isPreviewMode: Bool

    init(loadRuntimeState: Bool = true, previewState: CampusSpeedTestPreviewState? = nil) {
        isPreviewMode = previewState != nil
        decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .iso8601
        if let previewState {
            applyPreviewState(previewState)
        } else if loadRuntimeState {
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
        switch state {
        case .idle:
            phase = .idle
            reachabilityState = .reachable
            message = uiText("Ready to test over the direct campus route.", "校园网直连可测速")
        case .probing:
            phase = .probing
            reachabilityState = .probing
            message = uiText("Probing direct campus route", "正在验证校内直连路径")
        case .download:
            phase = .measuring
            reachabilityState = .reachable
            activeMeasurementPhase = "download"
            downloadMbps = 53
            message = uiText("Measuring download speed", "正在测量下载速度")
        case .upload:
            phase = .measuring
            reachabilityState = .reachable
            activeMeasurementPhase = "upload"
            downloadMbps = 53
            uploadMbps = 12
            message = uiText("Measuring upload speed", "正在测量上传速度")
        case .completed:
            phase = .completed
            reachabilityState = .reachable
            downloadMbps = 53
            uploadMbps = 12
            message = uiText("Campus speed test complete.", "校园测速完成")
        case .failed:
            phase = .failed
            reachabilityState = .failed
            latencyMs = nil
            route = nil
            message = uiText("Campus speed test failed.", "校园测速失败")
        }
    }

    func start() {
        guard !isRunning else { return }
        resetTransientState()
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
                    ? uiText("The external speed-test helper is ready.", "外部测速依赖已就绪")
                    : uiText("Install librespeed-cli-soundconnect with Homebrew first.", "请先通过 Homebrew 安装测速依赖")
            }
        }
    }

    func confirmComponentDownload() {
        guard phase == .componentRequired else { return }
        phase = .downloading
        message = uiText("Installing speed-test helper", "正在安装校园测速依赖")
        launch(arguments: ["speedtest", "component", "install", "--yes", "--json-events"]) { [weak self] code in
            guard let self else { return }
            if code == 0 {
                self.runMeasurement()
            } else if self.phase == .downloading {
                self.phase = .failed
                self.message = uiText("Speed-test component installation failed.", "测速组件安装失败")
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
        message = uiText("Speed test cancelled.", "校园测速已取消")
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
        if latencySamples.isEmpty {
            reachabilityState = .probing
        }
        latencySamplingTask = Task { [weak self] in
            guard let self else { return }
            var successfulSamples = 0
            for sampleIndex in 0..<3 {
                guard !Task.isCancelled else { return }
                let (result, code) = await self.captureLatencyProbe()
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
                    self.latencyMs = self.medianLatency
                    self.reachabilityState = self.classifiedReachability(for: self.latencyMs)
                    self.message = uiText("Campus speed-test route reachable.", "校园测速线路可达")
                    successfulSamples += 1
                } else if code == 127, self.latencySamples.isEmpty {
                    self.reachabilityState = .unknown
                }
                if sampleIndex < 2 {
                    try? await Task.sleep(for: .milliseconds(400))
                }
            }
            if successfulSamples == 0 {
                self.reachabilityState = .failed
                self.latencyMs = nil
                self.route = nil
                self.message = uiText("Campus speed-test route unavailable.", "校园测速线路不可达")
            }
            self.lastLatencySamplingAt = Date()
            self.isLatencySampling = false
            self.latencySamplingTask = nil
        }
    }

    func refreshReachability() {
        beginLatencySampling(force: true)
    }

    private func captureLatencyProbe() async -> (CampusProbeResult?, Int32) {
        await withCheckedContinuation { continuation in
            runCapture(arguments: ["speedtest", "probe", "--route", "auto", "--json"]) { [weak self] data, code in
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
        }
    }

    private func inspectComponent(completion: @escaping @MainActor (ComponentStatus) -> Void) {
        runCapture(arguments: ["speedtest", "component", "status", "--json"]) { [weak self] data, _ in
            guard let self,
                  let status = try? self.decoder.decode(ComponentStatus.self, from: data)
            else {
                self?.phase = .failed
                self?.message = uiText("Unable to read component status.", "无法读取测速组件状态")
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
        message = uiText("Probing campus speed-test route", "正在探测校园测速线路")
        launch(arguments: ["speedtest", "campus", "--route", "auto", "--json-events"]) { [weak self] code in
            guard let self else { return }
            if code != 0, self.phase != .connectionRequired, self.phase != .completed {
                self.phase = .failed
                self.message = uiText("Campus speed test did not complete.", "校园测速未完成")
            }
        }
    }

    private func launch(arguments: [String], completion: @escaping @MainActor (Int32) -> Void) {
        guard process == nil else { return }
        guard let executable = helperExecutable() else {
            phase = .failed
            message = uiText("soundconnect helper not found.", "找不到 soundconnect 后端")
            return
        }
        let child = Process()
        let stdout = Pipe()
        let stderr = Pipe()
        child.executableURL = executable
        child.arguments = arguments
        child.standardOutput = stdout
        child.standardError = stderr
        child.environment = ["PATH": "/usr/bin:/bin:/usr/sbin:/sbin"]
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
                    self.message = uiText("Speed test cancelled.", "校园测速已取消")
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
            message = uiText("Unable to start soundconnect helper.", "无法启动 soundconnect 后端")
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
                ? uiText("Installing speed-test component", "正在安装校园测速组件")
                : uiText("Downloading speed-test component", "正在下载校园测速组件")
        case "measurement_progress":
            route = event.route ?? route
            switch event.phase {
            case "probing", "route_selected":
                phase = .probing
                reachabilityState = .probing
                activeMeasurementPhase = nil
                message = event.route == "soundconnect"
                    ? uiText("Probing soundconnect route", "正在验证 soundconnect 路径")
                    : uiText("Probing direct campus route", "正在验证校内直连路径")
            case "download":
                phase = .measuring
                reachabilityState = .reachable
                activeMeasurementPhase = "download"
                downloadMbps = event.mbps
                message = uiText("Measuring download speed", "正在测量下载速度")
            case "upload":
                phase = .measuring
                reachabilityState = .reachable
                activeMeasurementPhase = "upload"
                uploadMbps = event.mbps
                message = uiText("Measuring upload speed", "正在测量上传速度")
            default:
                phase = .measuring
            }
        case "result":
            guard let result = event.result else { return }
            lastResult = result
            route = result.route
            downloadMbps = result.downloadMbps
            uploadMbps = result.uploadMbps
            phase = result.status == "success" ? .completed : .failed
            reachabilityState = result.status == "success" ? classifiedReachability(for: result.pingMs) : .failed
            latencyMs = result.pingMs
            activeMeasurementPhase = nil
            message = result.status == "success"
                ? uiText("Campus speed test complete.", "校园测速完成")
                : (result.failure?.message ?? uiText("Campus speed test failed.", "校园测速失败"))
        case "error":
            reachabilityState = .failed
            activeMeasurementPhase = nil
            if event.error?.code == "soundconnect_required" {
                phase = .connectionRequired
                message = uiText(
                    "Direct route unavailable. Connect soundconnect to retry.",
                    "校内测速服务直连不可达，请先连接 soundconnect"
                )
                beginConnectionPolling()
            } else {
                phase = .failed
                message = event.error?.message ?? uiText("Campus speed test failed.", "校园测速失败")
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
                    self.message = uiText("soundconnect connected. Ready to retry.", "soundconnect 已连接，可以重新测速")
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
        child.environment = ["PATH": "/usr/bin:/bin:/usr/sbin:/sbin"]
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
            .appendingPathComponent("Contents/Helpers/soundconnect", isDirectory: false)
        if FileManager.default.isExecutableFile(atPath: bundled.path) {
            return bundled
        }
        if let override = ProcessInfo.processInfo.environment["SOUNDCONNECT_HELPER"],
           FileManager.default.isExecutableFile(atPath: override)
        {
            return URL(fileURLWithPath: override)
        }
        return nil
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
        route = nil
        activeMeasurementPhase = nil
    }

    private var medianLatency: Double? {
        guard !latencySamples.isEmpty else { return nil }
        let sorted = latencySamples.sorted()
        let middle = sorted.count / 2
        if sorted.count.isMultiple(of: 2) {
            return (sorted[middle - 1] + sorted[middle]) / 2
        }
        return sorted[middle]
    }

    private func classifiedReachability(for latencyMs: Double?) -> CampusReachabilityState {
        guard let latencyMs else { return .reachable }
        return latencyMs >= 200 ? .slow : .reachable
    }
}

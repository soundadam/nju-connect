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
    let pingMS: Double?
    let jitterMS: Double?
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

private struct ComponentStatus: Decodable {
    let installed: Bool
    let helperVersion: String
    let downloadSize: Int64
    let downloadReady: Bool
}

private struct RuntimeStatus: Decodable {
    let running: Bool
    let state: String
}

@MainActor
final class SpeedTestController: ObservableObject, @unchecked Sendable {
    @Published private(set) var phase: CampusSpeedTestPhase = .idle
    @Published private(set) var message = "测量到南京大学校内测速服务的 IPv4 路径"
    @Published private(set) var componentVersion = ""
    @Published private(set) var componentSize: Int64 = 0
    @Published private(set) var componentProgress: Double = 0
    @Published private(set) var downloadMbps: Double?
    @Published private(set) var uploadMbps: Double?
    @Published private(set) var lastResult: CampusSpeedTestResult?
    @Published private(set) var route: String?
    @Published private(set) var canRetryAfterConnection = false

    private var process: Process?
    private var cancelRequested = false
    private var pollTask: Task<Void, Never>?
    private var outputBuffer = Data()
    private let decoder: JSONDecoder

    init() {
        decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .iso8601
        refreshInitialState()
    }

    var isRunning: Bool {
        phase == .downloading || phase == .probing || phase == .measuring
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
                self.phase = .componentRequired
                self.message = status.downloadReady
                    ? "首次测速需要下载可选组件"
                    : "测速组件尚未发布，当前无法下载"
            }
        }
    }

    func confirmComponentDownload() {
        guard phase == .componentRequired else { return }
        phase = .downloading
        message = "正在下载校园测速组件"
        launch(arguments: ["speedtest", "component", "install", "--yes", "--json-events"]) { [weak self] code in
            guard let self else { return }
            if code == 0 {
                self.runMeasurement()
            } else if self.phase == .downloading {
                self.phase = .failed
                self.message = "测速组件安装失败"
            }
        }
    }

    func cancel() {
        cancelRequested = true
        process?.interrupt()
        pollTask?.cancel()
        pollTask = nil
        phase = .idle
        message = "校园测速已取消"
    }

    func retryAfterConnection() {
        guard phase == .connectionRequired, canRetryAfterConnection else { return }
        runMeasurement()
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
                self?.message = "无法读取测速组件状态"
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
        message = "正在探测校园测速线路"
        launch(arguments: ["speedtest", "campus", "--route", "auto", "--json-events"]) { [weak self] code in
            guard let self else { return }
            if code != 0, self.phase != .connectionRequired, self.phase != .completed {
                self.phase = .failed
                self.message = "校园测速未完成"
            }
        }
    }

    private func launch(arguments: [String], completion: @escaping @MainActor (Int32) -> Void) {
        guard process == nil else { return }
        guard let executable = helperExecutable() else {
            phase = .failed
            message = "找不到 soundconnect 后端"
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
                    self.message = "校园测速已取消"
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
            message = "无法启动 soundconnect 后端"
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
        case "measurement_progress":
            route = event.route ?? route
            switch event.phase {
            case "probing", "route_selected":
                phase = .probing
                message = event.route == "soundconnect" ? "正在验证 soundconnect 路径" : "正在验证校内直连路径"
            case "download":
                phase = .measuring
                downloadMbps = event.mbps
                message = "正在测量下载速度"
            case "upload":
                phase = .measuring
                uploadMbps = event.mbps
                message = "正在测量上传速度"
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
            message = result.status == "success" ? "校园测速完成" : (result.failure?.message ?? "校园测速失败")
        case "error":
            if event.error?.code == "soundconnect_required" {
                phase = .connectionRequired
                message = "校内测速服务直连不可达，请先连接 soundconnect"
                beginConnectionPolling()
            } else {
                phase = .failed
                message = event.error?.message ?? "校园测速失败"
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
                    self.message = "soundconnect 已连接，可以重新测速"
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
        pollTask?.cancel()
        pollTask = nil
        canRetryAfterConnection = false
        componentProgress = 0
        downloadMbps = nil
        uploadMbps = nil
        route = nil
    }
}

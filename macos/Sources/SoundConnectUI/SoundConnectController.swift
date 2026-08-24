import Foundation

struct SoundConnectTrafficSnapshot: Equatable {
	let uploadBytes: UInt64
	let downloadBytes: UInt64
	let activeConnections: Int
	let sampledAtUnixMilli: Int64

	init(
		uploadBytes: UInt64,
		downloadBytes: UInt64,
		activeConnections: Int,
		sampledAtUnixMilli: Int64 = 0
	) {
		self.uploadBytes = uploadBytes
		self.downloadBytes = downloadBytes
		self.activeConnections = activeConnections
		self.sampledAtUnixMilli = sampledAtUnixMilli
	}
}

struct SoundConnectRuntimeSnapshot: Equatable {
    let configured: Bool
    let running: Bool
    let state: String
    let socksListen: String
    let traffic: SoundConnectTrafficSnapshot?
}

struct SoundConnectBackendError: LocalizedError, Sendable {
    let message: String

    var errorDescription: String? { message }
}

typealias SoundConnectStatusCompletion = @MainActor @Sendable (Result<SoundConnectRuntimeSnapshot, SoundConnectBackendError>) -> Void
typealias SoundConnectActionCompletion = @MainActor @Sendable (Result<Void, SoundConnectBackendError>) -> Void

@MainActor
protocol SoundConnectControlling: AnyObject {
    func readStatus(completion: @escaping SoundConnectStatusCompletion)
    func startStatusMonitoring(completion: @escaping SoundConnectStatusCompletion)
    func stopStatusMonitoring()
    func saveConfiguration(
        server: String,
        account: String,
        password: String,
        completion: @escaping SoundConnectActionCompletion
    )
    func connect(
        verificationRequested: @escaping @MainActor @Sendable () -> Void,
        completion: @escaping SoundConnectActionCompletion
    )
    func submitVerificationCode(_ code: String) -> Bool
    func disconnect(completion: @escaping SoundConnectActionCompletion)
}

let missingHelperMessage = uiText(
    "The bundled soundconnect CLI is missing. Reinstall soundconnect to restore it.",
    "找不到内置 soundconnect CLI，请重新安装应用以修复"
)

private struct RuntimeStatusPayload: Decodable {
	struct Traffic: Decodable {
		let uploadBytes: UInt64
		let downloadBytes: UInt64
		let activeConnections: Int
		let sampledAtUnixMilli: Int64?
    }

    let running: Bool
    let state: String
    let socksListen: String?
    let traffic: Traffic?
}

private struct DoctorPayload: Decodable {
    let ready: Bool
}

@MainActor
final class SoundConnectController: SoundConnectControlling {
    private let decoder: JSONDecoder
    private var connectionProcess: Process?
    private var connectionInput: Pipe?
    private var connectionErrorText = ""
    private var didRequestVerification = false
    private var verificationHandler: (@MainActor @Sendable () -> Void)?
    private var connectionCompletion: SoundConnectActionCompletion?
    private var statusMonitorProcess: Process?
    private var statusMonitorOutput: Pipe?
    private var statusMonitorBuffer = Data()
    private var statusMonitorCompletion: SoundConnectStatusCompletion?
    private var statusMonitorConfigured: Bool?

    init() {
        decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
    }

    func readStatus(completion: @escaping SoundConnectStatusCompletion) {
        runCapture(arguments: ["status", "--json"]) { [weak self] data, _, errorText in
            guard let self,
                  let status = try? self.decoder.decode(RuntimeStatusPayload.self, from: data)
            else {
                completion(.failure(SoundConnectBackendError(message: errorText.isEmpty ? "Unable to read runtime status." : errorText)))
                return
            }
            let finish: (Bool) -> Void = { configured in
                completion(.success(self.runtimeSnapshot(from: status, configured: configured)))
            }
            guard !status.running else {
                finish(true)
                return
            }
            self.runCapture(arguments: ["doctor", "--json"]) { data, _, _ in
                finish((try? self.decoder.decode(DoctorPayload.self, from: data).ready) ?? false)
            }
        }
    }

    func startStatusMonitoring(completion: @escaping SoundConnectStatusCompletion) {
        stopStatusMonitoring()
        guard let executable = helperExecutable() else {
            completion(.failure(SoundConnectBackendError(message: missingHelperMessage)))
            return
        }
        let child = Process()
        let output = Pipe()
        child.executableURL = executable
        child.arguments = ["status", "--json", "--watch"]
        child.standardOutput = output
        child.standardError = FileHandle.nullDevice
        statusMonitorProcess = child
        statusMonitorOutput = output
        statusMonitorCompletion = completion
        statusMonitorBuffer.removeAll(keepingCapacity: true)
        statusMonitorConfigured = nil
        output.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else { return }
            Task { @MainActor in self?.consumeStatusMonitorData(data) }
        }
        child.terminationHandler = { [weak self] finished in
            Task { @MainActor in
                guard self?.statusMonitorProcess === finished else { return }
                self?.clearStatusMonitorState()
            }
        }
        do {
            try child.run()
        } catch {
            clearStatusMonitorState()
            completion(.failure(SoundConnectBackendError(message: "Unable to start status monitoring.")))
        }
    }

    func stopStatusMonitoring() {
        let process = statusMonitorProcess
        clearStatusMonitorState()
        if process?.isRunning == true {
            process?.terminate()
        }
    }

    func saveConfiguration(
        server: String,
        account: String,
        password: String,
        completion: @escaping SoundConnectActionCompletion
    ) {
        guard let executable = helperExecutable() else {
            completion(.failure(SoundConnectBackendError(message: missingHelperMessage)))
            return
        }
        let child = Process()
        let input = Pipe()
        let output = Pipe()
        let errors = Pipe()
        child.executableURL = executable
        child.arguments = ["setup", "--server", server, "--username", account, "--password-stdin"]
        child.standardInput = input
        child.standardOutput = output
        child.standardError = errors
        child.terminationHandler = { finished in
            let errorData = errors.fileHandleForReading.readDataToEndOfFile()
            let errorText = String(decoding: errorData, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            Task { @MainActor in
                if finished.terminationStatus == 0 {
                    completion(.success(()))
                } else {
                    completion(.failure(SoundConnectBackendError(message: errorText.isEmpty ? "Unable to save configuration." : errorText)))
                }
            }
        }
        do {
            try child.run()
            input.fileHandleForWriting.write(Data((password + "\n").utf8))
            try? input.fileHandleForWriting.close()
        } catch {
            completion(.failure(SoundConnectBackendError(message: "Unable to start soundconnect setup.")))
        }
    }

    func connect(
        verificationRequested: @escaping @MainActor @Sendable () -> Void,
        completion: @escaping SoundConnectActionCompletion
    ) {
        guard connectionProcess == nil else {
            completion(.failure(SoundConnectBackendError(message: "A connection attempt is already running.")))
            return
        }
        guard let executable = helperExecutable() else {
            completion(.failure(SoundConnectBackendError(message: missingHelperMessage)))
            return
        }
        let child = Process()
        let input = Pipe()
        let output = Pipe()
        let errors = Pipe()
        child.executableURL = executable
        child.arguments = ["connect", "--background", "--verification-code-stdin"]
        child.standardInput = input
        child.standardOutput = output
        child.standardError = errors
        connectionProcess = child
        connectionInput = input
        connectionErrorText = ""
        didRequestVerification = false
        verificationHandler = verificationRequested
        connectionCompletion = completion

        errors.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else { return }
            let text = String(decoding: data, as: UTF8.self)
            Task { @MainActor in self?.consumeConnectionError(text) }
        }
        child.terminationHandler = { [weak self] finished in
            Task { @MainActor in
                self?.finishConnection(
                    process: finished,
                    exitCode: finished.terminationStatus,
                    errorHandle: errors.fileHandleForReading
                )
            }
        }
        do {
            try child.run()
        } catch {
            errors.fileHandleForReading.readabilityHandler = nil
            clearConnectionState()
            completion(.failure(SoundConnectBackendError(message: "Unable to start soundconnect.")))
        }
    }

    func submitVerificationCode(_ code: String) -> Bool {
        guard !code.isEmpty, let input = connectionInput else { return false }
        input.fileHandleForWriting.write(Data((code + "\n").utf8))
        try? input.fileHandleForWriting.close()
        connectionInput = nil
        return true
    }

    func disconnect(completion: @escaping SoundConnectActionCompletion) {
        if let process = connectionProcess, process.isRunning {
            process.terminate()
            clearConnectionState()
        }
        runCapture(arguments: ["disconnect"]) { _, code, errorText in
            if code == 0 {
                completion(.success(()))
            } else {
                completion(.failure(SoundConnectBackendError(message: errorText.isEmpty ? "Unable to stop soundconnect." : errorText)))
            }
        }
    }

    private func consumeConnectionError(_ text: String) {
        connectionErrorText += text
        if !didRequestVerification,
           connectionErrorText.localizedCaseInsensitiveContains("verification code")
        {
            didRequestVerification = true
            verificationHandler?()
        }
    }

    private func consumeStatusMonitorData(_ data: Data) {
        statusMonitorBuffer.append(data)
        while let newline = statusMonitorBuffer.firstIndex(of: 0x0A) {
            let line = Data(statusMonitorBuffer[..<newline])
            statusMonitorBuffer.removeSubrange(...newline)
            guard !line.isEmpty,
                  let status = try? decoder.decode(RuntimeStatusPayload.self, from: line),
                  let completion = statusMonitorCompletion
            else { continue }
            if status.running {
                statusMonitorConfigured = true
                completion(.success(runtimeSnapshot(from: status, configured: true)))
            } else if let configured = statusMonitorConfigured {
                completion(.success(runtimeSnapshot(from: status, configured: configured)))
            } else {
                readStatus { [weak self] result in
                    guard let self else { return }
                    if case .success(let snapshot) = result {
                        self.statusMonitorConfigured = snapshot.configured
                    }
                    completion(result)
                }
            }
        }
    }

    private func runtimeSnapshot(from status: RuntimeStatusPayload, configured: Bool) -> SoundConnectRuntimeSnapshot {
        SoundConnectRuntimeSnapshot(
            configured: configured,
            running: status.running,
            state: status.state,
            socksListen: status.socksListen ?? "",
            traffic: status.traffic.map {
                SoundConnectTrafficSnapshot(
                    uploadBytes: $0.uploadBytes,
                    downloadBytes: $0.downloadBytes,
                    activeConnections: $0.activeConnections,
                    sampledAtUnixMilli: $0.sampledAtUnixMilli ?? 0
                )
            }
        )
    }

    private func clearStatusMonitorState() {
        statusMonitorOutput?.fileHandleForReading.readabilityHandler = nil
        statusMonitorProcess = nil
        statusMonitorOutput = nil
        statusMonitorCompletion = nil
        statusMonitorBuffer.removeAll(keepingCapacity: true)
        statusMonitorConfigured = nil
    }

    private func finishConnection(process: Process, exitCode: Int32, errorHandle: FileHandle) {
        guard connectionProcess === process else { return }
        errorHandle.readabilityHandler = nil
        let remainder = errorHandle.readDataToEndOfFile()
        if !remainder.isEmpty {
            connectionErrorText += String(decoding: remainder, as: UTF8.self)
        }
        let completion = connectionCompletion
        let errorText = connectionErrorText.trimmingCharacters(in: .whitespacesAndNewlines)
        clearConnectionState()
        if exitCode == 0 {
            completion?(.success(()))
        } else {
            completion?(.failure(SoundConnectBackendError(message: errorText.isEmpty ? "Connection failed." : errorText)))
        }
    }

    private func clearConnectionState() {
        connectionProcess = nil
        connectionInput = nil
        connectionErrorText = ""
        didRequestVerification = false
        verificationHandler = nil
        connectionCompletion = nil
    }

    private func runCapture(
        arguments: [String],
        completion: @escaping @MainActor @Sendable (Data, Int32, String) -> Void
    ) {
        guard let executable = helperExecutable() else {
            completion(Data(), 127, missingHelperMessage)
            return
        }
        let child = Process()
        let output = Pipe()
        let errors = Pipe()
        child.executableURL = executable
        child.arguments = arguments
        child.standardOutput = output
        child.standardError = errors
        child.terminationHandler = { finished in
            let data = output.fileHandleForReading.readDataToEndOfFile()
            let errorData = errors.fileHandleForReading.readDataToEndOfFile()
            let errorText = String(decoding: errorData, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            Task { @MainActor in completion(data, finished.terminationStatus, errorText) }
        }
        do {
            try child.run()
        } catch {
            completion(Data(), 127, "Unable to start soundconnect helper.")
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
}

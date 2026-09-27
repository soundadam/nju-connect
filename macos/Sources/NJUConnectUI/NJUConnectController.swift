import Foundation

struct NJUConnectTrafficSnapshot: Equatable {
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

struct NJUConnectRuntimeSnapshot: Equatable {
    let configured: Bool
    let running: Bool
    let state: String
    let socksListen: String
    let traffic: NJUConnectTrafficSnapshot?
    var profile: String? = nil
    var accessEvidence: String? = nil

    /// The backend that owns the running runtime, derived from the Go
    /// status profile. Nil when stopped or when the profile is unknown.
    var backend: NJUConnectBackend? {
        switch profile {
        case "community-utls", "easyconnect-7.6.7": return .easyConnect
        case "atrust-tcp": return .aTrust
        default: return nil
        }
    }
}

struct NJUConnectBackendError: LocalizedError, Sendable {
    let message: String

    var errorDescription: String? { message }
}

typealias NJUConnectStatusCompletion = @MainActor @Sendable (Result<NJUConnectRuntimeSnapshot, NJUConnectBackendError>) -> Void
typealias NJUConnectActionCompletion = @MainActor @Sendable (Result<Void, NJUConnectBackendError>) -> Void
typealias NJUConnectCatalogCompletion = @MainActor @Sendable (NJUConnectBackendCatalog?) -> Void

@MainActor
protocol NJUConnectControlling: AnyObject {
    func readStatus(completion: @escaping NJUConnectStatusCompletion)
    func startStatusMonitoring(completion: @escaping NJUConnectStatusCompletion)
    func stopStatusMonitoring()
    func loadBackendCatalog(completion: @escaping NJUConnectCatalogCompletion)
    func saveConfiguration(
        backend: NJUConnectBackend,
        server: String,
        account: String,
        password: String,
        completion: @escaping NJUConnectActionCompletion
    )
    /// Selects `backend` in the CLI's non-secret profile, then starts it.
    /// Completion reports success once the runtime has taken over: when the
    /// background handoff exits for EasyConnect, or when the foreground aTrust
    /// process reports authentication.
    func connect(
        backend: NJUConnectBackend,
        verificationRequested: @escaping @MainActor @Sendable () -> Void,
        completion: @escaping NJUConnectActionCompletion
    )
    func submitVerificationCode(_ code: String) -> Bool
    func disconnect(completion: @escaping NJUConnectActionCompletion)
    /// Forgets the saved aTrust session and OAuth browser profile while
    /// keeping the shared password (`account forget --session`).
    func forgetSession(completion: @escaping NJUConnectActionCompletion)
}

let missingHelperMessage = "The bundled nju-connect CLI is missing. Reinstall nju-connect to restore it."

// CLI payloads are internal so tests can decode the shared fixtures in
// testdata/contract, which the Go tests regenerate from real CLI output.
struct RuntimeStatusPayload: Decodable {
    struct Traffic: Decodable {
        let uploadBytes: UInt64
        let downloadBytes: UInt64
        let activeConnections: Int
        let sampledAtUnixMilli: Int64?
    }

    let running: Bool
    let state: String
    let profile: String?
    let socksListen: String?
    let accessEvidence: String?
    let traffic: Traffic?
}

struct DoctorPayload: Decodable {
    let ready: Bool
}

@MainActor
final class NJUConnectController: NJUConnectControlling {
    private let decoder: JSONDecoder
    private var connectionProcess: Process?
    private var connectionInput: Pipe?
    private var connectionErrorText = ""
    private var didRequestVerification = false
    private var verificationHandler: (@MainActor @Sendable () -> Void)?
    private var connectionCompletion: NJUConnectActionCompletion?
    private var connectionOutputText = ""
    private var statusMonitorProcess: Process?
    private var statusMonitorOutput: Pipe?
    private var statusMonitorBuffer = Data()
    private var statusMonitorCompletion: NJUConnectStatusCompletion?
    private var statusMonitorConfigured: Bool?

    init() {
        decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
    }

    func readStatus(completion: @escaping NJUConnectStatusCompletion) {
        runCapture(arguments: ["status", "--json"]) { [weak self] data, _, errorText in
            guard let self,
                  let status = try? self.decoder.decode(RuntimeStatusPayload.self, from: data)
            else {
                completion(.failure(NJUConnectBackendError(message: errorText.isEmpty ? "Unable to read runtime status." : errorText)))
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

    func startStatusMonitoring(completion: @escaping NJUConnectStatusCompletion) {
        stopStatusMonitoring()
        guard let executable = helperExecutable() else {
            completion(.failure(NJUConnectBackendError(message: missingHelperMessage)))
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
            completion(.failure(NJUConnectBackendError(message: "Unable to start status monitoring.")))
        }
    }

    func stopStatusMonitoring() {
        let process = statusMonitorProcess
        clearStatusMonitorState()
        if process?.isRunning == true {
            process?.terminate()
        }
    }

    func loadBackendCatalog(completion: @escaping NJUConnectCatalogCompletion) {
        runCapture(arguments: ["backends", "--json"]) { data, code, _ in
            guard code == 0 else {
                completion(nil)
                return
            }
            completion(try? JSONDecoder().decode(NJUConnectBackendCatalog.self, from: data))
        }
    }

    func saveConfiguration(
        backend: NJUConnectBackend,
        server: String,
        account: String,
        password: String,
        completion: @escaping NJUConnectActionCompletion
    ) {
        guard let executable = helperExecutable() else {
            completion(.failure(NJUConnectBackendError(message: missingHelperMessage)))
            return
        }
        let child = Process()
        let input = Pipe()
        let output = Pipe()
        let errors = Pipe()
        child.executableURL = executable
        child.arguments = [
            "setup", "--backend", backend.rawValue,
            "--server", server, "--username", account, "--password-stdin",
        ]
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
                    completion(.failure(NJUConnectBackendError(message: errorText.isEmpty ? "Unable to save configuration." : errorText)))
                }
            }
        }
        do {
            try child.run()
            input.fileHandleForWriting.write(Data((password + "\n").utf8))
            try? input.fileHandleForWriting.close()
        } catch {
            completion(.failure(NJUConnectBackendError(message: "Unable to start nju-connect setup.")))
        }
    }

    func connect(
        backend: NJUConnectBackend,
        verificationRequested: @escaping @MainActor @Sendable () -> Void,
        completion: @escaping NJUConnectActionCompletion
    ) {
        guard connectionProcess == nil else {
            completion(.failure(NJUConnectBackendError(message: "A connection attempt is already running.")))
            return
        }
        guard helperExecutable() != nil else {
            completion(.failure(NJUConnectBackendError(message: missingHelperMessage)))
            return
        }
        runCapture(arguments: ["configure", "--backend", backend.rawValue]) { [weak self] _, code, errorText in
            guard let self else { return }
            guard code == 0 else {
                completion(.failure(NJUConnectBackendError(
                    message: errorText.isEmpty ? "Unable to select the \(backend.title) backend." : errorText
                )))
                return
            }
            self.launchConnection(
                backend: backend,
                verificationRequested: verificationRequested,
                completion: completion
            )
        }
    }

    private func launchConnection(
        backend: NJUConnectBackend,
        verificationRequested: @escaping @MainActor @Sendable () -> Void,
        completion: @escaping NJUConnectActionCompletion
    ) {
        guard connectionProcess == nil else {
            completion(.failure(NJUConnectBackendError(message: "A connection attempt is already running.")))
            return
        }
        guard let executable = helperExecutable() else {
            completion(.failure(NJUConnectBackendError(message: missingHelperMessage)))
            return
        }
        let child = Process()
        let input = Pipe()
        let output = Pipe()
        let errors = Pipe()
        child.executableURL = executable
        // EasyConnect hands off to a detached runtime. The aTrust runtime has
        // no background mode yet, so its foreground process is the runtime.
        child.arguments = backend == .aTrust
            ? ["connect", "--verification-code-stdin"]
            : ["connect", "--background", "--verification-code-stdin"]
        child.standardInput = input
        child.standardOutput = output
        child.standardError = errors
        connectionProcess = child
        connectionInput = input
        connectionErrorText = ""
        connectionOutputText = ""
        didRequestVerification = false
        verificationHandler = verificationRequested
        connectionCompletion = completion

        output.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else { return }
            let text = String(decoding: data, as: UTF8.self)
            Task { @MainActor in self?.consumeConnectionOutput(text) }
        }
        errors.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else { return }
            let text = String(decoding: data, as: UTF8.self)
            Task { @MainActor in self?.consumeConnectionError(text) }
        }
        child.terminationHandler = { [weak self] finished in
            Task { @MainActor in
                output.fileHandleForReading.readabilityHandler = nil
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
            output.fileHandleForReading.readabilityHandler = nil
            clearConnectionState()
            completion(.failure(NJUConnectBackendError(message: "Unable to start nju-connect.")))
        }
    }

    func submitVerificationCode(_ code: String) -> Bool {
        guard !code.isEmpty, let input = connectionInput else { return false }
        input.fileHandleForWriting.write(Data((code + "\n").utf8))
        try? input.fileHandleForWriting.close()
        connectionInput = nil
        return true
    }

    func disconnect(completion: @escaping NJUConnectActionCompletion) {
        if let process = connectionProcess, process.isRunning {
            process.terminate()
            clearConnectionState()
        }
        runCapture(arguments: ["disconnect"]) { _, code, errorText in
            if code == 0 {
                completion(.success(()))
            } else {
                completion(.failure(NJUConnectBackendError(message: errorText.isEmpty ? "Unable to stop nju-connect." : errorText)))
            }
        }
    }

    func forgetSession(completion: @escaping NJUConnectActionCompletion) {
        runCapture(arguments: ["account", "forget", "--session"]) { _, code, errorText in
            if code == 0 {
                completion(.success(()))
            } else {
                completion(.failure(NJUConnectBackendError(
                    message: errorText.isEmpty ? "Unable to forget the saved aTrust session." : errorText
                )))
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

    /// A foreground runtime prints "authentication: accepted" (or "resumed")
    /// after its status socket is live; that is the handoff point.
    private func consumeConnectionOutput(_ text: String) {
        guard connectionCompletion != nil else { return }
        connectionOutputText += text
        if connectionOutputText.count > 4_096 {
            connectionOutputText = String(connectionOutputText.suffix(4_096))
        }
        if connectionOutputText.contains("authentication: accepted")
            || connectionOutputText.contains("authentication: resumed"),
           connectionOutputText.contains("backend: atrust")
        {
            let completion = connectionCompletion
            connectionCompletion = nil
            completion?(.success(()))
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

    private func runtimeSnapshot(from status: RuntimeStatusPayload, configured: Bool) -> NJUConnectRuntimeSnapshot {
        NJUConnectRuntimeSnapshot(
            configured: configured,
            running: status.running,
            state: status.state,
            socksListen: status.socksListen ?? "",
            traffic: status.traffic.map {
                NJUConnectTrafficSnapshot(
                    uploadBytes: $0.uploadBytes,
                    downloadBytes: $0.downloadBytes,
                    activeConnections: $0.activeConnections,
                    sampledAtUnixMilli: $0.sampledAtUnixMilli ?? 0
                )
            },
            profile: status.profile,
            accessEvidence: status.accessEvidence
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
            completion?(.failure(NJUConnectBackendError(message: errorText.isEmpty ? "Connection failed." : errorText)))
        }
    }

    private func clearConnectionState() {
        connectionProcess = nil
        connectionInput = nil
        connectionErrorText = ""
        connectionOutputText = ""
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
            completion(Data(), 127, "Unable to start nju-connect helper.")
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
}

import AppKit
import CryptoKit
import Foundation
import ServiceManagement

private struct LauncherError: LocalizedError {
    let message: String

    init(_ message: String) {
        self.message = message
    }

    var errorDescription: String? { message }
}

private enum ServerState: Equatable {
    case stopped
    case starting
    case stopping
    case running
    case external
    case failed(String)

    var label: String {
        switch self {
        case .stopped:
            return "Stopped"
        case .starting:
            return "Starting..."
        case .stopping:
            return "Stopping..."
        case .running:
            return "Running"
        case .external:
            return "Running outside this app"
        case .failed(let message):
            return "Needs attention: \(message)"
        }
    }

    var isRunning: Bool {
        self == .running || self == .external
    }
}

private struct ServerInfoEnvelope: Decodable {
    let data: ServerInfo

    struct ServerInfo: Decodable {
        let activeStreams: Int?

        enum CodingKeys: String, CodingKey {
            case activeStreams = "active_streams"
        }
    }
}

private final class ServerManager {
    var onStateChange: ((ServerState) -> Void)?
    var onActiveStreamCountChange: ((Int) -> Void)?

    let supportRoot: URL
    let configURL: URL
    let launcherLogURL: URL
    let serverLogURL: URL

    private var process: Process?
    private var logHandle: FileHandle?
    private var healthTimer: Timer?
    private var activityTimer: Timer?
    private var activityRequestInFlight = false
    private var stopCompletions: [() -> Void] = []
    private var stopRequested = false
    private var startupDeadline = Date()

    private(set) var state: ServerState = .stopped {
        didSet {
            guard oldValue != state else { return }
            DispatchQueue.main.async { [weak self, state] in
                guard let self, self.state == state else { return }
                self.updateActivityPolling(for: state)
                self.onStateChange?(state)
            }
        }
    }

    private(set) var activeStreamCount = 0 {
        didSet {
            guard oldValue != activeStreamCount else { return }
            onActiveStreamCountChange?(activeStreamCount)
        }
    }

    init() {
        let environment = ProcessInfo.processInfo.environment
        if let override = environment["TATER_TUBE_SERVER_DATA_DIR"]?.trimmingCharacters(in: .whitespacesAndNewlines),
           !override.isEmpty {
            supportRoot = URL(fileURLWithPath: override, isDirectory: true)
        } else {
            supportRoot = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
                .appendingPathComponent("Tater Tube Server", isDirectory: true)
        }
        configURL = supportRoot.appendingPathComponent("config.yaml")
        launcherLogURL = supportRoot.appendingPathComponent("launcher.log")
        serverLogURL = supportRoot.appendingPathComponent("tater-tube-server.log")
    }

    var dashboardURL: URL {
        URL(string: "http://127.0.0.1:\(configuredPort())")!
    }

    func start() {
        dispatchPrecondition(condition: .onQueue(.main))
        guard process == nil else { return }

        if state == .external {
            probeHealth { [weak self] healthy in
                guard let self else { return }
                if !healthy {
                    self.state = .stopped
                    self.start()
                }
            }
            return
        }

        state = .starting
        stopRequested = false

        probeHealth { [weak self] healthy in
            guard let self, self.process == nil, !self.stopRequested else { return }
            if healthy {
                self.state = .external
                return
            }
            self.launchBundledServer()
        }
    }

    func restart() {
        dispatchPrecondition(condition: .onQueue(.main))
        if state == .external {
            state = .failed("Port \(configuredPort()) is already used by another server.")
            return
        }
        stop { [weak self] in self?.start() }
    }

    func stop(completion: (() -> Void)? = nil) {
        dispatchPrecondition(condition: .onQueue(.main))
        if let completion {
            stopCompletions.append(completion)
        }
        healthTimer?.invalidate()
        healthTimer = nil
        stopRequested = true

        guard let process else {
            if state != .external {
                state = .stopped
            }
            completeStops()
            return
        }

        state = .stopping
        if process.isRunning {
            process.terminate()
            let pid = process.processIdentifier
            DispatchQueue.global(qos: .userInitiated).async { [weak self, weak process] in
                let deadline = Date().addingTimeInterval(12)
                while process?.isRunning == true, Date() < deadline {
                    Thread.sleep(forTimeInterval: 0.1)
                }
                if process?.isRunning == true {
                    kill(pid, SIGKILL)
                }
                process?.waitUntilExit()
                DispatchQueue.main.async {
                    self?.finishProcess(exitStatus: process?.terminationStatus ?? 0)
                }
            }
        } else {
            finishProcess(exitStatus: process.terminationStatus)
        }
    }

    private func launchBundledServer() {
        dispatchPrecondition(condition: .onQueue(.main))
        guard let executable = Bundle.main.url(
            forResource: "tater-tube-server",
            withExtension: nil,
            subdirectory: "Server"
        ) else {
            state = .failed("The bundled server executable is missing.")
            return
        }

        do {
            try FileManager.default.createDirectory(at: supportRoot, withIntermediateDirectories: true)
            if !FileManager.default.fileExists(atPath: launcherLogURL.path) {
                FileManager.default.createFile(atPath: launcherLogURL.path, contents: nil)
            }
            let handle = try FileHandle(forWritingTo: launcherLogURL)
            try handle.seekToEnd()
            let heading = "\n--- Tater Tube Server launch \(ISO8601DateFormatter().string(from: Date())) ---\n"
            try handle.write(contentsOf: Data(heading.utf8))

            var environment = ProcessInfo.processInfo.environment
            var pathParts: [String] = []
            if let ffmpegBin = Bundle.main.resourceURL?
                .appendingPathComponent("FFmpeg/bin", isDirectory: true),
               FileManager.default.fileExists(atPath: ffmpegBin.path) {
                pathParts.append(ffmpegBin.path)
            }
            pathParts.append(contentsOf: ["/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"])
            if let existing = environment["PATH"], !existing.isEmpty {
                pathParts.append(existing)
            }
            environment["PATH"] = pathParts.joined(separator: ":")

            let task = Process()
            task.executableURL = executable
            task.arguments = ["serve", "--config=\(configURL.path)"]
            task.currentDirectoryURL = supportRoot
            task.environment = environment
            task.standardOutput = handle
            task.standardError = handle
            task.terminationHandler = { [weak self, weak task] _ in
                let status = task?.terminationStatus ?? 1
                DispatchQueue.main.async {
                    self?.finishProcess(exitStatus: status)
                }
            }

            process = task
            logHandle = handle
            startupDeadline = Date().addingTimeInterval(75)
            try task.run()
            startHealthChecks()
        } catch {
            logHandle?.closeFile()
            logHandle = nil
            process = nil
            state = .failed(error.localizedDescription)
        }
    }

    private func startHealthChecks() {
        healthTimer?.invalidate()
        healthTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            guard let self else { return }
            self.probeHealth { [weak self] healthy in
                guard let self else { return }
                if healthy {
                    self.healthTimer?.invalidate()
                    self.healthTimer = nil
                    self.state = .running
                } else if Date() > self.startupDeadline {
                    self.healthTimer?.invalidate()
                    self.healthTimer = nil
                    let message = "The dashboard did not become ready. Check the server log."
                    self.stop { [weak self] in self?.state = .failed(message) }
                }
            }
        }
        healthTimer?.fire()
    }

    private func probeHealth(completion: @escaping (Bool) -> Void) {
        let url = dashboardURL.appendingPathComponent("live")
        var request = URLRequest(url: url)
        request.timeoutInterval = 1.5
        request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        URLSession.shared.dataTask(with: request) { _, response, _ in
            let healthy = (response as? HTTPURLResponse).map { (200..<500).contains($0.statusCode) } ?? false
            DispatchQueue.main.async { completion(healthy) }
        }.resume()
    }

    private func updateActivityPolling(for state: ServerState) {
        activityTimer?.invalidate()
        activityTimer = nil
        activityRequestInFlight = false

        guard state.isRunning else {
            activeStreamCount = 0
            return
        }

        refreshActiveStreamCount()
        activityTimer = Timer.scheduledTimer(withTimeInterval: 4, repeats: true) { [weak self] _ in
            self?.refreshActiveStreamCount()
        }
    }

    private func refreshActiveStreamCount() {
        guard state.isRunning, !activityRequestInFlight else { return }
        activityRequestInFlight = true

        let url = dashboardURL.appendingPathComponent("api/tater/server")
        var request = URLRequest(url: url)
        request.timeoutInterval = 2
        request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        URLSession.shared.dataTask(with: request) { [weak self] data, response, _ in
            let count: Int
            if let http = response as? HTTPURLResponse,
               (200..<300).contains(http.statusCode),
               let data,
               let envelope = try? JSONDecoder().decode(ServerInfoEnvelope.self, from: data) {
                count = max(0, envelope.data.activeStreams ?? 0)
            } else {
                count = 0
            }
            DispatchQueue.main.async {
                guard let self else { return }
                self.activityRequestInFlight = false
                guard self.state.isRunning else { return }
                self.activeStreamCount = count
            }
        }.resume()
    }

    private func finishProcess(exitStatus: Int32) {
        dispatchPrecondition(condition: .onQueue(.main))
        guard process != nil else { return }
        healthTimer?.invalidate()
        healthTimer = nil
        process = nil
        logHandle?.closeFile()
        logHandle = nil

        if stopRequested {
            state = .stopped
        } else {
            let detail = lastUsefulLogLine() ?? "Server exited with status \(exitStatus)."
            state = .failed(detail)
        }
        completeStops()
    }

    private func completeStops() {
        let completions = stopCompletions
        stopCompletions.removeAll()
        completions.forEach { $0() }
    }

    private func configuredPort() -> Int {
        guard let text = try? String(contentsOf: configURL, encoding: .utf8) else { return 8080 }
        var inServerBlock = false
        for rawLine in text.components(separatedBy: .newlines) {
            let line = rawLine.replacingOccurrences(of: "\t", with: "    ")
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.isEmpty || trimmed.hasPrefix("#") { continue }
            let indentation = line.prefix { $0 == " " }.count
            if indentation == 0 {
                inServerBlock = trimmed == "server:"
                continue
            }
            if inServerBlock, trimmed.hasPrefix("port:") {
                let value = trimmed.dropFirst("port:".count).trimmingCharacters(in: .whitespaces)
                if let port = Int(value), (1...65535).contains(port) {
                    return port
                }
            }
        }
        return 8080
    }

    private func lastUsefulLogLine() -> String? {
        guard let data = try? Data(contentsOf: launcherLogURL),
              let text = String(data: data.suffix(32 * 1024), encoding: .utf8) else { return nil }
        let line = text.components(separatedBy: .newlines)
            .reversed()
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .first { !$0.isEmpty && !$0.hasPrefix("--- Tater Tube Server launch") }
        return line.map { String($0.prefix(180)) }
    }
}

private struct UpdateManifest: Decodable, Equatable {
    let version: String
    let build: Int
    let url: URL
    let sha256: String
    let notes: String?
}

private enum UpdateState: Equatable {
    case idle
    case checking
    case current
    case available(UpdateManifest)
    case downloading(UpdateManifest)
    case installing(UpdateManifest)
    case failed(String)

    var isBusy: Bool {
        switch self {
        case .checking, .downloading, .installing:
            return true
        case .idle, .current, .available, .failed:
            return false
        }
    }
}

private final class UpdateManager {
    var onStateChange: ((UpdateState) -> Void)?

    private let updatesRoot: URL
    private var availableManifest: UpdateManifest?

    private(set) var state: UpdateState = .idle {
        didSet {
            DispatchQueue.main.async { [state, onStateChange] in
                onStateChange?(state)
            }
        }
    }

    init(supportRoot: URL) {
        updatesRoot = supportRoot.appendingPathComponent("updates", isDirectory: true)
    }

    func checkForUpdates(manual: Bool) {
        guard !state.isBusy else { return }
        state = .checking
        DispatchQueue.global(qos: .utility).async { [weak self] in
            guard let self else { return }
            do {
                let manifest = try self.fetchManifest()
                if self.isNewerThanCurrent(manifest) {
                    self.availableManifest = manifest
                    self.state = .available(manifest)
                } else {
                    self.availableManifest = nil
                    self.state = manual ? .current : .idle
                }
            } catch {
                self.state = manual ? .failed(error.localizedDescription) : .idle
            }
        }
    }

    func installAvailableUpdate() {
        let manifest: UpdateManifest?
        switch state {
        case .available(let value):
            manifest = value
        default:
            manifest = availableManifest
        }
        guard let manifest else { return }

        state = .downloading(manifest)
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self else { return }
            do {
                let app = try self.prepareUpdate(manifest)
                self.state = .installing(manifest)
                try self.launchInstaller(newApp: app)
                DispatchQueue.main.async { NSApp.terminate(nil) }
            } catch {
                self.state = .failed(error.localizedDescription)
            }
        }
    }

    private func fetchManifest() throws -> UpdateManifest {
        guard let url = manifestURL() else { throw LauncherError("No update manifest URL is configured.") }
        var request = URLRequest(url: url)
        request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        request.timeoutInterval = 30
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue("Tater-Tube-Server-macOS", forHTTPHeaderField: "User-Agent")
        return try JSONDecoder().decode(UpdateManifest.self, from: loadData(from: request))
    }

    private func manifestURL() -> URL? {
        let environment = ProcessInfo.processInfo.environment
        if let raw = environment["TATER_TUBE_SERVER_UPDATE_MANIFEST_URL"]?.trimmingCharacters(in: .whitespacesAndNewlines),
           !raw.isEmpty {
            return URL(string: raw)
        }
        if let raw = Bundle.main.object(forInfoDictionaryKey: "TaterTubeServerUpdateManifestURL") as? String {
            return URL(string: raw.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        return nil
    }

    private func isNewerThanCurrent(_ manifest: UpdateManifest) -> Bool {
        let current = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
        let comparison = compareVersion(manifest.version, to: current)
        if comparison != .orderedSame { return comparison == .orderedDescending }
        let currentBuild = versionParts(Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "0").first ?? 0
        return manifest.build > currentBuild
    }

    private func compareVersion(_ lhs: String, to rhs: String) -> ComparisonResult {
        let left = versionParts(lhs)
        let right = versionParts(rhs)
        for index in 0..<max(left.count, right.count) {
            let a = index < left.count ? left[index] : 0
            let b = index < right.count ? right[index] : 0
            if a > b { return .orderedDescending }
            if a < b { return .orderedAscending }
        }
        return .orderedSame
    }

    private func versionParts(_ raw: String) -> [Int] {
        raw.split { !$0.isNumber }.compactMap { Int($0) }
    }

    private func prepareUpdate(_ manifest: UpdateManifest) throws -> URL {
        try FileManager.default.createDirectory(at: updatesRoot, withIntermediateDirectories: true)
        let archive = updatesRoot.appendingPathComponent("Tater-Tube-Server-v\(manifest.version).zip")
        let staging = updatesRoot.appendingPathComponent("staging-\(UUID().uuidString)", isDirectory: true)
        try downloadFile(from: manifest.url, to: archive)
        try verifySHA256(of: archive, expected: manifest.sha256)
        try FileManager.default.createDirectory(at: staging, withIntermediateDirectories: true)
        try runCheckedProcess(executable: "/usr/bin/ditto", arguments: ["-x", "-k", archive.path, staging.path])
        let app = try findExtractedApp(in: staging)
        let executable = app.appendingPathComponent("Contents/MacOS/TaterTubeServer")
        guard FileManager.default.isExecutableFile(atPath: executable.path) else {
            throw LauncherError("The downloaded update did not contain Tater Tube Server.")
        }
        return app
    }

    private func verifySHA256(of url: URL, expected raw: String) throws {
        let expected = raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let hex = CharacterSet(charactersIn: "0123456789abcdef")
        guard expected.count == 64, expected.unicodeScalars.allSatisfy({ hex.contains($0) }) else {
            throw LauncherError("The update manifest has an invalid SHA-256 hash.")
        }
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        while true {
            let data = try handle.read(upToCount: 1024 * 1024) ?? Data()
            if data.isEmpty { break }
            hasher.update(data: data)
        }
        let actual = hasher.finalize().map { String(format: "%02x", $0) }.joined()
        guard actual == expected else { throw LauncherError("The update download did not match its checksum.") }
    }

    private func findExtractedApp(in root: URL) throws -> URL {
        let preferred = root.appendingPathComponent("Tater Tube Server.app", isDirectory: true)
        if FileManager.default.fileExists(atPath: preferred.path) { return preferred }
        guard let enumerator = FileManager.default.enumerator(
            at: root,
            includingPropertiesForKeys: [.isDirectoryKey],
            options: [.skipsHiddenFiles]
        ) else { throw LauncherError("Could not inspect the extracted update.") }
        for case let url as URL in enumerator where url.pathExtension == "app" { return url }
        throw LauncherError("The update archive did not contain a macOS app.")
    }

    private func launchInstaller(newApp: URL) throws {
        let target = Bundle.main.bundleURL.standardizedFileURL
        guard target.pathExtension == "app" else { throw LauncherError("The server is not running from an app bundle.") }
        try FileManager.default.createDirectory(at: updatesRoot, withIntermediateDirectories: true)
        let scriptURL = updatesRoot.appendingPathComponent("install-update-\(UUID().uuidString).sh")
        let script = """
        #!/bin/sh
        set -eu
        APP_PID="$1"
        NEW_APP="$2"
        TARGET_APP="$3"
        SCRIPT_PATH="$0"
        WAIT_COUNT=0
        while kill -0 "$APP_PID" 2>/dev/null && [ "$WAIT_COUNT" -lt 300 ]; do
          sleep 0.2
          WAIT_COUNT=$((WAIT_COUNT + 1))
        done
        if kill -0 "$APP_PID" 2>/dev/null; then exit 1; fi
        TARGET_PARENT="$(dirname "$TARGET_APP")"
        TARGET_NAME="$(basename "$TARGET_APP")"
        STAGED="${TARGET_PARENT}/.${TARGET_NAME}.updating"
        BACKUP="${TARGET_PARENT}/.${TARGET_NAME}.previous"
        NEW_PARENT="$(dirname "$NEW_APP")"
        rm -rf "$STAGED" "$BACKUP"
        ditto "$NEW_APP" "$STAGED"
        if [ -d "$TARGET_APP" ]; then mv "$TARGET_APP" "$BACKUP"; fi
        mv "$STAGED" "$TARGET_APP"
        xattr -dr com.apple.quarantine "$TARGET_APP" 2>/dev/null || true
        open "$TARGET_APP"
        rm -rf "$NEW_PARENT" "$BACKUP"
        rm -f "$SCRIPT_PATH"
        """
        try script.write(to: scriptURL, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: scriptURL.path)
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = [scriptURL.path, "\(getpid())", newApp.path, target.path]
        try process.run()
    }

    private func loadData(from request: URLRequest) throws -> Data {
        let semaphore = DispatchSemaphore(value: 0)
        var result: Result<Data, Error>?
        URLSession.shared.dataTask(with: request) { data, response, error in
            if let error {
                result = .failure(error)
            } else if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
                result = .failure(LauncherError("Update request failed with HTTP \(http.statusCode)."))
            } else {
                result = .success(data ?? Data())
            }
            semaphore.signal()
        }.resume()
        semaphore.wait()
        guard let result else { throw LauncherError("The update request did not complete.") }
        return try result.get()
    }

    private func downloadFile(from url: URL, to destination: URL) throws {
        let semaphore = DispatchSemaphore(value: 0)
        var result: Result<URL, Error>?
        URLSession.shared.downloadTask(with: url) { location, response, error in
            if let error {
                result = .failure(error)
            } else if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
                result = .failure(LauncherError("Update download failed with HTTP \(http.statusCode)."))
            } else if let location {
                result = .success(location)
            } else {
                result = .failure(LauncherError("The update download did not return a file."))
            }
            semaphore.signal()
        }.resume()
        semaphore.wait()
        guard let result else { throw LauncherError("The update download did not complete.") }
        let temporary = try result.get()
        if FileManager.default.fileExists(atPath: destination.path) {
            try FileManager.default.removeItem(at: destination)
        }
        try FileManager.default.copyItem(at: temporary, to: destination)
    }

    private func runCheckedProcess(executable: String, arguments: [String]) throws {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments
        process.standardOutput = Pipe()
        process.standardError = Pipe()
        try process.run()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            throw LauncherError("\(URL(fileURLWithPath: executable).lastPathComponent) exited with status \(process.terminationStatus).")
        }
    }
}

private final class AppDelegate: NSObject, NSApplicationDelegate {
    private let server = ServerManager()
    private lazy var updater = UpdateManager(supportRoot: server.supportRoot)
    private var statusItem: NSStatusItem?
    private var statusMenuItem: NSMenuItem?
    private var openMenuItem: NSMenuItem?
    private var startMenuItem: NSMenuItem?
    private var restartMenuItem: NSMenuItem?
    private var stopMenuItem: NSMenuItem?
    private var launchAtLoginMenuItem: NSMenuItem?
    private var updateMenuItem: NSMenuItem?
    private var checkUpdatesMenuItem: NSMenuItem?
    private var updateTimer: Timer?
    private var menuResetTimer: Timer?
    private var terminationInProgress = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        configureMenuBar()
        server.onStateChange = { [weak self] state in self?.serverStateChanged(state) }
        server.onActiveStreamCountChange = { [weak self] _ in self?.refreshServerPresentation() }
        updater.onStateChange = { [weak self] state in self?.updateStateChanged(state) }
        refreshLaunchAtLogin()
        server.start()
        scheduleAutomaticUpdates()
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if terminationInProgress { return .terminateLater }
        terminationInProgress = true
        updateTimer?.invalidate()
        menuResetTimer?.invalidate()
        server.stop { [weak sender] in sender?.reply(toApplicationShouldTerminate: true) }
        return .terminateLater
    }

    private func configureMenuBar() {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = item.button {
            button.image = makeMenuBarImage()
            button.imagePosition = .imageOnly
            button.font = .monospacedDigitSystemFont(ofSize: 11, weight: .semibold)
        }
        item.button?.toolTip = "Tater Tube Server"

        let menu = NSMenu()
        let status = NSMenuItem(title: "Status: \(server.state.label)", action: nil, keyEquivalent: "")
        status.isEnabled = false
        menu.addItem(status)

        let update = NSMenuItem(title: "Update Available", action: #selector(installUpdate), keyEquivalent: "")
        update.target = self
        update.isHidden = true
        menu.addItem(update)
        menu.addItem(.separator())

        let open = NSMenuItem(title: "Open Dashboard", action: #selector(openDashboard), keyEquivalent: "o")
        open.target = self
        menu.addItem(open)
        menu.addItem(.separator())

        let start = NSMenuItem(title: "Start Server", action: #selector(startServer), keyEquivalent: "s")
        start.target = self
        menu.addItem(start)
        let restart = NSMenuItem(title: "Restart Server", action: #selector(restartServer), keyEquivalent: "r")
        restart.target = self
        menu.addItem(restart)
        let stop = NSMenuItem(title: "Stop Server", action: #selector(stopServer), keyEquivalent: "")
        stop.target = self
        menu.addItem(stop)
        menu.addItem(.separator())

        let launchAtLogin = NSMenuItem(title: "Start at Login", action: #selector(toggleLaunchAtLogin), keyEquivalent: "")
        launchAtLogin.target = self
        menu.addItem(launchAtLogin)
        let data = NSMenuItem(title: "Open Data Folder", action: #selector(openDataFolder), keyEquivalent: "")
        data.target = self
        menu.addItem(data)
        let logs = NSMenuItem(title: "Show Server Log", action: #selector(showServerLog), keyEquivalent: "l")
        logs.target = self
        menu.addItem(logs)

        let check = NSMenuItem(title: "Check for Updates...", action: #selector(checkForUpdates), keyEquivalent: "")
        check.target = self
        menu.addItem(check)
        menu.addItem(.separator())

        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? ""
        let versionItem = NSMenuItem(title: "Tater Tube Server \(version) (\(build))", action: nil, keyEquivalent: "")
        versionItem.isEnabled = false
        menu.addItem(versionItem)
        let quit = NSMenuItem(title: "Quit Tater Tube Server", action: #selector(quit), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)

        item.menu = menu
        statusItem = item
        statusMenuItem = status
        openMenuItem = open
        startMenuItem = start
        restartMenuItem = restart
        stopMenuItem = stop
        launchAtLoginMenuItem = launchAtLogin
        updateMenuItem = update
        checkUpdatesMenuItem = check
        serverStateChanged(server.state)
        updateStateChanged(updater.state)
    }

    private func makeMenuBarImage() -> NSImage {
        let configuration = NSImage.SymbolConfiguration(pointSize: 15, weight: .medium)
        if let symbol = NSImage(
            systemSymbolName: "play.rectangle.on.rectangle",
            accessibilityDescription: "Tater Tube Server"
        )?.withSymbolConfiguration(configuration) {
            symbol.isTemplate = true
            return symbol
        }

        let image = NSImage(size: NSSize(width: 18, height: 18), flipped: false) { _ in
            NSColor.black.setStroke()
            NSColor.black.setFill()

            let rear = NSBezierPath(roundedRect: NSRect(x: 5, y: 6, width: 11, height: 8), xRadius: 2, yRadius: 2)
            rear.lineWidth = 1.4
            rear.stroke()

            let front = NSBezierPath(roundedRect: NSRect(x: 2, y: 3, width: 11, height: 8), xRadius: 2, yRadius: 2)
            front.lineWidth = 1.4
            front.stroke()

            let play = NSBezierPath()
            play.move(to: NSPoint(x: 6, y: 5))
            play.line(to: NSPoint(x: 6, y: 9))
            play.line(to: NSPoint(x: 9.5, y: 7))
            play.close()
            play.fill()
            return true
        }
        image.isTemplate = true
        return image
    }

    private func serverStateChanged(_ state: ServerState) {
        switch state {
        case .stopped, .failed:
            openMenuItem?.isEnabled = false
            startMenuItem?.title = "Start Server"
            startMenuItem?.isEnabled = true
            restartMenuItem?.isEnabled = false
            stopMenuItem?.title = "Server Stopped"
            stopMenuItem?.isEnabled = false
        case .starting:
            openMenuItem?.isEnabled = false
            startMenuItem?.title = "Starting..."
            startMenuItem?.isEnabled = false
            restartMenuItem?.isEnabled = false
            stopMenuItem?.title = "Stop Server"
            stopMenuItem?.isEnabled = true
        case .stopping:
            openMenuItem?.isEnabled = false
            startMenuItem?.title = "Stopping..."
            startMenuItem?.isEnabled = false
            restartMenuItem?.isEnabled = false
            stopMenuItem?.title = "Stopping..."
            stopMenuItem?.isEnabled = false
        case .running:
            openMenuItem?.isEnabled = true
            startMenuItem?.title = "Server Running"
            startMenuItem?.isEnabled = false
            restartMenuItem?.isEnabled = true
            stopMenuItem?.title = "Stop Server"
            stopMenuItem?.isEnabled = true
            openDashboardOnFirstRun()
        case .external:
            openMenuItem?.isEnabled = true
            startMenuItem?.title = "Server Running"
            startMenuItem?.isEnabled = false
            restartMenuItem?.isEnabled = false
            stopMenuItem?.title = "Managed Outside App"
            stopMenuItem?.isEnabled = false
        }
        refreshServerPresentation()
    }

    private func refreshServerPresentation() {
        let state = server.state
        let count = state.isRunning ? server.activeStreamCount : 0
        let streamLabel: String
        if count == 1 {
            streamLabel = "1 active stream"
        } else {
            streamLabel = "\(count) active streams"
        }

        statusMenuItem?.title = count > 0
            ? "Status: \(state.label) • \(streamLabel)"
            : "Status: \(state.label)"

        guard let button = statusItem?.button else { return }
        button.title = count > 0 ? (count > 99 ? "99+" : "\(count)") : ""
        button.imagePosition = count > 0 ? .imageLeading : .imageOnly
        button.toolTip = state.isRunning
            ? "Tater Tube Server — \(count > 0 ? streamLabel : state.label)"
            : "Tater Tube Server — \(state.label)"
    }

    private func updateStateChanged(_ state: UpdateState) {
        menuResetTimer?.invalidate()
        checkUpdatesMenuItem?.isEnabled = true
        checkUpdatesMenuItem?.title = "Check for Updates..."
        switch state {
        case .idle:
            hideUpdateItem()
        case .checking:
            hideUpdateItem()
            checkUpdatesMenuItem?.title = "Checking for Updates..."
            checkUpdatesMenuItem?.isEnabled = false
        case .current:
            hideUpdateItem()
            checkUpdatesMenuItem?.title = "Tater Tube Server is Up to Date"
            resetUpdateTitleSoon()
        case .available(let manifest):
            updateMenuItem?.isHidden = false
            updateMenuItem?.isEnabled = true
            updateMenuItem?.attributedTitle = NSAttributedString(
                string: "Install Tater Tube Server \(manifest.version)",
                attributes: [.foregroundColor: NSColor.systemOrange]
            )
        case .downloading(let manifest):
            showBusyUpdate("Downloading \(manifest.version)...")
        case .installing(let manifest):
            showBusyUpdate("Installing \(manifest.version)...")
        case .failed(let message):
            hideUpdateItem()
            checkUpdatesMenuItem?.title = "Update Check Failed"
            resetUpdateTitleSoon()
            if NSApp.isActive {
                showAlert(title: "Update Check Failed", message: message)
            }
        }
    }

    private func showBusyUpdate(_ title: String) {
        updateMenuItem?.isHidden = false
        updateMenuItem?.isEnabled = false
        updateMenuItem?.attributedTitle = NSAttributedString(string: title, attributes: [.foregroundColor: NSColor.systemOrange])
        checkUpdatesMenuItem?.isEnabled = false
    }

    private func hideUpdateItem() {
        updateMenuItem?.isHidden = true
        updateMenuItem?.isEnabled = false
        updateMenuItem?.attributedTitle = nil
    }

    private func resetUpdateTitleSoon() {
        menuResetTimer = Timer.scheduledTimer(withTimeInterval: 4, repeats: false) { [weak self] _ in
            self?.checkUpdatesMenuItem?.title = "Check for Updates..."
        }
    }

    private func scheduleAutomaticUpdates() {
        updateTimer = Timer.scheduledTimer(withTimeInterval: 8, repeats: false) { [weak self] _ in
            self?.updater.checkForUpdates(manual: false)
            self?.updateTimer = Timer.scheduledTimer(withTimeInterval: 12 * 60 * 60, repeats: true) { [weak self] _ in
                self?.updater.checkForUpdates(manual: false)
            }
        }
    }

    private func openDashboardOnFirstRun() {
        let key = "HasOpenedDashboard"
        guard !UserDefaults.standard.bool(forKey: key) else { return }
        UserDefaults.standard.set(true, forKey: key)
        openDashboard()
    }

    private func refreshLaunchAtLogin() {
        launchAtLoginMenuItem?.state = SMAppService.mainApp.status == .enabled ? .on : .off
    }

    private func showAlert(title: String, message: String) {
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }

    @objc private func openDashboard() {
        NSWorkspace.shared.open(server.dashboardURL)
    }

    @objc private func startServer() {
        server.start()
    }

    @objc private func restartServer() {
        server.restart()
    }

    @objc private func stopServer() {
        server.stop()
    }

    @objc private func openDataFolder() {
        try? FileManager.default.createDirectory(at: server.supportRoot, withIntermediateDirectories: true)
        NSWorkspace.shared.open(server.supportRoot)
    }

    @objc private func showServerLog() {
        let log = FileManager.default.fileExists(atPath: server.serverLogURL.path) ? server.serverLogURL : server.launcherLogURL
        if FileManager.default.fileExists(atPath: log.path) {
            NSWorkspace.shared.selectFile(log.path, inFileViewerRootedAtPath: server.supportRoot.path)
        } else {
            openDataFolder()
        }
    }

    @objc private func toggleLaunchAtLogin() {
        do {
            if SMAppService.mainApp.status == .enabled {
                try SMAppService.mainApp.unregister()
            } else {
                try SMAppService.mainApp.register()
            }
            refreshLaunchAtLogin()
        } catch {
            showAlert(title: "Could Not Change Login Setting", message: error.localizedDescription)
        }
    }

    @objc private func checkForUpdates() {
        updater.checkForUpdates(manual: true)
    }

    @objc private func installUpdate() {
        updater.installAvailableUpdate()
    }

    @objc private func quit() {
        NSApp.terminate(nil)
    }
}

private let application = NSApplication.shared
private let delegate = AppDelegate()
application.delegate = delegate
application.run()
